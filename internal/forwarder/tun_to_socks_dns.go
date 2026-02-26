package forwarder

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"

	"github.com/sbkg0002/ssm-proxy/internal/dns"
)

// UDP connection tracking for DNS
type udpConnKey struct {
	srcIP   uint32
	dstIP   uint32
	srcPort uint16
	dstPort uint16
}

// HandleUDPPacket processes UDP packets (primarily for DNS)
// Note: UDP DNS queries are captured here but forwarded via TCP through the tunnel
// for better SOCKS5 compatibility. This allows standard UDP DNS to work with SOCKS5 proxies.
func (t *TunToSOCKS) HandleUDPPacket(ctx context.Context, packet []byte, ihl int) error {
	if len(packet) < ihl+8 {
		return fmt.Errorf("packet too short for UDP")
	}

	udpHeader := packet[ihl:]
	srcPort := binary.BigEndian.Uint16(udpHeader[0:2])
	dstPort := binary.BigEndian.Uint16(udpHeader[2:4])
	udpLength := binary.BigEndian.Uint16(udpHeader[4:6])

	if len(udpHeader) < int(udpLength) {
		return fmt.Errorf("truncated UDP packet")
	}

	// Check if this is a DNS query (port 53)
	if dstPort != 53 {
		// Not DNS, ignore (could be extended to handle other UDP traffic)
		log.Debugf("UDP: ignoring non-DNS packet to port %d", dstPort)
		return nil
	}

	// Extract IP addresses
	srcIP := binary.BigEndian.Uint32(packet[12:16])
	dstIP := binary.BigEndian.Uint32(packet[16:20])

	// Extract DNS query payload
	dnsPayload := udpHeader[8:udpLength]

	// Handle DNS query
	return t.handleDNSQuery(ctx, packet, srcIP, dstIP, srcPort, dstPort, dnsPayload)
}

// handleDNSQuery processes a DNS query packet
// This function receives UDP DNS queries from applications and forwards them
// via TCP through the SOCKS5 tunnel (TCP DNS is more reliable through SOCKS5).
// The response is then converted back to UDP and sent to the application.
func (t *TunToSOCKS) handleDNSQuery(ctx context.Context, originalPacket []byte,
	srcIP, dstIP uint32, srcPort, dstPort uint16, queryData []byte) error {

	if t.dnsResolver == nil {
		// No DNS resolver configured, ignore
		log.Debugf("DNS: no resolver configured, ignoring query")
		return nil
	}

	// Extract domain name from query to check if we should handle it
	domain := dns.ExtractDomainFromQuery(queryData)
	if domain == "" {
		log.Debugf("DNS: could not extract domain from query")
		return nil
	}

	// Check if this domain should be resolved through the tunnel
	if !t.dnsResolver.ShouldHandle(domain) {
		log.Debugf("DNS: domain %s not configured for tunnel resolution", domain)
		return nil
	}

	log.Debugf("DNS: resolving %s through tunnel (via TCP)", domain)

	// Perform DNS query through tunnel using TCP (converted from UDP)
	responseData, err := t.dnsResolver.Query(ctx, queryData)
	if err != nil {
		log.Debugf("DNS: query failed for %s: %v", domain, err)
		return err
	}

	// Build UDP response packet
	responsePacket := buildUDPPacket(
		uint32ToIP(dstIP), dstPort,
		uint32ToIP(srcIP), srcPort,
		responseData,
	)

	// Send response back through TUN device
	_, err = t.tun.Write(responsePacket)
	if err != nil {
		return fmt.Errorf("failed to write DNS response: %w", err)
	}

	t.stats.IncrementRX(len(responsePacket))
	log.Debugf("DNS: sent response for %s (%d bytes)", domain, len(responsePacket))

	return nil
}

// buildUDPPacket constructs a UDP/IP packet
func buildUDPPacket(srcIP net.IP, srcPort uint16, dstIP net.IP, dstPort uint16, payload []byte) []byte {
	ipHdrLen := 20
	udpHdrLen := 8
	totalLen := ipHdrLen + udpHdrLen + len(payload)

	packet := make([]byte, totalLen)

	// IP Header
	packet[0] = 0x45 // Version 4, IHL 5
	binary.BigEndian.PutUint16(packet[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(packet[6:8], 0x4000) // Don't fragment
	packet[8] = 64                                  // TTL
	packet[9] = 17                                  // UDP
	copy(packet[12:16], srcIP.To4())
	copy(packet[16:20], dstIP.To4())

	// IP checksum
	binary.BigEndian.PutUint16(packet[10:12], ipChecksum(packet[:ipHdrLen]))

	// UDP Header
	udp := packet[ipHdrLen:]
	binary.BigEndian.PutUint16(udp[0:2], srcPort)
	binary.BigEndian.PutUint16(udp[2:4], dstPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpHdrLen+len(payload)))

	// Copy payload
	copy(udp[udpHdrLen:], payload)

	// UDP checksum (calculate for better compatibility)
	binary.BigEndian.PutUint16(udp[6:8], udpChecksum(srcIP, dstIP, udp))

	return packet
}

// udpChecksum calculates UDP checksum with pseudo-header
func udpChecksum(srcIP, dstIP net.IP, udpSegment []byte) uint16 {
	pseudo := make([]byte, 12+len(udpSegment))
	copy(pseudo[0:4], srcIP.To4())
	copy(pseudo[4:8], dstIP.To4())
	pseudo[9] = 17 // UDP protocol
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(udpSegment)))
	copy(pseudo[12:], udpSegment)

	// Clear checksum field before calculation
	pseudo[12+6] = 0
	pseudo[12+7] = 0

	sum := uint32(0)
	for i := 0; i < len(pseudo); i += 2 {
		if i+1 < len(pseudo) {
			sum += uint32(binary.BigEndian.Uint16(pseudo[i : i+2]))
		} else {
			sum += uint32(pseudo[i]) << 8
		}
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
