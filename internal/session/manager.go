package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Session represents an active SSM proxy session
type Session struct {
	Name       string    `json:"name"`
	InstanceID string    `json:"instance_id"`
	SessionID  string    `json:"session_id"`
	TunDevice  string    `json:"tun_device"`
	TunIP      string    `json:"tun_ip"`
	CIDRBlocks []string  `json:"cidr_blocks"`
	StartedAt  time.Time `json:"started_at"`
	PID        int       `json:"pid"`
}

// Manager manages session state persistence
type Manager struct {
	stateDir string
	mu       sync.RWMutex
}

// NewManager creates a new session manager
func NewManager() *Manager {
	return &Manager{
		stateDir: getStateDir(),
	}
}

// Save saves a session to disk
func (m *Manager) Save(sess *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Ensure state directory exists
	if err := os.MkdirAll(m.stateDir, 0700); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Serialize session to JSON
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	// Write to file
	filename := filepath.Join(m.stateDir, sess.Name+".json")
	if err := os.WriteFile(filename, data, 0600); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	return nil
}

// Get retrieves a session by name
func (m *Manager) Get(name string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filename := filepath.Join(m.stateDir, name+".json")

	// Read file
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s", name)
		}
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	// Deserialize
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session: %w", err)
	}

	return &sess, nil
}

// ListAll lists all active sessions
func (m *Manager) ListAll() ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Ensure state directory exists
	if err := os.MkdirAll(m.stateDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	// Read directory
	entries, err := os.ReadDir(m.stateDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read state directory: %w", err)
	}

	var sessions []*Session
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Skip non-JSON files
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		// Read and parse session file
		filename := filepath.Join(m.stateDir, entry.Name())
		data, err := os.ReadFile(filename)
		if err != nil {
			continue // Skip files we can't read
		}

		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue // Skip files we can't parse
		}

		sessions = append(sessions, &sess)
	}

	// Sort by start time (most recent first)
	sortSessionsByStartTime(sessions)

	return sessions, nil
}

// Remove removes a session from disk
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filename := filepath.Join(m.stateDir, name+".json")

	// Remove file
	if err := os.Remove(filename); err != nil {
		if os.IsNotExist(err) {
			return nil // Already removed
		}
		return fmt.Errorf("failed to remove session file: %w", err)
	}

	return nil
}

// RemoveStale removes sessions for processes that are no longer running
func (m *Manager) RemoveStale() ([]string, error) {
	sessions, err := m.ListAll()
	if err != nil {
		return nil, err
	}

	var removed []string
	for _, sess := range sessions {
		// Check if process is still running
		if !isProcessRunning(sess.PID) {
			if err := m.Remove(sess.Name); err == nil {
				removed = append(removed, sess.Name)
			}
		}
	}

	return removed, nil
}

// Exists checks if a session exists
func (m *Manager) Exists(name string) bool {
	filename := filepath.Join(m.stateDir, name+".json")
	_, err := os.Stat(filename)
	return err == nil
}

// Count returns the number of active sessions
func (m *Manager) Count() (int, error) {
	sessions, err := m.ListAll()
	if err != nil {
		return 0, err
	}
	return len(sessions), nil
}

// getStateDir returns the directory where session state is stored
func getStateDir() string {
	// Try to use ~/.ssm-proxy/sessions
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback to /tmp if can't get home dir
		return "/tmp/ssm-proxy/sessions"
	}

	return filepath.Join(home, ".ssm-proxy", "sessions")
}

// isProcessRunning checks if a process with the given PID is running
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Send signal 0 to check if process exists
	// This doesn't actually send a signal, just checks if we can
	err = process.Signal(os.Signal(nil))
	return err == nil
}

// sortSessionsByStartTime sorts sessions by start time (most recent first)
func sortSessionsByStartTime(sessions []*Session) {
	// Simple bubble sort (fine for small number of sessions)
	n := len(sessions)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if sessions[j].StartedAt.Before(sessions[j+1].StartedAt) {
				sessions[j], sessions[j+1] = sessions[j+1], sessions[j]
			}
		}
	}
}
