package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

const (
	// MaxSearchResults is the limit for maximum search results returned by the grep tool.
	MaxSearchResults = 100

	// MaxLineLength limits the length of matched lines to prevent long lines from consuming context.
	MaxLineLength = 300

	// MaxOutputSize limits the total output size in bytes to prevent context overflow.
	MaxOutputSize = 50 * 1024 // 50KB

	// Format strings for search result output
	searchResultHeaderFmt = "Search results for pattern '%s':\n"
	fileHeaderFmt         = "\nPattern found in file %s:\n\n"
	truncationIndicator   = "... [truncated]"
	// maxLineNumberDigits is used to calculate lineNumberOverhead.
	// Each output line has: line number (8 digits max) + separator (:/-) + newline = 10 chars
	maxLineNumberDigits = "12345678:N"
)

// Size estimation constants for output truncation, calculated from format strings
var (
	searchResultHeaderBuffer = len(searchResultHeaderFmt)
	fileHeaderOverhead       = len(fileHeaderFmt)
	truncationIndicatorLen   = len(truncationIndicator)
	lineNumberOverhead       = len(maxLineNumberDigits)
)

// TruncationReason indicates why results were truncated
type TruncationReason string

// Truncation reason constants
const (
	NotTruncated          TruncationReason = ""
	TruncatedByFileLimit  TruncationReason = "file_limit"
	TruncatedByOutputSize TruncationReason = "output_size"
)

// GrepToolResult represents the result of a grep/search operation
type GrepToolResult struct {
	pattern          string
	path             string
	include          string
	results          []SearchResult
	truncated        bool
	truncationReason TruncationReason
	maxResults       int
	err              string
}

// GetResult returns the formatted search results
func (r *GrepToolResult) GetResult() string {
	result := FormatSearchResults(r.pattern, r.results)

	if r.truncated {
		switch r.truncationReason {
		case TruncatedByFileLimit:
			result += fmt.Sprintf("\n\n[TRUNCATED DUE TO MAXIMUM %d FILE LIMIT - refine your search pattern or use include filter]", r.maxResults)
		case TruncatedByOutputSize:
			result += "\n\n[TRUNCATED DUE TO OUTPUT SIZE LIMIT (50KB) - refine your search pattern or use include filter]"
		default:
			result += fmt.Sprintf("\n\n[TRUNCATED DUE TO MAXIMUM %d RESULT LIMIT - refine your search pattern or use include filter]", r.maxResults)
		}
	}

	return result
}

// GetError returns the error message
func (r *GrepToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *GrepToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *GrepToolResult) AssistantFacing() string {
	var content string
	if !r.IsError() {
		content = r.GetResult()
	}
	return tooltypes.StringifyToolResult(content, r.GetError())
}

// StructuredData returns structured metadata about the search operation
func (r *GrepToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "grep_tool",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	// Convert internal SearchResult to metadata format
	metadataResults := make([]tooltypes.SearchResult, 0, len(r.results))
	for _, res := range r.results {
		matches := make([]tooltypes.SearchMatch, 0, len(res.LineNumbers))
		for _, lineNum := range res.LineNumbers {
			if content, isMatch := res.MatchedLines[lineNum]; isMatch {
				matchStart, matchEnd := 0, 0
				if positions, ok := res.MatchPositions[lineNum]; ok && len(positions) > 0 {
					matchStart = positions[0].Start
					matchEnd = positions[0].End
				}
				matches = append(matches, tooltypes.SearchMatch{
					LineNumber: lineNum,
					Content:    content,
					MatchStart: matchStart,
					MatchEnd:   matchEnd,
				})
			} else if content, exists := res.ContextLines[lineNum]; exists {
				matches = append(matches, tooltypes.SearchMatch{
					LineNumber: lineNum,
					Content:    content,
					IsContext:  true,
				})
			}
		}

		// Detect language from file extension
		language := osutil.DetectLanguageFromPath(res.Filename)

		metadataResults = append(metadataResults, tooltypes.SearchResult{
			FilePath: res.Filename,
			Language: language,
			Matches:  matches,
		})
	}

	// Always populate metadata, even for errors
	result.Metadata = &tooltypes.GrepMetadata{
		Pattern:   r.pattern,
		Path:      r.path,
		Include:   r.include,
		Results:   metadataResults,
		Truncated: r.truncated,
	}

	if r.IsError() {
		result.Error = r.GetError()
	}

	return result
}

// GrepTool provides functionality to search for patterns in files
type GrepTool struct{}

