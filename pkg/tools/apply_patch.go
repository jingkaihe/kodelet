package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aymanbagabas/go-udiff"
	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

const (
	beginPatchMarker        = "*** Begin Patch"
	endPatchMarker          = "*** End Patch"
	addFileMarker           = "*** Add File: "
	deleteFileMarker        = "*** Delete File: "
	updateFileMarker        = "*** Update File: "
	moveToMarker            = "*** Move to: "
	eofMarker               = "*** End of File"
	changeContextMarker     = "@@ "
	emptyChangeContextMaker = "@@"
)

// ApplyPatchTool applies codex-style file patch instructions.
type ApplyPatchTool struct{}

// ApplyPatchInput defines the JSON input for the apply_patch tool.
type ApplyPatchInput struct {
	Input string `json:"input" jsonschema:"description=The entire contents of the apply_patch command"`
}

//go:embed descriptions/apply_patch.txt
var applyPatchDescription string

type applyPatchToolResult struct {
	added    []string
	modified []string
	deleted  []string
	changes  []tooltypes.ApplyPatchChange
	err      string
}

func (r *applyPatchToolResult) GetResult() string {
	if r.IsError() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Success. Updated the following files:\n")
	for _, path := range r.added {
		b.WriteString(fmt.Sprintf("A %s\n", path))
	}
	for _, path := range r.modified {
		b.WriteString(fmt.Sprintf("M %s\n", path))
	}
	for _, path := range r.deleted {
		b.WriteString(fmt.Sprintf("D %s\n", path))
	}
	return b.String()
}

func (r *applyPatchToolResult) GetError() string {
	return r.err
}

func (r *applyPatchToolResult) IsError() bool {
	return r.err != ""
}

func (r *applyPatchToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(strings.TrimSuffix(r.GetResult(), "\n"), r.GetError())
}

func (r *applyPatchToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "apply_patch",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
		Metadata: &tooltypes.ApplyPatchMetadata{
			Changes:  r.changes,
			Added:    r.added,
			Modified: r.modified,
			Deleted:  r.deleted,
		},
	}
	if r.IsError() {
		result.Error = r.GetError()
	}
	return result
}

// Name returns the name of the tool.
func (t *ApplyPatchTool) Name() string {
	return "apply_patch"
}

// GenerateSchema generates the JSON schema for the tool input.
func (t *ApplyPatchTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[ApplyPatchInput]()
}

// Description returns the tool description.
func (t *ApplyPatchTool) Description() string {
	return applyPatchDescription
}

// TracingKVs returns tracing attributes.
func (t *ApplyPatchTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &ApplyPatchInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return nil, err
	}
	return []attribute.KeyValue{
		attribute.Int("input_length", len(input.Input)),
	}, nil
}

// ValidateInput validates the patch format and referenced files.
func (t *ApplyPatchTool) ValidateInput(_ tooltypes.State, parameters string) error {
	parsed, err := parseAndResolvePatchInput(parameters)
	if err != nil {
		return err
	}

	for _, hunk := range parsed.hunks {
		switch hunk.kind {
		case patchHunkDelete:
			info, statErr := os.Stat(hunk.path)
			if statErr != nil {
				return errors.Wrapf(statErr, "failed to stat %s", hunk.path)
			}
			if info.IsDir() {
				return errors.Errorf("failed to delete file %s: is a directory", hunk.path)
			}
		case patchHunkUpdate:
			info, statErr := os.Stat(hunk.path)
			if statErr != nil {
				return errors.Wrapf(statErr, "failed to stat %s", hunk.path)
			}
			if info.IsDir() {
				return errors.Errorf("failed to update file %s: is a directory", hunk.path)
			}
		}
	}

	return nil
}

// Execute applies the patch to disk.
func (t *ApplyPatchTool) Execute(_ context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	parsed, err := parseAndResolvePatchInput(parameters)
	if err != nil {
		return &applyPatchToolResult{err: err.Error()}
	}

	if len(parsed.hunks) == 0 {
		return &applyPatchToolResult{err: "No files were modified."}
	}

	result := &applyPatchToolResult{}

	for _, hunk := range parsed.hunks {
		switch hunk.kind {
		case patchHunkAdd:
			unlock := lockPaths(state, hunk.path)
			err = applyAddHunk(state, hunk, result)
			unlock()
		case patchHunkDelete:
			unlock := lockPaths(state, hunk.path)
			err = applyDeleteHunk(state, hunk, result)
			unlock()
		case patchHunkUpdate:
			paths := []string{hunk.path}
			if hunk.movePath != "" {
				paths = append(paths, hunk.movePath)
			}
			unlock := lockPaths(state, paths...)
			err = applyUpdateHunk(state, hunk, result)
			unlock()
		}

		if err != nil {
			result.err = err.Error()
			return result
		}
	}

	return result
}

