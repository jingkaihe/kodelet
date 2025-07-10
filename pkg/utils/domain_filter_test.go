package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainFilter_IsAllowed(t *testing.T) {
	// Create a temporary domains file for testing
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "test_domains.txt")

	domainsContent := `github.com
stackoverflow.com
google.com
# This is a comment
example.org
`
	err := os.WriteFile(domainsFile, []byte(domainsContent), 0644)
	require.NoError(t, err, "Failed to create test domains file")

	filter := NewDomainFilter(domainsFile)

	tests := []struct {
		name     string
		url      string
		expected bool
		wantErr  bool
	}{
		// Test allowed domains
		{"GitHub HTTPS", "https://github.com/user/repo", true, false},
		{"GitHub HTTP", "http://github.com/user/repo", true, false},
		{"StackOverflow", "https://stackoverflow.com/questions/123", true, false},
		{"Google", "https://google.com/search?q=test", true, false},
		{"Example.org", "https://example.org/page", true, false},

		// Test localhost addresses (should always be allowed)
		{"Localhost HTTPS", "https://localhost:8080/api", true, false},
		{"Localhost HTTP", "http://localhost:3000", true, false},
		{"127.0.0.1", "http://127.0.0.1:8000", true, false},
		{"127.0.0.1 HTTPS", "https://127.0.0.1:443", true, false},
		{"IPv6 loopback", "http://[::1]:8080", true, false},
		{"0.0.0.0", "http://0.0.0.0:8080", true, false},
		{"127.x.x.x range", "http://127.0.0.2:8080", true, false},
		{"127.x.x.x range 2", "http://127.1.1.1:8080", true, false},

		// Test blocked domains
		{"Blocked domain", "https://badsite.com/page", false, false},
		{"Another blocked domain", "https://malicious.org", false, false},
		{"Subdomain of allowed", "https://api.github.com", false, false}, // Subdomains are not automatically allowed

		// Test invalid/unusual URLs
		{"Invalid URL - no scheme", "not-a-url", false, false}, // Gets treated as relative URL with empty hostname
		{"Empty URL", "", false, false},                        // Gets parsed as empty hostname
		{"FTP scheme", "ftp://example.com", false, false},      // Valid URL but not in allowed list
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, err := filter.IsAllowed(tt.url)

			if tt.wantErr {
				assert.NotNil(t, err, "Expected error for URL %s, but got none", tt.url)
				return
			}

			assert.NoError(t, err, "Unexpected error for URL %s: %v", tt.url, err)
			if err != nil {
				return
			}

			assert.Equal(t, tt.expected, allowed, "For URL %s, expected %v but got %v", tt.url, tt.expected, allowed)
		})
	}
}

func TestDomainFilter_EmptyFile(t *testing.T) {
	// Test with non-existent file (should allow all)
	filter := NewDomainFilter("/nonexistent/file.txt")

	testURLs := []string{
		"https://example.com",
		"https://any-domain.org",
		"http://localhost:8080", // Should still be allowed
	}

	for _, url := range testURLs {
		allowed, err := filter.IsAllowed(url)
		assert.NoError(t, err, "Unexpected error for URL %s with empty file: %v", url, err)
		if err != nil {
			continue
		}
		assert.True(t, allowed, "Expected %s to be allowed with empty/nonexistent file", url)
	}
}

func TestDomainFilter_FileReload(t *testing.T) {
	// Create a temporary domains file
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "reload_test.txt")

	// Initially write one domain
	initialContent := "github.com\n"
	err := os.WriteFile(domainsFile, []byte(initialContent), 0644)
	require.NoError(t, err, "Failed to create test file")

	filter := NewDomainFilter(domainsFile)

	// Test initial state
	allowed, _ := filter.IsAllowed("https://github.com")
	assert.True(t, allowed, "Expected github.com to be allowed initially")

	allowed, _ = filter.IsAllowed("https://stackoverflow.com")
	assert.False(t, allowed, "Expected stackoverflow.com to be blocked initially")

	// Modify the file to add another domain
	newContent := "github.com\nstackoverflow.com\n"
	err = os.WriteFile(domainsFile, []byte(newContent), 0644)
	require.NoError(t, err, "Failed to update test file")

	// Force reload by simulating time passage
	// We'll manipulate the lastLoadTime to trigger a reload
	filter.mu.Lock()
	filter.lastLoadTime = time.Now().Add(-DomainRefreshInterval - time.Second)
	filter.mu.Unlock()

	// Test after reload
	allowed, _ = filter.IsAllowed("https://stackoverflow.com")
	assert.True(t, allowed, "Expected stackoverflow.com to be allowed after reload")
}

