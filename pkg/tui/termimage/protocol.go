// Package termimage draws inline images with the Kitty graphics or iTerm2
// inline image protocols. Images are laid out as placeholder cells in ordinary
// text; Kitty displays them natively, and [Output] draws iTerm2 images over them.
package termimage

import (
	"strings"

	"github.com/pkg/errors"
)

// Protocol identifies the inline image protocol used by the terminal.
type Protocol string

const (
	// None disables inline images.
	None Protocol = ""
	// Kitty uses Kitty graphics Unicode placeholders, supported by Kitty and Ghostty.
	Kitty Protocol = "kitty"
	// ITerm2 uses iTerm2's inline image protocol.
	ITerm2 Protocol = "iterm2"
)

// Image protocol settings accepted by [ParseSetting].
const (
	SettingAuto   = "auto"
	SettingKitty  = "kitty"
	SettingITerm2 = "iterm2"
	SettingNone   = "none"
)

// AvailableSettings returns the accepted image protocol settings.
func AvailableSettings() []string {
	return []string{SettingAuto, SettingKitty, SettingITerm2, SettingNone}
}

// ParseSetting normalizes an image protocol setting; blank selects auto.
func ParseSetting(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", SettingAuto:
		return SettingAuto, nil
	case SettingKitty:
		return SettingKitty, nil
	case SettingITerm2:
		return SettingITerm2, nil
	case SettingNone, "off":
		return SettingNone, nil
	}
	return "", errors.Errorf(
		"unknown image protocol %q (available: %s)",
		value,
		strings.Join(AvailableSettings(), ", "),
	)
}

// Resolve returns the protocol selected by a parsed setting, detecting it from
// the environment when the setting is auto.
func Resolve(setting string, getenv func(string) string) Protocol {
	switch setting {
	case SettingKitty:
		return Kitty
	case SettingITerm2:
		return ITerm2
	case SettingNone:
		return None
	}
	return Detect(getenv)
}

// FromTerminalVersion identifies a supported terminal from its XTVERSION
// reply. Unlike its environment, the reply survives SSH.
func FromTerminalVersion(name string) Protocol {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(name, "kitty"), strings.HasPrefix(name, "ghostty"):
		return Kitty
	case strings.HasPrefix(name, "iterm2"):
		return ITerm2
	}
	return None
}

// Detect identifies a supported terminal from its environment. Unknown
// terminals and multiplexers get no images.
func Detect(getenv func(string) string) Protocol {
	term := strings.ToLower(getenv("TERM"))
	if getenv("TMUX") != "" ||
		getenv("ZELLIJ") != "" ||
		strings.HasPrefix(term, "tmux") ||
		strings.HasPrefix(term, "screen") {
		return None
	}

	// The terminal sets TERM itself, and TERM survives SSH.
	if term == "xterm-kitty" || term == "xterm-ghostty" {
		return Kitty
	}

	// KITTY_WINDOW_ID and similar variables leak into terminals launched from
	// a Kitty or Ghostty shell, so TERM_PROGRAM takes precedence.
	if program := strings.ToLower(getenv("TERM_PROGRAM")); program != "" {
		switch program {
		case "kitty", "ghostty":
			return Kitty
		case "iterm.app":
			return ITerm2
		}
		return None
	}

	// LC_TERMINAL is forwarded by SSH's default SendEnv LC_* setting.
	switch {
	case getenv("KITTY_WINDOW_ID") != "" || getenv("GHOSTTY_RESOURCES_DIR") != "":
		return Kitty
	case getenv("LC_TERMINAL") == "iTerm2" || getenv("ITERM_SESSION_ID") != "":
		return ITerm2
	}
	return None
}
