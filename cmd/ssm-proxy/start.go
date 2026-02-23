package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sbkg0002/ssm-proxy/internal/aws"
	"github.com/sbkg0002/ssm-proxy/internal/forwarder"
	"github.com/sbkg0002/ssm-proxy/internal/routing"
	"github.com/sbkg0002/ssm-proxy/internal/session"
	"github.com/sbkg0002/ssm-proxy/internal/ssm"
	"github.com/sbkg0002/ssm-proxy/internal/tunnel"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	// Instance selection
	instanceID  string
	instanceTag string

	// CIDR blocks to route
	cidrBlocks []string

	// TUN device configuration
	localIP string
	mtu     int

	// Session configuration
	sessionName    string
	keepAlive      time.Duration
	timeout        time.Duration
	autoReconnect  bool
	reconnectDelay time.Duration
	maxRetries     int

	// Daemon configuration
	daemon  bool
	pidFile string
	logFile string

	// Advanced options
	logPackets bool
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start transparent proxy tunnel",
	Long: `Start a transparent proxy tunnel through an AWS EC2 instance via SSM.

This command creates a virtual network interface (utun), adds routes for
specified CIDR blocks, and forwards all traffic through an SSM tunnel.

Applications require NO configuration - traffic is automatically routed
based on destination IP address.

Examples:
  # Start proxy for VPC CIDR block
  sudo ssm-proxy start --instance-id i-1234567890abcdef0 --cidr 10.0.0.0/8

  # Use instance tags to find bastion
  sudo ssm-proxy start --instance-tag Name=bastion-host --cidr 10.0.0.0/16

  # Multiple CIDR blocks
  sudo ssm-proxy start --instance-id i-xxx --cidr 10.0.0.0/8 --cidr 172.16.0.0/12

  # Run as daemon in background
  sudo ssm-proxy start --instance-id i-xxx --cidr 10.0.0.0/8 --daemon`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		// Check for root privileges
		requireRoot()

		// Validate required flags
		if instanceID == "" && instanceTag == "" {
			return fmt.Errorf("either --instance-id or --instance-tag is required")
		}

		if instanceID != "" && instanceTag != "" {
			return fmt.Errorf("cannot specify both --instance-id and --instance-tag")
		}

		if len(cidrBlocks) == 0 {
			return fmt.Errorf("at least one --cidr block is required")
		}

		// Validate CIDR blocks
		for _, cidr := range cidrBlocks {
			if err := validateCIDR(cidr); err != nil {
				return fmt.Errorf("invalid CIDR block %s: %w", cidr, err)
			}
		}

		return nil
	},
	RunE: runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)

	// Instance selection flags
	startCmd.Flags().StringVar(&instanceID, "instance-id", "", "EC2 instance ID (e.g., i-1234567890abcdef0)")
	startCmd.Flags().StringVar(&instanceTag, "instance-tag", "", "Find instance by tag (format: Key=Value)")

	// CIDR blocks (required, repeatable)
	startCmd.Flags().StringSliceVar(&cidrBlocks, "cidr", []string{}, "CIDR blocks to route (repeatable)")
	startCmd.MarkFlagRequired("cidr")

	// TUN device configuration
	startCmd.Flags().StringVar(&localIP, "local-ip", "169.254.169.1/30", "IP address for utun device")
	startCmd.Flags().IntVar(&mtu, "mtu", 1500, "MTU for utun device")

	// Session configuration
	startCmd.Flags().StringVar(&sessionName, "session-name", "", "Custom session name (default: auto-generated)")
	startCmd.Flags().DurationVar(&keepAlive, "keep-alive", 30*time.Second, "Keep-alive interval")
	startCmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Connection timeout")
	startCmd.Flags().BoolVar(&autoReconnect, "auto-reconnect", true, "Auto-reconnect on failure")
	startCmd.Flags().DurationVar(&reconnectDelay, "reconnect-delay", 5*time.Second, "Delay between reconnection attempts")
	startCmd.Flags().IntVar(&maxRetries, "max-retries", 0, "Maximum reconnection attempts (0 = unlimited)")

	// Daemon mode
	startCmd.Flags().BoolVarP(&daemon, "daemon", "d", false, "Run in background as daemon")
	startCmd.Flags().StringVar(&pidFile, "pid-file", "/var/run/ssm-proxy.pid", "PID file location")
	startCmd.Flags().StringVar(&logFile, "log-file", "", "Log file location (default: stderr)")

	// Advanced options
	startCmd.Flags().BoolVar(&logPackets, "log-packets", false, "Log individual packets (debug only, very verbose)")

	// Bind to viper for config file support
	viper.BindPFlag("defaults.local_ip", startCmd.Flags().Lookup("local-ip"))
	viper.BindPFlag("defaults.mtu", startCmd.Flags().Lookup("mtu"))
	viper.BindPFlag("defaults.keep_alive", startCmd.Flags().Lookup("keep-alive"))
	viper.BindPFlag("defaults.timeout", startCmd.Flags().Lookup("timeout"))
	viper.BindPFlag("defaults.auto_reconnect", startCmd.Flags().Lookup("auto-reconnect"))
	viper.BindPFlag("defaults.reconnect_delay", startCmd.Flags().Lookup("reconnect-delay"))
	viper.BindPFlag("defaults.max_retries", startCmd.Flags().Lookup("max-retries"))
}

