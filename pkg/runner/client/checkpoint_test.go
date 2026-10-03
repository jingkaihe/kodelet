package client

import (
	"context"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerCheckpointPrecedesExtensionEffects(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	for _, name := range []string{"success", "save failure", "no peer", "invalid cwd", "invalid policy", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			provider := &recordingRuntimeProvider{}
			checkpoints, environments := 0, 0
			service := newRegisteredTestService(t, workspace, ServiceOptions{
				RuntimeProvider: provider,
				ConfigLoader: func(string) (llmtypes.Config, error) {
					if name == "invalid policy" {
						return llmtypes.Config{}, errors.New("invalid environment policy")
					}
					return llmtypes.Config{ExtensionSettings: map[string]any{"enabled": true}}, nil
				},
				EnvironmentFactory: func(cwd string, runtime *extensions.Runtime) agentenv.Environment {
					environments++
					assert.Equal(t, 1, checkpoints)
					return agentenv.NewLocalEnvironment(cwd, runtime)
				},
			})
			callService[protocol.WorkspaceTerminalOpenResult](t, service, protocol.MethodWorkspaceTerminalOpen, protocol.WorkspaceTerminalOpenParams{
				ConversationID: "conversation", Draft: true,
			})
			service.mu.Lock()
			deadline := service.workspaceSessionLeases["conversation"]
			service.mu.Unlock()
			require.False(t, deadline.IsZero())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if name == "no peer" {
				service.Attach(nil)
			} else {
				service.Attach(&modelHelperPeer{call: func(_ context.Context, method string, params, _ any) error {
					// A slow opening first turn pins, rather than promotes, the
					// draft until the durable checkpoint succeeds.
					require.NoError(t, service.reapWorkspaceSessions(deadline.Add(time.Second)))
					service.mu.Lock()
					assert.Equal(t, deadline, service.workspaceSessionLeases["conversation"])
					assert.NotContains(t, service.discardedWorkspaceSessions, "conversation")
					service.mu.Unlock()
					checkpoints++
					assert.Equal(t, protocol.MethodRunCheckpoint, method)
					assert.Equal(t, protocol.RunCheckpointParams{RunID: "run", CWD: workspace}, params)
					assert.Zero(t, provider.activeCalls)
					assert.Zero(t, environments)
					if name == "save failure" {
						return errors.New("checkpoint rejected")
					}
					if name == "cancelled" {
						cancel()
					}
					return nil
				}})
			}
			params := protocol.RunOpenParams{RunID: "run", ConversationID: "conversation", RequireCheckpoint: true}
			if name == "invalid cwd" {
				params.CWD = workspace + "/absent"
			}
			_, err := service.openRun(ctx, params)
			if name == "success" {
				require.NoError(t, err)
				assert.Equal(t, 1, environments)
			} else {
				require.Error(t, err)
				assert.Zero(t, environments)
				assert.Zero(t, provider.activeCalls)
				_, ids, _ := service.HeartbeatSnapshotRuns()
				assert.Empty(t, ids)
			}
			service.mu.Lock()
			promoted := service.workspaceSessionLeases["conversation"].IsZero()
			service.mu.Unlock()
			assert.Equal(t, name == "success" || name == "cancelled", promoted, "a successful checkpoint persists the conversation even if later work is cancelled")
			require.NoError(t, service.reapWorkspaceSessions(deadline.Add(time.Second)))
			service.mu.Lock()
			_, retained := service.workspaceSessionLeases["conversation"]
			service.mu.Unlock()
			assert.Equal(t, promoted, retained, "unpublished failed startup must still expire")
		})
	}
}
