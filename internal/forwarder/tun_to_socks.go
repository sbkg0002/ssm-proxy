package forwarder

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sbkg0002/ssm-proxy/internal/tunnel"
	"golang.org/x/net/proxy"
)

const (
	// TCP flags
	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
	tcpPSH = 0x08
	tcpACK = 0x10

	// Connection timeouts
	connTimeout   = 5 * time.Minute
	dialTimeout   = 30 * time.Second
	readTimeout   = 100 * time.Millisecond
	cleanupTicker = 30 * time.Second
)

// TunToSOCKS handles transparent packet forwarding from TUN to SOCKS5 proxy
type TunToSOCKS struct {
	tun         *tunnel.TunDevice
	socksAddr   string
	socksDialer proxy.Dialer
	connections map[connKey]*tcpConn
	connMu      sync.RWMutex
	stopCh      chan struct{}
	wg          sync.WaitGroup
	stats       *Stats
}

// connKey uniquely identifies a TCP connection
type connKey struct {
	srcIP   uint32
	dstIP   uint32
	srcPort uint16
	dstPort uint16
}

// tcpConn represents a single TCP connection
type tcpConn struct {
	key         connKey
	socksConn   net.Conn
	lastActive  time.Time
	seqNum      uint32
	ackNum      uint32
	established bool
	closing     bool
	mu          sync.Mutex
}

// NewTunToSOCKS creates a new TUN-to-SOCKS translator
func NewTunToSOCKS(tun *tunnel.TunDevice, socksAddr string) (*TunToSOCKS, error) {
	// Create SOCKS5 dialer
	dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
	}

	return &TunToSOCKS{
		tun:         tun,
		socksAddr:   socksAddr,
		socksDialer: dialer,
		connections: make(map[connKey]*tcpConn),
		stopCh:      make(chan struct{}),
		stats:       &Stats{},
	}, nil
}

// Start starts the TUN-to-SOCKS translator
func (t *TunToSOCKS) Start(ctx context.Context) error {
	log.Info("Starting TUN-to-SOCKS translator")

	t.wg.Add(1)
	go t.readPackets(ctx)

	t.wg.Add(1)
	go t.cleanupConnections(ctx)

	log.Info("TUN-to-SOCKS translator started")
	return nil
}

// Stop stops the TUN-to-SOCKS translator
func (t *TunToSOCKS) Stop() error {
	log.Info("Stopping TUN-to-SOCKS translator")
	close(t.stopCh)

	// Close all connections
	t.connMu.Lock()
	for _, conn := range t.connections {
		conn.close()
	}
	t.connections = make(map[connKey]*tcpConn)
	t.connMu.Unlock()

	// Wait for goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Info("TUN-to-SOCKS translator stopped cleanly")
	case <-time.After(5 * time.Second):
		log.Warn("Timeout waiting for TUN-to-SOCKS translator to stop")
	}

	return nil
}

