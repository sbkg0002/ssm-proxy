package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sbkg0002/ssm-proxy/internal/session"
	"github.com/spf13/cobra"
)

var (
	statusJSON       bool
	statusWatch      bool
	statusShowRoutes bool
	statusShowStats  bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of active proxy sessions",
	Long: `Display status of active proxy sessions including session details,
routing information, and traffic statistics.

Examples:
  # Show status
  ssm-proxy status

  # JSON output
  ssm-proxy status --json

  # Watch mode (refresh every 2s)
  ssm-proxy status --watch

  # Detailed output with routes and stats
  ssm-proxy status --show-routes --show-stats`,
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)

	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output in JSON format")
	statusCmd.Flags().BoolVarP(&statusWatch, "watch", "w", false, "Watch mode (refresh every 2s)")
	statusCmd.Flags().BoolVar(&statusShowRoutes, "show-routes", false, "Show routing table entries")
	statusCmd.Flags().BoolVar(&statusShowStats, "show-stats", false, "Show traffic statistics")
}

func runStatus(cmd *cobra.Command, args []string) error {
	if statusWatch {
		return runStatusWatch()
	}

	return displayStatus()
}

func runStatusWatch() error {
	// Clear screen and hide cursor
	fmt.Print("\033[2J")
	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h") // Show cursor on exit

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Display immediately
	fmt.Print("\033[H") // Move cursor to top
	if err := displayStatus(); err != nil {
		return err
	}

	for range ticker.C {
		fmt.Print("\033[H") // Move cursor to top
		if err := displayStatus(); err != nil {
			return err
		}
	}

	return nil
}

func displayStatus() error {
	sessionMgr := session.NewManager()
	sessions, err := sessionMgr.ListAll()
	if err != nil {
		return fmt.Errorf("failed to list sessions: %w", err)
	}

	if statusJSON {
		return displayStatusJSON(sessions)
	}

	return displayStatusTable(sessions)
}

func displayStatusJSON(sessions []*session.Session) error {
	type SessionJSON struct {
		Name          string    `json:"name"`
		InstanceID    string    `json:"instance_id"`
		Status        string    `json:"status"`
		TunDevice     string    `json:"tun_device"`
		TunIP         string    `json:"tun_ip"`
		CIDRBlocks    []string  `json:"cidr_blocks"`
		StartedAt     time.Time `json:"started_at"`
		UptimeSeconds int64     `json:"uptime_seconds"`
		PID           int       `json:"pid"`
	}

	output := struct {
		Sessions []SessionJSON `json:"sessions"`
	}{
		Sessions: make([]SessionJSON, len(sessions)),
	}

	for i, sess := range sessions {
		uptime := time.Since(sess.StartedAt)
		status := "active"
		if !isProcessRunning(sess.PID) {
			status = "stale"
		}

		output.Sessions[i] = SessionJSON{
			Name:          sess.Name,
			InstanceID:    sess.InstanceID,
			Status:        status,
			TunDevice:     sess.TunDevice,
			TunIP:         sess.TunIP,
			CIDRBlocks:    sess.CIDRBlocks,
			StartedAt:     sess.StartedAt,
			UptimeSeconds: int64(uptime.Seconds()),
			PID:           sess.PID,
		}
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func displayStatusTable(sessions []*session.Session) error {
	if len(sessions) == 0 {
		fmt.Println("No active sessions found")
		fmt.Println()
		fmt.Println("Start a session with:")
		fmt.Println("  sudo ssm-proxy start --instance-id i-xxx --cidr 10.0.0.0/8")
		return nil
	}

	fmt.Println()
	fmt.Println("ACTIVE SSM PROXY SESSIONS")
	fmt.Println()
	fmt.Println("SESSION       INSTANCE ID          STATUS    UTUN     CIDR BLOCKS           UPTIME")
	fmt.Println("────────────────────────────────────────────────────────────────────────────────────────")

	for _, sess := range sessions {
		uptime := formatUptime(time.Since(sess.StartedAt))
		status := "active"
		statusIcon := "✓"
		if !isProcessRunning(sess.PID) {
			status = "stale"
			statusIcon = "✗"
		}

		cidrDisplay := formatCIDRList(sess.CIDRBlocks)

		fmt.Printf("%-13s %-20s %s %-6s %-8s %-21s %s\n",
			truncate(sess.Name, 13),
			sess.InstanceID,
			statusIcon,
			status,
			sess.TunDevice,
			cidrDisplay,
			uptime,
		)
	}
	fmt.Println()

	// Show routing table if requested
	if statusShowRoutes {
		fmt.Println()
		fmt.Println("ROUTING TABLE (filtered for ssm-proxy):")
		if err := displayRoutes(); err != nil {
			log.Warnf("Failed to display routes: %v", err)
		}
		fmt.Println()
	}

	// Show statistics if requested
	if statusShowStats {
		fmt.Println()
		fmt.Println("TRAFFIC STATISTICS:")
		fmt.Println("(Statistics collection not yet implemented)")
		fmt.Println()
	}

	return nil
}

func displayRoutes() error {
	cmd := exec.Command("netstat", "-rn")
	output, err := cmd.Output()
	if err != nil {
		return err
	}

	lines := strings.Split(string(output), "\n")
	found := false

	for _, line := range lines {
		if strings.Contains(line, "utun") {
			if !found {
				fmt.Println("DESTINATION        GATEWAY          FLAGS    INTERFACE")
				fmt.Println("─────────────────────────────────────────────────────")
				found = true
			}
			// Clean up and display the route line
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				fmt.Printf("%-18s %-16s %-8s %s\n", fields[0], fields[1], fields[2], fields[len(fields)-1])
			}
		}
	}

	if !found {
		fmt.Println("No utun routes found")
	}

	return nil
}

func formatUptime(duration time.Duration) string {
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	} else if duration < time.Hour {
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	} else if duration < 24*time.Hour {
		hours := int(duration.Hours())
		minutes := int(duration.Minutes()) % 60
		return fmt.Sprintf("%dh %dm", hours, minutes)
	} else {
		days := int(duration.Hours()) / 24
		hours := int(duration.Hours()) % 24
		return fmt.Sprintf("%dd %dh", days, hours)
	}
}

func formatCIDRList(cidrs []string) string {
	if len(cidrs) == 0 {
		return ""
	}
	if len(cidrs) == 1 {
		return cidrs[0]
	}
	if len(cidrs) == 2 {
		return cidrs[0] + ", " + cidrs[1]
	}
	return fmt.Sprintf("%s, +%d", cidrs[0], len(cidrs)-1)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Send signal 0 to check if process exists
	err = process.Signal(os.Signal(nil))
	return err == nil
}
