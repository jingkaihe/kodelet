// Package client implements the workspace-bound side of the runner protocol.
package client

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

func buildWireManifest(
	local agentenv.Manifest,
	config llmtypes.Config,
	runtime *extensions.Runtime,
	runnerID string,
	runID string,
	generation int64,
	reservedToolNames []string,
) (runnerpayload.Manifest, error) {
	reserved := make(map[string]struct{}, len(reservedToolNames))
	for _, name := range reservedToolNames {
		name = strings.TrimSpace(name)
		if name != "" {
			reserved[name] = struct{}{}
		}
	}
	if runtime != nil {
		for _, tool := range runtime.Tools() {
			if tool == nil {
				continue
			}
			if _, collision := reserved[tool.Name()]; collision {
				return runnerpayload.Manifest{}, errors.Errorf("extension tool %s collides with a reserved control-plane tool", tool.Name())
			}
		}
	}

	contextPaths := make([]string, 0, len(local.Contexts))
	for path := range local.Contexts {
		contextPaths = append(contextPaths, path)
	}
	sort.Strings(contextPaths)
	contexts := make([]runnerpayload.ContextFile, 0, len(contextPaths))
	for _, path := range contextPaths {
		content := local.Contexts[path]
		contexts = append(contexts, runnerpayload.ContextFile{
			Path:    path,
			Content: content,
			Digest:  contentDigest(content),
		})
	}

	definitions := append([]agentenv.ToolDefinition(nil), local.Tools...)
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	wireTools := make([]runnerpayload.ToolDefinition, 0, len(definitions))
	var skillDefinitions []runnerpayload.SkillDefinition
	for _, definition := range definitions {
		if definition.Placement == agentenv.ToolPlacementControlPlane {
			continue
		}
		if _, collision := reserved[definition.Name]; collision {
			return runnerpayload.Manifest{}, errors.Errorf("runner tool %s collides with a reserved control-plane tool", definition.Name)
		}
		if runtimeTool := runtimeToolByName(runtime, definition.Name); runtimeTool != nil && runtimeTool != definition.Tool {
			return runnerpayload.Manifest{}, errors.Errorf("extension tool %s collides with a runner tool", definition.Name)
		}
		wireTools = append(wireTools, runnerpayload.ToolDefinition{
			Name:        definition.Name,
			Description: definition.Description,
			InputSchema: cloneJSONMap(definition.InputSchema),
			Placement:   string(agentenv.ToolPlacementEnvironment),
		})
		if skillTool, ok := definition.Tool.(*tools.SkillTool); ok {
			for _, skill := range skillTool.GetSkills() {
				if skill == nil {
					continue
				}
				skillDefinitions = append(skillDefinitions, runnerpayload.SkillDefinition{
					Name:        skill.Name,
					Description: skill.Description,
					Source:      skill.Directory,
					Digest:      contentDigest(skill.Content),
				})
			}
		}
	}
	sort.Slice(skillDefinitions, func(i, j int) bool { return skillDefinitions[i].Name < skillDefinitions[j].Name })

	commands := append([]slashcommands.Command(nil), local.Commands...)
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })

	systemPromptPath, systemPromptContent, err := loadSystemPrompt(config, local.WorkingDirectory)
	if err != nil {
		return runnerpayload.Manifest{}, err
	}
	manifest := runnerpayload.Manifest{
		ProtocolVersion:  protocol.Version,
		RunnerID:         runnerID,
		RunID:            runID,
		Generation:       generation,
		WorkingDirectory: local.WorkingDirectory,
		ContextFiles:     contexts,
		Tools:            wireTools,
		Skills:           skillDefinitions,
		Commands:         commands,
		Config: runnerpayload.EnvironmentConfig{
			AllowedCommands:     append([]string(nil), config.AllowedCommands...),
			ToolMode:            config.ToolMode,
			EnableFSSearchTools: config.EnableFSSearchTools,
			SystemPromptPath:    systemPromptPath,
			SystemPromptContent: systemPromptContent,
			SystemPromptArgs:    maps.Clone(config.SyspromptArgs),
		},
		ExtensionGeneration: 1,
		Capabilities: runnerpayload.EnvironmentCapabilities{
			ToolUpdates:        true,
			InteractiveUI:      true,
			PersistentSurfaces: true,
			Commands:           true,
		},
	}
	digest, err := runnerpayload.ComputeManifestDigest(manifest)
	if err != nil {
		return runnerpayload.Manifest{}, err
	}
	manifest.Digest = digest
	return manifest, nil
}

func runtimeToolByName(runtime *extensions.Runtime, name string) tooltypes.Tool {
	if runtime == nil {
		return nil
	}
	for _, tool := range runtime.Tools() {
		if tool != nil && tool.Name() == name {
			return tool
		}
	}
	return nil
}

func contentDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func loadSystemPrompt(config llmtypes.Config, workingDirectory string) (string, string, error) {
	path := strings.TrimSpace(config.Sysprompt)
	if path == "" {
		return "", "", nil
	}
	if strings.ContainsRune(path, '\x00') {
		return "", "", errors.New("custom system prompt path contains a null byte")
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", errors.Wrap(err, "failed to resolve custom system prompt home directory")
		}
		switch {
		case path == "~":
			path = home
		case strings.HasPrefix(path, "~/"):
			path = filepath.Join(home, path[2:])
		default:
			return "", "", errors.Errorf("unsupported custom system prompt path %s", path)
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDirectory, path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to stat custom system prompt")
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("custom system prompt must be a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to read custom system prompt")
	}
	if !utf8.Valid(content) {
		return "", "", errors.New("custom system prompt is not valid UTF-8")
	}
	return path, string(content), nil
}

func cloneJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		switch typed := item.(type) {
		case map[string]any:
			result[key] = cloneJSONMap(typed)
		case []any:
			items := make([]any, len(typed))
			for i, child := range typed {
				if nested, ok := child.(map[string]any); ok {
					items[i] = cloneJSONMap(nested)
				} else {
					items[i] = child
				}
			}
			result[key] = items
		default:
			result[key] = item
		}
	}
	return result
}
