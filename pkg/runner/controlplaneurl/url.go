// Package controlplaneurl provides canonical control-plane URL identity and endpoint construction.
package controlplaneurl

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// NormalizeBase validates and canonicalizes a control-plane HTTP base URL.
func NormalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("control-plane URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse control-plane URL")
	}
	parsed.Scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("control-plane URL must use http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("control-plane URL must contain only scheme, host, and an optional base path")
	}

	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "" {
		return "", errors.New("control-plane URL must contain only scheme, host, and an optional base path")
	}
	port := parsed.Port()
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", errors.New("control-plane URL contains an invalid port")
		}
		port = strconv.Itoa(portNumber)
		if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
			port = ""
		}
	}
	if parsed.Scheme == "http" && !IsLoopbackHostname(hostname) {
		return "", errors.New("remote control-plane connections require https; http is allowed only for loopback servers")
	}
	if ip := net.ParseIP(hostname); ip != nil {
		hostname = ip.String()
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	} else {
		parsed.Host = hostname
	}

	canonicalPath, escapedPath, err := canonicalPath(parsed.EscapedPath())
	if err != nil {
		return "", err
	}
	parsed.Path = canonicalPath
	parsed.RawPath = escapedPath
	return parsed.String(), nil
}

// Endpoint appends escaped path segments to a canonical control-plane base URL.
func Endpoint(base string, segments ...string) (string, error) {
	normalized, err := NormalizeBase(base)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse normalized control-plane URL")
	}
	escapedPath := strings.TrimSuffix(parsed.EscapedPath(), "/")
	for _, segment := range segments {
		segment = strings.Trim(segment, "/")
		if segment == "" {
			continue
		}
		escapedPath += "/" + url.PathEscape(segment)
	}
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", errors.Wrap(err, "failed to build control-plane endpoint path")
	}
	parsed.Path = decodedPath
	parsed.RawPath = escapedPath
	return parsed.String(), nil
}

// WebSocketEndpoint builds a control-plane endpoint and converts its scheme to WebSocket.
func WebSocketEndpoint(base string, segments ...string) (string, error) {
	endpoint, err := Endpoint(base, segments...)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse control-plane WebSocket endpoint")
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	return parsed.String(), nil
}

// IsLoopbackHostname reports whether hostname identifies the local machine.
func IsLoopbackHostname(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func canonicalPath(escaped string) (string, string, error) {
	if escaped == "" || escaped == "/" {
		return "", "", nil
	}
	normalized, err := normalizeEscapes(escaped)
	if err != nil {
		return "", "", errors.Wrap(err, "control-plane URL contains an invalid path escape")
	}
	stack := make([]string, 0)
	for _, segment := range strings.Split(normalized, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			stack = append(stack, segment)
		}
	}
	if len(stack) == 0 {
		return "", "", nil
	}
	escapedPath := "/" + strings.Join(stack, "/")
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to decode canonical control-plane path")
	}
	return decodedPath, escapedPath, nil
}

func normalizeEscapes(value string) (string, error) {
	var result strings.Builder
	result.Grow(len(value))
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			result.WriteByte(value[i])
			continue
		}
		if i+2 >= len(value) {
			return "", errors.New("incomplete percent escape")
		}
		high, ok := hexValue(value[i+1])
		if !ok {
			return "", errors.New("invalid percent escape")
		}
		low, ok := hexValue(value[i+2])
		if !ok {
			return "", errors.New("invalid percent escape")
		}
		decoded := high<<4 | low
		if isUnreserved(decoded) {
			result.WriteByte(decoded)
		} else {
			result.WriteByte('%')
			result.WriteByte(hex[decoded>>4])
			result.WriteByte(hex[decoded&0x0f])
		}
		i += 2
	}
	return result.String(), nil
}

func hexValue(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func isUnreserved(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("-._~", rune(value))
}
