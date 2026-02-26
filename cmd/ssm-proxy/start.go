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
	"github.com/sbkg0002/ssm-proxy/internal/dns"
	"github.com/sbkg0002/ssm-proxy/internal/forwarder"
	"github.com/sbkg0002/ssm-proxy/internal/routing"
	"github.com/sbkg0002/ssm-proxy/internal/session"
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

	// DNS configuration
	dnsResolver string
	dnsDomains  []string
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

	// DNS configuration
	startCmd.Flags().StringVar(&dnsResolver, "dns-resolver", "", "DNS server accessible through tunnel (e.g., '10.0.0.2:53' or '169.254.169.253:53' for AWS VPC DNS)")
	startCmd.Flags().StringSliceVar(&dnsDomains, "dns-domains", []string{}, "Domain suffixes to resolve through tunnel (e.g., '.internal.company.com,.amazonaws.com'). If empty, all DNS queries routed through tunnel")

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

	// Step 3: Start SSH tunnel with dynamic SOCKS5 forwarding over SSM
	fmt.Println("✓ Starting SSH tunnel over SSM...")
	sshTunnel := tunnel.NewSSHTunnel(tunnel.SSHTunnelConfig{
		InstanceID:       instance.InstanceID,
		Region:           awsClient.Region(),
		AWSProfile:       awsProfile,
		AWSConfig:        awsClient.Config(),
		AvailabilityZone: instance.AvailabilityZone,
		SOCKSPort:        1080,
		SSHUser:          "ec2-user",
	})

	if err := sshTunnel.Start(ctx); err != nil {
		return fmt.Errorf("failed to start SSH tunnel: %w", err)
	}
	defer sshTunnel.Stop()

	fmt.Printf("  ├─ SOCKS5 proxy: %s\n", sshTunnel.SOCKSAddr())
	fmt.Printf("  └─ Tunnel established ✓\n")

	// Step 4: Create TUN device
	fmt.Println("✓ Creating utun device...")
	tun, err := tunnel.CreateTUN()
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	// TUN will be closed during shutdown sequence (must be closed before stopping forwarder)

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

	// Step 6: Configure DNS resolver if specified
	var dnsConfig *dns.Config
	var macOSResolver *dns.MacOSResolverConfig
	if dnsResolver != "" {
		dnsConfig = &dns.Config{
			Resolver: dnsResolver,
			Domains:  dnsDomains,
		}
		fmt.Printf("✓ DNS resolver configured: %s\n", dnsResolver)
		if len(dnsDomains) > 0 {
			fmt.Printf("  └─ Domains: %v\n", dnsDomains)

			// Set up macOS DNS resolver configuration
			fmt.Println("✓ Configuring macOS DNS resolver...")
			macOSResolver = dns.NewMacOSResolverConfig(dnsDomains, dnsResolver)
			if err := macOSResolver.Setup(); err != nil {
				log.Warnf("Failed to configure macOS DNS resolver: %v", err)
				fmt.Printf("  ⚠️  Could not configure macOS DNS resolver automatically: %v\n", err)
				fmt.Printf("     Continuing without automatic DNS configuration...\n")
			}
		} else {
			fmt.Printf("  └─ All DNS queries will be routed through tunnel\n")
			fmt.Printf("  ⚠️  Note: No specific domains configured, skipping macOS DNS resolver setup\n")
		}
	}

	// Ensure macOS DNS resolver is cleaned up on exit
	if macOSResolver != nil {
		defer func() {
			if err := macOSResolver.Cleanup(); err != nil {
				log.Warnf("Failed to cleanup macOS DNS resolver: %v", err)
			}
		}()
	}

	// Step 7: Start TUN-to-SOCKS translator
	fmt.Println("✓ Starting transparent packet forwarder...")

	tunToSocks, err := forwarder.NewTunToSOCKS(tun, sshTunnel.SOCKSAddr(), dnsConfig)
	if err != nil {
		return fmt.Errorf("failed to create TUN-to-SOCKS translator: %w", err)
	}

	if err := tunToSocks.Start(ctx); err != nil {
		return fmt.Errorf("failed to start TUN-to-SOCKS translator: %w", err)
	}
	// Forwarder will be stopped during shutdown sequence (after closing TUN device)

	fmt.Printf("  └─ Transparent forwarding active ✓\n")

	// Step 8: Save session state
	sessionMgr := session.NewManager()
	sess := &session.Session{
		Name:       sessionName,
		InstanceID: instance.InstanceID,
		SessionID:  sessionName, // Use session name as ID for SSH tunnel
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
	printSuccessBanner(tun.Name(), cidrBlocks, dnsResolver, dnsDomains)

	// Step 9: Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Monitor SSH tunnel health if auto-reconnect is enabled
	if autoReconnect {
		go monitorTunnelHealth(ctx, sshTunnel, &reconnectDelay, maxRetries)
	}

	// Wait for signal
	<-sigCh
	fmt.Println("\n\n✓ Shutting down gracefully...")

	// Cancel context to stop health monitor and other goroutines
	cancel()

	// Shutdown sequence: Close TUN device BEFORE stopping forwarder
	// This ensures any blocked Read() operations are interrupted
	fmt.Println("✓ Closing utun device...")
	if err := tun.Close(); err != nil {
		log.Warnf("Error closing TUN device: %v", err)
	}

	// Now stop the forwarder (Read() will return error and goroutine will exit)
	fmt.Println("✓ Stopping packet forwarder...")
	if err := tunToSocks.Stop(); err != nil {
		log.Warnf("Error stopping forwarder: %v", err)
	}

	return nil
}

func printStartBanner() {
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  SSM Proxy - Transparent Network Tunnel")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
}

func printSuccessBanner(tunDevice string, cidrs []string, dnsResolver string, dnsDomains []string) {
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

	// Show DNS configuration if enabled
	if dnsResolver != "" {
		fmt.Println("DNS Resolution:")
		fmt.Printf("  • DNS Server: %s\n", dnsResolver)
		if len(dnsDomains) > 0 {
			fmt.Printf("  • Domains: %v\n", dnsDomains)
		} else {
			fmt.Printf("  • All DNS queries routed through tunnel\n")
		}
		fmt.Println()
	}

	fmt.Println("Your applications require NO configuration.")
	fmt.Println("Just connect normally:")
	fmt.Println()

	if dnsResolver != "" {
		fmt.Println("  # Database (using DNS name)")
		fmt.Println("  psql -h mydb.internal.company.com -p 5432 mydb")
		fmt.Println()
	}

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

func monitorTunnelHealth(ctx context.Context, sshTunnel *tunnel.SSHTunnel, delay *time.Duration, maxRetries int) {
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

			if !sshTunnel.IsRunning() {
				// Check if we're shutting down
				select {
				case <-ctx.Done():
					log.Debug("SSH tunnel down but context cancelled, not reconnecting")
					return
				default:
				}

				log.Warn("SSH tunnel down, attempting reconnection...")
				if maxRetries > 0 && retries >= maxRetries {
					log.Error("Max reconnection attempts reached, giving up")
					return
				}
				retries++
				time.Sleep(*delay)

				// Attempt to restart tunnel
				if err := sshTunnel.Start(ctx); err != nil {
					log.Errorf("Failed to restart SSH tunnel: %v", err)
				} else {
					log.Info("SSH tunnel reconnected successfully")
					retries = 0
				}
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
