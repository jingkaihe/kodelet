package extensions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryDiscoversDirectAndNestedExecutables(t *testing.T) {
	rootDir := t.TempDir()
	direct := writeExecutable(t, filepath.Join(rootDir, "kodelet-extension-weather"), "#!/bin/sh\nexit 0\n")
	nestedDir := filepath.Join(rootDir, "security")
	nested := writeExecutable(t, filepath.Join(nestedDir, "kodelet-extension-guardrails"), "#!/bin/sh\nexit 0\n")
	require.NoError(t, os.WriteFile(filepath.Join(rootDir, "kodelet-extension-disabled"), []byte("#!/bin/sh\nexit 0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(rootDir, "not-an-extension"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	discovery, err := NewDiscovery(
		WithConfig(DefaultConfig()),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)

	discovered, err := discovery.Discover()
	require.NoError(t, err)
	require.Len(t, discovered, 2)

	assert.Equal(t, "security", discovered[0].ID)
	assert.Equal(t, "security", discovered[0].Name)
	assert.Equal(t, nested, discovered[0].ExecPath)
	assert.Equal(t, nestedDir, discovered[0].Dir)

	assert.Equal(t, "weather", discovered[1].ID)
	assert.Equal(t, "weather", discovered[1].Name)
	assert.Equal(t, direct, discovered[1].ExecPath)
	assert.Equal(t, rootDir, discovered[1].Dir)
}

func TestDiscoveryUsesRootPrecedenceForDuplicateIDs(t *testing.T) {
	localRoot := t.TempDir()
	globalRoot := t.TempDir()
	local := writeExecutable(t, filepath.Join(localRoot, "kodelet-extension-weather"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(globalRoot, "kodelet-extension-weather"), "#!/bin/sh\nexit 0\n")

	discovery, err := NewDiscovery(
		WithConfig(DefaultConfig()),
		WithRoots(
			Root{Dir: localRoot, Kind: SourceKindLocalStandalone},
			Root{Dir: globalRoot, Kind: SourceKindGlobalStandalone},
		),
	)
	require.NoError(t, err)

	discovered, err := discovery.Discover()
	require.NoError(t, err)
	require.Len(t, discovered, 1)
	assert.Equal(t, local, discovered[0].ExecPath)
}

func TestDiscoveryAllowDenyByPath(t *testing.T) {
	workingDir := t.TempDir()
	rootDir := filepath.Join(workingDir, ".kodelet", "extensions")
	weather := writeExecutable(t, filepath.Join(rootDir, "weather", "kodelet-extension-weather"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(rootDir, "danger", "kodelet-extension-danger"), "#!/bin/sh\nexit 0\n")

	config := DefaultConfig()
	config.Allow = []string{"./.kodelet/extensions/weather"}
	config.Deny = []string{weather}
	discovery, err := NewDiscovery(
		WithConfig(config),
		WithWorkingDir(workingDir),
		WithRoots(Root{Dir: "./.kodelet/extensions", Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)

	discovered, err := discovery.Discover()
	require.NoError(t, err)
	assert.Empty(t, discovered, "deny should win over allow when both match")
}

func TestDiscoveryAllowDenyByPluginRef(t *testing.T) {
	rootDir := t.TempDir()
	writeExecutable(t, filepath.Join(rootDir, "weather", "kodelet-extension-weather"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(rootDir, "security", "kodelet-extension-guardrails"), "#!/bin/sh\nexit 0\n")

	config := DefaultConfig()
	config.Allow = []string{"org@repo/weather", "org@repo/security"}
	config.Deny = []string{"org@repo/security"}
	discovery, err := NewDiscovery(
		WithConfig(config),
		WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalPlugin, PluginPrefix: "org@repo"}),
	)
	require.NoError(t, err)

	discovered, err := discovery.Discover()
	require.NoError(t, err)
	require.Len(t, discovered, 1)
	assert.Equal(t, "org@repo/weather", discovered[0].ID)
	assert.Equal(t, "org@repo/weather", discovered[0].PluginRef)
}

func TestDiscoveryDisabledConfigSkipsDiscovery(t *testing.T) {
	rootDir := t.TempDir()
	writeExecutable(t, filepath.Join(rootDir, "kodelet-extension-weather"), "#!/bin/sh\nexit 0\n")

	config := DefaultConfig()
	config.Enabled = false
	discovery, err := NewDiscovery(WithConfig(config), WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}))
	require.NoError(t, err)

	discovered, err := discovery.Discover()
	require.NoError(t, err)
	assert.Empty(t, discovered)
}

func TestDiscoveryPATHFiltersEntriesWithoutExecuting(t *testing.T) {
	workingDir := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "bin with spaces ")
	t.Chdir(workingDir)
	t.Setenv("HOME", t.TempDir())
	marker := filepath.Join(workingDir, "executed")
	script := fmt.Sprintf(`#!/bin/sh
echo executed > %q
`, marker)
	target := writeExecutable(t, filepath.Join(workingDir, "weather"), script)
	link := filepath.Join(binDir, "kodelet-extension-weather")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	require.NoError(t, os.Symlink(target, link))
	require.NoError(t, os.Symlink(filepath.Join(workingDir, "missing"), filepath.Join(binDir, "kodelet-extension-broken")))
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "kodelet-extension-disabled"), []byte(script), 0o644))
	for _, name := range []string{"kodelet-extension-", "kodelet-extension-node", "other-command"} {
		writeExecutable(t, filepath.Join(binDir, name), script)
	}
	writeExecutable(t, filepath.Join(binDir, "nested", "kodelet-extension-nested"), script)
	writeExecutable(t, filepath.Join(workingDir, "kodelet-extension-cwd"), script)
	writeExecutable(t, filepath.Join(workingDir, "relative", "kodelet-extension-relative"), script)
	t.Setenv("PATH", strings.Join([]string{
		"", ".", "relative", filepath.Join(workingDir, "missing"), target, binDir, binDir,
	}, string(os.PathListSeparator)))

	discovery, err := NewDiscovery(WithWorkingDir(workingDir))
	require.NoError(t, err)
	discovered, err := discovery.Discover()
	require.NoError(t, err)
	require.Len(t, discovered, 1)
	assert.Equal(t, "weather", discovered[0].ID)
	assert.Equal(t, SourceKindPath, discovered[0].Kind)
	assert.Equal(t, normalizePath(link, ""), discovered[0].ExecPath)
	assert.Equal(t, filepath.Dir(discovered[0].ExecPath), discovered[0].Dir)
	assert.NoFileExists(t, marker)
}

