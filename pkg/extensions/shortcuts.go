package extensions

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pkg/errors"
)

// NormalizeShortcutKey validates and canonicalizes a native TUI shortcut key.
func NormalizeShortcutKey(key string) (string, error) {
	original := key
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "", errors.New("shortcut key is required")
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return "", errors.Errorf("invalid shortcut key %q", original)
	}

	modifierAliases := map[string]string{
		"control": "ctrl",
		"option":  "alt",
	}
	baseAliases := map[string]string{
		"escape":   "esc",
		"return":   "enter",
		"pageup":   "pgup",
		"pagedown": "pgdown",
	}
	modifiers := map[string]bool{}
	base := ""
	for _, rawPart := range strings.Split(key, "+") {
		if rawPart == "" {
			return "", errors.Errorf("invalid shortcut key %q", original)
		}
		part := rawPart
		if alias := modifierAliases[part]; alias != "" {
			part = alias
		}
		switch part {
		case "ctrl", "alt", "shift":
			if modifiers[part] {
				return "", errors.Errorf("invalid shortcut key %q", original)
			}
			modifiers[part] = true
			continue
		case "cmd", "command", "meta", "super":
			return "", errors.Errorf("unsupported shortcut modifier %q", rawPart)
		}
		if base != "" {
			return "", errors.Errorf("invalid shortcut key %q", original)
		}
		if alias := baseAliases[part]; alias != "" {
			part = alias
		}
		base = part
	}
	if base == "" {
		return "", errors.Errorf("invalid shortcut key %q", original)
	}
	if !validShortcutBase(base) {
		return "", errors.Errorf("invalid shortcut key %q", original)
	}
	if !modifiers["ctrl"] && !modifiers["alt"] && !isFunctionKey(base) {
		return "", errors.Errorf("shortcut %q must use ctrl or alt, or be a function key", original)
	}

	parts := make([]string, 0, 4)
	for _, modifier := range []string{"ctrl", "alt", "shift"} {
		if modifiers[modifier] {
			parts = append(parts, modifier)
		}
	}
	return strings.Join(append(parts, base), "+"), nil
}

func validShortcutBase(base string) bool {
	if utf8.RuneCountInString(base) == 1 {
		return true
	}
	if isFunctionKey(base) {
		return true
	}
	switch base {
	case "esc", "enter", "tab", "space", "backspace", "delete", "insert", "home", "end", "pgup", "pgdown", "up", "down", "left", "right":
		return true
	default:
		return false
	}
}

func isFunctionKey(base string) bool {
	if !strings.HasPrefix(base, "f") {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(base, "f"))
	return err == nil && n >= 1 && n <= 12
}

// ExecuteShortcut invokes the effective extension shortcut registered for key.
func (r *Runtime) ExecuteShortcut(ctx context.Context, key string, callContext ExtensionCallContext) (bool, error) {
	if r == nil {
		return false, nil
	}
	normalized, err := NormalizeShortcutKey(key)
	if err != nil {
		return false, err
	}

	r.mu.RLock()
	shortcut, ok := r.shortcuts[normalized]
	r.mu.RUnlock()
	if !ok {
		return false, nil
	}
	if shortcut.process == nil {
		return true, errors.Errorf("extension shortcut %s has no process", normalized)
	}
	if err := shortcut.process.ExecuteShortcut(ctx, normalized, callContext); err != nil {
		return true, errors.Wrapf(err, "failed to execute extension shortcut %s from %s", normalized, shortcut.ExtensionID)
	}
	return true, nil
}
