package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile    string
	awsProfile string
	awsRegion  string
	verbose    bool
	debug      bool
	quiet      bool
	log        = logrus.New()
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "ssm-proxy",
	Short: "Transparent network proxy through AWS SSM",
	Long: `ssm-proxy creates transparent system-level routing for specified CIDR blocks
through an AWS EC2 instance via SSM Session Manager.

Applications require zero configuration - traffic is automatically routed
based on destination IP address.

Example:
  # Start proxy for VPC CIDR block
  sudo ssm-proxy start --instance-id i-1234567890abcdef0 --cidr 10.0.0.0/8

  # Now access resources transparently
  psql -h 10.0.1.5 -p 5432 mydb
  curl http://10.0.2.100:8080
  redis-cli -h 10.0.3.25

For more information: https://github.com/sbkg0002/ssm-proxy`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Set up logging based on flags
		if quiet {
			log.SetLevel(logrus.ErrorLevel)
		} else if debug {
			log.SetLevel(logrus.DebugLevel)
		} else if verbose {
			log.SetLevel(logrus.InfoLevel)
		} else {
			log.SetLevel(logrus.WarnLevel)
		}

		log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		})
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute(version, commit, buildTime string) error {
	// Set version info
	rootCmd.Version = fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, buildTime)

	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.ssm-proxy/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&awsProfile, "profile", "", "AWS profile name (default: $AWS_PROFILE or 'default')")
	rootCmd.PersistentFlags().StringVar(&awsRegion, "region", "", "AWS region (default: $AWS_REGION or from profile)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "debug output (very verbose)")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "quiet mode (errors only)")

	// Bind flags to viper
	viper.BindPFlag("aws.profile", rootCmd.PersistentFlags().Lookup("profile"))
	viper.BindPFlag("aws.region", rootCmd.PersistentFlags().Lookup("region"))
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		if err != nil {
			log.Warn("Could not determine home directory:", err)
			return
		}

		// Search config in home directory with name ".ssm-proxy" (without extension).
		configDir := filepath.Join(home, ".ssm-proxy")
		viper.AddConfigPath(configDir)
		viper.AddConfigPath(".")
		viper.AddConfigPath("/etc/ssm-proxy")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	// Read in environment variables that match
	viper.SetEnvPrefix("SSM_PROXY")
	viper.AutomaticEnv()

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		if verbose || debug {
			log.Debug("Using config file:", viper.ConfigFileUsed())
		}
	}
}
