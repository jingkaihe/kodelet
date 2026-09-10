package controlplane

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/pkg/errors"
)

// NormalizePublicBaseURL validates the advertised image-link base independently of the listen address.
func NormalizePublicBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" || parsed.Opaque != "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		strings.Contains(raw, "#") || strings.ContainsAny(raw, "\\") ||
		strings.ContainsFunc(raw, unicode.IsSpace) {
		return "", errors.New("public base URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." ||
			strings.ContainsFunc(segment, unicode.IsControl) || strings.Contains(segment, "\\") {
			return "", errors.New("public base URL must not contain traversal or control characters")
		}
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