// CodeSearchInput defines the input parameters for the grep_tool
type CodeSearchInput struct {
	Pattern       string `json:"pattern" jsonschema:"description=The pattern to search for (regex by default or literal string if fixed_strings is true)"`
	Path          string `json:"path" jsonschema:"description=The absolute path to search in. Can be a directory (searches all files recursively) or a single file. Defaults to current working directory if not specified"`
	Include       string `json:"include" jsonschema:"description=The optional glob pattern to filter files for example: '*.go' '*.{go,py}'. Only applies when searching directories"`
	IgnoreCase    bool   `json:"ignore_case" jsonschema:"description=If true use case-insensitive search. Default is false (smart-case: case-insensitive if pattern is all lowercase)"`
	FixedStrings  bool   `json:"fixed_strings" jsonschema:"description=If true treat pattern as literal string instead of regex. Default is false"`
	SurroundLines int    `json:"surround_lines" jsonschema:"description=Number of lines to show before and after each match. Default is 0 (no context lines)"`
	MaxResults    int    `json:"max_results" jsonschema:"description=Maximum number of files to return results from. Default is 100. Use a smaller value to reduce output size"`
}

// Name returns the name of the tool
func (t *GrepTool) Name() string {
	return "grep_tool"
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *GrepTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[CodeSearchInput]()
}

// TracingKVs returns tracing key-value pairs for observability
func (t *GrepTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &CodeSearchInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("pattern", input.Pattern),
		attribute.String("path", input.Path),
		attribute.String("include", input.Include),
		attribute.Bool("ignore_case", input.IgnoreCase),
		attribute.Bool("fixed_strings", input.FixedStrings),
		attribute.Int("surround_lines", input.SurroundLines),
		attribute.Int("max_results", input.MaxResults),
	}, nil
}

// Description returns the description of the tool
func (t *GrepTool) Description() string {
	return fmt.Sprintf(`Search for a pattern in the codebase using regex.

## Important Notes
* You should prioritise using this tool over search via grep, egrep, or other grep-like UNIX commands.
* Files matching .gitignore patterns are automatically excluded.
* Binary files and hidden files/directories (starting with .) are skipped by default.
* Results are sorted by modification time (newest first), returning at most %d files by default. Pay attention to the truncation notice and refine your search pattern to narrow down the results.
* To get the best result, you should use the ${glob_tool} to narrow down the files to search in, and then use this tool for a more targeted search.

## Input
- pattern: The pattern to search for (regex by default, or literal string if fixed_strings is true). For example: "func TestFoo_(.*) {", "type Foo struct {"
- path: The absolute path to search in. Can be a directory (searches all files recursively) or a single file. Defaults to current working directory if not specified.
- include: The optional glob pattern to filter files, for example: '*.go' '*.{go,py}'. Only applies when searching directories. Leave it empty if you are not sure about the file name pattern or extension.
- ignore_case: If true, use case-insensitive search. Default is false (smart-case: case-insensitive if pattern is all lowercase).
- fixed_strings: If true, treat pattern as a literal string instead of regex. Useful when searching for strings containing special characters like "foo.bar()" or "[test]".
- surround_lines: Number of lines to show before and after each match for context. Default is 0 (no context lines).
- max_results: Number of files to return results from (1-%d). Default and maximum is %d. Use a smaller value to reduce output size when searching large codebases.

If you need to do multi-turn search using grep_tool and glob_tool, use subagentTool instead.
`, MaxSearchResults, MaxSearchResults, MaxSearchResults)
}

// ValidateInput validates the input parameters for the tool
func (t *GrepTool) ValidateInput(_ tooltypes.State, parameters string) error {
	var input CodeSearchInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return err
	}

	if strings.HasPrefix(input.Path, ".") {
		return errors.New("path must be an absolute path")
	}

	if input.Pattern == "" {
		return errors.New("pattern is required")
	}

	// Validate that path exists if provided
	if input.Path != "" {
		if _, err := os.Stat(input.Path); err != nil {
			return errors.Wrapf(err, "invalid path %q", input.Path)
		}
	}

	// Validate max_results doesn't exceed the limit
	if input.MaxResults > MaxSearchResults {
		return errors.Errorf("max_results cannot exceed %d", MaxSearchResults)
	}

	return nil
}

// MatchPosition represents the start and end position of a match within a line
type MatchPosition struct {
	Start int
	End   int
}

// SearchResult represents a search result from a file
type SearchResult struct {
	Filename       string
	MatchedLines   map[int]string          // Line number -> content (matched lines)
	MatchPositions map[int][]MatchPosition // Line number -> match positions within the line
	ContextLines   map[int]string          // Line number -> content (context lines)
	LineNumbers    []int                   // All line numbers in order
}

// truncateLine truncates a line if it exceeds maxLength, adding an ellipsis indicator
func truncateLine(line string, maxLength int) string {
	if len(line) <= maxLength {
		return line
	}
	return line[:maxLength] + truncationIndicator
}