func TestDomainFilter_GetAllowedDomains(t *testing.T) {
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "test_domains.txt")

	content := `github.com
stackoverflow.com
# comment should be ignored
google.com
`
	err := os.WriteFile(domainsFile, []byte(content), 0644)
	require.NoError(t, err, "Failed to create test file")

	filter := NewDomainFilter(domainsFile)
	domains := filter.GetAllowedDomains()

	expected := []string{"github.com", "stackoverflow.com", "google.com"}
	assert.Len(t, domains, len(expected), "Expected %d domains, got %d", len(expected), len(domains))

	// Check that all expected domains are present
	domainMap := make(map[string]bool)
	for _, domain := range domains {
		domainMap[domain] = true
	}

	for _, expectedDomain := range expected {
		assert.True(t, domainMap[expectedDomain], "Expected domain %s not found in result", expectedDomain)
	}
}

func TestDomainFilter_DomainNormalization(t *testing.T) {
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "normalization_test.txt")

	// Test that domains are normalized (protocol stripped, lowercase)
	content := `GITHUB.COM
https://stackoverflow.com
http://example.org/
Google.Com/path
`
	err := os.WriteFile(domainsFile, []byte(content), 0644)
	require.NoError(t, err, "Failed to create test file")

	filter := NewDomainFilter(domainsFile)

	tests := []struct {
		url      string
		expected bool
	}{
		{"https://github.com", true},
		{"https://GITHUB.COM", true},
		{"https://stackoverflow.com", true},
		{"https://example.org", true},
		{"https://google.com", true},
	}

	for _, tt := range tests {
		allowed, err := filter.IsAllowed(tt.url)
		assert.NoError(t, err, "Error checking %s: %v", tt.url, err)
		if err != nil {
			continue
		}
		assert.Equal(t, tt.expected, allowed, "For %s, expected %v but got %v", tt.url, tt.expected, allowed)
	}
}

func TestDomainFilter_TildeExpansion(t *testing.T) {
	// Test that tilde expansion works
	filter := NewDomainFilter("~/test_domains.txt")

	// We can't easily test the actual file loading without creating files in the user's home,
	// but we can test that the path was expanded
	assert.NotEqual(t, "~/test_domains.txt", filter.filePath, "Tilde was not expanded in file path")
}

