package codemode

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func catalogPageNames(page Page) []string {
	names := make([]string, len(page.Tools))
	for i, tool := range page.Tools {
		names[i] = tool.Name
	}
	return names
}

func TestCatalogListAndDescribe(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo": map[string]any{"type": "string", "description": "Repository slug"},
			"limit": map[string]any{
				"type": "integer", "default": 10,
			},
		},
		"required":             []string{"repo"},
		"additionalProperties": false,
	}
	output := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	definitions := []Definition{
		{Name: "z", Description: "Shell tool", Group: "core"},
		{
			Name: `git-hub/"issues"`, Description: "Find\n repository issues", Group: "mcp/github",
			InputSchema: input, OutputSchema: output,
		},
	}
	catalog := NewCatalog(definitions)
	page, err := catalog.List(CatalogOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{`git-hub/"issues"`, "z"}, catalogPageNames(page))
	assert.Equal(t, "Find repository issues", page.Tools[0].Description)
	assert.Empty(t, page.NextCursor)

	description, err := catalog.Describe(`git-hub/"issues"`)
	require.NoError(t, err)
	assert.Contains(t, description.Declaration, `"git-hub/\"issues\""`)
	assert.Contains(t, description.Declaration, `repo: string`)
	assert.Contains(t, description.Declaration, `limit?: number`)
	assert.Contains(t, description.Declaration, `Promise<ToolReply<Array<string>>>`)
	assert.Equal(t, "Find\n repository issues", description.Description)
	payload, err := json.Marshal(description)
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"inputSchema":`)
	assert.NotContains(t, string(payload), `"Definition":`)

	// Neither the caller's schemas nor a previous description can mutate the snapshot.
	input["properties"].(map[string]any)["repo"].(map[string]any)["type"] = "boolean"
	output["items"].(map[string]any)["type"] = "number"
	definitions[1].Name = "changed"
	description.InputSchema["properties"].(map[string]any)["repo"].(map[string]any)["type"] = "null"
	description.OutputSchema["items"].(map[string]any)["type"] = "object"
	again, err := catalog.Describe(`git-hub/"issues"`)
	require.NoError(t, err)
	assert.Equal(t, description.Declaration, again.Declaration)
	assert.Equal(t, "string", again.InputSchema["properties"].(map[string]any)["repo"].(map[string]any)["type"])

	page, err = catalog.List(CatalogOptions{Group: "mcp/github"})
	require.NoError(t, err)
	assert.Equal(t, []string{`git-hub/"issues"`}, catalogPageNames(page))
	page, err = catalog.List(CatalogOptions{Group: "mcp/git"})
	require.NoError(t, err)
	assert.Empty(t, page.Tools)
	_, err = catalog.Describe("hidden_admin_tool")
	require.EqualError(t, err, "tool is not available in this catalog")
	_, err = catalog.Describe("Z")
	require.Error(t, err, "describe must use the exact registered name")
	description, err = catalog.Describe("z")
	require.NoError(t, err)
	assert.Contains(t, description.Declaration, "(input: unknown) => Promise<ToolReply<unknown>>")
	assert.Nil(t, description.OutputSchema)
}

func TestCatalogOutputSchema(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []string{"id"}}
	catalog := NewCatalog([]Definition{
		{Name: "typed", OutputSchema: schema},
		{Name: "untyped"},
	})
	got := catalog.OutputSchema("typed")
	assert.Equal(t, map[string]any{"type": "object", "required": []any{"id"}}, got)
	got["type"] = "array"
	schema["type"] = "array"
	assert.Equal(t, "object", catalog.OutputSchema("typed")["type"], "callers get defensive copies")
	assert.Nil(t, catalog.OutputSchema("untyped"))
	assert.Nil(t, catalog.OutputSchema("missing"))
}

func TestRuntimeDeclarationIsSharedByDescribe(t *testing.T) {
	for _, field := range []string{
		"declare const catalog",
		"declare const tools",
		"interface ToolReply<T = unknown>",
		"data: T | null;",
		"attachments: ArtifactRef[];",
		"interface ArtifactRef",
		"interface ToolError extends Error",
		`outcome: "not_started" | "completed" | "unknown";`,
		"result?: ToolReply;",
		"declare namespace emit",
	} {
		assert.Contains(t, RuntimeDeclaration, field)
	}
	description, err := NewCatalog([]Definition{{Name: "tool"}}).Describe("tool")
	require.NoError(t, err)
	assert.Equal(t, "declare const tools: {\n  \"tool\": (input: unknown) => Promise<ToolReply<unknown>>;\n};",
		description.Declaration, "describe declares only the tool; shared types live in the code_execute description")
}

func TestCatalogPagination(t *testing.T) {
	definitions := make([]Definition, 250)
	for i := range definitions {
		definitions[i] = Definition{Name: fmt.Sprintf("tool_%03d", i), Description: "tool lookup"}
	}
	catalog := NewCatalog(definitions)
	first, err := catalog.List(CatalogOptions{})
	require.NoError(t, err)
	assert.Len(t, first.Tools, 20)
	require.NotEmpty(t, first.NextCursor)
	capped, err := catalog.List(CatalogOptions{Limit: 10000})
	require.NoError(t, err)
	assert.Len(t, capped.Tools, 100)
	_, err = catalog.List(CatalogOptions{Limit: -1})
	require.Error(t, err)

	// The same authorized definitions, in another order/instance, accept the cursor.
	slices.Reverse(definitions)
	other := NewCatalog(definitions)
	second, err := other.List(CatalogOptions{Cursor: first.NextCursor, Limit: 100})
	require.NoError(t, err)
	assert.Equal(t, "tool_020", second.Tools[0].Name)
	third, err := other.List(CatalogOptions{Cursor: second.NextCursor, Limit: 100})
	require.NoError(t, err)
	last, err := other.List(CatalogOptions{Cursor: third.NextCursor, Limit: 100})
	require.NoError(t, err)
	assert.Len(t, last.Tools, 30)
	assert.Equal(t, "tool_249", last.Tools[len(last.Tools)-1].Name)
	assert.Empty(t, last.NextCursor)

	searched, err := catalog.Search("lookup", CatalogOptions{Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, searched.NextCursor)
	continued, err := other.Search(" LOOKUP ", CatalogOptions{Cursor: searched.NextCursor, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, "tool_001", continued.Tools[0].Name)
	_, err = other.List(CatalogOptions{Cursor: searched.NextCursor})
	require.ErrorContains(t, err, "does not match")
	_, err = other.Search("tool", CatalogOptions{Cursor: searched.NextCursor})
	require.ErrorContains(t, err, "does not match")
	_, err = other.Search("lookup", CatalogOptions{Cursor: searched.NextCursor, Group: "core"})
	require.ErrorContains(t, err, "does not match")

	definitions[0].OutputSchema = map[string]any{"type": "string"}
	_, err = NewCatalog(definitions).List(CatalogOptions{Cursor: first.NextCursor})
	require.ErrorContains(t, err, "does not match", "schema changes invalidate cursors")
	_, err = NewCatalog(definitions[1:]).List(CatalogOptions{Cursor: first.NextCursor})
	require.ErrorContains(t, err, "does not match", "authorization changes invalidate cursors")

	for _, cursor := range []string{"?", strings.Repeat("a", 1025), base64.RawURLEncoding.EncodeToString([]byte("not json"))} {
		_, err := catalog.List(CatalogOptions{Cursor: cursor})
		require.ErrorContains(t, err, "invalid catalog cursor")
	}
	bytes, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
	require.NoError(t, err)
	var cursor catalogCursor
	require.NoError(t, json.Unmarshal(bytes, &cursor))
	for _, offset := range []int{-1, 251} {
		cursor.Offset = offset
		payload, err := json.Marshal(cursor)
		require.NoError(t, err)
		_, err = catalog.List(CatalogOptions{Cursor: base64.RawURLEncoding.EncodeToString(payload)})
		require.ErrorContains(t, err, "offset")
	}
}

func TestCatalogSearch(t *testing.T) {
	catalog := NewCatalog([]Definition{
		{Name: "github_listPullRequests", Description: "List open pull requests", Group: "mcp/github"},
		{Name: "gitlab_listPullRequests", Description: "List open pull requests", Group: "mcp/gitlab"},
		{Name: "a_other", Description: "github_listPullRequests github_listPullRequests"},
		{Name: "shell", Description: "Execute a shell command"},
		{
			Name: "lookup", Description: "Find data", InputSchema: map[string]any{
				"type": "object", "properties": map[string]any{
					"accountID": map[string]any{"type": "string", "description": "Customer identifier", "enum": []string{"unindexabletoken"}},
				},
			},
		},
	})
	page, err := catalog.Search("github_listPullRequests", CatalogOptions{})
	require.NoError(t, err)
	assert.Equal(t, "github_listPullRequests", page.Tools[0].Name, "exact name beats description repetition")
	page, err = catalog.Search("listPullRequests", CatalogOptions{Group: "mcp/github"})
	require.NoError(t, err)
	assert.Equal(t, []string{"github_listPullRequests"}, catalogPageNames(page))
	page, err = catalog.Search("open pull requests", CatalogOptions{Group: "mcp/gitlab"})
	require.NoError(t, err)
	assert.Equal(t, []string{"gitlab_listPullRequests"}, catalogPageNames(page))
	page, err = catalog.Search("customer", CatalogOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"lookup"}, catalogPageNames(page))
	page, err = catalog.Search("account id", CatalogOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"lookup"}, catalogPageNames(page))
	page, err = catalog.Search("shell utterly_unmatched_term", CatalogOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"shell"}, catalogPageNames(page), "partial term matches are allowed")
	for _, query := range []string{"unindexabletoken", "properties", "notfound", "", "   "} {
		page, err := catalog.Search(query, CatalogOptions{})
		require.NoError(t, err)
		assert.Empty(t, page.Tools, query)
		assert.NotNil(t, page.Tools)
	}
	_, err = catalog.Search(strings.Repeat("x", 4097), CatalogOptions{})
	require.Error(t, err)

	weighted := NewCatalog([]Definition{
		{Name: "needle_tool"},
		{Name: "another", Description: "needle"},
		{Name: "unrelated"},
	})
	page, err = weighted.Search("needle", CatalogOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"needle_tool", "another"}, catalogPageNames(page))
}

func TestCatalogTokenization(t *testing.T) {
	assert.Equal(t, []string{"github", "list", "pull", "requests"}, catalogTokens("github_listPullRequests"))
	assert.Equal(t, []string{"http", "server", "account", "id", "café", "日本"}, catalogTokens("HTTPServer.accountID/café 日本"))
	assert.Empty(t, catalogTokens("--- / _"))
}

func TestCatalogSchemaDeclarations(t *testing.T) {
	for _, tt := range []struct {
		name   string
		schema map[string]any
		want   string
	}{
		{name: "missing", want: "unknown"},
		{name: "reference", schema: map[string]any{"$ref": "#/$defs/X", "type": "object"}, want: "unknown"},
		{name: "single member union", schema: map[string]any{"anyOf": []any{map[string]any{"type": "string"}}}, want: "string"},
		{
			name: "nullable union",
			schema: map[string]any{"anyOf": []any{
				map[string]any{"type": "string"},
				map[string]any{"type": "null"},
			}},
			want: "string | null",
		},
		{name: "one of", schema: map[string]any{"oneOf": []any{map[string]any{"const": "a"}, map[string]any{"const": 1}}}, want: `"a" | 1`},
		{name: "unrepresentable member", schema: map[string]any{"anyOf": []any{map[string]any{"$ref": "#/$defs/X"}}}, want: "unknown"},
		{name: "empty union members", schema: map[string]any{"anyOf": []any{}}, want: "unknown"},
		{name: "intersection", schema: map[string]any{"allOf": []any{map[string]any{"type": "string"}}}, want: "unknown"},
		{name: "tuple", schema: map[string]any{"type": "array", "prefixItems": []any{}}, want: "unknown"},
		{name: "enum", schema: map[string]any{"enum": []any{"open", "closed", nil}}, want: `"open" | "closed" | null`},
		{name: "empty enum", schema: map[string]any{"enum": []any{}}, want: "unknown"},
		{name: "object enum", schema: map[string]any{"enum": []any{map[string]any{"x": true}}}, want: "unknown"},
		{name: "large enum", schema: map[string]any{"enum": make([]any, 33)}, want: "unknown"},
		{name: "const", schema: map[string]any{"const": true}, want: "true"},
		{name: "precise number", schema: map[string]any{"const": int64(9007199254740993)}, want: "9007199254740993"},
		{name: "long const", schema: map[string]any{"const": strings.Repeat("x", 1025)}, want: "unknown"},
		{name: "nullable", schema: map[string]any{"type": []string{"integer", "null"}}, want: "number | null"},
		{name: "unknown union", schema: map[string]any{"type": []string{"string", "unsupported"}}, want: "unknown"},
		{name: "empty union", schema: map[string]any{"type": []string{}}, want: "unknown"},
		{name: "array", schema: map[string]any{"type": "array"}, want: "Array<unknown>"},
		{name: "empty object", schema: map[string]any{"type": "object"}, want: "{ [key: string]: unknown }"},
		{name: "required unknown", schema: map[string]any{"type": "object", "required": []string{"id"}, "additionalProperties": false}, want: `{ id: unknown }`},
		{name: "closed empty object", schema: map[string]any{"type": "object", "additionalProperties": false}, want: "{}"},
		{
			name: "quoted key",
			schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"a-b": map[string]any{"type": "string"}},
				"additionalProperties": false,
			},
			want: `{ "a-b"?: string }`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			catalog := NewCatalog([]Definition{{Name: "tool", InputSchema: tt.schema, OutputSchema: tt.schema}})
			description, err := catalog.Describe("tool")
			require.NoError(t, err)
			assert.Contains(t, description.Declaration, "(input: "+tt.want+")")
			assert.Contains(t, description.Declaration, "Promise<ToolReply<"+tt.want+">>")
		})
	}
	properties := make(map[string]any)
	for i := range 65 {
		properties[fmt.Sprintf("field%d", i)] = map[string]any{"type": "string"}
	}
	assert.Equal(t, "unknown", catalogSchemaType(map[string]any{"type": "object", "properties": properties}, 0))
	assert.Equal(t, "unknown", catalogSchemaType(map[string]any{"type": "string"}, 9))
}

func TestCatalogEmptyAndBoundedSummaries(t *testing.T) {
	empty := NewCatalog(nil)
	page, err := empty.List(CatalogOptions{})
	require.NoError(t, err)
	payload, err := json.Marshal(page)
	require.NoError(t, err)
	assert.JSONEq(t, `{"tools":[]}`, string(payload))
	page, err = empty.Search("anything", CatalogOptions{})
	require.NoError(t, err)
	assert.Empty(t, page.Tools)

	catalog := NewCatalog([]Definition{
		{Name: "tool", Description: strings.Repeat("界", 500)},
		{Name: "tool", Description: "ignored duplicate"},
	})
	page, err = catalog.List(CatalogOptions{})
	require.NoError(t, err)
	require.Len(t, page.Tools, 1)
	assert.Len(t, []rune(page.Tools[0].Description), summaryMaxRunes, "listings use the one-line summary")
	assert.True(t, strings.HasSuffix(page.Tools[0].Description, "…"))
	description, err := catalog.Describe("tool")
	require.NoError(t, err)
	assert.Len(t, []rune(description.Description), 500)
}

func TestCatalogConcurrentDiscovery(t *testing.T) {
	catalog := NewCatalog([]Definition{{Name: "tool", InputSchema: map[string]any{"type": "string"}}})
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 10 {
				page, err := catalog.Search("tool", CatalogOptions{})
				assert.NoError(t, err)
				assert.Len(t, page.Tools, 1)
				description, err := catalog.Describe("tool")
				assert.NoError(t, err)
				description.InputSchema["type"] = "number"
			}
		})
	}
	workers.Wait()
}
