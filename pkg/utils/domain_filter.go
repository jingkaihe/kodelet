package utils

import (
	"bufio"
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/glob"
	"github.com/jingkaihe/kodelet/pkg/logger"
)

const (
	// DomainRefreshInterval defines how often to reload the domains file
	DomainRefreshInterval = 30 * time.Second
)

// DomainFilter manages allowed domains loaded from a file
type DomainFilter struct {
	mu           sync.RWMutex
	filePath     string
	domains      map[string]bool // exact match domains
	globPatterns []glob.Glob     // compiled glob patterns
	rawPatterns  []string        // original pattern strings for debugging
	lastLoadTime time.Time
}

// NewDomainFilter creates a new domain filter with the specified file path
func NewDomainFilter(filePath string) *DomainFilter {
	// Expand tilde if present
	if strings.HasPrefix(filePath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			// If we can't get the home directory, keep the original path
			// This will likely fail later, but allows for better error reporting
			// when the file is actually accessed
		} else {
			filePath = filepath.Join(home, filePath[2:])
		}
	}

	df := &DomainFilter{
		filePath:     filePath,
		domains:      make(map[string]bool),
		globPatterns: make([]glob.Glob, 0),
		rawPatterns:  make([]string, 0),
	}
	df.loadDomains()
	return df
}

// loadDomains loads domains from the file
func (df *DomainFilter) loadDomains() {
	df.mu.Lock()
	defer df.mu.Unlock()

	// Check if file exists
	if _, err := os.Stat(df.filePath); os.IsNotExist(err) {
		// File doesn't exist, clear domains and return
		df.domains = make(map[string]bool)
		df.globPatterns = make([]glob.Glob, 0)
		df.rawPatterns = make([]string, 0)
		df.lastLoadTime = time.Now()
		return
	}

	file, err := os.Open(df.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.G(context.TODO()).WithError(err).Error("Failed to open allowed domains file")
		}
		// Unable to open file, clear domains and return
		df.domains = make(map[string]bool)
		df.globPatterns = make([]glob.Glob, 0)
		df.rawPatterns = make([]string, 0)
		df.lastLoadTime = time.Now()
		return
	}
	defer file.Close()

	newDomains := make(map[string]bool)
	newGlobPatterns := make([]glob.Glob, 0)
	newRawPatterns := make([]string, 0)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			// Normalize domain - handle URLs, protocols, and paths
			domain := strings.ToLower(line)

			// Add protocol if missing to help with parsing
			if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
				domain = "https://" + domain
			}

			// Parse as URL to extract just the hostname
			var hostname string
			if parsed, err := url.Parse(domain); err == nil && parsed.Hostname() != "" {
				hostname = parsed.Hostname()
			} else {
				// Fallback: treat as raw domain, strip protocol and path manually
				hostname = strings.TrimPrefix(domain, "https://")
				hostname = strings.TrimPrefix(hostname, "http://")
				if slashIndex := strings.Index(hostname, "/"); slashIndex != -1 {
					hostname = hostname[:slashIndex]
				}
				hostname = strings.TrimSuffix(hostname, "/")
			}

			if hostname != "" {
				// Check if this is a glob pattern (contains * or ?)
				if strings.ContainsAny(hostname, "*?") {
					// Compile as glob pattern
					if globPattern, err := glob.Compile(hostname); err == nil {
						newGlobPatterns = append(newGlobPatterns, globPattern)
						newRawPatterns = append(newRawPatterns, hostname)
					}
					// If glob compilation fails, treat as exact match
				} else {
					// Exact match
					newDomains[hostname] = true
				}
			}
		}
	}

	df.domains = newDomains
	df.globPatterns = newGlobPatterns
	df.rawPatterns = newRawPatterns
	df.lastLoadTime = time.Now()
}

// shouldReload checks if the domains should be reloaded based on time
func (df *DomainFilter) shouldReload() bool {
	df.mu.RLock()
	defer df.mu.RUnlock()
	return time.Since(df.lastLoadTime) > DomainRefreshInterval
}

// IsAllowed checks if a domain is allowed, reloading the file if necessary
func (df *DomainFilter) IsAllowed(urlStr string) (bool, error) {
	// Reload domains if necessary
	if df.shouldReload() {
		df.loadDomains()
	}

	// Parse URL to extract domain
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return false, err
	}

	domain := strings.ToLower(parsedURL.Hostname())

	// Always allow localhost addresses by default
	if isLocalHostDomain(domain) {
		return true, nil
	}

	df.mu.RLock()
	defer df.mu.RUnlock()

	// If no domains are configured (empty file or file doesn't exist), allow all
	if len(df.domains) == 0 && len(df.globPatterns) == 0 {
		return true, nil
	}

	// Check exact match first
	if df.domains[domain] {
		return true, nil
	}

	// Check glob patterns
	for _, pattern := range df.globPatterns {
		if pattern.Match(domain) {
			return true, nil
		}
	}

	return false, nil
}

// isLocalHostDomain checks if the given hostname/IP is a localhost or internal address
func isLocalHostDomain(hostname string) bool {
	// Check for common localhost names and IPs
	switch hostname {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}

	// Check for other loopback addresses (127.0.0.0/8)
	if strings.HasPrefix(hostname, "127.") {
		return true
	}

	// Parse as IP and check if it's loopback
	if ip := net.ParseIP(hostname); ip != nil {
		return ip.IsLoopback()
	}

	return false
}

// GetAllowedDomains returns a copy of the current allowed domains and patterns (for testing/debugging)
func (df *DomainFilter) GetAllowedDomains() []string {
	df.mu.RLock()
	defer df.mu.RUnlock()

	result := make([]string, 0, len(df.domains)+len(df.rawPatterns))

	// Add exact match domains
	for domain := range df.domains {
		result = append(result, domain)
	}

	// Add glob patterns
	result = append(result, df.rawPatterns...)

	return result
}
