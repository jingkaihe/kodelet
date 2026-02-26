package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	result := tool.Execute(context.Background(), state, params)

	require.False(t, result.IsError())
	assert.Contains(t, result.GetResult(), "A "+filepath.Join(tmp, "hello.txt"))

	content, err := os.ReadFile(filepath.Join(tmp, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Hello\nWorld\n", string(content))

	structured := result.StructuredData()
	var meta tooltypes.ApplyPatchMetadata
	require.True(t, tooltypes.ExtractMetadata(structured.Metadata, &meta))
	require.Len(t, meta.Changes, 1)
	assert.Equal(t, tooltypes.ApplyPatchOperationAdd, meta.Changes[0].Operation)
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
	assert.Contains(t, result.GetResult(), "A "+filepath.Join(tmp, "empty.txt"))

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
	assert.Contains(t, result.GetResult(), "M "+filepath.Join(tmp, "edit.txt"))
	assert.Contains(t, result.GetResult(), "D "+filepath.Join(tmp, "gone.txt"))

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
	assert.Contains(t, result.GetResult(), "M "+filepath.Join(tmp, "renamed", "name.txt"))

	_, err := os.Stat(filepath.Join(tmp, "old", "name.txt"))
	assert.True(t, os.IsNotExist(err))

	content, err := os.ReadFile(filepath.Join(tmp, "renamed", "name.txt"))
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
	assert.Contains(t, result.GetResult(), "A "+filepath.Join(tmp, "nested", "new.txt"))
	assert.Contains(t, result.GetResult(), "M "+filepath.Join(tmp, "modify.txt"))
	assert.Contains(t, result.GetResult(), "D "+filepath.Join(tmp, "remove.txt"))

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
