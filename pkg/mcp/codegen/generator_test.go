package codegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildTypeScriptType_NestedArrayOfObjects(t *testing.T) {
	// Simulate the Grafana Selector schema with nested LabelMatcher array
	labelMatcherSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "The name of the label to match against",
			},
			"value": map[string]interface{}{
				"type":        "string",
				"description": "The value to match against",
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "One of the '=' or '!=' or '=~' or '!~'",
			},
		},
		"required": []interface{}{"name", "value", "type"},
	}

	selectorSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filters": map[string]interface{}{
				"type":  "array",
				"items": labelMatcherSchema,
			},
		},
	}

	matchesProperty := map[string]interface{}{
		"type":  "array",
		"items": selectorSchema,
	}

	requiredFields := []string{}
	result := buildTypeScriptType(matchesProperty, requiredFields)

	// Expected type should have nested object types with proper structure
	assert.Contains(t, result, "Array<{ filters?:")
	assert.Contains(t, result, "name: string")
	assert.Contains(t, result, "value: string")
	assert.Contains(t, result, "type: string")

	// Verify it's not just "object" or "any"
	assert.NotContains(t, result, "Array<object>")
	assert.NotContains(t, result, "object[]")

	t.Logf("Generated TypeScript type:\n%s", result)
}

func TestBuildTypeScriptType_SimpleTypes(t *testing.T) {
	tests := []struct {
		name     string
		prop     map[string]interface{}
		expected string
	}{
		{
			name:     "string",
			prop:     map[string]interface{}{"type": "string"},
			expected: "string",
		},
		{
			name:     "number",
			prop:     map[string]interface{}{"type": "number"},
			expected: "number",
		},
		{
			name:     "integer",
			prop:     map[string]interface{}{"type": "integer"},
			expected: "number",
		},
		{
			name:     "boolean",
			prop:     map[string]interface{}{"type": "boolean"},
			expected: "boolean",
		},
		{
			name: "array of strings",
			prop: map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			expected: "Array<string>",
		},
		{
			name: "array of numbers",
			prop: map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "number"},
			},
			expected: "Array<number>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildTypeScriptType(tt.prop, []string{})
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildInlineObjectType(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"requiredField": map[string]interface{}{
				"type": "string",
			},
			"optionalField": map[string]interface{}{
				"type": "number",
			},
		},
		"required": []interface{}{"requiredField"},
	}

	result := buildInlineObjectType(schema, []string{})

	assert.Contains(t, result, "requiredField: string")
	assert.Contains(t, result, "optionalField?: number")
	assert.True(t, result[0] == '{' && result[len(result)-1] == '}')

	t.Logf("Generated inline object type:\n%s", result)
}

func TestExtractSchemaProperties_NestedArrays(t *testing.T) {
	// Test the full schema extraction with nested arrays
	labelMatcherSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "The name of the label",
			},
			"value": map[string]interface{}{
				"type":        "string",
				"description": "The value",
			},
		},
		"required": []interface{}{"name", "value"},
	}

	selectorSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filters": map[string]interface{}{
				"type":        "array",
				"items":       labelMatcherSchema,
				"description": "List of filters",
			},
		},
	}

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"matches": map[string]interface{}{
				"type":        "array",
				"items":       selectorSchema,
				"description": "List of selectors",
			},
		},
	}

	properties := extractSchemaProperties(schema)

	assert.Len(t, properties, 1)
	matchesProp := properties[0]

	assert.Equal(t, "matches", matchesProp.Name)
	assert.Equal(t, "List of selectors", matchesProp.Description)
	assert.True(t, matchesProp.IsArrayOfObjects)

	// Check TypeScript type is properly nested
	assert.Contains(t, matchesProp.TypeScriptType, "filters?:")
	assert.Contains(t, matchesProp.TypeScriptType, "name: string")
	assert.Contains(t, matchesProp.TypeScriptType, "value: string")

	// Check that ArrayItemProperties are extracted
	assert.Len(t, matchesProp.ArrayItemProperties, 1)
	filtersProp := matchesProp.ArrayItemProperties[0]
	assert.Equal(t, "filters", filtersProp.Name)
	assert.True(t, filtersProp.IsArrayOfObjects)

	t.Logf("Generated TypeScript type:\n%s", matchesProp.TypeScriptType)
}
