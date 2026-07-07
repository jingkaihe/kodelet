package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyPatchTool_BasicMetadata(t *testing.T) {
	tool := &ApplyPatchTool{}
	assert.Equal(t, "apply_patch", tool.Name())
	assert.NotNil(t, tool.GenerateSchema())
	assert.Contains(t, tool.Description(), "*** Begin Patch")
}

func TestApplyPatchTool_AddFile(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	patch := `*** Begin Patch
*** Add File: hello.txt
+Hello
+World
*** End Patch`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	require.NoError(t, tool.ValidateInput(state, params))

	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Write "+filepath.Join(tmp, "hello.txt")+" (+2 -0)")

	content, err := os.ReadFile(filepath.Join(tmp, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Hello\nWorld\n", string(content))

	structured := result.StructuredData()
	var meta tooltypes.ApplyPatchMetadata
	require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &meta))
	require.Len(t, meta.Changes, 1)
	assert.Equal(t, tooltypes.ApplyPatchOperationAdd, meta.Changes[0].Operation)
}

func TestApplyPatchTool_AddFileOverwritesExistingFile(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	existingPath := filepath.Join(tmp, "hello.txt")
	require.NoError(t, os.WriteFile(existingPath, []byte("existing\n"), 0o644))

	patch := `*** Begin Patch
*** Add File: hello.txt
+new content
*** End Patch`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Write "+existingPath+" (+1 -1)")

	content, err := os.ReadFile(existingPath)
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(content))

	structured := result.StructuredData()
	var meta tooltypes.ApplyPatchMetadata
	require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &meta))
	require.Len(t, meta.Changes, 1)
	assert.Equal(t, tooltypes.ApplyPatchOperationAdd, meta.Changes[0].Operation)
	assert.Equal(t, "existing\n", meta.Changes[0].OldContent)
	assert.Equal(t, "new content\n", meta.Changes[0].NewContent)
}