// FormatSearchResults formats the search results for output
func FormatSearchResults(pattern string, results []SearchResult) string {
	if len(results) == 0 {
		return "No matches found for pattern '" + pattern + "'"
	}

	var output strings.Builder
	fmt.Fprintf(&output, searchResultHeaderFmt, pattern)

	for _, result := range results {
		if len(result.MatchedLines) == 0 {
			continue
		}

		fmt.Fprintf(&output, fileHeaderFmt, result.Filename)

		for _, lineNum := range result.LineNumbers {
			if content, isMatch := result.MatchedLines[lineNum]; isMatch {
				fmt.Fprintf(&output, "%d:%s\n", lineNum, truncateLine(content, MaxLineLength))
			} else if content, exists := result.ContextLines[lineNum]; exists {
				fmt.Fprintf(&output, "%d-%s\n", lineNum, truncateLine(content, MaxLineLength))
			}
		}
	}

	return output.String()
}

// getRipgrepPath returns the path to the managed ripgrep binary
func getRipgrepPath() string {
	return binaries.GetRipgrepPath()
}

// rgJSONMatch represents a match in ripgrep's JSON output
type rgJSONMatch struct {
	Type string          `json:"type"`
	Data rgJSONMatchData `json:"data"`
}

type rgJSONMatchData struct {
	Path       rgJSONPath       `json:"path"`
	Lines      rgJSONLines      `json:"lines"`
	LineNumber int              `json:"line_number"`
	Submatches []rgJSONSubmatch `json:"submatches"`
}

type rgJSONPath struct {
	Text string `json:"text"`
}

type rgJSONLines struct {
	Text string `json:"text"`
}

type rgJSONSubmatch struct {
	Match rgJSONSubmatchText `json:"match"`
	Start int                `json:"start"`
	End   int                `json:"end"`
}

type rgJSONSubmatchText struct {
	Text string `json:"text"`
}

// searchPath searches for pattern using ripgrep in a file or directory
func searchPath(ctx context.Context, searchPath, pattern, includePattern string, ignoreCase, fixedStrings bool, surroundLines int) ([]SearchResult, error) {
	rgPath := getRipgrepPath()
	if rgPath == "" {
		return nil, errors.New("ripgrep not found")
	}

	// Check if path is a file or directory
	info, err := os.Stat(searchPath)
	if err != nil {
		return nil, fmt.Errorf("cannot access path %q: %w", searchPath, err)
	}
	isFile := !info.IsDir()

	args := []string{
		"--json",
		"--no-heading",
		"--no-messages", // Suppress error messages for unreadable files
	}

	// Only sort by modified time for directory searches
	if !isFile {
		args = append(args, "--sortr=modified")
	}

	if ignoreCase {
		args = append(args, "-i")
	}

	if fixedStrings {
		args = append(args, "-F")
	}

	if surroundLines > 0 {
		args = append(args, "-C", strconv.Itoa(surroundLines))
	}

	// Add glob pattern if specified (only makes sense for directory searches)
	if includePattern != "" && !isFile {
		args = append(args, "-g", includePattern)
	}

	// Exclude hidden files and directories (only for directory searches)
	if !isFile {
		args = append(args, "-g", "!.*")
	}

	var cmd *exec.Cmd
	var rootDir string

	if isFile {
		// For file search, pass the file path directly to ripgrep
		args = append(args, pattern, searchPath)
		cmd = exec.CommandContext(ctx, rgPath, args...)
		rootDir = filepath.Dir(searchPath)
	} else {
		// For directory search, set working directory and search "."
		args = append(args, pattern, ".")
		cmd = exec.CommandContext(ctx, rgPath, args...)
		cmd.Dir = searchPath
		rootDir = searchPath
	}

	output, err := cmd.Output()
	// ripgrep returns exit code 1 if no matches found, which is not an error
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				// No matches found
				return []SearchResult{}, nil
			}
			// Exit code 2 means error
			return nil, fmt.Errorf("ripgrep error: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	return parseRipgrepJSON(output, rootDir)
}

