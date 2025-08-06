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
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
	"go.opentelemetry.io/otel/attribute"
)

type GlobToolResult struct {
	pattern   string
	path      string
	files     []string
	truncated bool
	err       string
}

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

func (r *GlobToolResult) GetError() string {
	return r.err
}

func (r *GlobToolResult) IsError() bool {
	return r.err != ""
}

func (r *GlobToolResult) AssistantFacing() string {
	var content string
	if !r.IsError() {
		content = r.GetResult()
	}
	return tooltypes.StringifyToolResult(content, r.GetError())
}

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
			language = utils.DetectLanguageFromPath(file)
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

type GlobTool struct{}

type GlobInput struct {
	Pattern string `json:"pattern" jsonschema:"description=The glob pattern"`
	Path    string `json:"path" jsonschema:"description=The optional path to search in, defaults to current directory, MUST NOT be a relative path"`
}

func (t *GlobTool) Name() string {
	return "glob_tool"
}

func (t *GlobTool) GenerateSchema() *jsonschema.Schema {
	return GenerateSchema[GlobInput]()
}

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

func (t *GlobTool) Description() string {
	return `Find files matching a glob pattern in the filesystem.

## Important Notes
* Hidden files/directories (starting with .) are skipped by default.
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

If you need to do multi-turn search using grep_tool and glob_tool, use subagentTool instead.

`
}

func (t *GlobTool) ValidateInput(state tooltypes.State, parameters string) error {
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

func (t *GlobTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
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
		// Skip directories in the result list, but continue walking
		if d.IsDir() {
			return nil
		}

		// Skip hidden files and directories, but allow common development directories
		if strings.HasPrefix(path, ".") {
			// Allow common development directories that start with dot
			pathParts := strings.Split(path, string(filepath.Separator))
			firstPart := pathParts[0]

			// List of allowed dot directories
			allowedDotDirs := []string{".github", ".vscode", ".idea", ".gitignore", ".gitattributes"}
			allowed := false
			for _, allowedDir := range allowedDotDirs {
				if firstPart == allowedDir {
					allowed = true
					break
				}
			}

			if !allowed {
				return nil
			}
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
