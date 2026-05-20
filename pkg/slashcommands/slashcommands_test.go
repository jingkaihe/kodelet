package slashcommands

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantCommand string
		wantArgs    string
		wantFound   bool
	}{
		{name: "recipe only", text: "/init", wantCommand: "init", wantFound: true},
		{name: "recipe with args", text: "/github/pr target=main polish", wantCommand: "github/pr", wantArgs: "target=main polish", wantFound: true},
		{name: "trims whitespace", text: "  /init focus  ", wantCommand: "init", wantArgs: "focus", wantFound: true},
		{name: "not command", text: "hello /init", wantFound: false},
		{name: "just slash", text: "/", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, args, found := Parse(tt.text)
			assert.Equal(t, tt.wantCommand, command)
			assert.Equal(t, tt.wantArgs, args)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           string
		wantKV         map[string]string
		wantAdditional string
	}{
		{name: "empty", args: "", wantKV: map[string]string{}, wantAdditional: ""},
		{name: "kv only", args: "target=main draft=false", wantKV: map[string]string{"target": "main", "draft": "false"}, wantAdditional: ""},
		{name: "kv and text", args: "target=main fix the bug", wantKV: map[string]string{"target": "main"}, wantAdditional: "fix the bug"},
		{name: "quoted value", args: `title="my feature" draft=true`, wantKV: map[string]string{"title": "my feature", "draft": "true"}, wantAdditional: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kv, additional := ParseArgs(tt.args)
			assert.Equal(t, tt.wantKV, kv)
			assert.Equal(t, tt.wantAdditional, additional)
		})
	}
}

func TestBuildCommandHint(t *testing.T) {
	assert.Equal(t, "additional instructions (optional)", BuildCommandHint(nil))

	arguments := map[string]fragments.ArgumentMeta{
		"target": {Default: "main"},
		"draft":  {},
	}
	assert.Equal(t, "[draft=<value> target=main] additional instructions", BuildCommandHint(arguments))
}