// readPackets reads packets from TUN device
func (t *TunToSOCKS) readPackets(ctx context.Context) {
	defer t.wg.Done()
	buf := make([]byte, 65535)

	for {
		select {
		case <-ctx.Done():
			log.Debug("readPackets: context cancelled, exiting")
			return
		case <-t.stopCh:
			log.Debug("readPackets: stop signal received, exiting")
			return
		default:
		}

		n, err := t.tun.Read(buf)
		if err != nil {
			// Check if we're stopping (TUN device closed during shutdown)
			select {
			case <-t.stopCh:
				log.Debug("readPackets: stop signal received after read error, exiting")
				return
			case <-ctx.Done():
				log.Debug("readPackets: context cancelled after read error, exiting")
				return
			default:
				// Transient error, retry after short delay
				log.Debugf("readPackets: read error (will retry): %v", err)
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}

		if n < 20 {
			continue
		}

		packet := make([]byte, n)
		copy(packet, buf[:n])

		if err := t.handlePacket(ctx, packet); err != nil {
			log.Debugf("Packet handling error: %v", err)
			t.stats.IncrementErrorsTX()
		} else {
			t.stats.IncrementTX(n)
		}
	}
}

// handlePacket processes an incoming IP packet
func (t *TunToSOCKS) handlePacket(ctx context.Context, packet []byte) error {
	// Validate IP header
	if len(packet) < 20 || (packet[0]>>4) != 4 {
		return fmt.Errorf("invalid IPv4 packet")
	}

	ihl := int(packet[0]&0x0F) * 4
	if len(packet) < ihl || ihl < 20 {
		return fmt.Errorf("invalid IP header length")
	}

	protocol := packet[9]
	if protocol != 6 { // Only TCP
		return nil
	}

	srcIP := binary.BigEndian.Uint32(packet[12:16])
	dstIP := binary.BigEndian.Uint32(packet[16:20])

	// Validate TCP header
	if len(packet) < ihl+20 {
		return fmt.Errorf("packet too short for TCP")
	}

	tcpHeader := packet[ihl:]
	srcPort := binary.BigEndian.Uint16(tcpHeader[0:2])
	dstPort := binary.BigEndian.Uint16(tcpHeader[2:4])
	seqNum := binary.BigEndian.Uint32(tcpHeader[4:8])
	ackNum := binary.BigEndian.Uint32(tcpHeader[8:12])
	dataOffset := int(tcpHeader[12]>>4) * 4
	flags := tcpHeader[13]

	if len(tcpHeader) < dataOffset {
		return fmt.Errorf("invalid TCP data offset")
	}

	payload := tcpHeader[dataOffset:]

	key := connKey{srcIP, dstIP, srcPort, dstPort}

	// Handle RST
	if flags&tcpRST != 0 {
		t.closeConn(key)
		return nil
	}

	// Handle FIN
	if flags&tcpFIN != 0 {
		t.closeConn(key)
		return nil
	}

	// Handle SYN (new connection)
	if flags&tcpSYN != 0 && flags&tcpACK == 0 {
		return t.handleSYN(ctx, key, seqNum)
	}

	// Get existing connection
	t.connMu.RLock()
	conn, exists := t.connections[key]
	t.connMu.RUnlock()

	if !exists {
		return nil // Connection not found, ignore
	}

	conn.mu.Lock()
	conn.lastActive = time.Now()
	conn.seqNum = seqNum
	conn.ackNum = ackNum
	conn.mu.Unlock()

	// Forward payload if present
	if len(payload) > 0 && conn.socksConn != nil {
		_, err := conn.socksConn.Write(payload)
		if err != nil {
			t.closeConn(key)
			return fmt.Errorf("SOCKS write failed: %w", err)
		}
	}

	return nil
}

// handleSYN handles a new TCP SYN packet
func (t *TunToSOCKS) handleSYN(ctx context.Context, key connKey, seqNum uint32) error {
	dstAddr := fmt.Sprintf("%s:%d", uint32ToIP(key.dstIP), key.dstPort)

	log.Debugf("New connection: %s:%d -> %s", uint32ToIP(key.srcIP), key.srcPort, dstAddr)

	// Dial through SOCKS5
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	socksConn, err := t.socksDialer.(interface {
		DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	}).DialContext(dialCtx, "tcp", dstAddr)

	if err != nil {
		// If DialContext not available, try regular Dial
		socksConn, err = t.socksDialer.Dial("tcp", dstAddr)
		if err != nil {
			log.Debugf("SOCKS dial failed for %s: %v", dstAddr, err)
			return err
		}
	}

	conn := &tcpConn{
		key:         key,
		socksConn:   socksConn,
		lastActive:  time.Now(),
		seqNum:      seqNum,
		ackNum:      seqNum + 1,
		established: true,
	}

	t.connMu.Lock()
	t.connections[key] = conn
	t.connMu.Unlock()

	// Send SYN-ACK
	t.sendSYNACK(key, seqNum)

	// Start reading from SOCKS connection
	t.wg.Add(1)
	go t.readFromSOCKS(conn)

	return nil
}

// sendSYNACK sends a SYN-ACK response
func (t *TunToSOCKS) sendSYNACK(key connKey, seqNum uint32) {
	packet := buildTCPPacket(
		uint32ToIP(key.dstIP), key.dstPort,
		uint32ToIP(key.srcIP), key.srcPort,
		0, seqNum+1,
		tcpSYN|tcpACK, nil,
	)

	t.tun.Write(packet)
	t.stats.IncrementRX(len(packet))
}

// readFromSOCKS reads data from SOCKS connection and sends to TUN
func (t *TunToSOCKS) readFromSOCKS(conn *tcpConn) {
	defer t.wg.Done()
	defer t.closeConn(conn.key)

	buf := make([]byte, 16384)

	for {
		select {
		case <-t.stopCh:
			log.Debug("readFromSOCKS: stop signal received, closing connection")
			return
		default:
		}

		conn.socksConn.SetReadDeadline(time.Now().Add(readTimeout))
		n, err := conn.socksConn.Read(buf)

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}

		if n > 0 {
			conn.mu.Lock()
			ackNum := conn.ackNum
			conn.ackNum += uint32(n)
			lastActive := time.Now()
			conn.lastActive = lastActive
			conn.mu.Unlock()

			// Send data packet
			packet := buildTCPPacket(
				uint32ToIP(conn.key.dstIP), conn.key.dstPort,
				uint32ToIP(conn.key.srcIP), conn.key.srcPort,
				ackNum, conn.seqNum,
				tcpPSH|tcpACK, buf[:n],
			)

			t.tun.Write(packet)
			t.stats.IncrementRX(len(packet))
		}
	}
}

