package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/binaries"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

// GlobToolResult represents the result of a glob pattern search
type GlobToolResult struct {
	pattern   string
	path      string
	files     []string
	truncated bool
	err       string
}

// GetResult returns the formatted search results
func (r *GlobToolResult) GetResult() string {
	var result strings.Builder
	for _, file := range r.files {
		result.WriteString(file)
		result.WriteString("\n")
	}

	if r.truncated {
		result.WriteString("\n[Results truncated to 100 files. Please refine your pattern to narrow down the results.]\n")
	}

	return result.String()
}

// GetError returns the error message
func (r *GlobToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *GlobToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *GlobToolResult) AssistantFacing() string {
	var content string
	if !r.IsError() {
		content = r.GetResult()
	}
	return tooltypes.StringifyToolResult(content, r.GetError())
}

// StructuredData returns structured metadata about the glob search
func (r *GlobToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  "glob_tool",
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	if r.IsError() {
		result.Error = r.GetError()
		return result
	}

	fileInfos := make([]tooltypes.FileInfo, 0, len(r.files))
	for _, file := range r.files {
		info, err := os.Stat(file)
		fileType := "file"
		var size int64
		var modTime time.Time

		if err == nil {
			if info.IsDir() {
				fileType = "directory"
			}
			size = info.Size()
			modTime = info.ModTime()
		}

		language := ""
		if fileType == "file" {
			language = osutil.DetectLanguageFromPath(file)
		}

		fileInfos = append(fileInfos, tooltypes.FileInfo{
			Path:     file,
			Size:     size,
			ModTime:  modTime,
			Type:     fileType,
			Language: language,
		})
	}

	result.Metadata = &tooltypes.GlobMetadata{
		Pattern:   r.pattern,
		Path:      r.path,
		Files:     fileInfos,
		Truncated: r.truncated,
	}

	return result
}

// GlobTool provides functionality to search for files using glob patterns
type GlobTool struct{}

// GlobInput defines the input parameters for the glob_tool
type GlobInput struct {
	Pattern         string `json:"pattern" jsonschema:"description=The glob pattern"`
	Path            string `json:"path" jsonschema:"description=The optional path to search in, defaults to current directory, MUST NOT be a relative path"`
	IgnoreGitignore bool   `json:"ignore_gitignore,omitempty" jsonschema:"description=If true, do not respect .gitignore rules (default: false, meaning .gitignore is respected)"`
}

// Name returns the name of the tool
func (t *GlobTool) Name() string {
	return "glob_tool"
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *GlobTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[GlobInput]()
}

// TracingKVs returns tracing key-value pairs for observability
func (t *GlobTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	input := &GlobInput{}
	err := json.Unmarshal([]byte(parameters), input)
	if err != nil {
		return nil, err
	}

	return []attribute.KeyValue{
		attribute.String("pattern", input.Pattern),
		attribute.String("path", input.Path),
		attribute.Bool("ignore_gitignore", input.IgnoreGitignore),
	}, nil
}

// Description returns the description of the tool
func (t *GlobTool) Description() string {
	return `Find files matching a glob pattern in the filesystem.

## Important Notes
* By default, .gitignore patterns are respected and matching files are excluded.
* Hidden files/directories (starting with .) are excluded by default.
* The result returns at maximum 100 files sorted by modification time (newest first). Pay attention to the truncation notice and refine your pattern to narrow down the results.
* This tool only supports glob pattern matching, not regex.
* This tool only matches filenames. For file content matching, use the ${grep_tool}.

## Input
- pattern: The glob pattern to match files. For example:
  * "*.go" - Find all Go files in the current directory
  * "**/*.go" - Find all Go files recursively
  * "*.{json,yaml}" - Find all JSON and YAML files
  * "cmd/*.go" - Find all Go files in the cmd directory
- path: The optional path to search in, defaults to the current directory. If specified, it must be an absolute path.
- ignore_gitignore: (optional) If true, do not respect .gitignore rules. Default is false (respects .gitignore).

If you need to do multi-turn search using grep_tool and glob_tool, use subagentTool instead.

`
}