func TestDiscoveryPATHPrecedenceAndConfig(t *testing.T) {
	workingDir := t.TempDir()
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", firstDir+string(os.PathListSeparator)+secondDir)
	const script = `#!/bin/sh
exit 0
`
	local := writeExecutable(t, filepath.Join(workingDir, ".kodelet", "extensions", "kodelet-extension-local"), script)
	writeExecutable(t, filepath.Join(firstDir, "kodelet-extension-local"), script)
	first := writeExecutable(t, filepath.Join(firstDir, "kodelet-extension-weather"), script)
	second := writeExecutable(t, filepath.Join(secondDir, "kodelet-extension-weather"), script)

	for _, tc := range []struct {
		name        string
		disablePath bool
		disabled    bool
		allow       []string
		deny        []string
		want        []string
	}{
		{name: "default root and PATH order", want: []string{local, first}},
		{name: "PATH disabled", disablePath: true, want: []string{local}},
		{name: "extensions disabled", disabled: true},
		{name: "deny does not select shadowed executable", deny: []string{first}, want: []string{local}},
		{name: "allow does not select shadowed executable", allow: []string{second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultConfig()
			if tc.disablePath {
				config.DiscoverPath = false
			}
			config.Enabled = !tc.disabled
			config.Allow = tc.allow
			config.Deny = tc.deny
			discovery, err := NewDiscovery(WithConfig(config), WithWorkingDir(workingDir))
			require.NoError(t, err)
			discovered, err := discovery.Discover()
			require.NoError(t, err)
			var paths []string
			for _, ext := range discovered {
				paths = append(paths, ext.ExecPath)
			}
			assert.Equal(t, tc.want, paths)
		})
	}
}

