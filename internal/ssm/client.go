package ssm

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/gorilla/websocket"
	awsclient "github.com/sbkg0002/ssm-proxy/internal/aws"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

// Session Manager protocol constants
const (
	MessageSchemaVersion = "1.0"

	// Message types
	MessageTypeInputStreamData   = "input_stream_data"
	MessageTypeOutputStreamData  = "output_stream_data"
	MessageTypeAgentSessionState = "agent_session_state"
	MessageTypeChannelClosed     = "channel_closed"
	MessageTypeAcknowledge       = "acknowledge"

	// Session states
	SessionStateConnected   = "Connected"
	SessionStateTerminating = "Terminating"
	SessionStateTerminated  = "Terminated"
)

// Client represents an SSM client for managing sessions
type Client struct {
	awsClient  *awsclient.Client
	ssmClient  *ssm.Client
	instanceID string
	region     string
}

// Session represents an active SSM session with WebSocket connection
type Session struct {
	sessionID   string
	instanceID  string
	tokenValue  string
	streamURL   string
	client      *Client
	conn        *websocket.Conn
	closed      atomic.Bool
	startTime   time.Time
	lastActive  time.Time
	sequenceNum atomic.Int64
	readChan    chan []byte
	writeChan   chan []byte
	errorChan   chan error
	closeChan   chan struct{}
	mu          sync.RWMutex
}

// SessionMessage represents a Session Manager protocol message
type SessionMessage struct {
	MessageSchemaVersion string                 `json:"MessageSchemaVersion"`
	MessageType          string                 `json:"MessageType"`
	MessageId            string                 `json:"MessageId,omitempty"`
	SequenceNumber       int64                  `json:"SequenceNumber"`
	Flags                int64                  `json:"Flags"`
	Payload              string                 `json:"Payload,omitempty"`
	PayloadType          int                    `json:"PayloadType,omitempty"`
	CreatedDate          string                 `json:"CreatedDate,omitempty"`
	Content              map[string]interface{} `json:"Content,omitempty"`
}

// NewClient creates a new SSM client for the specified instance
func NewClient(ctx context.Context, awsClient *awsclient.Client, instanceID string) (*Client, error) {
	return &Client{
		awsClient:  awsClient,
		ssmClient:  awsClient.SSMClient(),
		instanceID: instanceID,
		region:     awsClient.Region(),
	}, nil
}

// StartSession starts a new SSM session and establishes WebSocket connection
func (c *Client) StartSession(ctx context.Context, name string) (*Session, error) {
	// Start SSM session using AWS-StartInteractiveCommand
	input := &ssm.StartSessionInput{
		Target:       aws.String(c.instanceID),
		DocumentName: aws.String("AWS-StartInteractiveCommand"),
		Parameters: map[string][]string{
			"command": {"bash"}, // Start bash for packet forwarding
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

	tokenValue := aws.ToString(result.TokenValue)
	streamURL := aws.ToString(result.StreamUrl)

	if streamURL == "" {
		streamURL = fmt.Sprintf("wss://ssmmessages.%s.amazonaws.com/v1/data-channel/%s?role=publish_subscribe",
			c.region, sessionID)
	}

	log.WithFields(logrus.Fields{
		"session_id": sessionID,
		"stream_url": streamURL,
	}).Debug("SSM session started")

	// Create session object
	session := &Session{
		sessionID:  sessionID,
		instanceID: c.instanceID,
		tokenValue: tokenValue,
		streamURL:  streamURL,
		client:     c,
		startTime:  time.Now(),
		lastActive: time.Now(),
		readChan:   make(chan []byte, 100),
		writeChan:  make(chan []byte, 100),
		errorChan:  make(chan error, 10),
		closeChan:  make(chan struct{}),
	}

	// Establish WebSocket connection with SigV4 authentication
	if err := session.connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect WebSocket: %w", err)
	}

	// Send opening handshake with token
	if err := session.sendOpeningHandshake(); err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to send opening handshake: %w", err)
	}

	// Start message processing goroutines
	go session.readLoop()
	go session.writeLoop()

	log.Info("SSM session WebSocket connected successfully")

	return session, nil
}

