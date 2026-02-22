package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	versionShort bool
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long: `Display version and build information for ssm-proxy.

Examples:
  # Show full version information
  ssm-proxy version

  # Show only version number
  ssm-proxy version --short`,
	Run: runVersion,
}

func init() {
	rootCmd.AddCommand(versionCmd)

	versionCmd.Flags().BoolVar(&versionShort, "short", false, "Show only version number")
}

func runVersion(cmd *cobra.Command, args []string) {
	if versionShort {
		fmt.Println(version)
		return
	}

	fmt.Printf("ssm-proxy version %s\n", version)
	fmt.Printf("  Commit:     %s\n", commit)
	fmt.Printf("  Built:      %s\n", buildTime)
	fmt.Printf("  Go version: %s\n", runtime.Version())
	fmt.Printf("  Platform:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
}
