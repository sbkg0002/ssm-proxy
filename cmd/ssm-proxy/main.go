package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/sirupsen/logrus"
)

var (
	// Version information (set at build time)
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	// Set up logging
	log := logrus.New()
	log.SetOutput(os.Stderr)
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	// Check platform
	if runtime.GOOS != "darwin" {
		log.Fatalf("Error: ssm-proxy currently only supports macOS (darwin)\nYour platform: %s", runtime.GOOS)
	}

	// Execute root command
	if err := Execute(version, commit, buildTime); err != nil {
		log.Fatal(err)
	}
}

// isRoot checks if the current process is running with root privileges
func isRoot() bool {
	return os.Geteuid() == 0
}

// requireRoot checks if running as root and exits with error if not
func requireRoot() {
	if !isRoot() {
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Fprintf(os.Stderr, "⚠️  Root privileges required\n")
		fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "This command requires root privileges to:\n")
		fmt.Fprintf(os.Stderr, "  • Create virtual network interface (utun device)\n")
		fmt.Fprintf(os.Stderr, "  • Modify system routing table\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "Please run with sudo:\n")
		fmt.Fprintf(os.Stderr, "  $ sudo ssm-proxy %s\n", os.Args[1])
		fmt.Fprintf(os.Stderr, "\n")
		os.Exit(1)
	}
}
