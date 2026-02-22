package ssm

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	awsclient "github.com/sbkg0002/ssm-proxy/internal/aws"
)

// Client represents an SSM client for managing sessions
type Client struct {
	awsClient  *awsclient.Client
	ssmClient  *ssm.Client
	instanceID string
}

// Session represents an active SSM session
type Session struct {
	sessionID  string
	instanceID string
	client     *Client
	reader     io.Reader
	writer     io.Writer
	closed     bool
	mu         sync.Mutex
	startTime  time.Time
	lastActive time.Time
}

// NewClient creates a new SSM client for the specified instance
func NewClient(ctx context.Context, awsClient *awsclient.Client, instanceID string) (*Client, error) {
	return &Client{
		awsClient:  awsClient,
		ssmClient:  awsClient.SSMClient(),
		instanceID: instanceID,
	}, nil
}

// StartSession starts a new SSM session to the instance
func (c *Client) StartSession(ctx context.Context, name string) (*Session, error) {
	// Start SSM session using AWS-StartInteractiveCommand document
	input := &ssm.StartSessionInput{
		Target:       aws.String(c.instanceID),
		DocumentName: aws.String("AWS-StartInteractiveCommand"),
		Parameters: map[string][]string{
			"command": {"sh"}, // Start a shell for packet forwarding
		},
	}

	result, err := c.ssmClient.StartSession(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to start SSM session: %w", err)
	}

	sessionID := aws.ToString(result.SessionId)
	if sessionID == "" {
		return nil, fmt.Errorf("received empty session ID from SSM")
	}

	// Create session object
	session := &Session{
		sessionID:  sessionID,
		instanceID: c.instanceID,
		client:     c,
		closed:     false,
		startTime:  time.Now(),
		lastActive: time.Now(),
	}

	// TODO: Establish WebSocket connection to SSM service
	// This is a placeholder - real implementation would:
	// 1. Connect to wss://ssmmessages.{region}.amazonaws.com/v1/data-channel/{sessionId}
	// 2. Authenticate with AWS SigV4
	// 3. Open bidirectional WebSocket for data transfer

	// For now, create placeholder reader/writer
	session.reader = &placeholderReader{}
	session.writer = &placeholderWriter{}

	return session, nil
}

// SessionID returns the SSM session ID
func (s *Session) SessionID() string {
	return s.sessionID
}

// InstanceID returns the EC2 instance ID
func (s *Session) InstanceID() string {
	return s.instanceID
}

// Read reads data from the SSM session
func (s *Session) Read(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, io.EOF
	}
	s.mu.Unlock()

	n, err := s.reader.Read(p)
	if err == nil {
		s.lastActive = time.Now()
	}
	return n, err
}

// Write writes data to the SSM session
func (s *Session) Write(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, fmt.Errorf("session is closed")
	}
	s.mu.Unlock()

	n, err := s.writer.Write(p)
	if err == nil {
		s.lastActive = time.Now()
	}
	return n, err
}

// Reader returns the session's reader
func (s *Session) Reader() io.Reader {
	return s
}

// Writer returns the session's writer
func (s *Session) Writer() io.Writer {
	return s
}

// IsHealthy checks if the session is still healthy
func (s *Session) IsHealthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}

	// Check if session has been active recently (within 2 minutes)
	return time.Since(s.lastActive) < 2*time.Minute
}

// Close closes the SSM session
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	// Terminate the SSM session
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	input := &ssm.TerminateSessionInput{
		SessionId: aws.String(s.sessionID),
	}

	_, err := s.client.ssmClient.TerminateSession(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to terminate SSM session: %w", err)
	}

	return nil
}

// Uptime returns how long the session has been running
func (s *Session) Uptime() time.Duration {
	return time.Since(s.startTime)
}

// LastActive returns when the session was last active
func (s *Session) LastActive() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActive
}

// placeholderReader is a placeholder for actual SSM WebSocket reader
// TODO: Replace with real implementation that reads from SSM WebSocket
type placeholderReader struct{}

func (r *placeholderReader) Read(p []byte) (int, error) {
	// Block indefinitely - real implementation would read from WebSocket
	time.Sleep(100 * time.Millisecond)
	return 0, io.EOF
}

// placeholderWriter is a placeholder for actual SSM WebSocket writer
// TODO: Replace with real implementation that writes to SSM WebSocket
type placeholderWriter struct{}

func (w *placeholderWriter) Write(p []byte) (int, error) {
	// Real implementation would write to WebSocket
	// For now, just pretend we wrote the data
	return len(p), nil
}

// EncapsulatePacket wraps an IP packet with protocol framing for transmission
func EncapsulatePacket(packet []byte) []byte {
	// Protocol format:
	// [4 bytes: magic] [4 bytes: length] [N bytes: packet]
	const magicNumber uint32 = 0x53534D50 // "SSMP" in hex

	header := make([]byte, 8)
	// Write magic number (big-endian)
	header[0] = byte((magicNumber >> 24) & 0xFF)
	header[1] = byte((magicNumber >> 16) & 0xFF)
	header[2] = byte((magicNumber >> 8) & 0xFF)
	header[3] = byte(magicNumber & 0xFF)
	// Write length (big-endian)
	length := uint32(len(packet))
	header[4] = byte((length >> 24) & 0xFF)
	header[5] = byte((length >> 16) & 0xFF)
	header[6] = byte((length >> 8) & 0xFF)
	header[7] = byte(length & 0xFF)

	// Combine header and packet
	frame := make([]byte, len(header)+len(packet))
	copy(frame, header)
	copy(frame[8:], packet)

	return frame
}

// DecapsulatePacket extracts an IP packet from protocol framing
func DecapsulatePacket(reader io.Reader) ([]byte, error) {
	const magicNumber uint32 = 0x53534D50 // "SSMP" in hex

	// Read header (8 bytes)
	header := make([]byte, 8)
	_, err := io.ReadFull(reader, header)
	if err != nil {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	// Verify magic number
	magic := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	if magic != magicNumber {
		return nil, fmt.Errorf("invalid magic number: 0x%x", magic)
	}

	// Read length
	length := uint32(header[4])<<24 | uint32(header[5])<<16 | uint32(header[6])<<8 | uint32(header[7])
	if length > 65535 {
		return nil, fmt.Errorf("packet too large: %d bytes", length)
	}

	// Read packet data
	packet := make([]byte, length)
	_, err = io.ReadFull(reader, packet)
	if err != nil {
		return nil, fmt.Errorf("failed to read packet: %w", err)
	}

	return packet, nil
}
