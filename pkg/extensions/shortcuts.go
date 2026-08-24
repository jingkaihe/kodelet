package extensions

import (
	"context"
	"strings"

	"github.com/pkg/errors"
)

const (
	// ShortcutActionSubmit asks the native TUI to submit a conversation message.
	ShortcutActionSubmit = "submit"
)

// NormalizeShortcutKey validates and canonicalizes a native TUI shortcut key.
func NormalizeShortcutKey(key string) (string, error) {
	original := key
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("shortcut key is required")
	}
	if !isASCII(key) {
		return "", errors.Errorf("unsupported shortcut %q: shortcut identifiers must use ASCII characters", original)
	}
	key = strings.ToLower(key)
	if strings.ContainsAny(key, " \t\r\n") {
		return "", errors.Errorf("invalid shortcut key %q", original)
	}

	modifierAliases := map[string]string{
		"control": "ctrl",
		"option":  "alt",
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
		case "ctrl", "alt":
			if modifiers[part] {
				return "", errors.Errorf("invalid shortcut key %q", original)
			}
			modifiers[part] = true
			continue
		case "shift":
			return "", errors.Errorf("unsupported shortcut modifier %q", rawPart)
		case "cmd", "command", "meta", "super":
			return "", errors.Errorf("unsupported shortcut modifier %q", rawPart)
		}
		if base != "" {
			return "", errors.Errorf("invalid shortcut key %q", original)
		}
		base = part
	}
	if base == "" {
		return "", errors.Errorf("invalid shortcut key %q", original)
	}

	ctrl := modifiers["ctrl"]
	alt := modifiers["alt"]
	if isFunctionKey(base) {
		if ctrl || alt {
			return "", errors.Errorf("unsupported shortcut %q: function keys must not use modifiers", original)
		}
		return base, nil
	}
	if !ctrl && !alt {
		return "", errors.Errorf("unsupported shortcut %q: use ctrl+letter, alt+letter-or-digit, ctrl+alt+letter, or f1 through f12", original)
	}
	validBase := isASCIILetter(base) || (alt && !ctrl && isASCIIDigit(base))
	if !validBase {
		return "", errors.Errorf("unsupported shortcut key %q", original)
	}
	if ctrl && (base == "i" || base == "m") {
		terminalKey := "tab"
		if base == "m" {
			terminalKey = "enter"
		}
		return "", errors.Errorf("unsupported shortcut %q: terminals report ctrl+%s as %s", original, base, terminalKey)
	}

	parts := make([]string, 0, 3)
	for _, modifier := range []string{"ctrl", "alt"} {
		if modifiers[modifier] {
			parts = append(parts, modifier)
		}
	}
	return strings.Join(append(parts, base), "+"), nil
}

func isASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] > 0x7f {
			return false
		}
	}
	return true
}

func isASCIILetter(base string) bool {
	return len(base) == 1 && base[0] >= 'a' && base[0] <= 'z'
}

func isASCIIDigit(base string) bool {
	return len(base) == 1 && base[0] >= '0' && base[0] <= '9'
}

func isFunctionKey(base string) bool {
	switch base {
	case "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12":
		return true
	default:
		return false
	}
}

// ExecuteShortcut invokes the effective extension shortcut registered for key.
func (r *Runtime) ExecuteShortcut(ctx context.Context, key string, callContext ExtensionCallContext) (bool, error) {
	matched, result, err := r.ExecuteShortcutWithResult(ctx, key, callContext)
	if err != nil {
		return matched, err
	}
	if result != nil {
		return matched, errors.New("extension shortcut returned a host action; use ExecuteShortcutWithResult")
	}
	return matched, nil
}

// ExecuteShortcutWithResult invokes the effective extension shortcut
// registered for key and returns its optional host action.
func (r *Runtime) ExecuteShortcutWithResult(ctx context.Context, key string, callContext ExtensionCallContext) (bool, *ShortcutResult, error) {
	if r == nil {
		return false, nil, nil
	}
	normalized, err := NormalizeShortcutKey(key)
	if err != nil {
		return false, nil, err
	}

	r.mu.RLock()
	shortcut, ok := r.shortcuts[normalized]
	r.mu.RUnlock()
	if !ok {
		return false, nil, nil
	}
	if shortcut.process == nil {
		return true, nil, errors.Errorf("extension shortcut %s has no process", normalized)
	}
	result, err := shortcut.process.ExecuteShortcutWithResult(ctx, normalized, callContext)
	if err != nil {
		return true, nil, errors.Wrapf(err, "failed to execute extension shortcut %s from %s", normalized, shortcut.ExtensionID)
	}
	return true, result, nil
}
