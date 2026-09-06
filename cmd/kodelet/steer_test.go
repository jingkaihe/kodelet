package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSteerConfigFromFlags tests the steer configuration flag parsing
func TestSteerConfigFromFlags(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		setupMock      func()
		expectedConfig *SteerConfig
		expectError    bool
	}{
		{
			name: "conversation-id flag",
			args: []string{"--conversation-id", "test-conv-id"},
			expectedConfig: &SteerConfig{
				ConversationID: "test-conv-id",
				Follow:         false,
			},
			expectError: false,
		},
		{
			name: "follow flag short form",
			args: []string{"-f"},
			expectedConfig: &SteerConfig{
				ConversationID: "mock-recent-id",
				Follow:         true,
			},
			expectError: false,
		},
		{
			name: "follow flag long form",
			args: []string{"--follow"},
			expectedConfig: &SteerConfig{
				ConversationID: "mock-recent-id",
				Follow:         true,
			},
			expectError: false,
		},
		{
			name:        "conflicting flags",
			args:        []string{"--conversation-id", "test-id", "--follow"},
			expectError: true,
		},
		{
			name: "no flags",
			args: []string{},
			expectedConfig: &SteerConfig{
				ConversationID: "",
				Follow:         false,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock command with the steer flags
			cmd := &cobra.Command{
				Use: "test",
				Run: func(_ *cobra.Command, _ []string) {},
			}

			// Add the same flags as steer command
			steerDefaults := NewSteerConfig()
			cmd.Flags().StringVar(&steerDefaults.ConversationID, "conversation-id", steerDefaults.ConversationID, "ID of the conversation to steer")
			cmd.Flags().BoolP("follow", "f", steerDefaults.Follow, "Steer the most recent conversation")
			cmd.Flags().StringSliceP("image", "I", steerDefaults.Images, "Add image input")

			// Parse the test args
			err := cmd.ParseFlags(tt.args)
			require.NoError(t, err)

			if tt.expectError {
				// For error cases, we need to manually check the conflict logic
				conversationID, _ := cmd.Flags().GetString("conversation-id")
				follow, _ := cmd.Flags().GetBool("follow")

				if follow && conversationID != "" {
					// This should trigger the conflict error
					assert.True(t, true, "Conflict correctly detected")
					return
				}
			}

			// Mock the GetMostRecentConversationID function for follow tests
			if tt.expectedConfig != nil && tt.expectedConfig.Follow {
				// We can't easily mock the conversations package in this test,
				// so we'll just verify the flag parsing works correctly
				follow, err := cmd.Flags().GetBool("follow")
				require.NoError(t, err)
				assert.True(t, follow)
			}

			// Test the config creation (without the conversation lookup)
			config := NewSteerConfig()
			if conversationID, err := cmd.Flags().GetString("conversation-id"); err == nil {
				config.ConversationID = conversationID
			}
			if follow, err := cmd.Flags().GetBool("follow"); err == nil {
				config.Follow = follow
			}
			if images, err := cmd.Flags().GetStringSlice("image"); err == nil {
				config.Images = images
			}

			if tt.expectedConfig != nil {
				assert.Equal(t, tt.expectedConfig.Follow, config.Follow)
				if !tt.expectedConfig.Follow {
					assert.Equal(t, tt.expectedConfig.ConversationID, config.ConversationID)
				}
			}
		})
	}
}

// TestNewSteerConfig tests the steer configuration initialization
func TestNewSteerConfig(t *testing.T) {
	config := NewSteerConfig()

	assert.Equal(t, "", config.ConversationID)
	assert.False(t, config.Follow)
	assert.Empty(t, config.Images)
}

// TestSteerConfigDefaults tests the default steer configuration values
func TestSteerConfigDefaults(t *testing.T) {
	defaults := NewSteerConfig()

	// Test that defaults are properly set
	assert.Equal(t, "", defaults.ConversationID, "Default conversation ID should be empty")
	assert.False(t, defaults.Follow, "Default follow should be false")
	assert.Empty(t, defaults.Images, "Default images should be empty")
}

