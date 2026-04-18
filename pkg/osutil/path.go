package osutil

import (
	"path/filepath"
	"runtime"
	"strings"
)

// CanonicalizePath cleans a path and applies stable platform-specific
// aliases so user-facing paths remain consistent across operating systems.
//
// On macOS, the system often reports paths under /private/var and /private/tmp
// even when callers originally referenced /var or /tmp. Converting those paths
// back keeps comparisons and rendered output stable.
func CanonicalizePath(path string) string {
	cleaned := filepath.Clean(path)
	if runtime.GOOS != "darwin" {
		return cleaned
	}

	switch {
	case cleaned == "/private/var":
		return "/var"
	case strings.HasPrefix(cleaned, "/private/var/"):
		return "/var/" + strings.TrimPrefix(cleaned, "/private/var/")
	case cleaned == "/private/tmp":
		return "/tmp"
	case strings.HasPrefix(cleaned, "/private/tmp/"):
		return "/tmp/" + strings.TrimPrefix(cleaned, "/private/tmp/")
	default:
		return cleaned
	}
}
