package extension

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeShortcutKey(t *testing.T) {
	valid := map[string]string{
		"Ctrl+G": "ctrl+g", "control+alt+K": "ctrl+alt+k", "alt+ctrl+k": "ctrl+alt+k",
		"option+1": "alt+1", "F5": "f5", "f12": "f12", " ctrl+z ": "ctrl+z",
	}
	for input, expected := range valid {
		normalized, err := normalizeShortcutKey(input)
		require.NoError(t, err, input)
		assert.Equal(t, expected, normalized, input)
	}
	invalid := map[string]string{
		"":             "required",
		"ctrl+shift+a": "modifier: shift",
		"cmd+a":        "modifier: cmd",
		"ctrl+1":       "unsupported shortcut key",
		"ctrl+i":       "as tab",
		"ctrl+m":       "as enter",
		"a":            "use ctrl+letter",
		"ctrl+f5":      "function keys must not use modifiers",
		"ctrl++":       "invalid shortcut",
		"ctrl+é":       "ASCII",
		"ctrl+a b":     "invalid shortcut",
		"ctrl+ctrl+a":  "invalid shortcut",
		"ctrl+a+b":     "invalid shortcut",
		"f13":          "use ctrl+letter",
	}
	for input, message := range invalid {
		_, err := normalizeShortcutKey(input)
		require.ErrorContains(t, err, message, input)
	}
}

type nestedInput struct {
	Items []struct {
		Name string `json:"name"`
	} `json:"items"`
	Optional *int              `json:"optional,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

func TestInferredSchemasCollapseNullableTypes(t *testing.T) {
	schema, err := inferInputSchema(reflect.TypeFor[nestedInput]())
	require.NoError(t, err)
	properties := schema.document["properties"].(map[string]any)
	items := properties["items"].(map[string]any)
	assert.Equal(t, "array", items["type"])
	assert.Equal(t, "string", items["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)["type"])
	assert.Equal(t, "integer", properties["optional"].(map[string]any)["type"])
	assert.Equal(t, "object", properties["labels"].(map[string]any)["type"])
	require.NoError(t, schema.validate([]byte(`{"items":[{"name":"a"}],"optional":3}`)))
	require.Error(t, schema.validate([]byte(`{"items":[{"name":1}]}`)))
	require.Error(t, schema.validate(nil), "missing input is validated as an empty object")
}