// closeConn closes a connection
func (t *TunToSOCKS) closeConn(key connKey) {
	t.connMu.Lock()
	defer t.connMu.Unlock()

	if conn, exists := t.connections[key]; exists {
		conn.close()
		delete(t.connections, key)
	}
}

// cleanupConnections periodically removes stale connections
func (t *TunToSOCKS) cleanupConnections(ctx context.Context) {
	defer t.wg.Done()
	ticker := time.NewTicker(cleanupTicker)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Debug("cleanupConnections: context cancelled, exiting")
			return
		case <-t.stopCh:
			log.Debug("cleanupConnections: stop signal received, exiting")
			return
		case <-ticker.C:
			t.cleanup()
		}
	}
}

// cleanup removes idle connections
func (t *TunToSOCKS) cleanup() {
	t.connMu.Lock()
	defer t.connMu.Unlock()

	now := time.Now()
	for key, conn := range t.connections {
		conn.mu.Lock()
		idle := now.Sub(conn.lastActive) > connTimeout
		conn.mu.Unlock()

		if idle {
			log.Debugf("Closing idle connection: %s:%d -> %s:%d",
				uint32ToIP(key.srcIP), key.srcPort,
				uint32ToIP(key.dstIP), key.dstPort)
			conn.close()
			delete(t.connections, key)
		}
	}
}

// close closes a TCP connection
func (c *tcpConn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closing {
		return
	}
	c.closing = true

	if c.socksConn != nil {
		c.socksConn.Close()
	}
}

// GetStats returns traffic statistics
func (t *TunToSOCKS) GetStats() Stats {
	return t.stats.Copy()
}

// buildTCPPacket constructs a TCP/IP packet
func buildTCPPacket(srcIP net.IP, srcPort uint16, dstIP net.IP, dstPort uint16,
	seqNum, ackNum uint32, flags byte, payload []byte) []byte {

	ipHdrLen := 20
	tcpHdrLen := 20
	totalLen := ipHdrLen + tcpHdrLen + len(payload)

	packet := make([]byte, totalLen)

	// IP Header
	packet[0] = 0x45 // Version 4, IHL 5
	binary.BigEndian.PutUint16(packet[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(packet[6:8], 0x4000) // Don't fragment
	packet[8] = 64                                  // TTL
	packet[9] = 6                                   // TCP
	copy(packet[12:16], srcIP.To4())
	copy(packet[16:20], dstIP.To4())

	// IP checksum
	binary.BigEndian.PutUint16(packet[10:12], ipChecksum(packet[:ipHdrLen]))

	// TCP Header
	tcp := packet[ipHdrLen:]
	binary.BigEndian.PutUint16(tcp[0:2], srcPort)
	binary.BigEndian.PutUint16(tcp[2:4], dstPort)
	binary.BigEndian.PutUint32(tcp[4:8], seqNum)
	binary.BigEndian.PutUint32(tcp[8:12], ackNum)
	tcp[12] = 0x50 // Data offset: 5 (20 bytes)
	tcp[13] = flags
	binary.BigEndian.PutUint16(tcp[14:16], 65535) // Window size

	// Copy payload
	copy(tcp[tcpHdrLen:], payload)

	// TCP checksum
	binary.BigEndian.PutUint16(tcp[16:18], tcpChecksum(srcIP, dstIP, tcp))

	return packet
}

// ipChecksum calculates IP header checksum
func ipChecksum(header []byte) uint16 {
	sum := uint32(0)
	for i := 0; i < len(header); i += 2 {
		if i+1 < len(header) {
			sum += uint32(binary.BigEndian.Uint16(header[i : i+2]))
		}
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// tcpChecksum calculates TCP checksum with pseudo-header
func tcpChecksum(srcIP, dstIP net.IP, tcpSegment []byte) uint16 {
	pseudo := make([]byte, 12+len(tcpSegment))
	copy(pseudo[0:4], srcIP.To4())
	copy(pseudo[4:8], dstIP.To4())
	pseudo[9] = 6 // TCP protocol
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(tcpSegment)))
	copy(pseudo[12:], tcpSegment)

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

// uint32ToIP converts uint32 to net.IP
func uint32ToIP(ip uint32) net.IP {
	return net.IPv4(byte(ip>>24), byte(ip>>16), byte(ip>>8), byte(ip))
}