// connect establishes WebSocket connection with AWS SigV4 authentication
func (s *Session) connect(ctx context.Context) error {
	// Parse the stream URL
	streamURL, err := url.Parse(s.streamURL)
	if err != nil {
		return fmt.Errorf("invalid stream URL: %w", err)
	}

	// Create HTTP request for WebSocket upgrade
	req := &http.Request{
		Method: "GET",
		URL:    streamURL,
		Header: make(http.Header),
	}

	// Get AWS credentials
	creds, err := s.client.awsClient.Config().Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve AWS credentials: %w", err)
	}

	// Sign the request with AWS SigV4
	signer := v4.NewSigner()
	payloadHash := sha256.Sum256([]byte{})

	err = signer.SignHTTP(ctx, creds, req, hex.EncodeToString(payloadHash[:]),
		"ssmmessages", s.client.region, time.Now())
	if err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}

	// Create WebSocket dialer
	dialer := websocket.Dialer{
		HandshakeTimeout: 45 * time.Second,
	}

	// Connect WebSocket
	conn, resp, err := dialer.DialContext(ctx, s.streamURL, req.Header)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			log.Errorf("WebSocket dial failed: status=%d, body=%s", resp.StatusCode, string(body))
		}
		return fmt.Errorf("failed to dial WebSocket: %w", err)
	}

	s.conn = conn
	log.Debug("WebSocket connection established")

	return nil
}

// sendOpeningHandshake sends the initial handshake message with the token
// AWS Session Manager requires an opening handshake to establish the data channel
func (s *Session) sendOpeningHandshake() error {
	log.WithFields(logrus.Fields{
		"session_id": s.sessionID,
		"has_token":  s.tokenValue != "",
	}).Debug("Sending opening handshake")

	// AWS Session Manager protocol expects the token in a channel_open request
	// The token must be in the Content field for the data channel to be established
	handshake := SessionMessage{
		MessageSchemaVersion: MessageSchemaVersion,
		MessageType:          "input_stream_data",
		SequenceNumber:       0,
		Flags:                3, // SYN flag to open channel
		Content: map[string]interface{}{
			"TokenValue": s.tokenValue,
		},
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(handshake)
	if err != nil {
		return fmt.Errorf("failed to marshal handshake: %w", err)
	}

	log.Debugf("Sending handshake message with token in Content field")

	// Send handshake message
	if err := s.conn.WriteMessage(websocket.TextMessage, jsonData); err != nil {
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	log.Debug("Opening handshake sent, waiting for acknowledgment...")

	// Wait a bit for the handshake to be processed
	// The server should respond with an acknowledgment
	time.Sleep(200 * time.Millisecond)

	return nil
}

// readLoop continuously reads messages from WebSocket
func (s *Session) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Panic in readLoop: %v", r)
		}
	}()

	for {
		select {
		case <-s.closeChan:
			return
		default:
		}

		if s.closed.Load() {
			return
		}

		// Read message from WebSocket
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Errorf("WebSocket read error: %v", err)
				s.errorChan <- err
			}
			return
		}

		// Parse Session Manager message
		var msg SessionMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Errorf("Failed to parse message: %v", err)
			continue
		}

		s.lastActive = time.Now()

		// Handle different message types
		switch msg.MessageType {
		case MessageTypeOutputStreamData:
			// Decode payload and send to read channel
			if msg.Payload != "" {
				data, err := base64.StdEncoding.DecodeString(msg.Payload)
				if err != nil {
					log.Errorf("Failed to decode payload: %v", err)
					continue
				}

				// Skip empty packets
				if len(data) > 0 {
					select {
					case s.readChan <- data:
					case <-s.closeChan:
						return
					default:
						log.Warn("Read channel full, dropping packet")
					}
				}
			}

		case MessageTypeAgentSessionState:
			// Log session state changes
			if content, ok := msg.Content["SessionState"].(string); ok {
				log.Debugf("Session state: %s", content)
				if content == SessionStateTerminated || content == SessionStateTerminating {
					return
				}
			}

		case MessageTypeChannelClosed:
			log.Info("Channel closed by remote")
			return

		case MessageTypeAcknowledge:
			// Acknowledgment received
			log.Debugf("Received acknowledge for sequence %d", msg.SequenceNumber)
			// Check if this is the handshake acknowledgment (sequence 0)
			if msg.SequenceNumber == 0 {
				log.Info("Handshake acknowledged by server")
			}

		default:
			log.Debugf("Unhandled message type: %s", msg.MessageType)
		}
	}
}

