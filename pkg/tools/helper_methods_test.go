package tools

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func attributeMap(kvs []attribute.KeyValue) map[string]any {
	attrs := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}
	return attrs
}

func isolateViper(t *testing.T) {
	t.Helper()

	originalSettings := viper.AllSettings()
	originalConfigFile := viper.ConfigFileUsed()
	viper.Reset()

	t.Cleanup(func() {
		viper.Reset()
		if originalConfigFile != "" {
			viper.SetConfigFile(originalConfigFile)
			_ = viper.ReadInConfig()
		}
		for key, value := range originalSettings {
			viper.Set(key, value)
		}
	})
}

func TestCoreToolTracingKVs(t *testing.T) {
	t.Run("file read defaults line limit", func(t *testing.T) {
		kvs, err := (&FileReadTool{}).TracingKVs(`{"file_path":"/tmp/demo.go","offset":3}`)
		require.NoError(t, err)

		attrs := attributeMap(kvs)
		assert.Equal(t, "/tmp/demo.go", attrs["file_path"])
		assert.Equal(t, int64(3), attrs["offset"])
		assert.Equal(t, int64(MaxLineLimit), attrs["line_limit"])

		kvs, err = (&FileReadTool{}).TracingKVs(`{`)
		require.Error(t, err)
		assert.Nil(t, kvs)
	})

	t.Run("file write", func(t *testing.T) {
		kvs, err := (&FileWriteTool{}).TracingKVs(`{"file_path":"/tmp/out.txt","text":"hello"}`)
		require.NoError(t, err)

		attrs := attributeMap(kvs)
		assert.Equal(t, "/tmp/out.txt", attrs["file_path"])
		assert.Equal(t, "hello", attrs["text"])

		kvs, err = (&FileWriteTool{}).TracingKVs(`{`)
		require.Error(t, err)
		assert.Nil(t, kvs)
	})

	t.Run("file edit", func(t *testing.T) {
		kvs, err := (&FileEditTool{}).TracingKVs(`{"file_path":"/tmp/main.go","old_text":"old","new_text":"new","replace_all":true}`)
		require.NoError(t, err)

		attrs := attributeMap(kvs)
		assert.Equal(t, "/tmp/main.go", attrs["file_path"])
		assert.Equal(t, "old", attrs["old_text"])
		assert.Equal(t, "new", attrs["new_text"])
		assert.Equal(t, true, attrs["replace_all"])

		kvs, err = (&FileEditTool{}).TracingKVs(`{`)
		require.Error(t, err)
		assert.Nil(t, kvs)
	})

	t.Run("bash", func(t *testing.T) {
		kvs, err := NewBashTool(nil, false).TracingKVs(`{"command":"echo hi","description":"print greeting","timeout":10}`)
		require.NoError(t, err)

		attrs := attributeMap(kvs)
		assert.Equal(t, "echo hi", attrs["command"])
		assert.Equal(t, "print greeting", attrs["description"])
		assert.Equal(t, int64(10), attrs["timeout"])

		kvs, err = NewBashTool(nil, false).TracingKVs(`{`)
		require.Error(t, err)
		assert.Nil(t, kvs)
	})

	t.Run("apply patch", func(t *testing.T) {
		kvs, err := (&ApplyPatchTool{}).TracingKVs(`{"input":"abc"}`)
		require.NoError(t, err)

		attrs := attributeMap(kvs)
		assert.Equal(t, int64(3), attrs["input_length"])

		kvs, err = (&ApplyPatchTool{}).TracingKVs(`{`)
		require.Error(t, err)
		assert.Nil(t, kvs)
	})

	t.Run("grep", func(t *testing.T) {
		kvs, err := (&GrepTool{}).TracingKVs(`{"pattern":"TODO","path":"/tmp/project","include":"*.go","ignore_case":true,"fixed_strings":true,"surround_lines":2,"max_results":5}`)
		require.NoError(t, err)

		attrs := attributeMap(kvs)
		assert.Equal(t, "TODO", attrs["pattern"])
		assert.Equal(t, "/tmp/project", attrs["path"])
		assert.Equal(t, "*.go", attrs["include"])
		assert.Equal(t, true, attrs["ignore_case"])
		assert.Equal(t, true, attrs["fixed_strings"])
		assert.Equal(t, int64(2), attrs["surround_lines"])
		assert.Equal(t, int64(5), attrs["max_results"])

		kvs, err = (&GrepTool{}).TracingKVs(`{`)
		require.Error(t, err)
		assert.Nil(t, kvs)
	})

	t.Run("glob", func(t *testing.T) {
		kvs, err := (&GlobTool{}).TracingKVs(`{"pattern":"**/*.go","path":"/tmp/project","ignore_gitignore":true}`)
		require.NoError(t, err)

		attrs := attributeMap(kvs)
		assert.Equal(t, "**/*.go", attrs["pattern"])
		assert.Equal(t, "/tmp/project", attrs["path"])
		assert.Equal(t, true, attrs["ignore_gitignore"])
	})
}

