package tunnel

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
	"golang.org/x/crypto/ssh"
)

// SSHKeyPair represents a temporary SSH key pair
type SSHKeyPair struct {
	PrivateKeyPath string
	PublicKey      string
	tempDir        string
}

// GenerateTemporarySSHKey generates a temporary SSH key pair
func GenerateTemporarySSHKey() (*SSHKeyPair, error) {
	// Create temporary directory for keys
	tempDir, err := os.MkdirTemp("", "ssm-proxy-ssh-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Generate RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	// Encode private key to PEM format
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}

	// Write private key to file
	privateKeyPath := filepath.Join(tempDir, "id_rsa")
	privateKeyFile, err := os.OpenFile(privateKeyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to create private key file: %w", err)
	}

	if err := pem.Encode(privateKeyFile, privateKeyPEM); err != nil {
		privateKeyFile.Close()
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to write private key: %w", err)
	}
	privateKeyFile.Close()

	// Generate OpenSSH public key
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to generate public key: %w", err)
	}

	publicKeyString := string(ssh.MarshalAuthorizedKey(publicKey))

	sshLog.Debugf("Generated temporary SSH key pair in %s", tempDir)

	return &SSHKeyPair{
		PrivateKeyPath: privateKeyPath,
		PublicKey:      publicKeyString,
		tempDir:        tempDir,
	}, nil
}

// Cleanup removes temporary key files
func (k *SSHKeyPair) Cleanup() error {
	if k.tempDir != "" {
		sshLog.Debugf("Cleaning up temporary SSH keys: %s", k.tempDir)
		return os.RemoveAll(k.tempDir)
	}
	return nil
}

// SendSSHPublicKeyToInstance sends the SSH public key to an EC2 instance using Instance Connect
func SendSSHPublicKeyToInstance(cfg aws.Config, instanceID, availabilityZone, osUser, publicKey string) error {
	client := ec2instanceconnect.NewFromConfig(cfg)

	input := &ec2instanceconnect.SendSSHPublicKeyInput{
		InstanceId:       aws.String(instanceID),
		InstanceOSUser:   aws.String(osUser),
		SSHPublicKey:     aws.String(publicKey),
		AvailabilityZone: aws.String(availabilityZone),
	}

	sshLog.Infof("Sending temporary SSH public key to instance %s (user: %s, az: %s)", instanceID, osUser, availabilityZone)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.SendSSHPublicKey(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to send SSH public key via Instance Connect: %w\n\nTroubleshooting:\n"+
			"  1. Verify instance supports EC2 Instance Connect:\n"+
			"     - Amazon Linux 2 (2.0.20190618 or later)\n"+
			"     - Ubuntu 16.04 or later\n"+
			"  2. Check IAM permissions:\n"+
			"     - ec2-instance-connect:SendSSHPublicKey\n"+
			"  3. Verify Instance Connect is installed on instance:\n"+
			"     sudo yum install ec2-instance-connect  # Amazon Linux\n"+
			"     sudo apt install ec2-instance-connect  # Ubuntu\n"+
			"  4. Alternative: Add your SSH key manually:\n"+
			"     aws ec2 describe-key-pairs\n"+
			"     ssh-keygen -t rsa -b 4096\n"+
			"     # Add ~/.ssh/id_rsa.pub to instance", err)
	}

	sshLog.Info("Successfully sent SSH public key to instance (valid for 60 seconds)")
	return nil
}

// CheckExistingSSHKey checks if user has an existing SSH key
func CheckExistingSSHKey() (string, bool) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}

	// Check common SSH key locations
	keyPaths := []string{
		filepath.Join(homeDir, ".ssh", "id_rsa"),
		filepath.Join(homeDir, ".ssh", "id_ed25519"),
		filepath.Join(homeDir, ".ssh", "id_ecdsa"),
	}

	for _, keyPath := range keyPaths {
		if _, err := os.Stat(keyPath); err == nil {
			sshLog.Debugf("Found existing SSH key: %s", keyPath)
			return keyPath, true
		}
	}

	return "", false
}

// GetSSHPublicKeyFromPrivate reads the public key from a private key file
func GetSSHPublicKeyFromPrivate(privateKeyPath string) (string, error) {
	privateKeyBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return "", fmt.Errorf("failed to read private key: %w", err)
	}

	// Use ssh.ParsePrivateKey which handles OpenSSH, PEM RSA, and PEM PKCS8 formats
	signer, err := ssh.ParsePrivateKey(privateKeyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key (supported formats: OpenSSH, PEM RSA, PEM PKCS8): %w", err)
	}

	// Get public key from the signer
	publicKey := signer.PublicKey()

	return string(ssh.MarshalAuthorizedKey(publicKey)), nil
}
