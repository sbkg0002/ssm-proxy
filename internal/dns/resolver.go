package dns

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"
)

var log = logrus.New()

// Config holds DNS resolver configuration
type Config struct {
	// Domains is a list of domain suffixes to resolve through the tunnel
	// e.g., ["internal.company.com", ".amazonaws.com"]
	// If empty, all DNS queries will be routed through the tunnel
	Domains []string

	// Resolver is the DNS server address to use through the tunnel
	// e.g., "10.0.0.2:53" or "169.254.169.253:53" (AWS VPC DNS)
	Resolver string

	// Timeout for DNS queries
	Timeout time.Duration

	// SOCKS5 dialer for routing DNS queries through the tunnel
	SOCKSDialer proxy.Dialer
}

// Resolver handles DNS resolution through the SSM tunnel
type Resolver struct {
	config      Config
	cache       map[string]*cacheEntry
	cacheMu     sync.RWMutex
	socksDialer proxy.Dialer
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

type cacheEntry struct {
	response []byte
	expires  time.Time
}

// NewResolver creates a new DNS resolver
func NewResolver(config Config) (*Resolver, error) {
	if config.Resolver == "" {
		return nil, fmt.Errorf("DNS resolver address is required")
	}

	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}

	r := &Resolver{
		config: config,
		cache:  make(map[string]*cacheEntry),
		stopCh: make(chan struct{}),
	}

	// Start cache cleanup goroutine
	r.wg.Add(1)
	go r.cleanupLoop()

	return r, nil
}

// ShouldHandle checks if a domain should be resolved through the tunnel
func (r *Resolver) ShouldHandle(domain string) bool {
	if len(r.config.Domains) == 0 {
		// If no domains specified, handle all DNS queries
		return true
	}

	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	for _, suffix := range r.config.Domains {
		suffix = strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(suffix, "."), "."))

		// Exact match
		if domain == suffix {
			return true
		}

		// Suffix match
		if strings.HasSuffix(domain, "."+suffix) {
			return true
		}

		// Handle patterns like ".amazonaws.com"
		if strings.HasPrefix(r.config.Domains[0], ".") && strings.HasSuffix(domain, suffix) {
			return true
		}
	}
	return false
}

// Query performs a DNS query through the tunnel
func (r *Resolver) Query(ctx context.Context, queryData []byte) ([]byte, error) {
	// Check cache first
	cacheKey := string(queryData)
	if cached := r.getFromCache(cacheKey); cached != nil {
		log.Debugf("DNS: cache hit")
		return cached, nil
	}

	// Create connection through SOCKS5 proxy (if available) or direct
	var conn net.Conn
	var err error

	if r.config.SOCKSDialer != nil {
		// Try to dial through SOCKS5 using DialContext if available
		if dialer, ok := r.config.SOCKSDialer.(interface {
			DialContext(ctx context.Context, network, addr string) (net.Conn, error)
		}); ok {
			dialCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
			defer cancel()
			conn, err = dialer.DialContext(dialCtx, "udp", r.config.Resolver)
		} else {
			// Fallback to regular Dial
			conn, err = r.config.SOCKSDialer.Dial("udp", r.config.Resolver)
		}
	} else {
		// Direct connection (no SOCKS5)
		dialer := &net.Dialer{Timeout: r.config.Timeout}
		conn, err = dialer.DialContext(ctx, "udp", r.config.Resolver)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to DNS server %s: %w", r.config.Resolver, err)
	}
	defer conn.Close()

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(r.config.Timeout)
	}
	conn.SetDeadline(deadline)

	// Send DNS query
	_, err = conn.Write(queryData)
	if err != nil {
		return nil, fmt.Errorf("failed to send DNS query: %w", err)
	}

	// Read response
	response := make([]byte, 512) // Standard DNS response size
	n, err := conn.Read(response)
	if err != nil {
		return nil, fmt.Errorf("failed to read DNS response: %w", err)
	}

	responseData := response[:n]

	// Cache the response (simple TTL-based caching)
	r.addToCache(cacheKey, responseData, 60*time.Second)

	log.Debugf("DNS: resolved query (%d bytes response)", n)
	return responseData, nil
}

// getFromCache retrieves a DNS response from cache
func (r *Resolver) getFromCache(key string) []byte {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()

	entry, exists := r.cache[key]
	if !exists {
		return nil
	}

	if time.Now().After(entry.expires) {
		// Expired entry
		return nil
	}

	return entry.response
}

// addToCache adds a DNS response to cache
func (r *Resolver) addToCache(key string, response []byte, ttl time.Duration) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	r.cache[key] = &cacheEntry{
		response: response,
		expires:  time.Now().Add(ttl),
	}
}

// cleanupLoop periodically removes expired entries from cache
func (r *Resolver) cleanupLoop() {
	defer r.wg.Done()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.cleanCache()
		}
	}
}

// cleanCache removes expired entries from cache
func (r *Resolver) cleanCache() {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	now := time.Now()
	for key, entry := range r.cache {
		if now.After(entry.expires) {
			delete(r.cache, key)
		}
	}
}

// Stop stops the DNS resolver
func (r *Resolver) Stop() {
	select {
	case <-r.stopCh:
		return
	default:
		close(r.stopCh)
	}
	r.wg.Wait()
}

// ExtractDomainFromQuery extracts the domain name from a DNS query packet
func ExtractDomainFromQuery(query []byte) string {
	if len(query) < 13 {
		return ""
	}

	// Skip DNS header (12 bytes)
	pos := 12
	domain := ""

	for pos < len(query) {
		length := int(query[pos])
		if length == 0 {
			break
		}
		if length > 63 || pos+1+length > len(query) {
			return ""
		}

		if domain != "" {
			domain += "."
		}
		domain += string(query[pos+1 : pos+1+length])
		pos += 1 + length
	}

	return domain
}

// SetLogger sets the logger for the DNS resolver
func SetLogger(logger *logrus.Logger) {
	log = logger
}
