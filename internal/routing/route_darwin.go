package routing

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Router manages routing table entries on macOS
type Router struct {
	routes map[string]string // CIDR -> interface mapping
	mu     sync.Mutex
}

// NewRouter creates a new router instance
func NewRouter() *Router {
	return &Router{
		routes: make(map[string]string),
	}
}

// AddRoute adds a route for the specified CIDR block to the given interface
func (r *Router) AddRoute(cidr, interfaceName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Parse CIDR to get network and netmask
	network, netmask, err := parseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("invalid CIDR %s: %w", cidr, err)
	}

	// Execute: route add -net <network> -netmask <mask> -interface <interface>
	cmd := exec.Command("route", "add", "-net", network, "-netmask", netmask, "-interface", interfaceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to add route: %s: %w", string(output), err)
	}

	// Track this route for cleanup
	r.routes[cidr] = interfaceName

	return nil
}

// DeleteRoute removes a route for the specified CIDR block
func (r *Router) DeleteRoute(cidr string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Parse CIDR to get network and netmask
	network, netmask, err := parseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("invalid CIDR %s: %w", cidr, err)
	}

	// Execute: route delete -net <network> -netmask <mask>
	cmd := exec.Command("route", "delete", "-net", network, "-netmask", netmask)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Ignore "not in table" errors as route may already be removed
		if strings.Contains(string(output), "not in table") {
			delete(r.routes, cidr)
			return nil
		}
		return fmt.Errorf("failed to delete route: %s: %w", string(output), err)
	}

	// Remove from tracking
	delete(r.routes, cidr)

	return nil
}

// Cleanup removes all routes managed by this router
func (r *Router) Cleanup() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errors []string

	for cidr := range r.routes {
		network, netmask, err := parseCIDR(cidr)
		if err != nil {
			errors = append(errors, fmt.Sprintf("invalid CIDR %s: %v", cidr, err))
			continue
		}

		cmd := exec.Command("route", "delete", "-net", network, "-netmask", netmask)
		output, err := cmd.CombinedOutput()
		if err != nil {
			// Ignore "not in table" errors
			if !strings.Contains(string(output), "not in table") {
				errors = append(errors, fmt.Sprintf("failed to delete route %s: %s", cidr, string(output)))
			}
		}
	}

	// Clear the tracked routes
	r.routes = make(map[string]string)

	if len(errors) > 0 {
		return fmt.Errorf("errors during cleanup: %s", strings.Join(errors, "; "))
	}

	return nil
}

// ListRoutes returns all routes managed by this router
func (r *Router) ListRoutes() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Return a copy to avoid race conditions
	routes := make(map[string]string, len(r.routes))
	for k, v := range r.routes {
		routes[k] = v
	}

	return routes
}

// parseCIDR converts CIDR notation to network and netmask
// e.g., "10.0.0.0/8" -> "10.0.0.0", "255.0.0.0"
func parseCIDR(cidr string) (network, netmask string, err error) {
	parts := strings.Split(cidr, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid CIDR format, expected x.x.x.x/y")
	}

	network = parts[0]
	prefix := parts[1]

	// Convert CIDR prefix length to netmask
	netmask = prefixToNetmask(prefix)
	if netmask == "" {
		return "", "", fmt.Errorf("invalid prefix length: %s", prefix)
	}

	return network, netmask, nil
}

// prefixToNetmask converts a CIDR prefix length to dotted decimal netmask
func prefixToNetmask(prefix string) string {
	masks := map[string]string{
		"1":  "128.0.0.0",
		"2":  "192.0.0.0",
		"3":  "224.0.0.0",
		"4":  "240.0.0.0",
		"5":  "248.0.0.0",
		"6":  "252.0.0.0",
		"7":  "254.0.0.0",
		"8":  "255.0.0.0",
		"9":  "255.128.0.0",
		"10": "255.192.0.0",
		"11": "255.224.0.0",
		"12": "255.240.0.0",
		"13": "255.248.0.0",
		"14": "255.252.0.0",
		"15": "255.254.0.0",
		"16": "255.255.0.0",
		"17": "255.255.128.0",
		"18": "255.255.192.0",
		"19": "255.255.224.0",
		"20": "255.255.240.0",
		"21": "255.255.248.0",
		"22": "255.255.252.0",
		"23": "255.255.254.0",
		"24": "255.255.255.0",
		"25": "255.255.255.128",
		"26": "255.255.255.192",
		"27": "255.255.255.224",
		"28": "255.255.255.240",
		"29": "255.255.255.248",
		"30": "255.255.255.252",
		"31": "255.255.255.254",
		"32": "255.255.255.255",
	}

	return masks[prefix]
}

// VerifyRoute checks if a route exists in the system routing table
func (r *Router) VerifyRoute(cidr string) (bool, error) {
	network, _, err := parseCIDR(cidr)
	if err != nil {
		return false, err
	}

	// Use 'route get' to check if route exists
	cmd := exec.Command("route", "get", network)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, nil // Route doesn't exist
	}

	// Check if the output contains our interface
	return len(output) > 0, nil
}
