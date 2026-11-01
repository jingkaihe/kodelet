package extension

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var functionKeyPattern = regexp.MustCompile(`^f(?:[1-9]|1[0-2])$`)

func normalizeShortcutKey(shortcut string) (string, error) {
	trimmed := strings.TrimSpace(shortcut)
	if trimmed == "" {
		return "", fmt.Errorf("shortcut key is required")
	}
	for _, character := range trimmed {
		if character > unicode.MaxASCII {
			return "", fmt.Errorf("unsupported shortcut %s; shortcut identifiers must use ASCII characters", shortcut)
		}
	}
	value := strings.ToLower(trimmed)
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("invalid shortcut %s", shortcut)
	}
	parts := strings.Split(value, "+")
	modifiers := map[string]bool{}
	base := ""
	for _, raw := range parts {
		if raw == "" {
			return "", fmt.Errorf("invalid shortcut %s", shortcut)
		}
		part := raw
		switch raw {
		case "control":
			part = "ctrl"
		case "option":
			part = "alt"
		}
		switch part {
		case "ctrl", "alt":
			if modifiers[part] {
				return "", fmt.Errorf("invalid shortcut %s", shortcut)
			}
			modifiers[part] = true
			continue
		case "shift", "cmd", "command", "meta", "super":
			return "", fmt.Errorf("unsupported shortcut modifier: %s", raw)
		}
		if base != "" {
			return "", fmt.Errorf("invalid shortcut %s", shortcut)
		}
		base = part
	}
	if base == "" {
		return "", fmt.Errorf("invalid shortcut %s", shortcut)
	}
	ctrl, alt := modifiers["ctrl"], modifiers["alt"]
	if functionKeyPattern.MatchString(base) {
		if ctrl || alt {
			return "", fmt.Errorf("unsupported shortcut %s; function keys must not use modifiers", shortcut)
		}
		return base, nil
	}
	if !ctrl && !alt {
		return "", fmt.Errorf("unsupported shortcut %s; use ctrl+letter, alt+letter-or-digit, ctrl+alt+letter, or f1 through f12", shortcut)
	}
	letter := len(base) == 1 && base[0] >= 'a' && base[0] <= 'z'
	digit := len(base) == 1 && base[0] >= '0' && base[0] <= '9'
	if !letter && (!alt || ctrl || !digit) {
		return "", fmt.Errorf("unsupported shortcut key: %s", shortcut)
	}
	if ctrl && (base == "i" || base == "m") {
		terminalKey := "tab"
		if base == "m" {
			terminalKey = "enter"
		}
		return "", fmt.Errorf("unsupported shortcut %s; terminals report ctrl+%s as %s", shortcut, base, terminalKey)
	}
	var normalized []string
	if ctrl {
		normalized = append(normalized, "ctrl")
	}
	if alt {
		normalized = append(normalized, "alt")
	}
	return strings.Join(append(normalized, base), "+"), nil
}
