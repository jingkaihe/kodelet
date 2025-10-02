package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"go.opentelemetry.io/otel/attribute"
)

// excludedHighVolumeDirs contains directories that are typically very large
// and would flood the glob results with thousands of irrelevant files.
// These are skipped by default for performance reasons.
var excludedHighVolumeDirs = map[string]bool{
	".git":             true, // Git objects, refs, hooks - thousands of files
	"node_modules":     true, // NPM packages - can be 10,000+ files
	".next":            true, // Next.js build output
	".nuxt":            true, // Nuxt.js build output
	"dist":             true, // Build outputs
	"build":            true, // Build outputs
	".cache":           true, // Various tool caches
	".parcel-cache":    true, // Parcel bundler cache
	"coverage":         true, // Test coverage reports
	".nyc_output":      true, // NYC coverage data
	".pytest_cache":    true, // Pytest cache
	"__pycache__":      true, // Python bytecode cache
	".venv":            true, // Python virtual environments
	"venv":             true, // Python virtual environments
	".tox":             true, // Tox test environments
	"vendor":           true, // Go vendor directory
	".terraform":       true, // Terraform providers and modules
	".serverless":      true, // Serverless framework
	"target":           true, // Rust/Maven build output
	".turbo":           true, // Turborepo cache
	".yarn":            true, // Yarn cache
	"bower_components": true, // Bower packages
}

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

	// Convert file paths to FileInfo structures
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

		// Detect language from file extension
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
	Pattern           string `json:"pattern" jsonschema:"description=The glob pattern"`
	Path              string `json:"path" jsonschema:"description=The optional path to search in, defaults to current directory, MUST NOT be a relative path"`
	IncludeHighVolume bool   `json:"include_high_volume,omitempty" jsonschema:"description=Include high-volume directories like .git and node_modules (default: false)"`
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
	}, nil
}

// Description returns the description of the tool
func (t *GlobTool) Description() string {
	return `Find files matching a glob pattern in the filesystem.

## Important Notes
* High-volume directories (node_modules, .git, build outputs, etc.) are skipped by default for performance.
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
- include_high_volume: (optional) Include high-volume directories like .git and node_modules. Default is false.

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

	return nil
}

// shouldExcludePath checks if a path should be excluded based on high-volume directories
func shouldExcludePath(path string, includeHighVolume bool) bool {
	if includeHighVolume {
		return false
	}

	// Check all path segments for excluded directories
	pathParts := strings.Split(path, string(filepath.Separator))
	for _, part := range pathParts {
		if excludedHighVolumeDirs[part] {
			return true
		}
	}

	return false
}

// Execute searches for files matching the glob pattern
func (t *GlobTool) Execute(_ context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	var input GlobInput
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return &GlobToolResult{
			pattern: input.Pattern,
			path:    input.Path,
			err:     err.Error(),
		}
	}

	// Default to current directory if path is empty
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

	// Holds file information for sorting
	type fileInfo struct {
		path    string
		modTime time.Time
	}

	var fileInfos []fileInfo
	truncated := false

	// Walk the file tree
	err = doublestar.GlobWalk(os.DirFS(searchPath), input.Pattern, func(path string, d fs.DirEntry) error {
		// Check if this path should be excluded
		if shouldExcludePath(path, input.IncludeHighVolume) {
			if d.IsDir() {
				// Skip entire directory tree for performance
				return filepath.SkipDir
			}
			// Skip this individual file
			return nil
		}

		// Skip directories in the result list, but continue walking
		if d.IsDir() {
			return nil
		}

		absPath := filepath.Join(searchPath, path)
		info, err := os.Stat(absPath)
		if err != nil {
			return nil // simply skip the file
		}

		fileInfos = append(fileInfos, fileInfo{
			path:    absPath,
			modTime: info.ModTime(),
		})

		return nil
	})
	if err != nil {
		return &GlobToolResult{
			pattern: input.Pattern,
			path:    searchPath,
			err:     fmt.Sprintf("Error walking the path: %v", err),
		}
	}

	// Sort files by modification time (newest first)
	sort.Slice(fileInfos, func(i, j int) bool {
		return fileInfos[i].modTime.After(fileInfos[j].modTime)
	})

	// Limit results to 100 files
	if len(fileInfos) > 100 {
		fileInfos = fileInfos[:100]
		truncated = true
	}

	// Extract file paths
	var files []string
	for _, fileInfo := range fileInfos {
		files = append(files, fileInfo.path)
	}

	return &GlobToolResult{
		pattern:   input.Pattern,
		path:      searchPath,
		files:     files,
		truncated: truncated,
	}
}
