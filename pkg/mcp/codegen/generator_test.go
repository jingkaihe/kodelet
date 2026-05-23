package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTypeScriptType_NestedArrayOfObjects(t *testing.T) {
	// Simulate the Grafana Selector schema with nested LabelMatcher array
	labelMatcherSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of the label to match against",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "The value to match against",
			},
			"type": map[string]any{
				"type":        "string",
				"description": "One of the '=' or '!=' or '=~' or '!~'",
			},
		},
		"required": []any{"name", "value", "type"},
	}

	selectorSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filters": map[string]any{
				"type":  "array",
				"items": labelMatcherSchema,
			},
		},
	}

	matchesProperty := map[string]any{
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
		prop     map[string]any
		expected string
	}{
		{
			name:     "string",
			prop:     map[string]any{"type": "string"},
			expected: "string",
		},
		{
			name:     "number",
			prop:     map[string]any{"type": "number"},
			expected: "number",
		},
		{
			name:     "integer",
			prop:     map[string]any{"type": "integer"},
			expected: "number",
		},
		{
			name:     "boolean",
			prop:     map[string]any{"type": "boolean"},
			expected: "boolean",
		},
		{
			name: "array of strings",
			prop: map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			expected: "Array<string>",
		},
		{
			name: "array of numbers",
			prop: map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "number"},
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
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"requiredField": map[string]any{
				"type": "string",
			},
			"optionalField": map[string]any{
				"type": "number",
			},
		},
		"required": []any{"requiredField"},
	}

	result := buildInlineObjectType(schema, []string{})

	assert.Contains(t, result, "requiredField: string")
	assert.Contains(t, result, "optionalField?: number")
	assert.True(t, result[0] == '{' && result[len(result)-1] == '}')

	t.Logf("Generated inline object type:\n%s", result)
}

