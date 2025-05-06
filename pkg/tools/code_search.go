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

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/jingkaihe/kodelet/pkg/utils"
)

type CodeSearchTool struct{}

type CodeSearchInput struct {
	Pattern string `json:"pattern" jsonschema:"description=The regex pattern to search for"`
	Path    string `json:"path" jsonschema:"description=The path to search for the pattern default using the current directory"`
	Include string `json:"include" jsonschema:"description=The optional include path to search for the pattern for example: '*.go' '*.{go,py}'"`
}

func (t *CodeSearchTool) Name() string {
	return "code_search"
}

func (t *CodeSearchTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[CodeSearchInput]()
}

func (t *CodeSearchTool) Description() string {
	return `Search for a pattern in the codebase using regex.

IMPORTANT: You should prioritise using this tool over search via grep or egrep.

This tool takes three parameters:
- pattern: The regex pattern to search for. For example: "func TestFoo_(.*) {", "type Foo struct {"
- path: The path to search for the pattern default using the current directory
- include: The optional include path to search for the pattern for example: '*.go' '*.{go,py}'. Leave it empty if you are not sure about the file name pattern or extension.
`
}

func (t *CodeSearchTool) ValidateInput(state state.State, parameters string) error {
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
	CodeSearchSurroundingLines = 3
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

func (t *CodeSearchTool) Execute(ctx context.Context, state state.State, parameters string) ToolResult {
	var input CodeSearchInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return ToolResult{
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
		return ToolResult{
			Error: fmt.Sprintf("search failed: %s", err),
		}
	}

	// Format and return the results
	return ToolResult{
		Result: FormatSearchResults(input.Pattern, results),
	}
}
