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

func writeExecutable(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return normalizePath(path, "")
}