func TestDomainFilter_GlobPatterns(t *testing.T) {
	// Create a temporary domains file with glob patterns
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "glob_test.txt")

	domainsContent := `# Exact matches
github.com
example.com

# Glob patterns
*.github.com
*.amazonaws.com
api.*.example.org
sub*.test.com
*.dev
`
	err := os.WriteFile(domainsFile, []byte(domainsContent), 0644)
	require.NoError(t, err, "Failed to create test domains file")

	filter := NewDomainFilter(domainsFile)

	tests := []struct {
		name     string
		url      string
		expected bool
		wantErr  bool
	}{
		// Exact matches should still work
		{"Exact match - github.com", "https://github.com/user/repo", true, false},
		{"Exact match - example.com", "https://example.com/page", true, false},

		// Glob pattern *.github.com
		{"Glob *.github.com - api", "https://api.github.com/repos", true, false},
		{"Glob *.github.com - raw", "https://raw.github.com/file", true, false},
		{"Glob *.github.com - gist", "https://gist.github.com/123", true, false},
		{"Not matching *.github.com - githubusercontent", "https://raw.githubusercontent.com/file", false, false},
		{"Root domain not matching *.github.com", "https://github.com", true, false}, // This should match exact rule

		// Glob pattern *.amazonaws.com
		{"Glob *.amazonaws.com - s3", "https://s3.amazonaws.com/bucket", true, false},
		{"Glob *.amazonaws.com - ec2", "https://ec2.amazonaws.com/instance", true, false},
		{"Glob *.amazonaws.com - rds", "https://rds.amazonaws.com/db", true, false},

		// Glob pattern api.*.example.org
		{"Glob api.*.example.org - dev", "https://api.dev.example.org/endpoint", true, false},
		{"Glob api.*.example.org - staging", "https://api.staging.example.org/endpoint", true, false},
		{"Glob api.*.example.org - prod", "https://api.prod.example.org/endpoint", true, false},
		{"Not matching api.*.example.org - wrong prefix", "https://web.dev.example.org/endpoint", false, false},
		{"Glob api.*.example.org - matches with empty subdomain", "https://api.example.org/endpoint", true, false},

		// Glob pattern sub*.test.com
		{"Glob sub*.test.com - sub1", "https://sub1.test.com/page", true, false},
		{"Glob sub*.test.com - subdomain", "https://subdomain.test.com/page", true, false},
		{"Glob sub*.test.com - sub-api", "https://sub-api.test.com/page", true, false},
		{"Not matching sub*.test.com - different prefix", "https://api.test.com/page", false, false},

		// Glob pattern *.dev
		{"Glob *.dev - any subdomain", "https://myapp.dev/page", true, false},
		{"Glob *.dev - api subdomain", "https://api.dev/page", true, false},
		{"Glob *.dev - nested subdomain", "https://api.staging.dev/page", true, false},

		// Test domains not matching any pattern
		{"No match - random domain", "https://random.com/page", false, false},
		{"No match - similar but different", "https://github.org/page", false, false},
		{"No match - partial match", "https://test.github.co/page", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, err := filter.IsAllowed(tt.url)

			if tt.wantErr {
				assert.NotNil(t, err, "Expected error for URL %s, but got none", tt.url)
				return
			}

			assert.NoError(t, err, "Unexpected error for URL %s: %v", tt.url, err)
			if err != nil {
				return
			}

			assert.Equal(t, tt.expected, allowed, "For URL %s, expected %v but got %v", tt.url, tt.expected, allowed)
		})
	}
}

func TestDomainFilter_MixedExactAndGlob(t *testing.T) {
	// Test that exact matches and glob patterns work together correctly
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "mixed_test.txt")

	domainsContent := `api.github.com
*.github.com
example.com
*.example.com
`
	err := os.WriteFile(domainsFile, []byte(domainsContent), 0644)
	require.NoError(t, err, "Failed to create test domains file")

	filter := NewDomainFilter(domainsFile)

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		// Should match exact rule first
		{"Exact match takes precedence", "https://api.github.com/repos", true},
		// Should match glob pattern
		{"Glob pattern match", "https://raw.github.com/file", true},
		// Both exact and glob available - exact should work
		{"Both rules available - exact", "https://example.com/page", true},
		// Both exact and glob available - glob should work
		{"Both rules available - glob", "https://api.example.com/endpoint", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, err := filter.IsAllowed(tt.url)
			assert.NoError(t, err, "Unexpected error for URL %s: %v", tt.url, err)
			if err != nil {
				return
			}

			assert.Equal(t, tt.expected, allowed, "For URL %s, expected %v but got %v", tt.url, tt.expected, allowed)
		})
	}
}

func TestDomainFilter_GetAllowedDomainsWithGlob(t *testing.T) {
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "glob_list_test.txt")

	content := `github.com
*.github.com
stackoverflow.com
*.amazonaws.com
# comment should be ignored
api.*.example.org
`
	err := os.WriteFile(domainsFile, []byte(content), 0644)
	require.NoError(t, err, "Failed to create test file")

	filter := NewDomainFilter(domainsFile)
	domains := filter.GetAllowedDomains()

	expected := []string{"github.com", "stackoverflow.com", "*.github.com", "*.amazonaws.com", "api.*.example.org"}
	assert.Len(t, domains, len(expected), "Expected %d domains/patterns, got %d: %v", len(expected), len(domains), domains)

	// Check that all expected domains and patterns are present
	domainMap := make(map[string]bool)
	for _, domain := range domains {
		domainMap[domain] = true
	}

	for _, expectedItem := range expected {
		assert.True(t, domainMap[expectedItem], "Expected domain/pattern %s not found in result: %v", expectedItem, domains)
	}
}