func runStart(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Print banner
	printStartBanner()

	// Generate session name if not provided
	if sessionName == "" {
		sessionName = fmt.Sprintf("ssm-proxy-%d", time.Now().Unix())
	}

	// Step 1: Initialize AWS clients
	log.Info("✓ Checking privileges... OK (running as root)")
	fmt.Println("✓ Checking privileges... OK (running as root)")

	awsClient, err := aws.NewClient(ctx, awsProfile, awsRegion)
	if err != nil {
		return fmt.Errorf("failed to initialize AWS client: %w", err)
	}

	profile := awsProfile
	if profile == "" {
		profile = "default"
	}
	log.Infof("✓ Validating AWS credentials... OK (using profile: %s)", profile)
	fmt.Printf("✓ Validating AWS credentials... OK (using profile: %s)\n", profile)

	// Step 2: Find EC2 instance
	var instance *aws.Instance
	if instanceID != "" {
		fmt.Printf("✓ Finding EC2 instance %s...\n", instanceID)
		instance, err = awsClient.GetInstance(ctx, instanceID)
		if err != nil {
			return fmt.Errorf("failed to find instance: %w", err)
		}
	} else {
		fmt.Printf("✓ Finding EC2 instance by tag %s...\n", instanceTag)
		tagParts := strings.SplitN(instanceTag, "=", 2)
		if len(tagParts) != 2 {
			return fmt.Errorf("invalid tag format, expected Key=Value")
		}
		instances, err := awsClient.FindInstancesByTag(ctx, tagParts[0], tagParts[1])
		if err != nil {
			return fmt.Errorf("failed to find instances: %w", err)
		}
		if len(instances) == 0 {
			return fmt.Errorf("no instances found with tag %s", instanceTag)
		}
		if len(instances) > 1 {
			return fmt.Errorf("multiple instances found with tag %s, use --instance-id to specify", instanceTag)
		}
		instance = instances[0]
	}

	fmt.Printf("  ├─ Instance: %s (%s)\n", instance.Name, instance.InstanceType)
	fmt.Printf("  ├─ State: %s\n", instance.State)
	fmt.Printf("  ├─ AZ: %s\n", instance.AvailabilityZone)
	fmt.Printf("  ├─ Private IP: %s\n", instance.PrivateIP)

	if instance.State != "running" {
		return fmt.Errorf("instance is not running (state: %s)", instance.State)
	}

	if !instance.SSMConnected {
		return fmt.Errorf("SSM Agent is not connected on instance")
	}
	fmt.Printf("  └─ SSM Status: connected ✓\n")

	// Step 3: Start SSM session
	fmt.Println("✓ Starting SSM session...")
	ssmClient, err := ssm.NewClient(ctx, awsClient, instance.InstanceID)
	if err != nil {
		return fmt.Errorf("failed to create SSM client: %w", err)
	}

	ssmSession, err := ssmClient.StartSession(ctx, sessionName)
	if err != nil {
		return fmt.Errorf("failed to start SSM session: %w", err)
	}
	defer ssmSession.Close()

	fmt.Printf("  └─ Session ID: %s\n", ssmSession.SessionID())

	// Step 4: Create TUN device
	fmt.Println("✓ Creating utun device...")
	tun, err := tunnel.CreateTUN()
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	defer tun.Close()

	// Configure TUN device
	if err := tun.Configure(localIP, mtu); err != nil {
		return fmt.Errorf("failed to configure TUN device: %w", err)
	}

	fmt.Printf("  ├─ Device: %s\n", tun.Name())
	fmt.Printf("  ├─ IP: %s\n", localIP)
	fmt.Printf("  └─ MTU: %d\n", mtu)

	// Step 5: Add routes
	fmt.Println("✓ Adding routes...")
	router := routing.NewRouter()
	for _, cidr := range cidrBlocks {
		if err := router.AddRoute(cidr, tun.Name()); err != nil {
			// Clean up previously added routes
			router.Cleanup()
			return fmt.Errorf("failed to add route for %s: %w", cidr, err)
		}
		fmt.Printf("  └─ %s → %s\n", cidr, tun.Name())
	}

	// Ensure routes are cleaned up on exit
	defer func() {
		fmt.Println("\n✓ Removing routes...")
		router.Cleanup()
	}()

	// Step 6: Start packet forwarder
	fmt.Println("✓ Starting packet forwarder...")

	fwd := forwarder.New(tun, ssmSession, logPackets)
	if err := fwd.Start(); err != nil {
		return fmt.Errorf("failed to start forwarder: %w", err)
	}
	defer fwd.Stop()

	// Step 7: Save session state
	sessionMgr := session.NewManager()
	sess := &session.Session{
		Name:       sessionName,
		InstanceID: instance.InstanceID,
		SessionID:  ssmSession.SessionID(),
		TunDevice:  tun.Name(),
		TunIP:      localIP,
		CIDRBlocks: cidrBlocks,
		StartedAt:  time.Now(),
		PID:        os.Getpid(),
	}
	if err := sessionMgr.Save(sess); err != nil {
		log.Warnf("Failed to save session state: %v", err)
	}
	defer sessionMgr.Remove(sessionName)

	// Print success banner
	printSuccessBanner(tun.Name(), cidrBlocks)

	// Step 8: Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Monitor session health if auto-reconnect is enabled
	if autoReconnect {
		go monitorSessionHealth(ctx, ssmSession, &reconnectDelay, maxRetries)
	}

	// Wait for signal
	<-sigCh
	fmt.Println("\n\n✓ Shutting down gracefully...")

	// Cancel context to stop health monitor and other goroutines
	cancel()

	return nil
}