func TestApplyPatchUnifiedDiffKeepsOriginMainTestChangesSeparate(t *testing.T) {
	oldContent := `func TestApplyPatchRenderer(t *testing.T) {
	renderer := &ApplyPatchRenderer{}

	t.Run("successful apply patch", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "apply_patch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ApplyPatchMetadata{
				Added:    []string{"/tmp/new.txt"},
				Modified: []string{"/tmp/edit.go"},
				Deleted:  []string{"/tmp/old.txt"},
				Changes: []tools.ApplyPatchChange{
					{
						Path:       "/tmp/new.txt",
						Operation:  tools.ApplyPatchOperationAdd,
						NewContent: "hello\n",
					},
					{
						Path:        "/tmp/edit.go",
						Operation:   tools.ApplyPatchOperationUpdate,
						OldContent:  "old\n",
						NewContent:  "new\n",
						UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
					},
					{
						Path:       "/tmp/old.txt",
						Operation:  tools.ApplyPatchOperationDelete,
						OldContent: "bye\n",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "Success. Updated the following files:")
		assert.Contains(t, output, "A /tmp/new.txt")
		assert.Contains(t, output, "M /tmp/edit.go")
		assert.Contains(t, output, "D /tmp/old.txt")
		assert.Contains(t, output, "@@ -1 +1 @@")
	})
}

func TestFileEditRendererRenderMarkdown(t *testing.T) {
	renderer := &FileEditRenderer{}
	_ = renderer
}

func TestFileWriteRendererMarkdownVariants(t *testing.T) {
	renderer := &FileWriteRenderer{}
	_ = renderer
}

func TestFileEditRendererMergedAndFallbackMarkdown(t *testing.T) {
	renderer := &FileEditRenderer{}
	_ = renderer
}

func TestApplyPatchRendererRenderMarkdown(t *testing.T) {
	renderer := &ApplyPatchRenderer{}
	result := tools.StructuredToolResult{
		ToolName:  "apply_patch",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tools.ApplyPatchMetadata{
			Added:    []string{"/tmp/new.txt"},
			Modified: []string{"/tmp/edit.go"},
			Deleted:  []string{"/tmp/old.txt"},
			Changes: []tools.ApplyPatchChange{
				{
					Path:       "/tmp/new.txt",
					Operation:  tools.ApplyPatchOperationAdd,
					NewContent: "hello\n",
				},
				{
					Path:        "/tmp/edit.go",
					Operation:   tools.ApplyPatchOperationUpdate,
					UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
				},
			},
		},
	}

	output := renderer.RenderMarkdown(result)
	assert.Contains(t, output, "Success. Updated the following files:")
	assert.Contains(t, output, "A /tmp/new.txt")
	assert.Contains(t, output, "M /tmp/edit.go")
	assert.Contains(t, output, "D /tmp/old.txt")
	assert.Contains(t, output, "@@ -1 +1 @@")
	assert.NotContains(t, output, "####")
	assert.NotContains(t, output, "- **Added:**")
}
`
	newContent := `func TestApplyPatchRenderer(t *testing.T) {
	renderer := &ApplyPatchRenderer{}

	t.Run("successful apply patch", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "apply_patch",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ApplyPatchMetadata{
				Changes: []tools.ApplyPatchChange{
					{
						Path:       "/tmp/new.txt",
						Operation:  tools.ApplyPatchOperationAdd,
						NewContent: "hello\n",
						UnifiedDiff: "--- /dev/null\n" +
							"+++ /tmp/new.txt\n" +
							"@@ -0,0 +1,1 @@\n" +
							"+hello\n",
					},
					{
						Path:        "/tmp/edit.go",
						Operation:   tools.ApplyPatchOperationUpdate,
						OldContent:  "old\n",
						NewContent:  "new\n",
						UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
					},
					{
						Path:       "/tmp/old.txt",
						Operation:  tools.ApplyPatchOperationDelete,
						OldContent: "bye\n",
						UnifiedDiff: "--- /tmp/old.txt\n" +
							"+++ /dev/null\n" +
							"@@ -1,1 +0,0 @@\n" +
							"-bye\n",
					},
				},
			},
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "Success. Updated files (+2 -2):")
		assert.Contains(t, output, "Write /tmp/new.txt (+1 -0)")
		assert.Contains(t, output, "Edit /tmp/edit.go (+1 -1)")
		assert.Contains(t, output, "Delete /tmp/old.txt (+0 -1)")
		assert.Contains(t, output, "  1 │ +hello")
		assert.Contains(t, output, "1   │ -old")
		assert.Contains(t, output, "  1 │ +new")
		assert.Contains(t, output, "@@ -1 +1 @@")
	})

	t.Run("error with partial metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "apply_patch",
			Success:   false,
			Error:     "patch failed after first file",
			Timestamp: time.Now(),
			Metadata: &tools.ApplyPatchMetadata{Changes: []tools.ApplyPatchChange{{
				Path:        "/tmp/partial.go",
				Operation:   tools.ApplyPatchOperationUpdate,
				UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
			}}},
		}

		output := renderer.RenderCLI(result)
		assert.Contains(t, output, "Patch failed (+1 -1):")
		assert.Contains(t, output, "Edit /tmp/partial.go (+1 -1)")
		assert.Contains(t, output, "Error: patch failed after first file")
	})
}

func TestFileEditRendererRenderMarkdown(t *testing.T) {
	renderer := &FileEditRenderer{}
	_ = renderer
}

func TestFileWriteRendererMarkdownVariants(t *testing.T) {
	renderer := &FileWriteRenderer{}
	_ = renderer
}

func TestFileEditRendererMergedAndFallbackMarkdown(t *testing.T) {
	renderer := &FileEditRenderer{}
	_ = renderer
}

func TestApplyPatchRendererRenderMarkdown(t *testing.T) {
	renderer := &ApplyPatchRenderer{}
	result := tools.StructuredToolResult{
		ToolName:  "apply_patch",
		Success:   true,
		Timestamp: time.Now(),
		Metadata: &tools.ApplyPatchMetadata{
			Changes: []tools.ApplyPatchChange{
				{
					Path:       "/tmp/new.txt",
					Operation:  tools.ApplyPatchOperationAdd,
					NewContent: "hello\n",
					UnifiedDiff: "--- /dev/null\n" +
						"+++ /tmp/new.txt\n" +
						"@@ -0,0 +1,1 @@\n" +
						"+hello\n",
				},
				{
					Path:        "/tmp/edit.go",
					Operation:   tools.ApplyPatchOperationUpdate,
					UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n",
				},
			},
		},
	}

	output := renderer.RenderMarkdown(result)
	assert.Contains(t, output, "Success. Updated files (+2 -1).")
	assert.Contains(t, output, "- **Write /tmp/new.txt (+1 -0)**")
	assert.Contains(t, output, "- **Edit /tmp/edit.go (+1 -1)**")
	assert.Contains(t, output, "@@ -1 +1 @@")
	assert.Contains(t, output, "1   │ -old")
	assert.Contains(t, output, "  1 │ +new")
	assert.NotContains(t, output, "####")
	assert.NotContains(t, output, "- **Added:**")
}
`

	diff := applyPatchUnifiedDiff("pkg/tools/renderers/file_renderers_test.go", "pkg/tools/renderers/file_renderers_test.go", oldContent, newContent)

	hunks := 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "@@ -") {
			hunks++
		}
	}
	assert.Equal(t, 4, hunks, diff)
	assert.Contains(t, diff, "-\t\t\t\tAdded:    []string{\"/tmp/new.txt\"},")
	assert.Contains(t, diff, "+\t\tassert.Contains(t, output, \"Patch failed (+1 -1):\")")
	assert.Contains(t, diff, "+\tassert.Contains(t, output, \"Success. Updated files (+2 -1).\")")
	assert.NotContains(t, diff, "func TestFileEditRendererRenderMarkdown")
	assert.NotContains(t, diff, "func TestFileWriteRendererMarkdownVariants")
}

func TestApplyPatchTool_DeleteThenAddSamePath(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	filePath := filepath.Join(tmp, "replace.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("old\n"), 0o644))

	patch := `*** Begin Patch
*** Delete File: replace.txt
*** Add File: replace.txt
+new
*** End Patch`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	require.NoError(t, tool.ValidateInput(state, params))

	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Delete "+filePath+" (+0 -1)")
	assert.Contains(t, result.GetResult(), "Write "+filePath+" (+1 -0)")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(content))
}

