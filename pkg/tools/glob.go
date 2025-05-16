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
	"go.opentelemetry.io/otel/attribute"
)

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
		return tooltypes.ToolResult{
			Error: err.Error(),
		}
	}

	// Default to current directory if path is empty
	searchPath := input.Path
	var err error
	if searchPath == "" {
		searchPath, err = os.Getwd()
		if err != nil {
			return tooltypes.ToolResult{
				Error: err.Error(),
			}
		}
	}

	// Holds file information for sorting
	type fileInfo struct {
		path    string
		modTime time.Time
	}

	var files []fileInfo
	truncated := false

	// Walk the file tree
	err = doublestar.GlobWalk(os.DirFS(searchPath), input.Pattern, func(path string, d fs.DirEntry) error {
		// Skip directories in the result list, but continue walking
		if d.IsDir() {
			return nil
		}

		if strings.HasPrefix(path, ".") {
			return nil
		}

		absPath := filepath.Join(searchPath, path)
		info, err := os.Stat(absPath)
		if err != nil {
			return nil // simply skip the file
		}

		files = append(files, fileInfo{
			path:    absPath,
			modTime: info.ModTime(),
		})

		return nil
	})

	if err != nil {
		return tooltypes.ToolResult{
			Error: fmt.Sprintf("Error walking the path: %v", err),
		}
	}

	// Sort files by modification time (newest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	// Limit results to 100 files
	if len(files) > 100 {
		files = files[:100]
		truncated = true
	}

	// Prepare the result
	var result strings.Builder
	for _, file := range files {
		result.WriteString(file.path)
		result.WriteString("\n")
	}

	if truncated {
		result.WriteString("\n[Results truncated to 100 files. Please refine your pattern to narrow down the results.]\n")
	}

	return tooltypes.ToolResult{
		Result: result.String(),
	}
}
