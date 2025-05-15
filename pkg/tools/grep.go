package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
	"go.opentelemetry.io/otel/attribute"
)

type GrepTool struct{}

type CodeSearchInput struct {
	Pattern string `json:"pattern" jsonschema:"description=The regex pattern to search for"`
	Path    string `json:"path" jsonschema:"description=The path to search for the pattern default using the current directory"`
	Include string `json:"include" jsonschema:"description=The optional include path to search for the pattern for example: '*.go' '*.{go,py}'"`
}

func (t *GrepTool) Name() string {
	return "grep_tool"
}

func (t *GrepTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[CodeSearchInput]()
}

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
	}, nil
}
func (t *GrepTool) Description() string {
	return `Search for a pattern in the codebase using regex.

## Important Notes
* You should prioritise using this tool over search via grep, egrep, or other grep-like UNIX commands.
* Binary files and hidden files/directories (starting with .) are skipped by default.
* The result returns at maximum 100 files sorted by modification time (newest first). Pay attention to the truncation notice and refine your search pattern to narrow down the results.

## Input
- pattern: The regex pattern to search for. For example: "func TestFoo_(.*) {", "type Foo struct {"
- path: The path to search for the pattern default using the current directory
- include: The optional include path to search for the pattern for example: '*.go' '*.{go,py}'. Leave it empty if you are not sure about the file name pattern or extension.

If you need to do multi-turn search, use ${subagentTool} instead.
`
}

func (t *GrepTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input CodeSearchInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return err
	}
	if input.Pattern == "" {
		return errors.New("pattern is required")
	}

	return nil
}

const (
	// CodeSearchSurroundingLines = 3
	CodeSearchSurroundingLines = 0
)

// SearchResult represents a search result from a file
type SearchResult struct {
	Filename     string
	MatchedLines []MatchLine
	ContextLines map[int]string // Line number -> content
	LineNumbers  []int          // All line numbers in order
}

// MatchLine represents a single matched line
type MatchLine struct {
	LineNumber  int
	LineContent string
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

		// Get all the line numbers in proper order
		for _, lineNum := range result.LineNumbers {
			// Check if this is a matched line
			isMatch := false
			for _, match := range result.MatchedLines {
				if match.LineNumber == lineNum {
					output.WriteString(fmt.Sprintf("%d:%s\n", lineNum, match.LineContent))
					isMatch = true
					break
				}
			}

			// If not a match, check if it's a context line
			if !isMatch {
				if content, exists := result.ContextLines[lineNum]; exists {
					output.WriteString(fmt.Sprintf("%d-%s\n", lineNum, content))
				}
			}
		}
	}

	return output.String()
}

// isFileIncluded checks if a file should be included based on the pattern
func isFileIncluded(filename, includePattern string) bool {
	if includePattern == "" {
		return true
	}

	// Simple glob matching
	patterns := strings.Split(includePattern, ",")
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)

		// Handle patterns like *.{go,py}
		if strings.Contains(pattern, "{") && strings.Contains(pattern, "}") {
			start := strings.Index(pattern, "{")
			end := strings.Index(pattern, "}")
			if start >= 0 && end > start {
				prefix := pattern[:start]
				exts := strings.Split(pattern[start+1:end], ",")
				for _, ext := range exts {
					if matched, _ := filepath.Match(prefix+ext, filename); matched {
						return true
					}
				}
			}
			continue
		}

		// Regular glob pattern
		matched, err := filepath.Match(pattern, filepath.Base(filename))
		if err == nil && matched {
			return true
		}
	}

	return false
}

