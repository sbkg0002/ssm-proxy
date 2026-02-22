package forwarder

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/sbkg0002/ssm-proxy/internal/ssm"
	"github.com/sbkg0002/ssm-proxy/internal/tunnel"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

// Forwarder handles bidirectional packet forwarding between TUN and SSM
type Forwarder struct {
	tun        *tunnel.TunDevice
	ssm        *ssm.Session
	logPackets bool
	stopCh     chan struct{}
	wg         sync.WaitGroup
	stats      *Stats
	mu         sync.RWMutex
}

// Stats holds traffic statistics
type Stats struct {
	PacketsTX uint64
	PacketsRX uint64
	BytesTX   uint64
	BytesRX   uint64
	ErrorsTX  uint64
	ErrorsRX  uint64
	mu        sync.RWMutex
}

// New creates a new packet forwarder
func New(tun *tunnel.TunDevice, ssm *ssm.Session, logPackets bool) *Forwarder {
	return &Forwarder{
		tun:        tun,
		ssm:        ssm,
		logPackets: logPackets,
		stopCh:     make(chan struct{}),
		stats:      &Stats{},
	}
}

// Start starts the packet forwarder
func (f *Forwarder) Start() error {
	// Start TUN -> SSM forwarding
	f.wg.Add(1)
	go f.forwardTunToSSM()

	// Start SSM -> TUN forwarding
	f.wg.Add(1)
	go f.forwardSSMToTun()

	log.Info("Packet forwarder started")
	return nil
}

// Stop stops the packet forwarder
func (f *Forwarder) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()

	select {
	case <-f.stopCh:
		return // Already stopped
	default:
		close(f.stopCh)
	}

	// Wait for goroutines to finish
	f.wg.Wait()
	log.Info("Packet forwarder stopped")
}

// forwardTunToSSM reads packets from TUN device and forwards to SSM
func (f *Forwarder) forwardTunToSSM() {
	defer f.wg.Done()

	buf := make([]byte, 65535)
	packetCount := 0

	for {
		select {
		case <-f.stopCh:
			log.Debug("TUN->SSM forwarder stopping")
			return
		default:
		}

		// Read IP packet from TUN device
		n, err := f.tun.Read(buf)
		if err != nil {
			select {
			case <-f.stopCh:
				return
			default:
				if err != io.EOF {
					log.Errorf("TUN read error: %v", err)
					f.stats.IncrementErrorsTX()
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}

		if n == 0 {
			continue
		}

		packet := buf[:n]
		packetCount++

		// Log packet if debug enabled
		if f.logPackets {
			logPacketDetails("TX", packetCount, packet)
		}

		// Encapsulate packet
		frame := ssm.EncapsulatePacket(packet)

		// Send through SSM tunnel
		_, err = f.ssm.Write(frame)
		if err != nil {
			log.Errorf("SSM write error: %v", err)
			f.stats.IncrementErrorsTX()
			continue
		}

		// Update statistics
		f.stats.IncrementTX(n)
	}
}

// forwardSSMToTun reads packets from SSM and forwards to TUN device
func (f *Forwarder) forwardSSMToTun() {
	defer f.wg.Done()

	packetCount := 0

	for {
		select {
		case <-f.stopCh:
			log.Debug("SSM->TUN forwarder stopping")
			return
		default:
		}

		// Read and decapsulate packet from SSM
		packet, err := ssm.DecapsulatePacket(f.ssm.Reader())
		if err != nil {
			select {
			case <-f.stopCh:
				return
			default:
				if err != io.EOF {
					log.Errorf("SSM read error: %v", err)
					f.stats.IncrementErrorsRX()
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}

		if len(packet) == 0 {
			continue
		}

		packetCount++

		// Log packet if debug enabled
		if f.logPackets {
			logPacketDetails("RX", packetCount, packet)
		}

		// Write packet to TUN device
		_, err = f.tun.Write(packet)
		if err != nil {
			log.Errorf("TUN write error: %v", err)
			f.stats.IncrementErrorsRX()
			continue
		}

		// Update statistics
		f.stats.IncrementRX(len(packet))
	}
}

// GetStats returns current traffic statistics
func (f *Forwarder) GetStats() Stats {
	return f.stats.Copy()
}

// IncrementTX increments transmit statistics
func (s *Stats) IncrementTX(bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PacketsTX++
	s.BytesTX += uint64(bytes)
}

// IncrementRX increments receive statistics
func (s *Stats) IncrementRX(bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PacketsRX++
	s.BytesRX += uint64(bytes)
}

// IncrementErrorsTX increments transmit error counter
func (s *Stats) IncrementErrorsTX() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorsTX++
}

// IncrementErrorsRX increments receive error counter
func (s *Stats) IncrementErrorsRX() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorsRX++
}

// Copy returns a copy of the statistics
func (s *Stats) Copy() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{
		PacketsTX: s.PacketsTX,
		PacketsRX: s.PacketsRX,
		BytesTX:   s.BytesTX,
		BytesRX:   s.BytesRX,
		ErrorsTX:  s.ErrorsTX,
		ErrorsRX:  s.ErrorsRX,
	}
}

// logPacketDetails logs details about a packet
func logPacketDetails(direction string, count int, packet []byte) {
	if len(packet) < 20 {
		log.Debugf("[%s #%d] Packet too small: %d bytes", direction, count, len(packet))
		return
	}

	// Parse IP header (basic)
	version := packet[0] >> 4
	protocol := packet[9]
	srcIP := fmt.Sprintf("%d.%d.%d.%d", packet[12], packet[13], packet[14], packet[15])
	dstIP := fmt.Sprintf("%d.%d.%d.%d", packet[16], packet[17], packet[18], packet[19])

	protoName := "unknown"
	switch protocol {
	case 1:
		protoName = "ICMP"
	case 6:
		protoName = "TCP"
	case 17:
		protoName = "UDP"
	}

	log.Debugf("[%s #%d] IPv%d %s %s -> %s (%d bytes)",
		direction, count, version, protoName, srcIP, dstIP, len(packet))
}
