package tui

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeTerminalTitle(t *testing.T) {
	sanitized := sanitizeTerminalTitle("  Project\t|\nWorking\x1b\x07\u009D\u009C |  Thread  ")
	assert.Equal(t, "Project | Working | Thread", sanitized)
}

func TestSanitizeTerminalTitleStripsInvisibleFormatChars(t *testing.T) {
	sanitized := sanitizeTerminalTitle("Pro\u202Ej\u2066e\u200Fc\u061Ct\u200B \uFEFFT\u2060itle")
	assert.Equal(t, "Project Title", sanitized)
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

func TestRefreshTerminalTitleSkipsUnchangedWrites(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: "/tmp/myproject"})
	t.Cleanup(m.cancel)
	m.running = true
	now := m.terminalTitleEpoch

	require.NotNil(t, m.refreshTerminalTitle(now))
	assert.Equal(t, "⠋ myproject", m.lastTerminalTitle)

	// Same spinner frame: no rewrite.
	assert.Nil(t, m.refreshTerminalTitle(now.Add(50*time.Millisecond)))

	// Next frame: rewrite.
	require.NotNil(t, m.refreshTerminalTitle(now.Add(100*time.Millisecond)))
	assert.Equal(t, "⠙ myproject", m.lastTerminalTitle)

	// Run finished: title drops the spinner segment.
	m.running = false
	require.NotNil(t, m.refreshTerminalTitle(now.Add(200*time.Millisecond)))
	assert.Equal(t, "myproject", m.lastTerminalTitle)
}

func TestRefreshTerminalTitleClearsWhenTitleBecomesEmpty(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: "/tmp/myproject"})
	t.Cleanup(m.cancel)
	now := m.terminalTitleEpoch

	// Nothing written yet, empty title: nothing to clear.
	m.cwd = ""
	assert.Nil(t, m.refreshTerminalTitle(now))

	m.cwd = "/tmp/myproject"
	require.NotNil(t, m.refreshTerminalTitle(now))
	require.True(t, m.terminalTitleWritten)

	m.cwd = ""
	assert.NotNil(t, m.refreshTerminalTitle(now))
	assert.False(t, m.terminalTitleWritten)
	assert.Empty(t, m.lastTerminalTitle)
}

func TestClearTerminalTitleOnlyClearsManagedTitle(t *testing.T) {
	m := newModel(context.Background(), Config{CWD: "/tmp/myproject"})
	t.Cleanup(m.cancel)

	assert.Nil(t, m.clearTerminalTitle())

	require.NotNil(t, m.refreshTerminalTitle(m.terminalTitleEpoch))
	assert.NotNil(t, m.clearTerminalTitle())
	assert.Nil(t, m.clearTerminalTitle())
}