func TestAssistantFacingForDeterministicResults(t *testing.T) {
	t.Run("file read success and error", func(t *testing.T) {
		success := (&FileReadToolResult{lines: []string{"package main", "func main() {}"}, offset: 10}).AssistantFacing()
		assert.Contains(t, success, "<result>")
		assert.Contains(t, success, "10: package main")
		assert.Contains(t, success, "11: func main() {}")
		assert.NotContains(t, success, "<error>")

		errorOutput := (&FileReadToolResult{err: "open failed"}).AssistantFacing()
		assert.Contains(t, errorOutput, "<error>")
		assert.Contains(t, errorOutput, "open failed")
		assert.Contains(t, errorOutput, "(No output)")
	})

	t.Run("file write success and error", func(t *testing.T) {
		success := (&FileWriteToolResult{filename: "notes.txt", text: "alpha\nbeta"}).AssistantFacing()
		assert.Contains(t, success, "file notes.txt has been written successfully")
		assert.Contains(t, success, "0: alpha")
		assert.Contains(t, success, "1: beta")

		errorOutput := (&FileWriteToolResult{filename: "notes.txt", err: "write failed"}).AssistantFacing()
		assert.Contains(t, errorOutput, "<error>")
		assert.Contains(t, errorOutput, "write failed")
		assert.Contains(t, errorOutput, "(No output)")
	})

	t.Run("file edit replace all samples", func(t *testing.T) {
		assert.Equal(t, 2, minInt(2, 3))
		assert.Equal(t, 2, minInt(3, 2))

		output := (&FileEditToolResult{
			filename:      "main.go",
			replaceAll:    true,
			replacedCount: 4,
			edits: []EditInfo{
				{StartLine: 1, EndLine: 1, NewContent: "one"},
				{StartLine: 5, EndLine: 5, NewContent: "two"},
				{StartLine: 9, EndLine: 9, NewContent: "three"},
				{StartLine: 13, EndLine: 13, NewContent: "four"},
			},
		}).AssistantFacing()

		assert.Contains(t, output, "Replaced 4 occurrences")
		assert.Contains(t, output, "Sample edited code blocks")
		assert.Contains(t, output, "Edit 3 (lines 9-9)")
		assert.Contains(t, output, "9: three")
		assert.Contains(t, output, "... and 1 more replacements")
		assert.NotContains(t, output, "Edit 4")
	})

	t.Run("glob success and error", func(t *testing.T) {
		success := (&GlobToolResult{files: []string{"/tmp/a.go"}, truncated: true}).AssistantFacing()
		assert.Contains(t, success, "/tmp/a.go")
		assert.Contains(t, success, "Results truncated to 100 files")

		errorOutput := (&GlobToolResult{err: "glob failed"}).AssistantFacing()
		assert.Contains(t, errorOutput, "glob failed")
		assert.Contains(t, errorOutput, "(No output)")
	})

	t.Run("grep success and error", func(t *testing.T) {
		success := (&GrepToolResult{pattern: "needle"}).AssistantFacing()
		assert.Contains(t, success, "No matches found for pattern 'needle'")

		errorOutput := (&GrepToolResult{err: "grep failed"}).AssistantFacing()
		assert.Contains(t, errorOutput, "grep failed")
		assert.Contains(t, errorOutput, "(No output)")
	})

	t.Run("apply patch success and error", func(t *testing.T) {
		success := (&applyPatchToolResult{
			changes: []tooltypes.ApplyPatchChange{
				{Path: "added.go", Operation: tooltypes.ApplyPatchOperationAdd, UnifiedDiff: "@@ -0,0 +1,1 @@\n+added\n"},
				{Path: "modified.go", Operation: tooltypes.ApplyPatchOperationUpdate, UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n"},
				{Path: "deleted.go", Operation: tooltypes.ApplyPatchOperationDelete, UnifiedDiff: "@@ -1,1 +0,0 @@\n-deleted\n"},
			},
		}).AssistantFacing()
		assert.Contains(t, success, "Write added.go (+1 -0)")
		assert.Contains(t, success, "Edit modified.go (+1 -1)")
		assert.Contains(t, success, "Delete deleted.go (+0 -1)")

		errorOutput := (&applyPatchToolResult{err: "bad patch"}).AssistantFacing()
		assert.Contains(t, errorOutput, "bad patch")
		assert.Contains(t, errorOutput, "(No output)")
	})
}

func TestViewImageToolResultAndDescriptionBranches(t *testing.T) {
	errorResult := &ViewImageToolResult{base: tooltypes.BaseToolResult{Error: "bad image"}}
	assert.Contains(t, errorResult.AssistantFacing(), "bad image")
	assert.Equal(t, "bad image", errorResult.GetError())
	assert.True(t, errorResult.IsError())
	assert.Nil(t, errorResult.ContentParts())
	errorStructured := errorResult.StructuredData()
	assert.Equal(t, "view_image", errorStructured.ToolName)
	assert.False(t, errorStructured.Success)
	assert.Equal(t, "bad image", errorStructured.Error)

	emptyResult := &ViewImageToolResult{}
	assert.Contains(t, emptyResult.AssistantFacing(), "(No output)")
	assert.Equal(t, "", emptyResult.GetResult())
	assert.Nil(t, emptyResult.ContentParts())

	supportedTool := NewViewImageTool("gpt-5.5", "openai")
	assert.Equal(t, "view_image", supportedTool.Name())
	assert.Contains(t, supportedTool.Description(), "supports only `original`")
	_, hasDetail := supportedTool.GenerateSchema().Properties.Get("detail")
	assert.True(t, hasDetail)

	unsupportedTool := NewViewImageTool("gpt-5", "openai")
	assert.Contains(t, unsupportedTool.Description(), "omit it")
	_, hasDetail = unsupportedTool.GenerateSchema().Properties.Get("detail")
	assert.False(t, hasDetail)

	kvs, err := supportedTool.TracingKVs(`{"path":"image.png","detail":" original "}`)
	require.NoError(t, err)
	attrs := attributeMap(kvs)
	assert.Equal(t, "image.png", attrs["path"])
	assert.Equal(t, "original", attrs["detail"])

	kvs, err = supportedTool.TracingKVs(`{`)
	require.Error(t, err)
	assert.Nil(t, kvs)
}

func TestViewImageToolValidateAndExecute(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "tiny.png")
	writeTinyPNG(t, imagePath)

	state := NewBasicState(context.Background(),
		WithWorkingDirectory(tmpDir),
		WithLLMConfig(llmtypes.Config{Provider: "openai", Model: "gpt-5.5", WorkingDirectory: tmpDir}),
	)
	unsupportedState := NewBasicState(context.Background(),
		WithWorkingDirectory(tmpDir),
		WithLLMConfig(llmtypes.Config{Provider: "openai", Model: "gpt-5", WorkingDirectory: tmpDir}),
	)
	tool := NewViewImageTool("gpt-5", "openai")

	assert.Error(t, tool.ValidateInput(state, `{`))
	assert.Error(t, tool.ValidateInput(state, `{"path":""}`))
	assert.Error(t, tool.ValidateInput(unsupportedState, `{"path":"tiny.png","detail":"original"}`))
	require.NoError(t, tool.ValidateInput(state, `{"path":"tiny.png"}`))

	result := tool.Execute(context.Background(), state, `{"path":"tiny.png"}`)
	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Viewed image")
	parts := result.(tooltypes.MultiModalToolResult).ContentParts()
	require.Len(t, parts, 1)
	assert.Equal(t, tooltypes.ToolResultContentPartTypeImage, parts[0].Type)
	assert.Equal(t, "image/png", parts[0].MimeType)
	assert.Contains(t, parts[0].ImageURL, "data:image/png;base64,")

	badJSON := tool.Execute(context.Background(), state, `{`)
	assert.True(t, badJSON.IsError())
	missingFile := tool.Execute(context.Background(), state, `{"path":"missing.png"}`)
	assert.True(t, missingFile.IsError())
}

