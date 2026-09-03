package tui

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeTerminalTitle(t *testing.T) {
	sanitized := sanitizeTerminalTitle("  Project\t|\nWorking\x1b\x07\u009D\u009C |  Thread  ")
	assert.Equal(t, "Project | Working | Thread", sanitized)
}

func TestSanitizeTerminalTitleStripsInvisibleFormatChars(t *testing.T) {
	sanitized := sanitizeTerminalTitle("Pro\u202Ej\u2066e\u200Fc\u061Ct\u200B \uFEFFT\u2060itle")
	assert.Equal(t, "Project Title", sanitized)
}

func TestSanitizeTerminalTitleStripsFormatCategoryChars(t *testing.T) {
	// Cf codepoints: tag char, Arabic number sign, musical format control.
	sanitized := sanitizeTerminalTitle("re\U000E0067po\u0600na\U0001D173me")
	assert.Equal(t, "reponame", sanitized)
}

func TestSanitizeTerminalTitleTruncates(t *testing.T) {
	sanitized := sanitizeTerminalTitle(strings.Repeat("a", maxTerminalTitleChars+10))
	assert.Equal(t, maxTerminalTitleChars, utf8.RuneCountInString(sanitized))
}

func TestSanitizeTerminalTitleTruncationPrefersVisibleCharOverPendingSpace(t *testing.T) {
	sanitized := sanitizeTerminalTitle(strings.Repeat("a", maxTerminalTitleChars-1) + " b")
	assert.Equal(t, maxTerminalTitleChars, utf8.RuneCountInString(sanitized))
	assert.True(t, strings.HasSuffix(sanitized, "b"))
}

func TestTerminalTitleSpinnerFrameAt(t *testing.T) {
	origin := time.Now()
	assert.Equal(t, "⠋", terminalTitleSpinnerFrameAt(origin, origin))
	assert.Equal(t, "⠙", terminalTitleSpinnerFrameAt(origin, origin.Add(100*time.Millisecond)))
	assert.Equal(t, "⠏", terminalTitleSpinnerFrameAt(origin, origin.Add(950*time.Millisecond)))
	assert.Equal(t, "⠋", terminalTitleSpinnerFrameAt(origin, origin.Add(time.Second)))
	assert.Equal(t, "⠋", terminalTitleSpinnerFrameAt(origin, origin.Add(-time.Second)))
}

func TestTerminalTitleActionRequiredPrefixAt(t *testing.T) {
	origin := time.Now()
	assert.Equal(t, terminalTitleActionRequiredPrefix, terminalTitleActionRequiredPrefixAt(origin, origin))
	assert.Equal(t, terminalTitleActionRequiredPrefixAlt, terminalTitleActionRequiredPrefixAt(origin, origin.Add(time.Second)))
	assert.Equal(t, terminalTitleActionRequiredPrefix, terminalTitleActionRequiredPrefixAt(origin, origin.Add(2*time.Second)))
}

func TestTerminalTitleText(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: "/tmp/myproject"})
	t.Cleanup(m.cancel)
	now := m.terminalTitleEpoch

	assert.Equal(t, "myproject", m.terminalTitleText(now))

	m.running = true
	assert.Equal(t, "⠋ myproject", m.terminalTitleText(now))
	assert.Equal(t, "⠙ myproject", m.terminalTitleText(now.Add(100*time.Millisecond)))

	m.activeUIPrompt = &uiPromptState{}
	assert.Equal(t, "[ ! ] Action Required | myproject", m.terminalTitleText(now))
	assert.Equal(t, "[ ? ] Action Required | myproject", m.terminalTitleText(now.Add(time.Second)))
}

func TestViewDeclaresTerminalTitleState(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: "/tmp/myproject"})
	t.Cleanup(m.cancel)
	m.terminalTitleEpoch = time.Now().Add(time.Hour)

	assert.Equal(t, "myproject", m.View().WindowTitle)

	m.running = true
	assert.Equal(t, "⠋ myproject", m.View().WindowTitle)

	m.activeUIPrompt = &uiPromptState{}
	assert.Equal(t, "[ ! ] Action Required | myproject", m.View().WindowTitle)

	m.activeUIPrompt = nil
	m.running = false
	m.cwd = ""
	assert.Empty(t, m.View().WindowTitle)
}