func applyAddHunk(state tooltypes.State, hunk parsedHunk, result *applyPatchToolResult) error {
	if parent := filepath.Dir(hunk.path); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return errors.Wrapf(err, "failed to create parent directories for %s", hunk.path)
		}
	}
	if err := os.WriteFile(hunk.path, []byte(hunk.contents), 0o644); err != nil {
		return errors.Wrapf(err, "failed to write file %s", hunk.path)
	}
	_ = state.SetFileLastAccessed(hunk.path, time.Now())

	result.added = append(result.added, hunk.path)
	result.changes = append(result.changes, tooltypes.ApplyPatchChange{
		Path:       hunk.path,
		Operation:  tooltypes.ApplyPatchOperationAdd,
		NewContent: hunk.contents,
	})

	return nil
}

func applyDeleteHunk(state tooltypes.State, hunk parsedHunk, result *applyPatchToolResult) error {
	oldContent, err := os.ReadFile(hunk.path)
	if err != nil {
		return errors.Wrapf(err, "failed to read %s", hunk.path)
	}
	if err := os.Remove(hunk.path); err != nil {
		return errors.Wrapf(err, "failed to delete file %s", hunk.path)
	}
	_ = state.ClearFileLastAccessed(hunk.path)

	result.deleted = append(result.deleted, hunk.path)
	result.changes = append(result.changes, tooltypes.ApplyPatchChange{
		Path:       hunk.path,
		Operation:  tooltypes.ApplyPatchOperationDelete,
		OldContent: string(oldContent),
	})

	return nil
}

func applyUpdateHunk(state tooltypes.State, hunk parsedHunk, result *applyPatchToolResult) error {
	oldContent, newContent, err := deriveUpdatedContent(hunk.path, hunk.chunks)
	if err != nil {
		return err
	}

	targetPath := hunk.path
	if hunk.movePath != "" {
		targetPath = hunk.movePath
		if parent := filepath.Dir(hunk.movePath); parent != "" && parent != "." {
			if mkErr := os.MkdirAll(parent, 0o755); mkErr != nil {
				return errors.Wrapf(mkErr, "failed to create parent directories for %s", hunk.movePath)
			}
		}
		if writeErr := os.WriteFile(hunk.movePath, []byte(newContent), 0o644); writeErr != nil {
			return errors.Wrapf(writeErr, "failed to write file %s", hunk.movePath)
		}
		if rmErr := os.Remove(hunk.path); rmErr != nil {
			return errors.Wrapf(rmErr, "failed to remove original %s", hunk.path)
		}
		_ = state.ClearFileLastAccessed(hunk.path)
	} else {
		if writeErr := os.WriteFile(hunk.path, []byte(newContent), 0o644); writeErr != nil {
			return errors.Wrapf(writeErr, "failed to write file %s", hunk.path)
		}
	}
	_ = state.SetFileLastAccessed(targetPath, time.Now())

	diff := udiff.Unified(hunk.path, targetPath, oldContent, newContent)

	result.modified = append(result.modified, targetPath)
	result.changes = append(result.changes, tooltypes.ApplyPatchChange{
		Path:        hunk.path,
		Operation:   tooltypes.ApplyPatchOperationUpdate,
		OldContent:  oldContent,
		NewContent:  newContent,
		UnifiedDiff: diff,
		MovePath:    hunk.movePath,
	})

	return nil
}

func lockPaths(state tooltypes.State, paths ...string) func() {
	unique := make(map[string]struct{})
	ordered := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := unique[path]; ok {
			continue
		}
		unique[path] = struct{}{}
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)

	for _, path := range ordered {
		state.LockFile(path)
	}

	return func() {
		for i := len(ordered) - 1; i >= 0; i-- {
			state.UnlockFile(ordered[i])
		}
	}
}

func parseAndResolvePatchInput(parameters string) (*parsedPatch, error) {
	input := &ApplyPatchInput{}
	if err := json.Unmarshal([]byte(parameters), input); err != nil {
		return nil, errors.Wrap(err, "invalid input")
	}
	if strings.TrimSpace(input.Input) == "" {
		return nil, errors.New("input is required")
	}

	parsed, err := parsePatch(input.Input)
	if err != nil {
		return nil, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get current working directory")
	}

	for i := range parsed.hunks {
		parsed.hunks[i].path = resolvePatchPath(cwd, parsed.hunks[i].path)
		if parsed.hunks[i].movePath != "" {
			parsed.hunks[i].movePath = resolvePatchPath(cwd, parsed.hunks[i].movePath)
		}
	}

	return parsed, nil
}

