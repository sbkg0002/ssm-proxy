package tunnel

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	// macOS system constants for utun control
	SYSPROTO_CONTROL  = 2
	UTUN_OPT_IFNAME   = 2
	UTUN_CONTROL_NAME = "com.apple.net.utun_control"
)

// TunDevice represents a macOS utun device
type TunDevice struct {
	name string
	fd   *os.File
	mtu  int
}

// CreateTUN creates a new utun device on macOS
func CreateTUN() (*TunDevice, error) {
	// Open the utun control socket
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, SYSPROTO_CONTROL)
	if err != nil {
		return nil, fmt.Errorf("failed to create utun socket: %w", err)
	}

	// Find the utun control ID
	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], UTUN_CONTROL_NAME)

	err = unix.IoctlCtlInfo(fd, ctlInfo)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("failed to get utun control info: %w", err)
	}

	// Connect to utun control (auto-assigns utun number)
	sc := &unix.SockaddrCtl{
		ID:   ctlInfo.Id,
		Unit: 0, // 0 = auto-assign next available
	}

	err = unix.Connect(fd, sc)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("failed to connect to utun control: %w", err)
	}

	// Get the assigned utun device name
	name, err := getDeviceName(fd)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}

	return &TunDevice{
		name: name,
		fd:   os.NewFile(uintptr(fd), name),
		mtu:  1500,
	}, nil
}

// getDeviceName retrieves the utun device name from the socket
func getDeviceName(fd int) (string, error) {
	// Get socket name to determine utun number
	var ifName [unix.IFNAMSIZ]byte
	ifNameLen := uint32(unix.IFNAMSIZ)

	_, _, errno := unix.Syscall6(
		unix.SYS_GETSOCKOPT,
		uintptr(fd),
		SYSPROTO_CONTROL,
		UTUN_OPT_IFNAME,
		uintptr(unsafe.Pointer(&ifName[0])),
		uintptr(unsafe.Pointer(&ifNameLen)),
		0,
	)

	if errno != 0 {
		return "", fmt.Errorf("failed to get utun device name: %v", errno)
	}

	// Convert to string and trim null bytes
	name := string(ifName[:ifNameLen])
	name = strings.TrimRight(name, "\x00")

	if name == "" {
		// Fallback: try to detect by listing interfaces
		// This is a backup method if UTUN_OPT_IFNAME doesn't work
		return "utun", nil
	}

	return name, nil
}

// Configure configures the TUN device with IP address and MTU
func (t *TunDevice) Configure(ipAddr string, mtu int) error {
	// Parse IP address (should be in format "169.254.169.1/30")
	parts := strings.Split(ipAddr, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid IP address format, expected x.x.x.x/y")
	}
	ip := parts[0]

	// Set IP address using ifconfig
	// ifconfig utun2 169.254.169.1 169.254.169.1 netmask 255.255.255.252
	cmd := exec.Command("ifconfig", t.name, ip, ip)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set IP address: %s: %w", string(output), err)
	}

	// Set MTU
	cmd = exec.Command("ifconfig", t.name, "mtu", fmt.Sprintf("%d", mtu))
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set MTU: %s: %w", string(output), err)
	}

	// Bring interface up
	cmd = exec.Command("ifconfig", t.name, "up")
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to bring interface up: %s: %w", string(output), err)
	}

	t.mtu = mtu
	return nil
}

// Read reads an IP packet from the utun device
func (t *TunDevice) Read(buf []byte) (int, error) {
	// macOS utun prepends 4-byte protocol header (AF_INET or AF_INET6)
	n, err := t.fd.Read(buf)
	if err != nil {
		return 0, fmt.Errorf("read from tun device failed: %w", err)
	}

	// Need at least 4 bytes for the protocol header
	if n < 4 {
		return 0, fmt.Errorf("packet too small: %d bytes", n)
	}

	// Skip the 4-byte protocol header and move packet data to start of buffer
	copy(buf, buf[4:n])
	return n - 4, nil
}

// Write writes an IP packet to the utun device
func (t *TunDevice) Write(packet []byte) (int, error) {
	if len(packet) == 0 {
		return 0, fmt.Errorf("empty packet")
	}

	// Determine IP version from first byte
	version := packet[0] >> 4
	var proto uint32
	if version == 6 {
		proto = unix.AF_INET6 // IPv6
	} else {
		proto = unix.AF_INET // IPv4 (default)
	}

	// Prepend 4-byte protocol header
	buf := make([]byte, 4+len(packet))
	binary.BigEndian.PutUint32(buf[0:4], proto)
	copy(buf[4:], packet)

	// Write to device
	n, err := t.fd.Write(buf)
	if err != nil {
		return 0, fmt.Errorf("write to tun device failed: %w", err)
	}

	// Return actual packet bytes written (excluding header)
	return n - 4, nil
}

// Close closes the TUN device
func (t *TunDevice) Close() error {
	if t.fd != nil {
		// Bring interface down
		cmd := exec.Command("ifconfig", t.name, "down")
		_ = cmd.Run() // Best effort

		return t.fd.Close()
	}
	return nil
}

// Name returns the device name (e.g., "utun2")
func (t *TunDevice) Name() string {
	return t.name
}

// MTU returns the MTU of the device
func (t *TunDevice) MTU() int {
	return t.mtu
}

// SetMTU sets the MTU of the device
func (t *TunDevice) SetMTU(mtu int) error {
	cmd := exec.Command("ifconfig", t.name, "mtu", fmt.Sprintf("%d", mtu))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set MTU: %s: %w", string(output), err)
	}
	t.mtu = mtu
	return nil
}

// FileDescriptor returns the underlying file descriptor
func (t *TunDevice) FileDescriptor() int {
	if t.fd == nil {
		return -1
	}
	return int(t.fd.Fd())
}
