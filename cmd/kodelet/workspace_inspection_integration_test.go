package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/jingkaihe/kodelet/pkg/webui"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise public CLI -> HTTP -> runner RPC on both placements. The client has
// poisoned recipes/extensions and no usable conversation store or provider key.
func TestWorkspaceInspectionAcrossRunnerPlacements(t *testing.T) {
	for _, placement := range []string{"embedded", "standalone"} {
		t.Run(placement, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
			defer cancel()
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("KODELET_BASE_PATH", filepath.Join(root, "server-state"))
			startup, workspace := filepath.Join(root, "startup"), filepath.Join(root, "selected")
			require.NoError(t, os.MkdirAll(startup, 0o700))
			require.NoError(t, os.MkdirAll(filepath.Join(workspace, ".kodelet", "recipes"), 0o700))
			require.NoError(t, os.MkdirAll(filepath.Join(workspace, ".kodelet", "extensions"), 0o700))
			templateMarker := filepath.Join(workspace, "template-ran")
			recipe := "---\nname: Runner recipe\ndescription: Selected workspace only\narguments:\n  subject:\n    default: world\n---\nHello {{.subject}}! {{bash \"/bin/sh\" \"-c\" \"pwd; touch template-ran\"}}"
			require.NoError(t, os.WriteFile(filepath.Join(workspace, ".kodelet", "recipes", "selected.md"), []byte(recipe), 0o600))
			executable, err := os.Executable()
			require.NoError(t, err)
			// The extension fixture records startup and advertises a dynamic recipe.
			script := fmt.Sprintf("#!/bin/sh\nKODELET_TEST_CHAT_EXTENSION=1 KODELET_TEST_INSPECTION_RECIPES=1 exec %q -test.run '^TestDaemonChatExtensionProcess$'\n", executable)
			extensionPath := filepath.Join(workspace, ".kodelet", "extensions", "kodelet-extension-pty")
			require.NoError(t, os.WriteFile(extensionPath, []byte(script), 0o700))
			extensionMarker := filepath.Join(workspace, "pty-initializations.jsonl")
			original := viper.AllSettings()
			viper.Reset()
			t.Cleanup(func() {
				viper.Reset()
				for k, v := range original {
					viper.Set(k, v)
				}
			})
			viper.Set("provider", "openai")
			viper.Set("model", "gpt-4o")
			viper.Set("extensions.enabled", true)
			viper.Set("skills.enabled", false)
			require.NoError(t, db.RunMigrations(ctx, migrations.All()))
			config := &controlplane.ServerConfig{Host: "127.0.0.1", AuthToken: "client-secret", RunnerAuthToken: "runner-secret", CompactRatio: 0.8}
			if placement == "embedded" {
				store, err := localstate.NewStore()
				require.NoError(t, err)
				config.EmbeddedRunner = &controlplane.EmbeddedRunnerConfig{Workspace: startup, Store: store}
			}
			frontend, err := webui.NewHandler()
			require.NoError(t, err)
			server, err := controlplane.NewServer(ctx, config, frontend)
			require.NoError(t, err)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			endpoint := "http://" + listener.Addr().String()
			serverCtx, stopServer := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- server.Serve(serverCtx, listener) }()
			t.Cleanup(func() {
				stopServer()
				select {
				case err := <-done:
					assert.NoError(t, err)
				case <-time.After(10 * time.Second):
					assert.Fail(t, "server did not stop")
				}
				assert.NoError(t, server.Close())
			})
			if placement == "standalone" {
				runnerCtx, stopRunner := context.WithCancel(ctx)
				env := []string{"HOME=" + root, "PATH=" + os.Getenv("PATH"), "SHELL=/bin/sh", "KODELET_TEST_CLI_PROCESS=1", "KODELET_BASE_PATH=" + filepath.Join(root, "runner-state")}
				process := daemonCLIProcess(runnerCtx, t, startup, env, "runner", "start", "--server="+endpoint, "--auth-token=runner-secret")
				var output bytes.Buffer
				process.Stdout, process.Stderr = &output, &output
				require.NoError(t, process.Start())
				t.Cleanup(func() {
					stopRunner()
					_ = process.Wait()
					if t.Failed() {
						t.Log(output.String())
					}
				})
			}
			var runnerID string
			require.Eventually(t, func() bool {
				runners, _, err := fetchRunners(ctx, endpoint, "client-secret")
				if err != nil {
					return false
				}
				for _, runner := range runners {
					if runner.Connected && runner.Status == runnerregistry.RunnerStatusIdle {
						runnerID = runner.ID
						return true
					}
				}
				return false
			}, 10*time.Second, 20*time.Millisecond)
			fixture := newWorkspaceInspectionFixture(t)
			invoke := func(args ...string) []byte {
				t.Helper()
				args = append(args, "--server="+endpoint, "--auth-token=client-secret", "--runner="+runnerID[:12], "--cwd="+workspace)
				output, err := daemonCLIProcess(ctx, t, fixture.cwd, fixture.env, args...).CombinedOutput()
				require.NoError(t, err, "%s", output)
				assert.NoFileExists(t, fixture.extensionMarker)
				assert.NoFileExists(t, fixture.templateMarker)
				assert.NoDirExists(t, filepath.Join(fixture.home, ".kodelet"))
				return output
			}
			output := invoke("extension", "list", "--json")
			var listed struct {
				Extensions []ExtensionOutput `json:"extensions"`
			}
			require.NoError(t, json.Unmarshal(output, &listed))
			require.Len(t, listed.Extensions, 1)
			assert.Equal(t, extensionPath, listed.Extensions[0].Path)
			output = invoke("extension", "inspect", extensionPath, "--json")
			var inspected ExtensionOutput
			require.NoError(t, json.Unmarshal(output, &inspected))
			assert.Equal(t, extensionPath, inspected.Path)
			assert.NoFileExists(t, extensionMarker)
			assert.NoFileExists(t, templateMarker)
			output = invoke("recipe", "show", "selected", "--arg=subject=operator")
			assert.Contains(t, string(output), "Hello operator! "+workspace)
			assert.FileExists(t, templateMarker)
			assert.NoFileExists(t, extensionMarker)
			output = invoke("recipe", "list", "--json", "--show-path")
			assert.Contains(t, string(output), "Runner recipe")
			assert.Contains(t, string(output), filepath.Join(workspace, ".kodelet", "recipes", "selected.md"))
			assert.NotContains(t, string(output), "Poison recipe")
			assert.Contains(t, string(output), "Dynamic runner recipe")
			assert.Contains(t, string(output), "extension:pty/dynamic-review")
			assert.NotContains(t, string(output), "not-a-recipe")
			assert.FileExists(t, extensionMarker)
		})
	}
}