func resolvePatchPath(cwd string, patchPath string) string {
	if filepath.IsAbs(patchPath) {
		return filepath.Clean(patchPath)
	}
	return filepath.Clean(filepath.Join(cwd, patchPath))
}

type patchHunkKind int

const (
	patchHunkAdd patchHunkKind = iota
	patchHunkDelete
	patchHunkUpdate
)

type updateFileChunk struct {
	changeContext *string
	oldLines      []string
	newLines      []string
	isEndOfFile   bool
}

type parsedHunk struct {
	kind     patchHunkKind
	path     string
	movePath string
	contents string
	chunks   []updateFileChunk
}

type parsedPatch struct {
	patch string
	hunks []parsedHunk
}

func parsePatch(patch string) (*parsedPatch, error) {
	trimmed := strings.TrimSpace(patch)
	if trimmed == "" {
		return nil, errors.New("invalid patch: empty patch")
	}

	lines := strings.Split(trimmed, "\n")
	lines, err := normalizePatchBoundaries(lines)
	if err != nil {
		return nil, err
	}

	remaining := lines[1 : len(lines)-1]
	lineNumber := 2
	hunks := make([]parsedHunk, 0)
	for len(remaining) > 0 {
		hunk, consumed, err := parseOneHunk(remaining, lineNumber)
		if err != nil {
			return nil, err
		}
		hunks = append(hunks, hunk)
		lineNumber += consumed
		remaining = remaining[consumed:]
	}

	return &parsedPatch{
		patch: strings.Join(lines, "\n"),
		hunks: hunks,
	}, nil
}

func parseOneHunk(lines []string, lineNumber int) (parsedHunk, int, error) {
	if len(lines) == 0 {
		return parsedHunk{}, 0, errors.New("invalid hunk: missing hunk header")
	}

	firstLine := strings.TrimSpace(lines[0])

	if strings.HasPrefix(firstLine, addFileMarker) {
		path := strings.TrimPrefix(firstLine, addFileMarker)
		var contents strings.Builder
		consumed := 1
		for _, line := range lines[1:] {
			if strings.HasPrefix(line, "+") {
				contents.WriteString(strings.TrimPrefix(line, "+"))
				contents.WriteString("\n")
				consumed++
				continue
			}
			break
		}
		return parsedHunk{kind: patchHunkAdd, path: path, contents: contents.String()}, consumed, nil
	}

	if strings.HasPrefix(firstLine, deleteFileMarker) {
		path := strings.TrimPrefix(firstLine, deleteFileMarker)
		return parsedHunk{kind: patchHunkDelete, path: path}, 1, nil
	}

	if strings.HasPrefix(firstLine, updateFileMarker) {
		path := strings.TrimPrefix(firstLine, updateFileMarker)
		remaining := lines[1:]
		consumed := 1

		movePath := ""
		if len(remaining) > 0 {
			moveLine := strings.TrimSpace(remaining[0])
			if strings.HasPrefix(moveLine, moveToMarker) {
				movePath = strings.TrimPrefix(moveLine, moveToMarker)
				remaining = remaining[1:]
				consumed++
			}
		}

		chunks := make([]updateFileChunk, 0)
		for len(remaining) > 0 {
			if strings.TrimSpace(remaining[0]) == "" {
				remaining = remaining[1:]
				consumed++
				continue
			}
			if strings.HasPrefix(remaining[0], "***") {
				break
			}

			chunk, chunkConsumed, err := parseUpdateFileChunk(remaining, lineNumber+consumed, len(chunks) == 0)
			if err != nil {
				return parsedHunk{}, 0, err
			}
			chunks = append(chunks, chunk)
			remaining = remaining[chunkConsumed:]
			consumed += chunkConsumed
		}

		if len(chunks) == 0 {
			return parsedHunk{}, 0, errors.Errorf("invalid hunk at line %d, Update file hunk for path '%s' is empty", lineNumber, path)
		}

		return parsedHunk{kind: patchHunkUpdate, path: path, movePath: movePath, chunks: chunks}, consumed, nil
	}

	return parsedHunk{}, 0, errors.Errorf("invalid hunk at line %d, '%s' is not a valid hunk header", lineNumber, firstLine)
}