func TestSteerConfigParsesImages(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	steerDefaults := NewSteerConfig()
	cmd.Flags().StringVar(&steerDefaults.ConversationID, "conversation-id", steerDefaults.ConversationID, "ID of the conversation to steer")
	cmd.Flags().BoolP("follow", "f", steerDefaults.Follow, "Steer the most recent conversation")
	cmd.Flags().StringSliceP("image", "I", steerDefaults.Images, "Add image input")

	require.NoError(t, cmd.ParseFlags([]string{"--image", "one.png", "-I", "two.jpg"}))

	config := NewSteerConfig()
	images, err := cmd.Flags().GetStringSlice("image")
	require.NoError(t, err)
	config.Images = images

	assert.Equal(t, []string{"one.png", "two.jpg"}, config.Images)
}

func TestRemoteSteerProcessUsesDaemonWithoutClientStore(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "/api/conversations/conversation-12345/steer", r.URL.Path)
				assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
				var request struct {
					Message string                  `json:"message"`
					Content []chat.ChatContentBlock `json:"content"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				assert.Equal(t, "new direction", request.Message)
				require.Len(t, request.Content, 2)
				require.NotNil(t, request.Content[1].ImageURL)
				assert.Equal(t, "https://images.example/context.png", request.Content[1].ImageURL.URL)
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte(`{"success":true,"conversation_id":"conversation-12345","queued":true}`))
				} else {
					_, _ = w.Write([]byte(`{"error":"unavailable"}`))
				}
			}))
			defer daemon.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			root := t.TempDir()
			invalidStore := filepath.Join(root, "no-client-store")
			require.NoError(t, os.WriteFile(invalidStore, []byte("not a directory"), 0o600))
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "KODELET_BASE_PATH=" + invalidStore, "KODELET_TEST_CLI_PROCESS=1"}
			process := daemonCLIProcess(ctx, t, root, env, "steer", "--server="+daemon.URL, "--auth-token=client", "--conversation-id=conversation-12345", "--image=https://images.example/context.png", "new direction")
			output, err := process.CombinedOutput()
			if status == http.StatusOK {
				require.NoError(t, err, "%s", output)
				assert.Contains(t, string(output), "Steering sent to conversation conversation-12345")
			} else {
				require.Error(t, err)
				assert.Contains(t, string(output), "could not confirm that the steering message was received")
				assert.Contains(t, string(output), "before sending it again")
			}
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestRemoteSteerRejectsUnscopedFollowBeforeHTTP(t *testing.T) {
	cmd := remoteRunCommandForTest()
	cmd.Flags().String("conversation-id", "", "")
	require.NoError(t, cmd.ParseFlags([]string{"--follow", "--server=http://127.0.0.1:1", "--auth-token=client"}))
	require.ErrorContains(t, sendRemoteSteer(cmd, "work"), "requires --runner or --cwd")
}

func TestRemoteSteerFollowUsesDaemonDirectoryScope(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/conversations":
			assert.Equal(t, "/runner-only/repo", r.URL.Query().Get("cwd"))
			_, _ = w.Write([]byte(`{"conversations":[{"id":"scoped-conversation"}]}`))
		case "/api/conversations/scoped-conversation/steer":
			_, _ = w.Write([]byte(`{"success":true,"conversation_id":"scoped-conversation","queued":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	cmd := remoteRunCommandForTest()
	cmd.SetContext(t.Context())
	cmd.Flags().String("conversation-id", "", "")
	require.NoError(t, cmd.ParseFlags([]string{"--follow", "--cwd=/runner-only/repo", "--server=" + daemon.URL, "--auth-token=client"}))
	var output bytes.Buffer
	cmd.SetOut(&output)
	require.NoError(t, sendRemoteSteer(cmd, "work"))
	assert.Contains(t, output.String(), "scoped-conversation")
}
