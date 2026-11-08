package client

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/codemode"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/skills"
	"github.com/jingkaihe/kodelet/pkg/tools"
	"github.com/pkg/errors"
)

// workspaceInventory lists what a run in the workspace would load. It opens the
// environment exactly as command discovery does, so allowed tools, skill
// settings and profiles resolve the same way they do for a run.
func (s *Service) workspaceInventory(ctx context.Context, params protocol.WorkspaceInventoryParams) (protocol.WorkspaceInventoryResult, error) {
	if s == nil {
		return protocol.WorkspaceInventoryResult{}, errors.New("runner service is required")
	}
	if err := params.Validate(); err != nil {
		return protocol.WorkspaceInventoryResult{}, err
	}
	if err := s.lockSnapshot(ctx); err != nil {
		return protocol.WorkspaceInventoryResult{}, err
	}
	defer s.unlockSnapshot()
	var result protocol.WorkspaceInventoryResult
	// Background-only tools are part of what a run loads, so list them too.
	err := s.probeEnvironmentLocked(ctx, params.CWD, params.Profile, params.EnvironmentProfile, params.Options, true, func(probe environmentProbe) error {
		discovered := map[string]*skills.Skill{}
		discovery, err := skills.NewDiscovery(skills.WithDefaultDirsForCWD(probe.manifest.WorkingDirectory))
		if err == nil {
			discovered, err = discovery.DiscoverSkills()
		}
		if err != nil {
			// Skills are optional; list tools and extensions without them.
			logger.G(ctx).WithError(err).Warn("failed to discover skills for the tool inventory")
		}
		homeDir, _ := os.UserHomeDir()
		result = buildWorkspaceInventory(probe.manifest, probe.runtime.ExtensionStatuses(), discovered, homeDir)
		return nil
	})
	if err != nil {
		return protocol.WorkspaceInventoryResult{}, err
	}
	result.EnvironmentProfile = normalizeEnvironmentProfile(params.EnvironmentProfile)
	return result, nil
}

// buildWorkspaceInventory groups the probed environment into built-in tools,
// plugins, and extensions and skills installed outside a plugin. Items the
// environment excludes are listed with Enabled false.
func buildWorkspaceInventory(
	manifest agentenv.Manifest,
	statuses []extensions.ExtensionStatus,
	discoveredSkills map[string]*skills.Skill,
	homeDir string,
) protocol.WorkspaceInventoryResult {
	cwd := manifest.WorkingDirectory
	result := protocol.WorkspaceInventoryResult{
		CWD:          cwd,
		BuiltinTools: []protocol.InventoryTool{},
		Plugins:      []protocol.InventoryPlugin{},
		Extensions:   []protocol.InventoryExtension{},
		Skills:       []protocol.InventorySkill{},
	}

	available := make(map[string]bool, len(manifest.Tools))
	enabledSkills := map[string]bool{}
	for _, definition := range manifest.Tools {
		available[definition.Name] = true
		if skillTool, ok := definition.Tool.(*tools.SkillTool); ok {
			for name := range skillTool.GetSkills() {
				enabledSkills[name] = true
			}
		}
	}
	extensionTools := map[string]bool{}
	for _, status := range statuses {
		for _, tool := range status.Tools {
			extensionTools[tool.Name] = true
		}
	}

	builtins := map[string]bool{}
	for _, definition := range manifest.Tools {
		if extensionTools[definition.Name] || builtins[definition.Name] {
			continue
		}
		builtins[definition.Name] = true
		result.BuiltinTools = append(result.BuiltinTools, protocol.InventoryTool{
			Name:        definition.Name,
			Description: toolSummary(definition.Name, definition.Description, definition.Short),
			Enabled:     true,
		})
	}
	for _, tool := range tools.GetToolsFromNames(tools.BuiltinToolNames()) {
		if builtins[tool.Name()] {
			continue
		}
		builtins[tool.Name()] = true
		result.BuiltinTools = append(result.BuiltinTools, protocol.InventoryTool{
			Name:        tool.Name(),
			Description: toolSummary(tool.Name(), tool.Description(), ""),
		})
	}
	sort.Slice(result.BuiltinTools, func(i, j int) bool { return result.BuiltinTools[i].Name < result.BuiltinTools[j].Name })

	plugins := map[string]*protocol.InventoryPlugin{}
	plugin := func(name, scope, path string) *protocol.InventoryPlugin {
		key := scope + "\x00" + name
		if existing, ok := plugins[key]; ok {
			return existing
		}
		created := &protocol.InventoryPlugin{
			Name:       name,
			Scope:      scope,
			Path:       path,
			Extensions: []protocol.InventoryExtension{},
			Skills:     []protocol.InventorySkill{},
		}
		plugins[key] = created
		return created
	}

	for _, status := range statuses {
		item := inventoryExtension(status, available)
		if prefix := status.Extension.PluginPrefix; prefix != "" {
			owner := plugin(prefix, item.Scope, filepath.Dir(status.Extension.RootDir))
			owner.Extensions = append(owner.Extensions, item)
			continue
		}
		result.Extensions = append(result.Extensions, item)
	}

	for name, skill := range discoveredSkills {
		if skill == nil {
			continue
		}
		item := protocol.InventorySkill{
			Name:        name,
			Description: toolSummary(name, skill.Description, ""),
			Scope:       inventoryScope(skill.Directory, cwd, homeDir),
			Path:        skill.Directory,
			Enabled:     enabledSkills[name],
		}
		// Plugin skills are named "<plugin>/<skill>".
		if pluginName, skillName, ok := strings.Cut(name, "/"); ok {
			item.Name = skillName
			owner := plugin(pluginName, item.Scope, filepath.Dir(filepath.Dir(skill.Directory)))
			owner.Skills = append(owner.Skills, item)
			continue
		}
		result.Skills = append(result.Skills, item)
	}

	for _, owner := range plugins {
		sortInventoryExtensions(owner.Extensions)
		sortInventorySkills(owner.Skills)
		result.Plugins = append(result.Plugins, *owner)
	}
	sort.Slice(result.Plugins, func(i, j int) bool {
		if result.Plugins[i].Name != result.Plugins[j].Name {
			return result.Plugins[i].Name < result.Plugins[j].Name
		}
		return result.Plugins[i].Scope < result.Plugins[j].Scope
	})
	sortInventoryExtensions(result.Extensions)
	sortInventorySkills(result.Skills)
	return result
}

