package extensions

import (
	"os"
	"path/filepath"
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

func TestDefaultRootsIncludeStandaloneAndPluginRoots(t *testing.T) {
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
	assert.Equal(t, Root{Dir: filepath.Join(".kodelet", "plugins", "org@repo", "extensions"), Kind: SourceKindLocalPlugin, PluginPrefix: "org@repo"}, discovery.roots[1])
	assert.Equal(t, Root{Dir: "~/custom-extensions", Kind: SourceKindGlobalStandalone}, discovery.roots[2])
	assert.Equal(t, Root{Dir: globalPluginExt, Kind: SourceKindGlobalPlugin, PluginPrefix: "global@plugin"}, discovery.roots[3])
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