// ValidateInput validates the input parameters for the tool
func (t *GlobTool) ValidateInput(_ tooltypes.State, parameters string) error {
	var input GlobInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return err
	}
	if input.Pattern == "" {
		return errors.New("pattern is required")
	}

	if input.Path != "" && !filepath.IsAbs(input.Path) {
		return errors.New("path must be an absolute path")
	}

	// Validate that path is a directory if provided
	if input.Path != "" {
		info, err := os.Stat(input.Path)
		if err != nil {
			return fmt.Errorf("invalid path %q: %w", input.Path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("path %q is not a directory - glob_tool searches directories, not individual files", input.Path)
		}
	}

	return nil
}

func getFdPath() string {
	return binaries.GetFdPath()
}

type fileInfo struct {
	path    string
	modTime time.Time
}

// parseGlobPattern extracts the directory prefix, file pattern, and whether it's recursive
// Examples:
//   - "*.go" -> dir="", pattern="*.go", recursive=false
//   - "**/*.go" -> dir="", pattern="*.go", recursive=true
//   - "subdir/*.go" -> dir="subdir", pattern="*.go", recursive=false
//   - "subdir/**/*.go" -> dir="subdir", pattern="*.go", recursive=true
func parseGlobPattern(pattern string) (dir, filePattern string, recursive bool) {
	parts := strings.Split(pattern, "/")

	var dirParts []string
	var patternParts []string
	foundGlob := false

	for _, part := range parts {
		if part == "**" {
			recursive = true
			foundGlob = true
		} else if strings.ContainsAny(part, "*?[]{}") {
			patternParts = append(patternParts, part)
			foundGlob = true
		} else if !foundGlob {
			dirParts = append(dirParts, part)
		} else {
			patternParts = append(patternParts, part)
		}
	}

	dir = strings.Join(dirParts, "/")
	if len(patternParts) > 0 {
		filePattern = strings.Join(patternParts, "/")
	} else {
		filePattern = "*"
	}

	return dir, filePattern, recursive
}

func searchWithFd(ctx context.Context, searchPath, pattern string, ignoreGitignore bool) ([]string, error) {
	fdPath := getFdPath()
	if fdPath == "" {
		return nil, errors.New("fd not found")
	}

	dir, filePattern, recursive := parseGlobPattern(pattern)

	effectiveSearchPath := searchPath
	if dir != "" {
		effectiveSearchPath = filepath.Join(searchPath, dir)
	}

	args := []string{
		"--glob",
		"--type", "f",
		"--absolute-path",
	}

	if !recursive {
		args = append(args, "--max-depth", "1")
	}

	if ignoreGitignore {
		args = append(args, "--no-ignore", "--hidden")
	}

	args = append(args, filePattern, effectiveSearchPath)

	cmd := exec.CommandContext(ctx, fdPath, args...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return []string{}, nil
			}
			return nil, fmt.Errorf("fd error: %s", string(exitErr.Stderr))
		}
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}

	return lines, nil
}

// Execute searches for files matching the glob pattern
func (t *GlobTool) Execute(ctx context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	var input GlobInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &GlobToolResult{
			pattern: input.Pattern,
			path:    input.Path,
			err:     err.Error(),
		}
	}

	searchPath := input.Path
	var err error
	if searchPath == "" {
		searchPath, err = os.Getwd()
		if err != nil {
			return &GlobToolResult{
				pattern: input.Pattern,
				path:    input.Path,
				err:     err.Error(),
			}
		}
	}

	files, err := searchWithFd(ctx, searchPath, input.Pattern, input.IgnoreGitignore)
	if err != nil {
		return &GlobToolResult{
			pattern: input.Pattern,
			path:    searchPath,
			err:     fmt.Sprintf("Error searching files: %v", err),
		}
	}

	var fileInfos []fileInfo
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		fileInfos = append(fileInfos, fileInfo{
			path:    file,
			modTime: info.ModTime(),
		})
	}

	sort.Slice(fileInfos, func(i, j int) bool {
		return fileInfos[i].modTime.After(fileInfos[j].modTime)
	})

	truncated := false
	if len(fileInfos) > 100 {
		fileInfos = fileInfos[:100]
		truncated = true
	}

	var resultFiles []string
	for _, fi := range fileInfos {
		resultFiles = append(resultFiles, fi.path)
	}

	return &GlobToolResult{
		pattern:   input.Pattern,
		path:      searchPath,
		files:     resultFiles,
		truncated: truncated,
	}
}