func TestApplyPatchTool_AddEmptyFile(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	patch := `*** Begin Patch
*** Add File: empty.txt
*** End Patch`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Write "+filepath.Join(tmp, "empty.txt")+" (+0 -0)")

	content, err := os.ReadFile(filepath.Join(tmp, "empty.txt"))
	require.NoError(t, err)
	assert.Equal(t, "", string(content))
}

func TestApplyPatchTool_UpdateAndDelete(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	require.NoError(t, os.WriteFile(filepath.Join(tmp, "edit.txt"), []byte("foo\nbar\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "gone.txt"), []byte("bye\n"), 0o644))

	patch := `*** Begin Patch
*** Update File: edit.txt
@@
 foo
-bar
+baz
*** Delete File: gone.txt
*** End Patch`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Edit "+filepath.Join(tmp, "edit.txt")+" (+1 -1)")
	assert.Contains(t, result.GetResult(), "Delete "+filepath.Join(tmp, "gone.txt")+" (+0 -1)")

	content, err := os.ReadFile(filepath.Join(tmp, "edit.txt"))
	require.NoError(t, err)
	assert.Equal(t, "foo\nbaz\n", string(content))

	_, err = os.Stat(filepath.Join(tmp, "gone.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestApplyPatchTool_MoveFile(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "old"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "old", "name.txt"), []byte("line\n"), 0o644))

	patch := `*** Begin Patch
*** Update File: old/name.txt
*** Move to: renamed/name.txt
@@
-line
+line2
*** End Patch`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Move "+filepath.Join(tmp, "old", "name.txt")+" → "+filepath.Join(tmp, "renamed", "name.txt")+" (+1 -1)")

	_, err := os.Stat(filepath.Join(tmp, "old", "name.txt"))
	assert.True(t, os.IsNotExist(err))

	content, err := os.ReadFile(filepath.Join(tmp, "renamed", "name.txt"))
	require.NoError(t, err)
	assert.Equal(t, "line2\n", string(content))
}

func TestApplyPatchTool_MoveToSamePathIsInPlaceUpdate(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	filePath := filepath.Join(tmp, "same.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("line\n"), 0o644))

	patch := `*** Begin Patch
*** Update File: same.txt
*** Move to: ./same.txt
@@
-line
+line2
*** End Patch`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Edit "+filePath+" (+1 -1)")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "line2\n", string(content))
}

func TestApplyPatchTool_ValidateInput(t *testing.T) {
	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())

	err := tool.ValidateInput(state, `{"input":""}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input is required")

	err = tool.ValidateInput(state, `{"input":"*** Begin Patch\n*** End Patch"}`)
	require.NoError(t, err)
}

func TestApplyPatchTool_InvalidPatch(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())

	params := mustJSON(t, ApplyPatchInput{Input: "bad patch"})
	result := tool.Execute(context.Background(), state, params)
	require.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "The first line of the patch must be '*** Begin Patch'")
}

func TestApplyPatchTool_LenientHeredocInput(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	patch := `<<'EOF'
*** Begin Patch
*** Add File: heredoc.txt
+hello
*** End Patch
EOF`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	content, err := os.ReadFile(filepath.Join(tmp, "heredoc.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(content))
}

func TestApplyPatchTool_FailsWhenContextMissing(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	require.NoError(t, os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("one\ntwo\n"), 0o644))

	patch := `*** Begin Patch
*** Update File: file.txt
@@
-not-found
+replacement
*** End Patch`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	result := tool.Execute(context.Background(), state, params)

	require.True(t, result.IsError())
	assert.Contains(t, result.GetError(), "failed to find expected lines")
}

func TestApplyPatchTool_MultipleOperations(t *testing.T) {
	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	require.NoError(t, os.Chdir(tmp))

	require.NoError(t, os.WriteFile(filepath.Join(tmp, "modify.txt"), []byte("a\nb\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "remove.txt"), []byte("gone\n"), 0o644))

	patch := `*** Begin Patch
*** Add File: nested/new.txt
+new file
*** Update File: modify.txt
@@
 a
-b
+c
*** Delete File: remove.txt
*** End Patch`
	params := mustJSON(t, ApplyPatchInput{Input: patch})

	tool := &ApplyPatchTool{}
	state := NewBasicState(context.Background())
	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "Write "+filepath.Join(tmp, "nested", "new.txt")+" (+1 -0)")
	assert.Contains(t, result.GetResult(), "Edit "+filepath.Join(tmp, "modify.txt")+" (+1 -1)")
	assert.Contains(t, result.GetResult(), "Delete "+filepath.Join(tmp, "remove.txt")+" (+0 -1)")

	modified, err := os.ReadFile(filepath.Join(tmp, "modify.txt"))
	require.NoError(t, err)
	assert.Equal(t, "a\nc\n", string(modified))
}

func mustJSON(t *testing.T, input ApplyPatchInput) string {
	t.Helper()
	b, err := json.Marshal(input)
	require.NoError(t, err)
	return string(b)
}
