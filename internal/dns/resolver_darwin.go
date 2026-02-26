//go:build darwin

package dns

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const resolverDir = "/etc/resolver"

// MacOSResolverConfig manages macOS DNS resolver configuration
type MacOSResolverConfig struct {
	domains   []string
	dnsServer string
	created   []string // Track created files for cleanup
}

// NewMacOSResolverConfig creates a new macOS resolver configuration manager
func NewMacOSResolverConfig(domains []string, dnsServer string) *MacOSResolverConfig {
	return &MacOSResolverConfig{
		domains:   domains,
		dnsServer: dnsServer,
		created:   make([]string, 0),
	}
}

// Setup configures macOS resolver files for the specified domains
func (m *MacOSResolverConfig) Setup() error {
	if len(m.domains) == 0 {
		log.Info("No DNS domains specified, skipping macOS resolver configuration")
		return nil
	}

	log.Info("Configuring macOS DNS resolver...")

	// Create /etc/resolver directory if it doesn't exist
	if err := os.MkdirAll(resolverDir, 0755); err != nil {
		return fmt.Errorf("failed to create %s directory: %w (are you running as root?)", resolverDir, err)
	}

	// Create resolver file for each domain
	for _, domain := range m.domains {
		baseDomain := extractBaseDomain(domain)
		if baseDomain == "" {
			log.Warnf("Skipping invalid domain pattern: %s", domain)
			continue
		}

		resolverFile := filepath.Join(resolverDir, baseDomain)

		// Check if file already exists
		if _, err := os.Stat(resolverFile); err == nil {
			// File exists, back it up
			backupFile := resolverFile + ".ssm-proxy-backup"
			if err := os.Rename(resolverFile, backupFile); err != nil {
				log.Warnf("Failed to backup existing resolver file %s: %v", resolverFile, err)
			} else {
				log.Debugf("  Backed up existing resolver file to %s", backupFile)
				m.created = append(m.created, backupFile) // Track backup for restoration
			}
		}

		// Create resolver file content
		// Only include IP address (without port) as macOS resolver format expects
		dnsIP := extractIPPort(m.dnsServer)
		content := fmt.Sprintf("nameserver %s\nsearch_order 1\n", dnsIP)

		if err := os.WriteFile(resolverFile, []byte(content), 0644); err != nil {
			// Clean up any files we created
			m.Cleanup()
			return fmt.Errorf("failed to create resolver file %s: %w", resolverFile, err)
		}

		m.created = append(m.created, resolverFile)
		log.Infof("  ✓ Configured DNS resolver: %s → %s", baseDomain, dnsIP)
	}

	// Flush DNS cache to apply changes immediately
	if err := flushDNSCache(); err != nil {
		log.Warnf("Failed to flush DNS cache: %v", err)
		log.Warn("You may need to manually flush DNS: sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder")
	} else {
		log.Debug("  ✓ DNS cache flushed")
	}

	return nil
}

// Cleanup removes all resolver files created by Setup and restores backups
func (m *MacOSResolverConfig) Cleanup() error {
	if len(m.created) == 0 {
		return nil
	}

	log.Info("Cleaning up macOS DNS resolver configuration...")

	var errors []string
	for _, file := range m.created {
		// Check if this is a backup file
		if strings.HasSuffix(file, ".ssm-proxy-backup") {
			// Restore backup
			originalFile := strings.TrimSuffix(file, ".ssm-proxy-backup")
			if err := os.Rename(file, originalFile); err != nil {
				if !os.IsNotExist(err) {
					errors = append(errors, fmt.Sprintf("restore %s: %v", file, err))
					log.Warnf("  Failed to restore backup %s: %v", file, err)
				}
			} else {
				log.Debugf("  ✓ Restored backup: %s", originalFile)
			}
		} else {
			// Remove resolver file we created
			if err := os.Remove(file); err != nil {
				if !os.IsNotExist(err) {
					errors = append(errors, fmt.Sprintf("remove %s: %v", file, err))
					log.Warnf("  Failed to remove %s: %v", file, err)
				}
			} else {
				log.Debugf("  ✓ Removed resolver file: %s", file)
			}
		}
	}

	// Flush DNS cache after cleanup
	if err := flushDNSCache(); err != nil {
		log.Warnf("Failed to flush DNS cache after cleanup: %v", err)
	} else {
		log.Debug("  ✓ DNS cache flushed")
	}

	m.created = nil

	if len(errors) > 0 {
		return fmt.Errorf("cleanup had errors: %s", strings.Join(errors, "; "))
	}

	log.Info("  ✓ macOS DNS resolver cleanup complete")
	return nil
}

// extractBaseDomain extracts the base domain from a pattern
func extractBaseDomain(pattern string) string {
	domain := strings.TrimSpace(pattern)
	domain = strings.TrimPrefix(domain, ".")
	domain = strings.TrimSuffix(domain, ".")

	if domain == "" || !strings.Contains(domain, ".") {
		return ""
	}

	return domain
}

// extractIPPort extracts just the IP address from "IP:PORT" format
// macOS resolver files expect just the IP without the port
func extractIPPort(addr string) string {
	if strings.Contains(addr, ":") {
		parts := strings.Split(addr, ":")
		return parts[0]
	}
	return addr
}

// flushDNSCache flushes the macOS DNS cache
func flushDNSCache() error {
	log.Debug("Flushing macOS DNS cache...")

	// Try dscacheutil (works on all modern macOS versions)
	cmd := exec.Command("dscacheutil", "-flushcache")
	if err := cmd.Run(); err != nil {
		log.Debugf("dscacheutil -flushcache failed: %v", err)
	}

	// Restart mDNSResponder to ensure DNS changes take effect
	cmd = exec.Command("killall", "-HUP", "mDNSResponder")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to restart mDNSResponder: %w", err)
	}

	return nil
}

// VerifyResolverConfiguration checks if resolver files exist and are configured correctly
func VerifyResolverConfiguration(domains []string, dnsServer string) bool {
	if len(domains) == 0 {
		return false
	}

	dnsIP := extractIPPort(dnsServer)

	for _, domain := range domains {
		baseDomain := extractBaseDomain(domain)
		if baseDomain == "" {
			continue
		}

		resolverFile := filepath.Join(resolverDir, baseDomain)
		content, err := os.ReadFile(resolverFile)
		if err != nil {
			return false
		}

		// Check if file contains our nameserver
		if !strings.Contains(string(content), dnsIP) {
			return false
		}
	}

	return true
}
