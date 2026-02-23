package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/sirupsen/logrus"
)

var sshLog = logrus.New()

// SSHTunnel manages an SSH tunnel with dynamic SOCKS5 forwarding over SSM
type SSHTunnel struct {
	instanceID       string
	region           string
	awsProfile       string
	awsConfig        aws.Config
	availabilityZone string
	socksPort        int
	cmd              *exec.Cmd
	running          bool
	mu               sync.RWMutex
	stopCh           chan struct{}
	stoppedCh        chan struct{}
	sshUser          string
	keyPair          *SSHKeyPair
}

// SSHTunnelConfig holds configuration for SSH tunnel
type SSHTunnelConfig struct {
	InstanceID       string
	Region           string
	AWSProfile       string
	AWSConfig        aws.Config
	AvailabilityZone string
	SOCKSPort        int
	SSHUser          string
}

// NewSSHTunnel creates a new SSH tunnel manager
func NewSSHTunnel(config SSHTunnelConfig) *SSHTunnel {
	if config.SOCKSPort == 0 {
		config.SOCKSPort = 1080 // Default SOCKS5 port
	}
	if config.SSHUser == "" {
		config.SSHUser = "ec2-user" // Default for Amazon Linux
	}

	return &SSHTunnel{
		instanceID:       config.InstanceID,
		region:           config.Region,
		awsProfile:       config.AWSProfile,
		awsConfig:        config.AWSConfig,
		availabilityZone: config.AvailabilityZone,
		socksPort:        config.SOCKSPort,
		sshUser:          config.SSHUser,
		stopCh:           make(chan struct{}),
		stoppedCh:        make(chan struct{}),
	}
}

// Start starts the SSH tunnel with dynamic forwarding
func (t *SSHTunnel) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return fmt.Errorf("SSH tunnel already running")
	}

	sshLog.WithFields(logrus.Fields{
		"instance_id": t.instanceID,
		"region":      t.region,
		"socks_port":  t.socksPort,
	}).Info("Starting SSH tunnel with dynamic forwarding")

	// Check for existing SSH key or generate temporary one
	var privateKeyPath string
	var publicKey string
	var err error

	if existingKey, exists := CheckExistingSSHKey(); exists {
		sshLog.Infof("Using existing SSH key: %s", existingKey)
		privateKeyPath = existingKey
		publicKey, err = GetSSHPublicKeyFromPrivate(existingKey)
		if err != nil {
			return fmt.Errorf("failed to read public key from existing key: %w", err)
		}
	} else {
		sshLog.Info("No existing SSH key found, generating temporary key pair")
		keyPair, err := GenerateTemporarySSHKey()
		if err != nil {
			return fmt.Errorf("failed to generate temporary SSH key: %w", err)
		}
		t.keyPair = keyPair
		privateKeyPath = keyPair.PrivateKeyPath
		publicKey = keyPair.PublicKey
		sshLog.Debugf("Temporary SSH key generated: %s", privateKeyPath)
	}

	// Send SSH public key to instance via EC2 Instance Connect
	sshLog.Info("Sending SSH public key to instance via EC2 Instance Connect...")
	err = SendSSHPublicKeyToInstance(t.awsConfig, t.instanceID, t.availabilityZone, t.sshUser, publicKey)
	if err != nil {
		if t.keyPair != nil {
			t.keyPair.Cleanup()
		}
		return fmt.Errorf("failed to send SSH key via Instance Connect: %w\n\n"+
			"Alternative: Manually add your SSH key to the instance:\n"+
			"  1. Generate key: ssh-keygen -t rsa -b 4096\n"+
			"  2. Add to instance: aws ec2-instance-connect send-ssh-public-key ...\n"+
			"  3. Or add to ~/.ssh/authorized_keys on instance", err)
	}

	// Build SSH command with SSM ProxyCommand
	proxyCommand := fmt.Sprintf("aws ssm start-session --target %s --document-name AWS-StartSSHSession --parameters 'portNumber=%%p' --region %s",
		t.instanceID, t.region)

	if t.awsProfile != "" {
		proxyCommand += fmt.Sprintf(" --profile %s", t.awsProfile)
	}

	args := []string{
		"-D", fmt.Sprintf("127.0.0.1:%d", t.socksPort), // Dynamic forwarding on localhost
		"-N",                 // Don't execute remote command
		"-i", privateKeyPath, // Use the SSH private key
		"-o", "StrictHostKeyChecking=no", // Don't check host keys
		"-o", "UserKnownHostsFile=/dev/null", // Don't save known hosts
		"-o", "ServerAliveInterval=30", // Keep connection alive
		"-o", "ServerAliveCountMax=3", // Max missed keepalives
		"-o", "ConnectTimeout=10", // Connection timeout (shorter since key is fresh)
		"-o", fmt.Sprintf("ProxyCommand=%s", proxyCommand),
		fmt.Sprintf("%s@%s", t.sshUser, t.instanceID),
	}

	sshLog.Debugf("SSH command: ssh %s", strings.Join(args, " "))

	t.cmd = exec.CommandContext(ctx, "ssh", args...)

	// Capture stderr for debugging
	stderr, errPipe := t.cmd.StderrPipe()
	if errPipe != nil {
		if t.keyPair != nil {
			t.keyPair.Cleanup()
		}
		return fmt.Errorf("failed to get stderr pipe: %w", errPipe)
	}

	// Start SSH command
	if err := t.cmd.Start(); err != nil {
		if t.keyPair != nil {
			t.keyPair.Cleanup()
		}
		return fmt.Errorf("failed to start SSH: %w", err)
	}

	// Monitor stderr in goroutine
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stderr.Read(buf)
			if err != nil {
				if err != io.EOF {
					sshLog.Debugf("SSH stderr read error: %v", err)
				}
				return
			}
			if n > 0 {
				sshLog.Debugf("SSH: %s", string(buf[:n]))
			}
		}
	}()

	// Wait for SOCKS5 port to be available
	if err := t.waitForSOCKS(ctx, 30*time.Second); err != nil {
		t.cmd.Process.Kill()
		if t.keyPair != nil {
			t.keyPair.Cleanup()
		}
		return fmt.Errorf("SSH tunnel failed to start: %w", err)
	}

	t.running = true

	// Monitor SSH process
	go t.monitor()

	sshLog.Info("SSH tunnel started successfully")
	return nil
}