func TestDomainFilter_InvalidGlobPatterns(t *testing.T) {
	// Test that invalid glob patterns are handled gracefully
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "invalid_glob_test.txt")

	// Include some patterns that might cause issues
	domainsContent := `github.com
*.github.com
[invalid-bracket-pattern.com
valid.example.com
*.valid.com
`
	err := os.WriteFile(domainsFile, []byte(domainsContent), 0644)
	require.NoError(t, err, "Failed to create test domains file")

	// Should not panic and should still load valid patterns
	filter := NewDomainFilter(domainsFile)

	// Test that valid patterns still work
	allowed, err := filter.IsAllowed("https://github.com")
	assert.NoError(t, err, "Unexpected error: %v", err)
	assert.True(t, allowed, "Expected github.com to be allowed")

	allowed, err = filter.IsAllowed("https://api.github.com")
	assert.NoError(t, err, "Unexpected error: %v", err)
	assert.True(t, allowed, "Expected api.github.com to be allowed via glob pattern")

	allowed, err = filter.IsAllowed("https://valid.example.com")
	assert.NoError(t, err, "Unexpected error: %v", err)
	assert.True(t, allowed, "Expected valid.example.com to be allowed")
}

func TestDomainFilter_CaseSensitiveGlobPatterns(t *testing.T) {
	// Test that glob patterns work with case insensitive matching
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "case_test.txt")

	domainsContent := `*.GitHub.Com
*.EXAMPLE.ORG
`
	err := os.WriteFile(domainsFile, []byte(domainsContent), 0644)
	require.NoError(t, err, "Failed to create test domains file")

	filter := NewDomainFilter(domainsFile)

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		// Test that patterns are normalized to lowercase and work case-insensitively
		{"Lowercase match", "https://api.github.com", true},
		{"Mixed case match", "https://API.GitHub.COM", true},
		{"Another case variation", "https://sub.EXAMPLE.org", true},
		{"Different domain", "https://api.github.net", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, err := filter.IsAllowed(tt.url)
			assert.NoError(t, err, "Unexpected error for URL %s: %v", tt.url, err)
			if err != nil {
				return
			}

			assert.Equal(t, tt.expected, allowed, "For URL %s, expected %v but got %v", tt.url, tt.expected, allowed)
		})
	}
}

func TestDomainFilter_ComplexGlobPatterns(t *testing.T) {
	// Test more complex glob patterns
	tmpDir := t.TempDir()
	domainsFile := filepath.Join(tmpDir, "complex_glob_test.txt")

	domainsContent := `api-*.example.com
*.s3.amazonaws.com
*-staging.*.com
`
	err := os.WriteFile(domainsFile, []byte(domainsContent), 0644)
	require.NoError(t, err, "Failed to create test domains file")

	filter := NewDomainFilter(domainsFile)

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		// Pattern: api-*.example.com
		{"api-* pattern match", "https://api-v1.example.com", true},
		{"api-* pattern match 2", "https://api-beta.example.com", true},
		{"api-* pattern no match", "https://api.example.com", false},
		{"api-* pattern no match 2", "https://web-api.example.com", false},

		// Pattern: *.s3.amazonaws.com
		{"s3 subdomain pattern", "https://my-bucket.s3.amazonaws.com", true},
		{"s3 subdomain pattern 2", "https://prod-data.s3.amazonaws.com", true},
		{"s3 wrong pattern", "https://s3.amazonaws.com", false},

		// Pattern: *-staging.*.com
		{"Complex pattern match", "https://api-staging.myapp.com", true},
		{"Complex pattern match 2", "https://web-staging.prod.com", true},
		{"Complex pattern no match", "https://staging.myapp.com", false},
		{"Complex pattern no match 2", "https://api-prod.myapp.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, err := filter.IsAllowed(tt.url)
			assert.NoError(t, err, "Unexpected error for URL %s: %v", tt.url, err)
			if err != nil {
				return
			}

			assert.Equal(t, tt.expected, allowed, "For URL %s, expected %v but got %v", tt.url, tt.expected, allowed)
		})
	}
}