// parseRipgrepJSON parses ripgrep's JSON output into SearchResult slice
func parseRipgrepJSON(output []byte, root string) ([]SearchResult, error) {
	resultsMap := make(map[string]*SearchResult)
	var orderedFiles []string

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		var msg rgJSONMatch
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue // Skip malformed lines
		}

		if msg.Type != "match" && msg.Type != "context" {
			continue
		}

		// Convert relative path to absolute path
		filename := msg.Data.Path.Text
		if !filepath.IsAbs(filename) {
			filename = filepath.Join(root, filename)
		}
		lineContent := strings.TrimSuffix(msg.Data.Lines.Text, "\n")

		if _, exists := resultsMap[filename]; !exists {
			resultsMap[filename] = &SearchResult{
				Filename:       filename,
				MatchedLines:   make(map[int]string),
				MatchPositions: make(map[int][]MatchPosition),
				ContextLines:   make(map[int]string),
				LineNumbers:    []int{},
			}
			orderedFiles = append(orderedFiles, filename)
		}

		result := resultsMap[filename]
		if msg.Type == "match" {
			result.MatchedLines[msg.Data.LineNumber] = lineContent
			positions := make([]MatchPosition, 0, len(msg.Data.Submatches))
			for _, submatch := range msg.Data.Submatches {
				positions = append(positions, MatchPosition{
					Start: submatch.Start,
					End:   submatch.End,
				})
			}
			result.MatchPositions[msg.Data.LineNumber] = positions
		} else {
			result.ContextLines[msg.Data.LineNumber] = lineContent
		}
		result.LineNumbers = append(result.LineNumbers, msg.Data.LineNumber)
	}

	// Build results in the order files were encountered (which is mod time order from ripgrep)
	results := make([]SearchResult, 0, len(orderedFiles))
	for _, filename := range orderedFiles {
		results = append(results, *resultsMap[filename])
	}

	return results, nil
}

// estimateResultSize estimates the output size in bytes for a single SearchResult
func estimateResultSize(result SearchResult) int {
	size := len(result.Filename) + fileHeaderOverhead
	for _, lineNum := range result.LineNumbers {
		if content, isMatch := result.MatchedLines[lineNum]; isMatch {
			lineLen := len(content)
			if lineLen > MaxLineLength {
				lineLen = MaxLineLength + truncationIndicatorLen
			}
			size += lineLen + lineNumberOverhead
		} else if content, exists := result.ContextLines[lineNum]; exists {
			lineLen := len(content)
			if lineLen > MaxLineLength {
				lineLen = MaxLineLength + truncationIndicatorLen
			}
			size += lineLen + lineNumberOverhead
		}
	}
	return size
}

// truncateResultsBySize truncates results to fit within MaxOutputSize
// The pattern parameter is used to accurately estimate the header size
func truncateResultsBySize(results []SearchResult, pattern string) ([]SearchResult, bool) {
	// Account for header size: format string overhead + pattern length
	totalSize := searchResultHeaderBuffer + len(pattern)
	var truncatedResults []SearchResult

	for _, result := range results {
		resultSize := estimateResultSize(result)
		if totalSize+resultSize > MaxOutputSize {
			return truncatedResults, true
		}
		totalSize += resultSize
		truncatedResults = append(truncatedResults, result)
	}

	return truncatedResults, false
}

// Execute searches for the pattern in files and returns the results
func (t *GrepTool) Execute(ctx context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	var input CodeSearchInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &GrepToolResult{
			pattern: input.Pattern,
			path:    input.Path,
			include: input.Include,
			err:     fmt.Sprintf("invalid input: %s", err),
		}
	}

	path, err := os.Getwd()
	if err != nil {
		return &GrepToolResult{
			pattern: input.Pattern,
			path:    input.Path,
			include: input.Include,
			err:     fmt.Sprintf("failed to get current working directory: %s", err),
		}
	}
	if input.Path != "" {
		path = input.Path
	}

	// Search using ripgrep
	results, err := searchPath(ctx, path, input.Pattern, input.Include, input.IgnoreCase, input.FixedStrings, input.SurroundLines)
	if err != nil {
		return &GrepToolResult{
			pattern: input.Pattern,
			path:    path,
			include: input.Include,
			err:     fmt.Sprintf("search failed: %s", err),
		}
	}

	// Use default max results if not specified
	maxResults := input.MaxResults
	if maxResults <= 0 {
		maxResults = MaxSearchResults
	}

	// Check if results need to be truncated by file count
	truncationReason := NotTruncated
	if len(results) > maxResults {
		truncationReason = TruncatedByFileLimit
		results = results[:maxResults]
	}

	// Always check size truncation, even after file limit truncation
	// This ensures output never exceeds MaxOutputSize
	var sizeTruncated bool
	results, sizeTruncated = truncateResultsBySize(results, input.Pattern)
	if sizeTruncated {
		// Size limit takes precedence for user messaging since it's the actual constraint
		truncationReason = TruncatedByOutputSize
	}

	// Return the results
	return &GrepToolResult{
		pattern:          input.Pattern,
		path:             path,
		include:          input.Include,
		results:          results,
		truncated:        truncationReason != NotTruncated,
		truncationReason: truncationReason,
		maxResults:       maxResults,
	}
}