// waitForSOCKS waits for the SOCKS5 port to become available
func (t *SSHTunnel) waitForSOCKS(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", t.socksPort)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			sshLog.Debugf("SOCKS5 port %d is now available", t.socksPort)
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for SOCKS5 port %d", t.socksPort)
}

// monitor monitors the SSH process and handles cleanup
func (t *SSHTunnel) monitor() {
	defer close(t.stoppedCh)

	// Wait for SSH process to exit
	err := t.cmd.Wait()

	t.mu.Lock()
	t.running = false
	t.mu.Unlock()

	select {
	case <-t.stopCh:
		// Intentional stop
		sshLog.Info("SSH tunnel stopped")
	default:
		// Unexpected exit
		if err != nil {
			sshLog.Errorf("SSH tunnel exited unexpectedly: %v", err)
		} else {
			sshLog.Warn("SSH tunnel exited unexpectedly")
		}
	}
}

// Stop stops the SSH tunnel
func (t *SSHTunnel) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return nil
	}

	sshLog.Info("Stopping SSH tunnel")

	// Signal stop
	select {
	case <-t.stopCh:
		// Already stopped
	default:
		close(t.stopCh)
	}

	// Kill SSH process
	if t.cmd != nil && t.cmd.Process != nil {
		if err := t.cmd.Process.Kill(); err != nil {
			sshLog.Warnf("Failed to kill SSH process: %v", err)
		}
	}

	// Wait for monitor to finish (with timeout)
	select {
	case <-t.stoppedCh:
		sshLog.Debug("SSH tunnel stopped cleanly")
	case <-time.After(5 * time.Second):
		sshLog.Warn("Timeout waiting for SSH tunnel to stop")
	}

	// Clean up temporary SSH keys
	if t.keyPair != nil {
		if err := t.keyPair.Cleanup(); err != nil {
			sshLog.Warnf("Failed to cleanup temporary SSH keys: %v", err)
		}
		t.keyPair = nil
	}

	t.running = false
	return nil
}

// IsRunning returns whether the SSH tunnel is running
func (t *SSHTunnel) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.running
}

// SOCKSAddr returns the SOCKS5 proxy address
func (t *SSHTunnel) SOCKSAddr() string {
	return fmt.Sprintf("127.0.0.1:%d", t.socksPort)
}

// SOCKSPort returns the SOCKS5 proxy port
func (t *SSHTunnel) SOCKSPort() int {
	return t.socksPort
}

// TestConnection tests the SOCKS5 connection
func (t *SSHTunnel) TestConnection(ctx context.Context) error {
	if !t.IsRunning() {
		return fmt.Errorf("SSH tunnel is not running")
	}

	addr := t.SOCKSAddr()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to SOCKS5 proxy: %w", err)
	}
	defer conn.Close()

	return nil
}