func TestDiscoveryNativeMCPReplacesOnlyEligibleLegacyPlugin(t *testing.T) {
	for _, tc := range []struct {
		name        string
		missing     bool
		denied      bool
		allowLegacy bool
		disablePath bool
		shadowed    bool
		localLegacy bool
		want        []string
	}{
		{name: "native replaces legacy, not other plugins", want: []string{"other@mcp/mcp", "mcp"}},
		{name: "native replaces workspace legacy", localLegacy: true, want: []string{"other@mcp/mcp", "mcp"}},
		{name: "native absent", missing: true, want: []string{"kodelet@mcp/mcp", "other@mcp/mcp"}},
		{name: "native denied", denied: true, want: []string{"kodelet@mcp/mcp", "other@mcp/mcp"}},
		{name: "only legacy allowed", allowLegacy: true, want: []string{"kodelet@mcp/mcp"}},
		{name: "PATH discovery disabled", disablePath: true, want: []string{"kodelet@mcp/mcp", "other@mcp/mcp"}},
		{name: "native shadowed by local extension", shadowed: true, want: []string{"mcp", "kodelet@mcp/mcp", "other@mcp/mcp"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace, home, binDir := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", binDir)
			const script = `#!/bin/sh
exit 0
`
			legacyHome := home
			if tc.localLegacy {
				legacyHome = workspace
			}
			legacy := writeExecutable(t, filepath.Join(legacyHome, ".kodelet", "plugins", "kodelet@mcp", "extensions", "mcp", "kodelet-extension-mcp"), script)
			writeExecutable(t, filepath.Join(home, ".kodelet", "plugins", "other@mcp", "extensions", "mcp", "kodelet-extension-mcp"), script)
			native := filepath.Join(binDir, "kodelet-extension-mcp")
			if !tc.missing {
				writeExecutable(t, native, script)
			}
			if tc.shadowed {
				writeExecutable(t, filepath.Join(workspace, ".kodelet", "extensions", "kodelet-extension-mcp"), script)
			}
			config := DefaultConfig()
			config.DiscoverPath = !tc.disablePath
			if tc.denied {
				config.Deny = []string{native}
			}
			if tc.allowLegacy {
				config.Allow = []string{"kodelet@mcp/mcp"}
			}
			discovery, err := NewDiscovery(WithConfig(config), WithWorkingDir(workspace))
			require.NoError(t, err)
			discovered, err := discovery.Discover()
			require.NoError(t, err)
			var ids []string
			for _, ext := range discovered {
				ids = append(ids, ext.ID)
			}
			assert.Equal(t, tc.want, ids)
			assert.FileExists(t, legacy, "discovery never deletes the old plugin")
		})
	}
}

func TestDefaultRootsIncludeStandaloneAndPluginRoots(t *testing.T) {
	t.Setenv("PATH", "")
	workingDir := t.TempDir()
	originalWD, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(originalWD)) })
	require.NoError(t, os.Chdir(workingDir))

	localPluginExt := filepath.Join(workingDir, ".kodelet", "plugins", "org@repo", "extensions")
	require.NoError(t, os.MkdirAll(localPluginExt, 0o755))
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	globalPluginExt := filepath.Join(homeDir, ".kodelet", "plugins", "global@plugin", "extensions")
	require.NoError(t, os.MkdirAll(globalPluginExt, 0o755))

	config := DefaultConfig()
	config.LocalDir = ".kodelet/extensions"
	config.GlobalDir = "~/custom-extensions"
	discovery, err := NewDiscovery(WithConfig(config), WithWorkingDir(workingDir))

	require.NoError(t, err)
	require.Len(t, discovery.roots, 4)
	assert.Equal(t, Root{Dir: ".kodelet/extensions", Kind: SourceKindLocalStandalone}, discovery.roots[0])
	assert.Equal(t, Root{Dir: localPluginExt, Kind: SourceKindLocalPlugin, PluginPrefix: "org@repo"}, discovery.roots[1])
	assert.Equal(t, Root{Dir: "~/custom-extensions", Kind: SourceKindGlobalStandalone}, discovery.roots[2])
	assert.Equal(t, Root{Dir: globalPluginExt, Kind: SourceKindGlobalPlugin, PluginPrefix: "global@plugin"}, discovery.roots[3])
}

