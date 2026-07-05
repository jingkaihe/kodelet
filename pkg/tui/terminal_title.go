package tui

// Terminal tab-title spinner.

import (
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

const maxTerminalTitleChars = 240

var terminalTitleSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	terminalTitleSpinnerInterval        = 100 * time.Millisecond
	terminalTitleActionRequiredInterval = time.Second

	terminalTitleActionRequiredPrefix    = "[ ! ] Action Required"
	terminalTitleActionRequiredPrefixAlt = "[ ? ] Action Required"
)

// refreshTerminalTitle rewrites the title, or returns nil when it is current.
func (m *model) refreshTerminalTitle(now time.Time) tea.Cmd {
	title := sanitizeTerminalTitle(m.terminalTitleText(now))
	if title == "" {
		return m.clearTerminalTitle()
	}
	if m.terminalTitleWritten && title == m.lastTerminalTitle {
		return nil
	}
	m.lastTerminalTitle = title
	m.terminalTitleWritten = true
	return tea.SetWindowTitle(title)
}

// clearTerminalTitle clears the title kodelet last wrote, if any.
func (m *model) clearTerminalTitle() tea.Cmd {
	if !m.terminalTitleWritten {
		return nil
	}
	m.lastTerminalTitle = ""
	m.terminalTitleWritten = false
	return tea.SetWindowTitle("")
}

func (m *model) terminalTitleText(now time.Time) string {
	project := terminalTitleProjectName(m.cwd)
	if m.activeUIPrompt != nil {
		prefix := terminalTitleActionRequiredPrefixAt(m.terminalTitleEpoch, now)
		if project == "" {
			return prefix
		}
		return prefix + " | " + project
	}
	if m.running {
		frame := terminalTitleSpinnerFrameAt(m.terminalTitleEpoch, now)
		if project == "" {
			return frame
		}
		return frame + " " + project
	}
	return project
}

func terminalTitleProjectName(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

func terminalTitleSpinnerFrameAt(origin, now time.Time) string {
	elapsed := now.Sub(origin)
	if elapsed < 0 {
		elapsed = 0
	}
	frame := int(elapsed/terminalTitleSpinnerInterval) % len(terminalTitleSpinnerFrames)
	return terminalTitleSpinnerFrames[frame]
}

func terminalTitleActionRequiredPrefixAt(origin, now time.Time) string {
	elapsed := now.Sub(origin)
	if elapsed < 0 {
		elapsed = 0
	}
	if (elapsed/terminalTitleActionRequiredInterval)%2 == 0 {
		return terminalTitleActionRequiredPrefix
	}
	return terminalTitleActionRequiredPrefixAlt
}

// sanitizeTerminalTitle drops disallowed codepoints, collapses whitespace,
// and caps the length in runes.
func sanitizeTerminalTitle(title string) string {
	var sanitized strings.Builder
	charsWritten := 0
	pendingSpace := false

	for _, r := range title {
		if unicode.IsSpace(r) {
			pendingSpace = sanitized.Len() > 0
			continue
		}
		if isDisallowedTerminalTitleChar(r) {
			continue
		}
		// > 1 so a truncated title never ends on the pending space.
		if pendingSpace && maxTerminalTitleChars-charsWritten > 1 {
			sanitized.WriteRune(' ')
			charsWritten++
			pendingSpace = false
		}
		if charsWritten >= maxTerminalTitleChars {
			break
		}
		sanitized.WriteRune(r)
		charsWritten++
	}
	return sanitized.String()
}

// isDisallowedTerminalTitleChar reports whether r is a control or invisible
// formatting codepoint.
func isDisallowedTerminalTitleChar(r rune) bool {
	if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
		return true
	}
	// Invisible codepoints outside the Cf category.
	switch {
	case r == '\u034F', // combining grapheme joiner
		r == '\u2065',                  // unassigned, inside the invisible-operator block
		r >= '\uFE00' && r <= '\uFE0F', // variation selectors
		r >= 0xE0100 && r <= 0xE01EF:   // variation selector supplement
		return true
	}
	return false
}