func parseUpdateFileChunk(lines []string, lineNumber int, allowMissingContext bool) (updateFileChunk, int, error) {
	if len(lines) == 0 {
		return updateFileChunk{}, 0, errors.Errorf("invalid hunk at line %d, Update hunk does not contain any lines", lineNumber)
	}

	start := 0
	var changeContext *string
	firstLineTrimmed := strings.TrimSpace(lines[0])

	switch {
	case firstLineTrimmed == emptyChangeContextMaker:
		start = 1
	case strings.HasPrefix(firstLineTrimmed, changeContextMarker):
		ctx := strings.TrimPrefix(firstLineTrimmed, changeContextMarker)
		changeContext = &ctx
		start = 1
	default:
		if !allowMissingContext {
			return updateFileChunk{}, 0, errors.Errorf("invalid hunk at line %d, Expected update hunk to start with a @@ context marker, got: '%s'", lineNumber, lines[0])
		}
	}

	if start >= len(lines) {
		return updateFileChunk{}, 0, errors.Errorf("invalid hunk at line %d, Update hunk does not contain any lines", lineNumber+1)
	}

	chunk := updateFileChunk{
		changeContext: changeContext,
		oldLines:      make([]string, 0),
		newLines:      make([]string, 0),
	}

	parsed := 0
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == eofMarker {
			if parsed == 0 {
				return updateFileChunk{}, 0, errors.Errorf("invalid hunk at line %d, Update hunk does not contain any lines", lineNumber+1)
			}
			chunk.isEndOfFile = true
			parsed++
			break
		}

		if line == "" {
			chunk.oldLines = append(chunk.oldLines, "")
			chunk.newLines = append(chunk.newLines, "")
			parsed++
			continue
		}

		switch line[0] {
		case ' ':
			chunk.oldLines = append(chunk.oldLines, line[1:])
			chunk.newLines = append(chunk.newLines, line[1:])
		case '+':
			chunk.newLines = append(chunk.newLines, line[1:])
		case '-':
			chunk.oldLines = append(chunk.oldLines, line[1:])
		default:
			if parsed == 0 {
				return updateFileChunk{}, 0, errors.Errorf("invalid hunk at line %d, Unexpected line found in update hunk: '%s'", lineNumber+1, line)
			}
			goto done
		}
		parsed++
	}

done:
	return chunk, parsed + start, nil
}

func deriveUpdatedContent(path string, chunks []updateFileChunk) (oldContent string, newContent string, err error) {
	bytes, readErr := os.ReadFile(path)
	if readErr != nil {
		return "", "", errors.Wrapf(readErr, "failed to read file to update %s", path)
	}
	oldContent = string(bytes)

	originalLines := strings.Split(oldContent, "\n")
	if len(originalLines) > 0 && originalLines[len(originalLines)-1] == "" {
		originalLines = originalLines[:len(originalLines)-1]
	}

	replacements, replErr := computeReplacements(originalLines, path, chunks)
	if replErr != nil {
		return "", "", replErr
	}

	updatedLines := applyReplacements(originalLines, replacements)
	if len(updatedLines) == 0 || updatedLines[len(updatedLines)-1] != "" {
		updatedLines = append(updatedLines, "")
	}
	newContent = strings.Join(updatedLines, "\n")

	return oldContent, newContent, nil
}

type replacement struct {
	startIdx int
	oldLen   int
	newLines []string
}

func computeReplacements(originalLines []string, path string, chunks []updateFileChunk) ([]replacement, error) {
	replacements := make([]replacement, 0)
	lineIndex := 0

	for _, chunk := range chunks {
		if chunk.changeContext != nil {
			idx := seekSequence(originalLines, []string{*chunk.changeContext}, lineIndex, false)
			if idx < 0 {
				return nil, errors.Errorf("failed to find context '%s' in %s", *chunk.changeContext, path)
			}
			lineIndex = idx + 1
		}

		if len(chunk.oldLines) == 0 {
			insertIdx := len(originalLines)
			if len(originalLines) > 0 && originalLines[len(originalLines)-1] == "" {
				insertIdx = len(originalLines) - 1
			}
			replacements = append(replacements, replacement{startIdx: insertIdx, oldLen: 0, newLines: chunk.newLines})
			continue
		}

		pattern := chunk.oldLines
		newSlice := chunk.newLines

		found := seekSequence(originalLines, pattern, lineIndex, chunk.isEndOfFile)
		if found < 0 && len(pattern) > 0 && pattern[len(pattern)-1] == "" {
			pattern = pattern[:len(pattern)-1]
			if len(newSlice) > 0 && newSlice[len(newSlice)-1] == "" {
				newSlice = newSlice[:len(newSlice)-1]
			}
			found = seekSequence(originalLines, pattern, lineIndex, chunk.isEndOfFile)
		}

		if found < 0 {
			return nil, errors.Errorf("failed to find expected lines in %s:\n%s", path, strings.Join(chunk.oldLines, "\n"))
		}

		replacements = append(replacements, replacement{startIdx: found, oldLen: len(pattern), newLines: newSlice})
		lineIndex = found + len(pattern)
	}

	sort.Slice(replacements, func(i, j int) bool {
		return replacements[i].startIdx < replacements[j].startIdx
	})

	return replacements, nil
}