// searchFile searches for the pattern in a single file
func searchFile(filename, pattern string, surroundingLines int) (SearchResult, error) {
	result := SearchResult{
		Filename:     filename,
		MatchedLines: []MatchLine{},
		ContextLines: make(map[int]string),
		LineNumbers:  []int{},
	}

	// Compile the regex
	re, err := regexp.Compile(pattern)
	if err != nil {
		return result, err
	}

	// Open the file
	file, err := os.Open(filename)
	if err != nil {
		return result, err
	}
	defer file.Close()

	// Prepare a scanner to read the file line by line
	scanner := bufio.NewScanner(file)
	lineNumber := 0

	// Read the whole file first to get all matches
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return result, err
	}

	// Process lines to find matches and context
	lineSet := make(map[int]bool)
	for i, line := range lines {
		lineNumber = i + 1 // 1-indexed line numbers

		if re.MatchString(line) {
			// Found a match
			result.MatchedLines = append(result.MatchedLines, MatchLine{
				LineNumber:  lineNumber,
				LineContent: line,
			})

			// Add the match line to the line set
			lineSet[lineNumber] = true

			// Add surrounding lines to the context
			for j := lineNumber - surroundingLines; j <= lineNumber+surroundingLines; j++ {
				if j > 0 && j <= len(lines) && j != lineNumber {
					// Add to context if not already a match
					if !lineSet[j] {
						result.ContextLines[j] = lines[j-1]
						lineSet[j] = true
					}
				}
			}
		}
	}

	// Build the sorted list of line numbers
	for ln := range lineSet {
		result.LineNumbers = append(result.LineNumbers, ln)
	}

	// Sort the line numbers
	for i := 0; i < len(result.LineNumbers); i++ {
		for j := i + 1; j < len(result.LineNumbers); j++ {
			if result.LineNumbers[i] > result.LineNumbers[j] {
				result.LineNumbers[i], result.LineNumbers[j] = result.LineNumbers[j], result.LineNumbers[i]
			}
		}
	}

	return result, nil
}

// searchDirectory recursively searches files in a directory
func searchDirectory(ctx context.Context, root, pattern, includePattern string, surroundingLines int) ([]SearchResult, error) {
	var results []SearchResult

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return err
		}

		// Skip hidden files and directories (starting with .)
		baseName := filepath.Base(path)
		if strings.HasPrefix(baseName, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		if !isFileIncluded(path, includePattern) {
			return nil
		}

		// Skip binary files (improved detection)
		if utils.IsBinaryFile(path) {
			return nil
		}

		// Search the file
		result, err := searchFile(path, pattern, surroundingLines)
		if err != nil {
			// Skip files we can't read
			return nil
		}

		// Add to results if there are matches
		if len(result.MatchedLines) > 0 {
			results = append(results, result)
		}

		return nil
	})

	return results, err
}

// sortSearchResultsByModTime sorts search results by file modification time (newest first)
func sortSearchResultsByModTime(results []SearchResult) {
	if len(results) <= 1 {
		return
	}
	
	// Get file modification times
	fileTimes := make(map[string]time.Time)
	for _, result := range results {
		info, err := os.Stat(result.Filename)
		if err == nil {
			fileTimes[result.Filename] = info.ModTime()
		}
	}
	
	// Sort results by modification time (newest first)
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			timeI := fileTimes[results[i].Filename]
			timeJ := fileTimes[results[j].Filename]
			if timeI.Before(timeJ) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

// Limit for maximum search results
const MaxSearchResults = 100

func (t *GrepTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	var input CodeSearchInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return tooltypes.ToolResult{
			Error: fmt.Sprintf("invalid input: %s", err),
		}
	}

	path := "."
	if input.Path != "" {
		path = input.Path
	}

	// Search for the pattern in the specified directory
	results, err := searchDirectory(ctx, path, input.Pattern, input.Include, CodeSearchSurroundingLines)
	if err != nil {
		return tooltypes.ToolResult{
			Error: fmt.Sprintf("search failed: %s", err),
		}
	}

	// Sort results by file modification time (newest first)
	sortSearchResultsByModTime(results)
	
	// Check if results need to be truncated
	isResultsTruncated := false
	if len(results) > MaxSearchResults {
		isResultsTruncated = true
		results = results[:MaxSearchResults]
	}

	// Format the results
	formattedResults := FormatSearchResults(input.Pattern, results)
	
	// Add truncation notice if needed
	if isResultsTruncated {
		formattedResults += "\n\n[TRUNCATED DUE TO MAXIMUM 100 RESULT LIMIT]"
	}

	// Return the results
	return tooltypes.ToolResult{
		Result: formattedResults,
	}
}