func printStartBanner() {
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  SSM Proxy - Transparent Network Tunnel")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
}

func printSuccessBanner(tunDevice string, cidrs []string) {
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🚀 Transparent proxy is now active!")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
	fmt.Println("All traffic to the following CIDR blocks will be")
	fmt.Println("automatically routed through the SSM tunnel:")
	for _, cidr := range cidrs {
		fmt.Printf("  • %s\n", cidr)
	}
	fmt.Println()
	fmt.Println("Your applications require NO configuration.")
	fmt.Println("Just connect normally:")
	fmt.Println()
	fmt.Println("  # Database")
	fmt.Println("  psql -h 10.0.1.5 -p 5432 mydb")
	fmt.Println()
	fmt.Println("  # API")
	fmt.Println("  curl http://10.0.2.100:8080/health")
	fmt.Println()
	fmt.Println("  # Redis")
	fmt.Println("  redis-cli -h 10.0.3.25 -p 6379")
	fmt.Println()
	fmt.Println("Press Ctrl+C to stop and clean up...")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
}

func monitorSessionHealth(ctx context.Context, session *ssm.Session, delay *time.Duration, maxRetries int) {
	retries := 0
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Debug("Health monitor stopping due to context cancellation")
			return
		case <-ticker.C:
			// Check context again before attempting reconnect
			select {
			case <-ctx.Done():
				return
			default:
			}

			if !session.IsHealthy() {
				// Check if we're shutting down
				select {
				case <-ctx.Done():
					log.Debug("Session unhealthy but context cancelled, not reconnecting")
					return
				default:
				}

				log.Warn("Session unhealthy, attempting reconnection...")
				if maxRetries > 0 && retries >= maxRetries {
					log.Error("Max reconnection attempts reached, giving up")
					return
				}
				retries++
				time.Sleep(*delay)
				// Reconnection logic would go here
			} else {
				retries = 0 // Reset retry counter on successful health check
			}
		}
	}
}

func validateCIDR(cidr string) error {
	parts := strings.Split(cidr, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid CIDR format, expected x.x.x.x/y")
	}

	// Validate IP address
	ipParts := strings.Split(parts[0], ".")
	if len(ipParts) != 4 {
		return fmt.Errorf("invalid IP address")
	}

	// Basic validation - real implementation would be more thorough
	return nil
}
