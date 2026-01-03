package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

// GrepToolResult represents the result of a grep/search operation
type GrepToolResult struct {
	pattern   string
	path      string
	include   string
	results   []SearchResult
	truncated bool
	err       string
}

// GetResult returns the formatted search results
func (r *GrepToolResult) GetResult() string {
	result := FormatSearchResults(r.pattern, r.results)

	if r.truncated {
		result += "\n\n[TRUNCATED DUE TO MAXIMUM 100 RESULT LIMIT]"
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
		matches := make([]tooltypes.SearchMatch, 0, len(res.MatchedLines))
		for lineNum, content := range res.MatchedLines {
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
	Path          string `json:"path" jsonschema:"description=The absolute path to search for the pattern default using the current directory"`
	Include       string `json:"include" jsonschema:"description=The optional include path to search for the pattern for example: '*.go' '*.{go,py}'"`
	IgnoreCase    bool   `json:"ignore_case" jsonschema:"description=If true use case-insensitive search. Default is false (smart-case: case-insensitive if pattern is all lowercase)"`
	FixedStrings  bool   `json:"fixed_strings" jsonschema:"description=If true treat pattern as literal string instead of regex. Default is false"`
	SurroundLines int    `json:"surround_lines" jsonschema:"description=Number of lines to show before and after each match. Default is 0 (no context lines)"`
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
	}, nil
}

// Description returns the description of the tool
func (t *GrepTool) Description() string {
	return `Search for a pattern in the codebase using regex.

## Important Notes
* You should prioritise using this tool over search via grep, egrep, or other grep-like UNIX commands.
* Files matching .gitignore patterns are automatically excluded.
* Binary files and hidden files/directories (starting with .) are skipped by default.
* The result returns at maximum 100 files sorted by modification time (newest first). Pay attention to the truncation notice and refine your search pattern to narrow down the results.
* To get the best result, you should use the ${glob_tool} to narrow down the files to search in, and then use this tool for a more targeted search.

## Input
- pattern: The pattern to search for (regex by default, or literal string if fixed_strings is true). For example: "func TestFoo_(.*) {", "type Foo struct {"
- path: The absolute path to search for the pattern default using the current directory
- include: The optional include path to search for the pattern for example: '*.go' '*.{go,py}'. Leave it empty if you are not sure about the file name pattern or extension.
- ignore_case: If true, use case-insensitive search. Default is false (smart-case: case-insensitive if pattern is all lowercase).
- fixed_strings: If true, treat pattern as a literal string instead of regex. Useful when searching for strings containing special characters like "foo.bar()" or "[test]".
- surround_lines: Number of lines to show before and after each match for context. Default is 0 (no context lines).

If you need to do multi-turn search using grep_tool and glob_tool, use subagentTool instead.
`
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

// FormatSearchResults formats the search results for output
func FormatSearchResults(pattern string, results []SearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No matches found for pattern '%s'", pattern)
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Search results for pattern '%s':\n", pattern))

	for _, result := range results {
		if len(result.MatchedLines) == 0 {
			continue
		}

		output.WriteString(fmt.Sprintf("\nPattern found in file %s:\n\n", result.Filename))

		for _, lineNum := range result.LineNumbers {
			if content, isMatch := result.MatchedLines[lineNum]; isMatch {
				output.WriteString(fmt.Sprintf("%d:%s\n", lineNum, content))
			} else if content, exists := result.ContextLines[lineNum]; exists {
				output.WriteString(fmt.Sprintf("%d-%s\n", lineNum, content))
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

// searchDirectory searches for pattern using ripgrep
func searchDirectory(ctx context.Context, root, pattern, includePattern string, ignoreCase, fixedStrings bool, surroundLines int) ([]SearchResult, error) {
	rgPath := getRipgrepPath()
	if rgPath == "" {
		return nil, errors.New("ripgrep not found")
	}

	args := []string{
		"--json",
		"--sortr=modified", // Sort by modification time, newest first
		"--no-heading",
		"--no-messages", // Suppress error messages for unreadable files
	}

	if ignoreCase {
		args = append(args, "-i")
	}

	if fixedStrings {
		args = append(args, "-F")
	}

	if surroundLines > 0 {
		args = append(args, "-C", fmt.Sprintf("%d", surroundLines))
	}

	// Add glob pattern if specified
	if includePattern != "" {
		args = append(args, "-g", includePattern)
	}

	// Exclude hidden files and directories (must come after include pattern)
	args = append(args, "-g", "!.*")

	// Add the pattern and search current directory (we'll set the working directory)
	args = append(args, pattern, ".")

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = root // Run from the search root for proper relative path matching
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

	return parseRipgrepJSON(output, root)
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

// MaxSearchResults is the limit for maximum search results returned by the grep tool.
const MaxSearchResults = 100

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
	results, err := searchDirectory(ctx, path, input.Pattern, input.Include, input.IgnoreCase, input.FixedStrings, input.SurroundLines)
	if err != nil {
		return &GrepToolResult{
			pattern: input.Pattern,
			path:    path,
			include: input.Include,
			err:     fmt.Sprintf("search failed: %s", err),
		}
	}

	// Check if results need to be truncated
	isResultsTruncated := false
	if len(results) > MaxSearchResults {
		isResultsTruncated = true
		results = results[:MaxSearchResults]
	}

	// Return the results
	return &GrepToolResult{
		pattern:   input.Pattern,
		path:      path,
		include:   input.Include,
		results:   results,
		truncated: isResultsTruncated,
	}
}