func TestDefaultRootsResolveLocalPluginsAgainstWorkingDir(t *testing.T) {
	t.Setenv("PATH", "")
	processDir := t.TempDir()
	workingDir := t.TempDir()
	originalWD, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(originalWD)) })
	require.NoError(t, os.Chdir(processDir))

	processPluginExt := filepath.Join(processDir, ".kodelet", "plugins", "wrong@repo", "extensions")
	require.NoError(t, os.MkdirAll(processPluginExt, 0o755))
	workingPluginExt := filepath.Join(workingDir, ".kodelet", "plugins", "right@repo", "extensions")
	extensionPath := writeExecutable(t, filepath.Join(workingPluginExt, "workspace", "kodelet-extension-workspace"), "#!/bin/sh\nexit 0\n")

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	config := DefaultConfig()
	config.LocalDir = filepath.Join(workingDir, ".kodelet", "extensions")
	config.GlobalDir = filepath.Join(homeDir, ".kodelet", "extensions")

	discovery, err := NewDiscovery(WithConfig(config), WithWorkingDir(workingDir))
	require.NoError(t, err)

	discovered, err := discovery.Discover()
	require.NoError(t, err)
	require.Len(t, discovered, 1)
	assert.Equal(t, "right@repo/workspace", discovered[0].ID)
	assert.Equal(t, extensionPath, discovered[0].ExecPath)
}

func TestDiscoveryAllowsRelativeExecutablePathAndDirectoryPath(t *testing.T) {
	workingDir := t.TempDir()
	rootDir := filepath.Join(workingDir, ".kodelet", "extensions")
	direct := writeExecutable(t, filepath.Join(rootDir, "kodelet-extension-weather"), "#!/bin/sh\nexit 0\n")
	nestedDir := filepath.Join(rootDir, "security")
	writeExecutable(t, filepath.Join(nestedDir, "kodelet-extension-guardrails"), "#!/bin/sh\nexit 0\n")

	config := DefaultConfig()
	config.Allow = []string{
		"./.kodelet/extensions/kodelet-extension-weather",
		"./.kodelet/extensions/security",
	}
	discovery, err := NewDiscovery(
		WithConfig(config),
		WithWorkingDir(workingDir),
		WithRoots(Root{Dir: "./.kodelet/extensions", Kind: SourceKindLocalStandalone}),
	)
	require.NoError(t, err)

	discovered, err := discovery.Discover()
	require.NoError(t, err)
	require.Len(t, discovered, 2)
	assert.Equal(t, "security", discovered[0].ID)
	assert.Equal(t, "weather", discovered[1].ID)
	assert.Equal(t, direct, discovered[1].ExecPath)
}

func TestDiscoverRootIgnoresNestedDirectoryWithoutExecutable(t *testing.T) {
	rootDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(rootDir, "empty"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rootDir, "empty", "kodelet-extension-empty"), []byte("#!/bin/sh\nexit 0\n"), 0o644))

	discovery, err := NewDiscovery(WithRoots(Root{Dir: rootDir, Kind: SourceKindLocalStandalone}))
	require.NoError(t, err)

	discovered, err := discovery.Discover()
	require.NoError(t, err)
	assert.Empty(t, discovered)
}

func TestNormalizePathExpandsHomeAndRelativePaths(t *testing.T) {
	homeDir := t.TempDir()
	workingDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	assert.Equal(t, normalizePath(filepath.Join(homeDir, "extensions"), ""), normalizePath("~/extensions", workingDir))
	assert.Equal(t, normalizePath(filepath.Join(workingDir, "relative", "extension"), ""), normalizePath("relative/extension", workingDir))
}

func TestFileInfoDirEntryAccessors(t *testing.T) {
	path := writeExecutable(t, filepath.Join(t.TempDir(), "kodelet-extension-weather"), "#!/bin/sh\nexit 0\n")
	info, err := os.Stat(path)
	require.NoError(t, err)
	entry := fileInfoDirEntry{info: info}

	assert.Equal(t, "kodelet-extension-weather", entry.Name())
	assert.False(t, entry.IsDir())
	assert.Equal(t, info.Mode().Type(), entry.Type())
	gotInfo, err := entry.Info()
	require.NoError(t, err)
	assert.Equal(t, info.Name(), gotInfo.Name())
}

func writeExecutable(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return normalizePath(path, "")
}