func TestExtractSchemaProperties_NestedArrays(t *testing.T) {
	// Test the full schema extraction with nested arrays
	labelMatcherSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of the label",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "The value",
			},
		},
		"required": []any{"name", "value"},
	}

	selectorSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filters": map[string]any{
				"type":        "array",
				"items":       labelMatcherSchema,
				"description": "List of filters",
			},
		},
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"matches": map[string]any{
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

func TestGenerateClient_UsesWorkspaceRelativeSocketDefault(t *testing.T) {
	outputDir := t.TempDir()
	generator := NewMCPCodeGenerator(nil, outputDir)

	err := generator.generateClient()
	require.NoError(t, err)

	clientTS, err := os.ReadFile(filepath.Join(outputDir, "client.ts"))
	require.NoError(t, err)

	assert.Contains(t, string(clientTS), "const CURRENT_DIR = path.dirname(fileURLToPath(import.meta.url));")
	assert.Contains(t, string(clientTS), "path.join(CURRENT_DIR, 'kodelet-mcp.sock')")
	assert.NotContains(t, string(clientTS), "/tmp/kodelet-mcp.sock")
}

func TestGenerateClient_SupportsHTTPRPCTransport(t *testing.T) {
	outputDir := t.TempDir()
	generator := NewMCPCodeGenerator(nil, outputDir)

	err := generator.generateClient()
	require.NoError(t, err)

	clientTS, err := os.ReadFile(filepath.Join(outputDir, "client.ts"))
	require.NoError(t, err)
	client := string(clientTS)

	assert.Contains(t, client, "const MCP_RPC_URL = process.env.MCP_RPC_URL;")
	assert.Contains(t, client, "const MCP_RPC_TOKEN = process.env.MCP_RPC_TOKEN;")
	assert.Contains(t, client, "headers['Authorization'] = `Bearer ${MCP_RPC_TOKEN}`;")
	assert.Contains(t, client, "const url = new URL(MCP_RPC_URL);")
	assert.Contains(t, client, "hostname: url.hostname")
	assert.Contains(t, client, "socketPath: MCP_RPC_SOCKET")
}

func TestGeneratorSimpleFileOutputs(t *testing.T) {
	outputDir := t.TempDir()
	generator := NewMCPCodeGenerator(nil, outputDir)

	require.NoError(t, generator.generatePackageJSON())
	packageJSON, err := os.ReadFile(filepath.Join(outputDir, "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(packageJSON), `"type": "module"`)
	assert.Contains(t, string(packageJSON), `"private": true`)

	serverDir := filepath.Join(outputDir, "servers", "demo")
	require.NoError(t, os.MkdirAll(serverDir, 0o755))
	tool := mcp.Tool{
		Name:        "get_user_info",
		Description: "Fetch a user record */ safely",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"user_id": map[string]any{
					"type":        "string",
					"description": "The user identifier",
					"default":     "me",
					"enum":        []any{"me", "you"},
					"minLength":   float64(2),
				},
			},
			Required: []string{"user_id"},
		},
		OutputSchema: mcp.ToolOutputSchema{
			Type: "object",
			Properties: map[string]any{
				"ok": map[string]any{"type": "boolean", "description": "Whether lookup succeeded"},
			},
		},
	}

	info, err := generator.generateToolFile(serverDir, "demo", tool)
	require.NoError(t, err)
	assert.Equal(t, ToolInfo{FunctionName: "getUserInfo", Description: tool.Description}, info)

	toolTS, err := os.ReadFile(filepath.Join(serverDir, "getUserInfo.ts"))
	require.NoError(t, err)
	toolSource := string(toolTS)
	assert.Contains(t, toolSource, "export async function getUserInfo")
	assert.Contains(t, toolSource, "getUserInfoInput")
	assert.Contains(t, toolSource, "getUserInfoResponse")
	assert.Contains(t, toolSource, "Fetch a user record *\\/ safely")
	assert.Contains(t, toolSource, "user_id: string;")
	assert.Contains(t, toolSource, `@enum ["me", "you"]`)
	assert.Contains(t, toolSource, "ok?: boolean;")

	require.NoError(t, generator.generateServerIndex(serverDir, []mcp.Tool{tool}))
	indexTS, err := os.ReadFile(filepath.Join(serverDir, "index.ts"))
	require.NoError(t, err)
	assert.Contains(t, string(indexTS), "export { getUserInfo } from './getUserInfo.js';")

	generator.SetServerFilter("demo")
	assert.Equal(t, "demo", generator.serverFilter)
	assert.Equal(t, GeneratorStats{}, generator.GetStats())
}

func TestParseSchemaAndHelperBranches(t *testing.T) {
	schema, err := parseSchema(map[string]any{
		"type":     "object",
		"required": []any{"name", 42},
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Display name",
				"default":     "octo",
				"enum":        []any{"octo", "bot"},
				"minimum":     1,
				"maximum":     float64(10),
				"minLength":   float64(2),
				"maxLength":   20,
				"pattern":     "^[a-z]+$",
				"format":      "hostname",
			},
			"ignored": "not an object",
		},
	})
	require.NoError(t, err)
	require.Len(t, schema.Properties, 1)
	prop := schema.Properties[0]
	assert.Equal(t, "name", prop.Name)
	assert.True(t, prop.Required)
	assert.Equal(t, "Display name", prop.Description)
	assert.Equal(t, "octo", prop.Default)
	assert.Equal(t, []any{"octo", "bot"}, prop.Enum)
	require.NotNil(t, prop.Minimum)
	assert.Equal(t, float64(1), *prop.Minimum)
	require.NotNil(t, prop.Maximum)
	assert.Equal(t, float64(10), *prop.Maximum)
	require.NotNil(t, prop.MinLength)
	assert.Equal(t, 2, *prop.MinLength)
	require.NotNil(t, prop.MaxLength)
	assert.Equal(t, 20, *prop.MaxLength)
	assert.Equal(t, "^[a-z]+$", prop.Pattern)
	assert.Equal(t, "hostname", prop.Format)
	assert.Equal(t, "string", prop.TypeScriptType)

	assert.Equal(t, "", toTitle(""))
	assert.Equal(t, "Hello", toTitle("hello"))
	assert.Equal(t, "alreadyCamel", sanitizeName("alreadyCamel"))
	assert.Equal(t, "helloWorld", sanitizeName("hello_world"))
	assert.Equal(t, "any", buildTypeScriptType(map[string]any{}, nil))
	assert.Equal(t, "any", buildTypeScriptType(map[string]any{"type": []string{"string"}}, nil))
	assert.Equal(t, "any", buildTypeScriptType(map[string]any{"type": "mystery"}, nil))
	assert.Equal(t, "Array<any>", buildTypeScriptType(map[string]any{"type": "array"}, nil))
	assert.Equal(t, "Record<string, any>", buildTypeScriptType(map[string]any{"type": "object"}, nil))
	assert.Equal(t, "Record<string, any>", buildInlineObjectType(map[string]any{"type": "object", "properties": map[string]any{"bad": "shape"}}, nil))
	assert.Nil(t, getFloat(map[string]any{"n": "nope"}, "n"))
	assert.Nil(t, getFloat(map[string]any{}, "n"))
	assert.Nil(t, getInt(map[string]any{"n": "nope"}, "n"))
	assert.Nil(t, getInt(map[string]any{}, "n"))
	assert.Nil(t, getArray(map[string]any{"values": "nope"}, "values"))
	assert.Nil(t, getArray(map[string]any{}, "values"))
	assert.Empty(t, getString(map[string]any{"name": 42}, "name"))
	assert.Empty(t, getRequiredFields(map[string]any{"required": []any{42}}))
	assert.Empty(t, extractSchemaProperties(map[string]any{"properties": []any{}}))
	assert.False(t, contains([]string{"a"}, "b"))
	assert.True(t, strings.HasPrefix(buildInlineObjectType(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"requiredChild": map[string]any{"type": "number"},
		},
	}, []string{"requiredChild"}), "{ requiredChild: number"))
}
