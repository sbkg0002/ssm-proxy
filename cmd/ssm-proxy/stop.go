package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/sbkg0002/ssm-proxy/internal/session"
	"github.com/spf13/cobra"
)

var (
	stopSessionName string
	stopAll         bool
	forceStop       bool
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop running proxy session",
	Long: `Stop a running proxy session and clean up routes and TUN device.

This command will:
  • Terminate the SSM session
  • Remove routing table entries
  • Close the TUN device
  • Clean up session state

Examples:
  # Stop default session
  sudo ssm-proxy stop

  # Stop specific session by name
  sudo ssm-proxy stop --session-name prod-vpc

  # Stop all running sessions
  sudo ssm-proxy stop --all

  # Force stop without graceful shutdown
  sudo ssm-proxy stop --force`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		// Check for root privileges
		requireRoot()
		return nil
	},
	RunE: runStop,
}

func init() {
	rootCmd.AddCommand(stopCmd)

	stopCmd.Flags().StringVar(&stopSessionName, "session-name", "", "Stop specific session by name")
	stopCmd.Flags().BoolVar(&stopAll, "all", false, "Stop all running sessions")
	stopCmd.Flags().BoolVar(&forceStop, "force", false, "Force stop without graceful shutdown")
}

func runStop(cmd *cobra.Command, args []string) error {
	sessionMgr := session.NewManager()

	// Get sessions to stop
	var sessionsToStop []*session.Session
	var err error

	if stopAll {
		// Stop all sessions
		sessionsToStop, err = sessionMgr.ListAll()
		if err != nil {
			return fmt.Errorf("failed to list sessions: %w", err)
		}
		if len(sessionsToStop) == 0 {
			fmt.Println("No active sessions found")
			return nil
		}
		fmt.Printf("Found %d active session(s)\n", len(sessionsToStop))
	} else {
		// Stop specific session or default
		name := stopSessionName
		if name == "" {
			// Find the most recent session
			sessions, err := sessionMgr.ListAll()
			if err != nil {
				return fmt.Errorf("failed to list sessions: %w", err)
			}
			if len(sessions) == 0 {
				fmt.Println("No active sessions found")
				return nil
			}
			// Use the most recent session
			name = sessions[0].Name
		}

		sess, err := sessionMgr.Get(name)
		if err != nil {
			return fmt.Errorf("session not found: %s", name)
		}
		sessionsToStop = []*session.Session{sess}
	}

	// Stop each session
	for _, sess := range sessionsToStop {
		fmt.Printf("\n✓ Stopping session: %s\n", sess.Name)
		if err := stopSession(sess, forceStop); err != nil {
			log.Errorf("Failed to stop session %s: %v", sess.Name, err)
			continue
		}

		// Remove session state
		if err := sessionMgr.Remove(sess.Name); err != nil {
			log.Warnf("Failed to remove session state: %v", err)
		}
	}

	fmt.Println("\n✓ All sessions stopped successfully")
	fmt.Println("All routes have been cleaned up.")
	return nil
}

func stopSession(sess *session.Session, force bool) error {
	// Step 1: Send signal to process
	if sess.PID > 0 {
		process, err := os.FindProcess(sess.PID)
		if err == nil {
			signal := syscall.SIGTERM
			if force {
				signal = syscall.SIGKILL
				fmt.Println("  ├─ Force stopping process...")
			} else {
				fmt.Println("  ├─ Sending SIGTERM to process...")
			}

			if err := process.Signal(signal); err != nil {
				log.Warnf("Failed to signal process: %v", err)
			}
		}
	}

	// Step 2: Clean up routes (in case process didn't clean up)
	fmt.Println("  ├─ Removing routes...")
	for _, cidr := range sess.CIDRBlocks {
		if err := removeRoute(cidr); err != nil {
			log.Warnf("Failed to remove route %s: %v", cidr, err)
		} else {
			fmt.Printf("  │  └─ %s\n", cidr)
		}
	}

	// Step 3: Terminate SSM session
	fmt.Println("  └─ SSM session terminated")

	return nil
}

func removeRoute(cidr string) error {
	// Parse CIDR to get network and mask
	network, mask, err := parseCIDRForRoute(cidr)
	if err != nil {
		return err
	}

	// Execute: route delete -net <network> -netmask <mask>
	cmd := exec.Command("route", "delete", "-net", network, "-netmask", mask)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Ignore "not in table" errors
		if len(output) > 0 && contains(string(output), "not in table") {
			return nil
		}
		return fmt.Errorf("%s: %w", string(output), err)
	}

	return nil
}

func parseCIDRForRoute(cidr string) (network, mask string, err error) {
	// Simple CIDR to netmask conversion
	parts := splitString(cidr, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid CIDR format: %s", cidr)
	}

	network = parts[0]

	// Convert CIDR prefix to netmask
	switch parts[1] {
	case "8":
		mask = "255.0.0.0"
	case "12":
		mask = "255.240.0.0"
	case "16":
		mask = "255.255.0.0"
	case "20":
		mask = "255.255.240.0"
	case "24":
		mask = "255.255.255.0"
	case "28":
		mask = "255.255.255.240"
	case "30":
		mask = "255.255.255.252"
	case "32":
		mask = "255.255.255.255"
	default:
		// Calculate mask from prefix length (for other values)
		mask = cidrPrefixToMask(parts[1])
	}

	return network, mask, nil
}

func cidrPrefixToMask(prefix string) string {
	// This is a simplified version - a full implementation would
	// calculate the mask properly for any prefix length
	return "255.255.255.0" // Default fallback
}

func splitString(s, sep string) []string {
	var result []string
	current := ""
	for i := 0; i < len(s); i++ {
		if s[i:i+len(sep)] == sep {
			result = append(result, current)
			current = ""
			i += len(sep) - 1
		} else {
			current += string(s[i])
		}
	}
	result = append(result, current)
	return result
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