func writeTinyPNG(t *testing.T, path string) {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()
	require.NoError(t, png.Encode(file, img))
}

func TestApplyPatchValidationAndPureHelpers(t *testing.T) {
	tmpDir := t.TempDir()
	state := NewBasicState(context.Background(), WithWorkingDirectory(tmpDir))
	tool := &ApplyPatchTool{}

	dirPath := filepath.Join(tmpDir, "dir")
	require.NoError(t, os.Mkdir(dirPath, 0o755))
	filePath := filepath.Join(tmpDir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\ntwo\n"), 0o644))

	err := tool.ValidateInput(state, mustJSON(t, ApplyPatchInput{Input: `*** Begin Patch
*** Add File: dir
+content
*** End Patch`}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists and is a directory")

	err = tool.ValidateInput(state, mustJSON(t, ApplyPatchInput{Input: `*** Begin Patch
*** Delete File: missing.txt
*** End Patch`}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stat")

	err = tool.ValidateInput(state, mustJSON(t, ApplyPatchInput{Input: `*** Begin Patch
*** Delete File: dir
*** End Patch`}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")

	err = tool.ValidateInput(state, mustJSON(t, ApplyPatchInput{Input: `*** Begin Patch
*** Update File: dir
@@
-old
+new
*** End Patch`}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")

	abs := resolvePatchPath(tmpDir, filePath)
	assert.Equal(t, filepath.Clean(filePath), abs)
	assert.Equal(t, filepath.Join(tmpDir, "nested", "file.txt"), resolvePatchPath(tmpDir, "nested/file.txt"))

	assert.Equal(t, `quote-'single' "double" hyphen- space`, normalizeSearchLine(" quote-‘single’ “double” hyphen— space\u00a0"))

	chunk, consumed, err := parseUpdateFileChunk([]string{"@@ context", " line", "-old", "+new", eofMarker}, 10, false)
	require.NoError(t, err)
	assert.Equal(t, 5, consumed)
	require.NotNil(t, chunk.changeContext)
	assert.Equal(t, "context", *chunk.changeContext)
	assert.True(t, chunk.isEndOfFile)
	assert.Equal(t, []string{"line", "old"}, chunk.oldLines)
	assert.Equal(t, []string{"line", "new"}, chunk.newLines)

	_, _, err = parseUpdateFileChunk([]string{"bad first line"}, 10, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Expected update hunk to start")

	chunks := []updateFileChunk{{oldLines: []string{"two"}, newLines: []string{"deux"}}}
	replacements, err := computeReplacements([]string{"one", "two", ""}, filePath, chunks)
	require.NoError(t, err)
	assert.Equal(t, []replacement{{startIdx: 1, oldLen: 1, newLines: []string{"deux"}}}, replacements)
	assert.Equal(t, []string{"one", "deux", ""}, applyReplacements([]string{"one", "two", ""}, replacements))

	chunks = []updateFileChunk{{oldLines: nil, newLines: []string{"inserted"}}}
	replacements, err = computeReplacements([]string{"one", ""}, filePath, chunks)
	require.NoError(t, err)
	assert.Equal(t, []replacement{{startIdx: 1, oldLen: 0, newLines: []string{"inserted"}}}, replacements)

	_, err = computeReplacements([]string{"one"}, filePath, []updateFileChunk{{oldLines: []string{"missing"}, newLines: []string{"new"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find expected lines")
}

func TestReadConversationToolMetadataAndTracing(t *testing.T) {
	tool := NewReadConversationTool()
	assert.Equal(t, "read_conversation", tool.Name())
	assert.Contains(t, tool.Description(), "Read a saved conversation by ID")

	schema := tool.GenerateSchema()
	require.NotNil(t, schema)
	assert.Equal(t, "https://github.com/jingkaihe/kodelet/pkg/tools/read-conversation-input", string(schema.ID))

	kvs, err := tool.TracingKVs(`{"conversation_id":" conv_123 ","goal":" extract fix "}`)
	require.NoError(t, err)
	attrs := attributeMap(kvs)
	assert.Equal(t, "conv_123", attrs["conversation_id"])
	assert.Equal(t, "extract fix", attrs["goal"])

	kvs, err = tool.TracingKVs(`{`)
	require.Error(t, err)
	assert.Nil(t, kvs)

	result := &ReadConversationToolResult{conversationID: "conv_123", goal: "extract fix", content: "relevant details"}
	assert.Equal(t, "relevant details", result.GetResult())
	assert.Empty(t, result.GetError())
	assert.False(t, result.IsError())
	assert.Contains(t, result.AssistantFacing(), "relevant details")

	structured := result.StructuredData()
	assert.Equal(t, "read_conversation", structured.ToolName)
	assert.True(t, structured.Success)
	var meta tooltypes.ReadConversationMetadata
	require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &meta))
	assert.Equal(t, "conv_123", meta.ConversationID)
	assert.Equal(t, "extract fix", meta.Goal)
	assert.Equal(t, "relevant details", meta.Content)

	errorResult := &ReadConversationToolResult{conversationID: "conv_123", goal: "extract fix", err: "missing conversation"}
	assert.True(t, errorResult.IsError())
	assert.Equal(t, "missing conversation", errorResult.GetError())
	structured = errorResult.StructuredData()
	assert.False(t, structured.Success)
	assert.Equal(t, "missing conversation", structured.Error)
}

func TestStateDeterministicHelpers(t *testing.T) {
	assert.Nil(t, allowedToolNameSet(llmtypes.Config{}))
	assert.Equal(t, map[string]struct{}{"bash": {}, "file_read": {}}, allowedToolNameSet(llmtypes.Config{
		AllowedTools: []string{" bash ", "", "file_read"},
	}))

	tools := []tooltypes.Tool{&BashTool{}, &FileReadTool{}, &GrepTool{}}
	filtered := filterDiscoveredToolsByAllowed(llmtypes.Config{AllowedTools: []string{"file_read"}}, tools)
	require.Len(t, filtered, 1)
	assert.Equal(t, "file_read", filtered[0].Name())
	assert.Empty(t, filterDiscoveredToolsByAllowed(llmtypes.Config{AllowedTools: []string{"   "}}, tools))
	assert.Equal(t, tools, filterDiscoveredToolsByAllowed(llmtypes.Config{}, tools))

	assert.False(t, skillsEnabledForConfig(llmtypes.Config{Skills: &llmtypes.SkillsConfig{Enabled: false}}))
	assert.True(t, skillsEnabledForConfig(llmtypes.Config{}))

	assert.Empty(t, filterOutSkill([]tooltypes.Tool{NewSkillTool(nil, false, false)}))

	workDir := t.TempDir()
	state := NewBasicState(context.Background(), WithWorkingDirectory(workDir))
	assert.Equal(t, filepath.Clean(workDir), state.WorkingDirectory())

	state = NewBasicState(context.Background(), WithLLMConfig(llmtypes.Config{WorkingDirectory: workDir}))
	assert.Equal(t, filepath.Clean(workDir), state.WorkingDirectory())

	// Keep imported encoding/json exercised in this helper-focused file with a
	// deterministic schema smoke check for the generic helper.
	data, err := json.Marshal(GenerateSchema[FileReadInput]())
	require.NoError(t, err)
	assert.Contains(t, string(data), "file_path")
}

func TestStateDiscoveryHelpersWithTempDirs(t *testing.T) {
	isolateViper(t)

	workDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(oldWD)) })

	skillDir := filepath.Join(workDir, ".kodelet", "skills", "review")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: review
description: Review helper
---
# Review
`), 0o644))

	skills := discoverSkills(context.Background(), llmtypes.Config{})
	require.Contains(t, skills, "review")
	assert.Equal(t, "Review helper", skills["review"].Description)

	filtered := discoverSkills(context.Background(), llmtypes.Config{Skills: &llmtypes.SkillsConfig{Enabled: true, Allowed: []string{"missing"}}})
	assert.NotContains(t, filtered, "review")

	viper.Set("no_skills", true)
	assert.Nil(t, discoverSkills(context.Background(), llmtypes.Config{}))
}

func TestBashAndGrepFormattingHelpers(t *testing.T) {
	assert.Equal(t, 0, countOutputLines(""))
	assert.Equal(t, 2, countOutputLines("one\ntwo"))
	assert.Equal(t, 2, countOutputLines("one\ntwo\n"))

	assert.Equal(t, 0, approxBytesForTokens(0))
	assert.Equal(t, 12, approxBytesForTokens(3))
	assert.Equal(t, 0, approxTokensFromByteCount(0))
	assert.Equal(t, 3, approxTokensFromByteCount(9))
	assert.Equal(t, 3, removedUnits(true, 9, 100))
	assert.Equal(t, 7, removedUnits(false, 99, 7))
	assert.Equal(t, "…3 tokens truncated…", formatBashTruncationMarker(true, 3))
	assert.Equal(t, "…7 chars truncated…", formatBashTruncationMarker(false, 7))
	assert.Equal(t, "short", truncateMiddleWithTokenBudget("short", 10))
	assert.Contains(t, truncateMiddleByBytesEstimate("abcdef", 0, false), "chars truncated")

	longLine := strings.Repeat("x", grepMaxLineLength+1)
	result := SearchResult{
		Filename:     "/tmp/demo.go",
		MatchedLines: map[int]string{2: longLine},
		ContextLines: map[int]string{1: "before"},
		LineNumbers:  []int{1, 2},
	}
	empty := SearchResult{Filename: "/tmp/empty.go", MatchedLines: map[int]string{}, LineNumbers: []int{1}}
	formatted := FormatSearchResults("needle", []SearchResult{result, empty})
	assert.Contains(t, formatted, "Search results for pattern 'needle'")
	assert.Contains(t, formatted, "1-before")
	assert.Contains(t, formatted, "2:"+strings.Repeat("x", grepMaxLineLength)+grepTruncationIndicator)
	assert.Greater(t, estimateResultSize(result), 0)
}

func TestGlobStructuredDataErrorSkipsMetadata(t *testing.T) {
	structured := (&GlobToolResult{pattern: "*.go", path: "/tmp", err: "bad glob"}).StructuredData()
	assert.Equal(t, "glob_tool", structured.ToolName)
	assert.False(t, structured.Success)
	assert.Equal(t, "bad glob", structured.Error)
	assert.Nil(t, structured.Metadata)
}