func inventoryExtension(status extensions.ExtensionStatus, available map[string]bool) protocol.InventoryExtension {
	ext := status.Extension
	path := ext.ExecPath
	if ext.Dir != "" && ext.Dir != ext.RootDir {
		path = ext.Dir
	}
	item := protocol.InventoryExtension{
		ID:        ext.ID,
		Name:      ext.Name,
		Version:   status.Version,
		Scope:     extensionScope(ext.Kind),
		Path:      path,
		Status:    protocol.InventoryStatusReady,
		Tools:     []protocol.InventoryTool{},
		Shortcuts: status.Shortcuts,
		Hooks:     status.Hooks,
	}
	if status.Err != nil {
		item.Status = protocol.InventoryStatusFailed
		item.Error = status.Err.Error()
	}
	for _, command := range status.Commands {
		item.Commands = append(item.Commands, "/"+command)
	}
	for _, tool := range status.Tools {
		item.Tools = append(item.Tools, protocol.InventoryTool{
			Name:        tool.Name,
			Description: toolSummary(tool.Name, tool.Description, ""),
			Enabled:     tool.Enabled && available[tool.Name],
		})
	}
	sort.Slice(item.Tools, func(i, j int) bool { return item.Tools[i].Name < item.Tools[j].Name })
	return item
}

func extensionScope(kind extensions.SourceKind) string {
	switch kind {
	case extensions.SourceKindLocalStandalone, extensions.SourceKindLocalPlugin:
		return protocol.InventoryScopeRepo
	case extensions.SourceKindGlobalStandalone, extensions.SourceKindGlobalPlugin:
		return protocol.InventoryScopeGlobal
	case extensions.SourceKindSession:
		return protocol.InventoryScopeSession
	default:
		return protocol.InventoryScopePath
	}
}

// inventoryScope places a skill by the directory root it was discovered under.
// A workspace at the home directory shares its roots with global installs.
func inventoryScope(path, cwd, homeDir string) string {
	within := func(base string) bool {
		if base == "" {
			return false
		}
		for _, root := range []string{filepath.Join(base, ".kodelet"), filepath.Join(base, ".agents")} {
			rel, err := filepath.Rel(root, path)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	if within(cwd) && filepath.Clean(cwd) != filepath.Clean(homeDir) {
		return protocol.InventoryScopeRepo
	}
	if within(homeDir) {
		return protocol.InventoryScopeGlobal
	}
	return protocol.InventoryScopePath
}

func toolSummary(name, description, short string) string {
	return codemode.Summary(codemode.Definition{Name: name, Description: description, Short: short})
}

func sortInventoryExtensions(items []protocol.InventoryExtension) {
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
}

func sortInventorySkills(items []protocol.InventorySkill) {
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
}