func applyReplacements(lines []string, replacements []replacement) []string {
	out := make([]string, len(lines))
	copy(out, lines)

	for i := len(replacements) - 1; i >= 0; i-- {
		repl := replacements[i]

		if repl.oldLen > 0 {
			out = append(out[:repl.startIdx], out[repl.startIdx+repl.oldLen:]...)
		}

		if len(repl.newLines) > 0 {
			prefix := append([]string{}, out[:repl.startIdx]...)
			suffix := append([]string{}, out[repl.startIdx:]...)
			prefix = append(prefix, repl.newLines...)
			out = append(prefix, suffix...)
		}
	}

	return out
}

func seekSequence(lines []string, pattern []string, start int, eof bool) int {
	if len(pattern) == 0 {
		return start
	}
	if len(pattern) > len(lines) {
		return -1
	}

	searchStart := start
	if eof && len(lines) >= len(pattern) {
		searchStart = len(lines) - len(pattern)
	}

	// Exact
	for i := searchStart; i <= len(lines)-len(pattern); i++ {
		match := true
		for j := range pattern {
			if lines[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}

	// Trim trailing spaces
	for i := searchStart; i <= len(lines)-len(pattern); i++ {
		match := true
		for j := range pattern {
			if strings.TrimRight(lines[i+j], " \t") != strings.TrimRight(pattern[j], " \t") {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}

	// Trim both sides
	for i := searchStart; i <= len(lines)-len(pattern); i++ {
		match := true
		for j := range pattern {
			if strings.TrimSpace(lines[i+j]) != strings.TrimSpace(pattern[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}

	// Normalize typographic punctuation
	for i := searchStart; i <= len(lines)-len(pattern); i++ {
		match := true
		for j := range pattern {
			if normalizeSearchLine(lines[i+j]) != normalizeSearchLine(pattern[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}

	return -1
}

func normalizeSearchLine(in string) string {
	trimmed := strings.TrimSpace(in)

	var out strings.Builder
	for _, ch := range trimmed {
		switch ch {
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			out.WriteRune('-')
		case '\u2018', '\u2019', '\u201A', '\u201B':
			out.WriteRune('\'')
		case '\u201C', '\u201D', '\u201E', '\u201F':
			out.WriteRune('"')
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			out.WriteRune(' ')
		default:
			out.WriteRune(ch)
		}
	}

	return out.String()
}

func normalizePatchBoundaries(lines []string) ([]string, error) {
	if err := checkPatchBoundariesStrict(lines); err == nil {
		return lines, nil
	}

	inner, innerErr := extractLenientHeredocPatch(lines)
	if innerErr != nil {
		return nil, errors.Wrap(innerErr, "invalid patch")
	}
	if err := checkPatchBoundariesStrict(inner); err != nil {
		return nil, errors.Wrap(err, "invalid patch")
	}
	return inner, nil
}

func checkPatchBoundariesStrict(lines []string) error {
	if len(lines) == 0 {
		return errors.New("The first line of the patch must be '*** Begin Patch'")
	}

	firstLine := strings.TrimSpace(lines[0])
	if firstLine != beginPatchMarker {
		return errors.New("The first line of the patch must be '*** Begin Patch'")
	}

	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine != endPatchMarker {
		return errors.New("The last line of the patch must be '*** End Patch'")
	}

	return nil
}

func extractLenientHeredocPatch(lines []string) ([]string, error) {
	if len(lines) < 4 {
		return nil, errors.New("The first line of the patch must be '*** Begin Patch'")
	}

	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if (first == "<<EOF" || first == "<<'EOF'" || first == "<<\"EOF\"") && strings.HasSuffix(last, "EOF") {
		return lines[1 : len(lines)-1], nil
	}

	return nil, errors.New("The first line of the patch must be '*** Begin Patch'")
}