// writeLoop continuously writes messages to WebSocket
func (s *Session) writeLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Panic in writeLoop: %v", r)
		}
	}()

	for {
		select {
		case <-s.closeChan:
			return
		case data := <-s.writeChan:
			if s.closed.Load() {
				return
			}

			// Create Session Manager message
			seqNum := s.sequenceNum.Add(1)
			msg := SessionMessage{
				MessageSchemaVersion: MessageSchemaVersion,
				MessageType:          MessageTypeInputStreamData,
				SequenceNumber:       seqNum,
				Flags:                0,
				Payload:              base64.StdEncoding.EncodeToString(data),
				PayloadType:          1,
			}

			log.Debugf("Sending packet: seq=%d, size=%d bytes", seqNum, len(data))

			// Marshal to JSON
			jsonData, err := json.Marshal(msg)
			if err != nil {
				log.Errorf("Failed to marshal message: %v", err)
				continue
			}

			// Write to WebSocket
			if err := s.conn.WriteMessage(websocket.TextMessage, jsonData); err != nil {
				log.Errorf("WebSocket write error: %v", err)
				s.errorChan <- err
				return
			}

			s.lastActive = time.Now()
		}
	}
}

// Read reads data from the SSM session
func (s *Session) Read(p []byte) (int, error) {
	if s.closed.Load() {
		return 0, io.EOF
	}

	select {
	case data := <-s.readChan:
		n := copy(p, data)
		return n, nil
	case err := <-s.errorChan:
		return 0, err
	case <-s.closeChan:
		return 0, io.EOF
	case <-time.After(100 * time.Millisecond):
		// Timeout to prevent blocking indefinitely
		return 0, nil
	}
}

// Write writes data to the SSM session
func (s *Session) Write(p []byte) (int, error) {
	if s.closed.Load() {
		return 0, fmt.Errorf("session is closed")
	}

	// Make a copy of the data
	data := make([]byte, len(p))
	copy(data, p)

	select {
	case s.writeChan <- data:
		return len(p), nil
	case <-s.closeChan:
		return 0, fmt.Errorf("session is closed")
	case <-time.After(5 * time.Second):
		return 0, fmt.Errorf("write timeout")
	}
}

// Reader returns the session's reader
func (s *Session) Reader() io.Reader {
	return s
}

// Writer returns the session's writer
func (s *Session) Writer() io.Writer {
	return s
}

// SessionID returns the SSM session ID
func (s *Session) SessionID() string {
	return s.sessionID
}

// InstanceID returns the EC2 instance ID
func (s *Session) InstanceID() string {
	return s.instanceID
}

// IsHealthy checks if the session is still healthy
func (s *Session) IsHealthy() bool {
	if s.closed.Load() {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check if session has been active recently (within 2 minutes)
	return time.Since(s.lastActive) < 2*time.Minute
}

// Close closes the SSM session
func (s *Session) Close() error {
	if s.closed.Swap(true) {
		return nil // Already closed
	}

	log.Info("Closing SSM session")

	// Signal close to goroutines
	close(s.closeChan)

	// Close WebSocket connection
	if s.conn != nil {
		// Send close message
		err := s.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		if err != nil {
			log.Warnf("Failed to send close message: %v", err)
		}

		s.conn.Close()
	}

	// Terminate the SSM session
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	input := &ssm.TerminateSessionInput{
		SessionId: aws.String(s.sessionID),
	}

	_, err := s.client.ssmClient.TerminateSession(ctx, input)
	if err != nil {
		log.Warnf("Failed to terminate SSM session: %v", err)
	}

	log.Info("SSM session closed")
	return nil
}

// Uptime returns how long the session has been running
func (s *Session) Uptime() time.Duration {
	return time.Since(s.startTime)
}

// LastActive returns when the session was last active
func (s *Session) LastActive() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastActive
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
