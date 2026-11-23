package termimage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSetting(t *testing.T) {
	for input, want := range map[string]string{
		"":         SettingAuto,
		" Auto ":   SettingAuto,
		"KITTY":    SettingKitty,
		"iterm2":   SettingITerm2,
		"none":     SettingNone,
		"off":      SettingNone,
		" iTerm2 ": SettingITerm2,
	} {
		got, err := ParseSetting(input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}

	_, err := ParseSetting("sixel")
	assert.ErrorContains(t, err, `unknown image protocol "sixel" (available: auto, kitty, iterm2, none)`)
}

func TestResolveHonorsExplicitSettings(t *testing.T) {
	kittyEnv := env(map[string]string{"TERM": "xterm-kitty"})
	assert.Equal(t, ITerm2, Resolve(SettingITerm2, kittyEnv))
	assert.Equal(t, None, Resolve(SettingNone, kittyEnv))
	assert.Equal(t, Kitty, Resolve(SettingKitty, env(nil)))
	assert.Equal(t, Kitty, Resolve(SettingAuto, kittyEnv))
}

func TestDetect(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
		want Protocol
	}{
		{name: "kitty terminfo", env: map[string]string{"TERM": "xterm-kitty"}, want: Kitty},
		{name: "kitty window", env: map[string]string{"TERM": "xterm-256color", "KITTY_WINDOW_ID": "1"}, want: Kitty},
		{name: "ghostty program", env: map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "ghostty"}, want: Kitty},
		{name: "ghostty terminfo over ssh", env: map[string]string{"TERM": "xterm-ghostty"}, want: Kitty},
		{name: "iterm2 program", env: map[string]string{"TERM_PROGRAM": "iTerm.app"}, want: ITerm2},
		{name: "iterm2 over ssh", env: map[string]string{"TERM": "xterm-256color", "LC_TERMINAL": "iTerm2"}, want: ITerm2},
		{name: "kitty launched from iterm2", env: map[string]string{"TERM": "xterm-kitty", "TERM_PROGRAM": "iTerm.app"}, want: Kitty},
		{
			name: "vscode launched from kitty",
			env:  map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "vscode", "KITTY_WINDOW_ID": "1"},
			want: None,
		},
		{name: "tmux inside kitty", env: map[string]string{"TERM": "tmux-256color", "TMUX": "/tmp/tmux", "KITTY_WINDOW_ID": "1"}, want: None},
		{name: "screen", env: map[string]string{"TERM": "screen-256color", "TERM_PROGRAM": "ghostty"}, want: None},
		{name: "zellij", env: map[string]string{"TERM": "xterm-kitty", "ZELLIJ": "0"}, want: None},
		{name: "wezterm", env: map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "WezTerm"}, want: None},
		{name: "unknown", env: map[string]string{"TERM": "xterm-256color"}, want: None},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, Detect(env(test.env)))
		})
	}
}

func TestFromTerminalVersion(t *testing.T) {
	for name, want := range map[string]Protocol{
		"ghostty 1.4.0":   Kitty,
		"kitty(0.48.2)":   Kitty,
		"iTerm2 3.5.10":   ITerm2,
		"WezTerm 2026":    None,
		"tmux 3.5a":       None,
		"":                None,
		" Ghostty 1.0.1 ": Kitty,
	} {
		assert.Equal(t, want, FromTerminalVersion(name), name)
	}
}

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
