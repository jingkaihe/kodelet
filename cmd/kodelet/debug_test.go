package main

import (
	"net"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setDebugConfigForTest sets the debug section in the config layer, below env and flags.
func setDebugConfigForTest(t *testing.T, enabled bool, addr string) {
	t.Helper()
	t.Setenv("KODELET_DEBUG_ENABLED", "")
	t.Setenv("KODELET_DEBUG_ADDR", "")
	previous := map[string]any{"enabled": viper.GetBool("debug.enabled"), "addr": viper.GetString("debug.addr")}
	require.NoError(t, viper.MergeConfigMap(map[string]any{"debug": map[string]any{"enabled": enabled, "addr": addr}}))
	t.Cleanup(func() {
		require.NoError(t, viper.MergeConfigMap(map[string]any{"debug": previous}))
	})
}

func TestResolveDebugConfig(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		addr    string
		args    []string
		want    debugConfig
	}{
		{name: "disabled by default", addr: "127.0.0.1:7070"},
		{name: "enabled uses the default address", enabled: true, want: debugConfig{Address: defaultDebugAddr}},
		{name: "enabled with an address", enabled: true, addr: "0.0.0.0:7070", want: debugConfig{Address: "0.0.0.0:7070"}},
		{name: "flag overrides config", enabled: true, addr: "127.0.0.1:7070", args: []string{"--debug-addr=127.0.0.1:9090"}, want: debugConfig{Address: "127.0.0.1:9090", Explicit: true}},
		{name: "empty flag disables", enabled: true, args: []string{"--debug-addr="}, want: debugConfig{Explicit: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			setDebugConfigForTest(t, test.enabled, test.addr)
			cmd := &cobra.Command{Use: "test"}
			addDebugFlags(cmd)
			require.NoError(t, cmd.ParseFlags(test.args))
			assert.Equal(t, test.want, resolveDebugConfig(cmd))
		})
	}
}

func TestStartDebugListener(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()
	taken := occupied.Addr().String()

	listener, err := startDebugListener(t.Context(), debugConfig{Address: taken})
	require.NoError(t, err, "a taken configured port only disables profiling")
	assert.Nil(t, listener.server)
	assert.Error(t, listener.bindErr)

	_, err = startDebugListener(t.Context(), debugConfig{Address: taken, Explicit: true})
	assert.ErrorContains(t, err, "address already in use")

	_, err = startDebugListener(t.Context(), debugConfig{Address: ":6060"})
	assert.ErrorContains(t, err, "has no host")
}
