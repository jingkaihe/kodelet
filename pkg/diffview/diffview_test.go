package diffview

import (
	"testing"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromApplyPatchChangeParsesHunksCountsAndMoveHeader(t *testing.T) {
	file := FromApplyPatchChange(tooltypes.ApplyPatchChange{
		Path:      "old.go",
		MovePath:  "new.go",
		Operation: tooltypes.ApplyPatchOperationUpdate,
		UnifiedDiff: "--- old.go\n" +
			"+++ new.go\n" +
			"@@ -10,3 +10,4 @@\n" +
			" context\n" +
			"-old\n" +
			"+new\n" +
			"+added\n" +
			" same\n" +
			"@@ -30 +31 @@\n" +
			"-old2\n" +
			"+new2\n",
	})

	require.Len(t, file.Lines, 9)
	assert.Equal(t, 3, file.Added)
	assert.Equal(t, 2, file.Removed)
	assert.Equal(t, "Move old.go → new.go (+3 -2)", file.Header())
	assert.Equal(t, Line{Kind: LineHeader, Content: "@@ -10,3 +10,4 @@"}, file.Lines[0])
	assert.Equal(t, Line{Kind: LineContext, OldLine: 10, NewLine: 10, Content: "context"}, file.Lines[1])
	assert.Equal(t, Line{Kind: LineRemoved, OldLine: 11, Content: "old"}, file.Lines[2])
	assert.Equal(t, Line{Kind: LineAdded, NewLine: 11, Content: "new"}, file.Lines[3])
	assert.Equal(t, Line{Kind: LineAdded, NewLine: 12, Content: "added"}, file.Lines[4])
	assert.Equal(t, Line{Kind: LineHeader, Content: "@@ -30 +31 @@"}, file.Lines[6])
	assert.Equal(t, Line{Kind: LineRemoved, OldLine: 30, Content: "old2"}, file.Lines[7])
	assert.Equal(t, Line{Kind: LineAdded, NewLine: 31, Content: "new2"}, file.Lines[8])
}

func TestFromFileWriteMetadataUsesDiffCountsAndLineNumbers(t *testing.T) {
	summary := FromFileWriteMetadata(tooltypes.FileWriteMetadata{
		FilePath:    "new.go",
		UnifiedDiff: "--- new.go\n+++ new.go\n@@ -0,0 +1,2 @@\n+package main\n+\n",
	})

	require.Len(t, summary.Files, 1)
	file := summary.Files[0]
	assert.Equal(t, "Write new.go (+2 -0)", file.Header())
	assert.Equal(t, 2, summary.Added)
	assert.Zero(t, summary.Removed)
	assert.Contains(t, file.Lines, Line{Kind: LineAdded, NewLine: 1, Content: "package main"})
	assert.Contains(t, file.Lines, Line{Kind: LineAdded, NewLine: 2, Content: ""})
}

func TestFromFileEditMetadataUsesAbsoluteLineNumbersAcrossEdits(t *testing.T) {
	oldContent := "one\ntwo\nthree\nfour\nold\nsix\nseven\neight\nnine\nold2\n"
	newContent := "one\ntwo\nthree\nfour\nnew\nextra\nsix\nseven\neight\nnine\nnew2\n"
	summary := FromFileEditMetadata(tooltypes.FileEditMetadata{
		FilePath:    "edit.go",
		UnifiedDiff: UnifiedDiff("edit.go", "edit.go", oldContent, newContent),
	})

	require.Len(t, summary.Files, 1)
	file := summary.Files[0]
	assert.Equal(t, "Edit edit.go (+3 -2)", file.Header())
	assert.Equal(t, 3, summary.Added)
	assert.Equal(t, 2, summary.Removed)
	assert.Contains(t, file.Lines, Line{Kind: LineRemoved, OldLine: 5, Content: "old"})
	assert.Contains(t, file.Lines, Line{Kind: LineAdded, NewLine: 5, Content: "new"})
	assert.Contains(t, file.Lines, Line{Kind: LineAdded, NewLine: 6, Content: "extra"})
	assert.Contains(t, file.Lines, Line{Kind: LineRemoved, OldLine: 10, Content: "old2"})
	assert.Contains(t, file.Lines, Line{Kind: LineAdded, NewLine: 11, Content: "new2"})
}

func TestUnifiedDiffPreservesFinalNewlineChanges(t *testing.T) {
	diff := UnifiedDiff("edit.go", "edit.go", "value", "value\n")

	assert.NotEmpty(t, diff)
	assert.Contains(t, diff, `\ No newline at end of file`)
	file := FromFileEditMetadata(tooltypes.FileEditMetadata{FilePath: "edit.go", UnifiedDiff: diff}).Files[0]
	assert.Equal(t, 1, file.Added)
	assert.Equal(t, 1, file.Removed)
}

func TestRenderFileBodyWidthWrapsLongLinesWithGutterContinuation(t *testing.T) {
	file := FileDiff{Lines: []Line{{Kind: LineAdded, NewLine: 1, Content: "abcdefghijklmnop"}}}

	rendered := RenderFileBodyWidth(file, 12)

	require.Greater(t, len(rendered), 1)
	assert.Equal(t, "  1 │ +abcde", rendered[0].Text)
	assert.Equal(t, "    │  fghij", rendered[1].Text)
	for _, line := range rendered {
		assert.LessOrEqual(t, displayWidth(line.Text), 12)
	}
}
