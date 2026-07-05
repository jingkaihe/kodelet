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

// refreshTerminalTitle returns a command that rewrites the title, or nil when
// it is already current.
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

// clearTerminalTitle clears the title kodelet last wrote, if any; restoring
// the shell's previous title is not portable across terminals.
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

// sanitizeTerminalTitle drops disallowed codepoints, collapses whitespace runs
// to single spaces, and caps the result at maxTerminalTitleChars runes.
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

// isDisallowedTerminalTitleChar reports whether r is a control character or an
// invisible/bidi formatting codepoint that must not reach the title sequence.
func isDisallowedTerminalTitleChar(r rune) bool {
	if unicode.IsControl(r) {
		return true
	}
	switch {
	case r == '\u00AD', // soft hyphen
		r == '\u034F',                  // combining grapheme joiner
		r == '\u061C',                  // arabic letter mark
		r == '\u180E',                  // mongolian vowel separator
		r >= '\u200B' && r <= '\u200F', // zero-width spaces/joiners, LRM/RLM
		r >= '\u202A' && r <= '\u202E', // bidi embedding/override controls
		r >= '\u2060' && r <= '\u206F', // word joiner, invisible operators, bidi isolates
		r >= '\uFE00' && r <= '\uFE0F', // variation selectors
		r == '\uFEFF',                  // zero-width no-break space / BOM
		r >= '\uFFF9' && r <= '\uFFFB', // interlinear annotation controls
		r >= 0x1BCA0 && r <= 0x1BCA3,   // shorthand format controls
		r >= 0xE0100 && r <= 0xE01EF:   // variation selector supplement
		return true
	}
	return false
}
