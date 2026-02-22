package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	magicNumber uint32 = 0x53534D50 // "SSMP"
	headerSize         = 8
)

var (
	// Statistics
	stats struct {
		packetsTX uint64
		packetsRX uint64
		bytesTX   uint64
		bytesRX   uint64
		mu        sync.RWMutex
	}
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Create TUN device for packet forwarding
	tun, err := createTUN()
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	defer tun.Close()

	fmt.Fprintf(os.Stderr, "SSM Proxy Agent started on TUN device: %s\n", tun.Name())

	// Start packet forwarding goroutines
	errCh := make(chan error, 2)

	// stdin → TUN (receive packets from client, write to TUN)
	go func() {
		err := forwardStdinToTUN(os.Stdin, tun)
		errCh <- fmt.Errorf("stdin→TUN: %w", err)
	}()

	// TUN → stdout (read packets from TUN, send to client)
	go func() {
		err := forwardTUNToStdout(tun, os.Stdout)
		errCh <- fmt.Errorf("TUN→stdout: %w", err)
	}()

	// Print stats periodically
	go printStats()

	// Wait for signal or error
	select {
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "Received signal: %v\n", sig)
		return nil
	case err := <-errCh:
		return err
	}
}

// forwardStdinToTUN reads encapsulated packets from stdin and writes to TUN
func forwardStdinToTUN(reader io.Reader, tun *TUN) error {
	for {
		// Read header
		header := make([]byte, headerSize)
		if _, err := io.ReadFull(reader, header); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read header: %w", err)
		}

		// Verify magic number
		magic := binary.BigEndian.Uint32(header[0:4])
		if magic != magicNumber {
			return fmt.Errorf("invalid magic number: 0x%x", magic)
		}

		// Read length
		length := binary.BigEndian.Uint32(header[4:8])
		if length > 65535 {
			return fmt.Errorf("packet too large: %d bytes", length)
		}

		// Read packet data
		packet := make([]byte, length)
		if _, err := io.ReadFull(reader, packet); err != nil {
			return fmt.Errorf("read packet: %w", err)
		}

		// Write to TUN device
		if _, err := tun.Write(packet); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: TUN write error: %v\n", err)
			continue
		}

		// Update stats
		stats.mu.Lock()
		stats.packetsRX++
		stats.bytesRX += uint64(length)
		stats.mu.Unlock()
	}
}

// forwardTUNToStdout reads packets from TUN and writes encapsulated to stdout
func forwardTUNToStdout(tun *TUN, writer io.Writer) error {
	buf := make([]byte, 65535)

	for {
		// Read from TUN device
		n, err := tun.Read(buf)
		if err != nil {
			return fmt.Errorf("TUN read: %w", err)
		}

		if n == 0 {
			continue
		}

		packet := buf[:n]

		// Encapsulate packet
		frame := encapsulatePacket(packet)

		// Write to stdout
		if _, err := writer.Write(frame); err != nil {
			return fmt.Errorf("stdout write: %w", err)
		}

		// Update stats
		stats.mu.Lock()
		stats.packetsTX++
		stats.bytesTX += uint64(n)
		stats.mu.Unlock()
	}
}

// encapsulatePacket wraps a packet with protocol framing
func encapsulatePacket(packet []byte) []byte {
	header := make([]byte, headerSize)

	// Write magic number
	binary.BigEndian.PutUint32(header[0:4], magicNumber)

	// Write length
	binary.BigEndian.PutUint32(header[4:8], uint32(len(packet)))

	// Combine header and packet
	frame := make([]byte, headerSize+len(packet))
	copy(frame, header)
	copy(frame[headerSize:], packet)

	return frame
}

// printStats prints statistics every 30 seconds
func printStats() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats.mu.RLock()
		fmt.Fprintf(os.Stderr, "Stats: TX=%d packets (%d bytes), RX=%d packets (%d bytes)\n",
			stats.packetsTX, stats.bytesTX, stats.packetsRX, stats.bytesRX)
		stats.mu.RUnlock()
	}
}

// TUN represents a Linux TUN device
type TUN struct {
	fd   int
	name string
}

// createTUN creates a new TUN device on Linux
func createTUN() (*TUN, error) {
	// Open /dev/net/tun
	fd, err := syscall.Open("/dev/net/tun", syscall.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open /dev/net/tun: %w", err)
	}

	// IFF_TUN for TUN device, IFF_NO_PI for no packet info
	const IFF_TUN = 0x0001
	const IFF_NO_PI = 0x1000

	// Create ifreq structure
	var ifr struct {
		name  [16]byte
		flags uint16
		_     [22]byte // padding
	}

	// Set device name (empty means kernel assigns one)
	copy(ifr.name[:], []byte("tun%d"))
	ifr.flags = IFF_TUN | IFF_NO_PI

	// TUNSETIFF ioctl
	const TUNSETIFF = 0x400454ca

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), TUNSETIFF, uintptr(unsafe.Pointer(&ifr)))
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("TUNSETIFF ioctl failed: %v", errno)
	}

	// Get device name
	name := string(ifr.name[:])
	// Trim null bytes
	for i, b := range ifr.name {
		if b == 0 {
			name = string(ifr.name[:i])
			break
		}
	}

	tun := &TUN{
		fd:   fd,
		name: name,
	}

	// Configure the TUN device
	if err := tun.configure(); err != nil {
		tun.Close()
		return nil, fmt.Errorf("failed to configure TUN: %w", err)
	}

	return tun, nil
}

// configure sets up the TUN device
func (t *TUN) configure() error {
	// Bring interface up and set IP address
	// ip link set <name> up
	// ip addr add 169.254.100.1/30 dev <name>

	// Use ip command for simplicity
	cmds := [][]string{
		{"ip", "link", "set", t.name, "up"},
		{"ip", "addr", "add", "169.254.100.1/30", "dev", t.name},
	}

	for _, cmd := range cmds {
		if err := execCommand(cmd[0], cmd[1:]...); err != nil {
			return fmt.Errorf("failed to execute %v: %w", cmd, err)
		}
	}

	return nil
}

// Read reads a packet from the TUN device
func (t *TUN) Read(p []byte) (int, error) {
	n, err := syscall.Read(t.fd, p)
	if err != nil {
		return 0, fmt.Errorf("read: %w", err)
	}
	return n, nil
}

// Write writes a packet to the TUN device
func (t *TUN) Write(p []byte) (int, error) {
	n, err := syscall.Write(t.fd, p)
	if err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}
	return n, nil
}

// Close closes the TUN device
func (t *TUN) Close() error {
	return syscall.Close(t.fd)
}

// Name returns the device name
func (t *TUN) Name() string {
	return t.name
}

// execCommand executes a shell command
func execCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %s: %w", name, string(output), err)
	}
	return nil
}
