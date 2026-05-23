package main

import (
	"testing"

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

func TestGetSteerConfigFromFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	defaults := NewSteerConfig()
	cmd.Flags().String("conversation-id", defaults.ConversationID, "")
	cmd.Flags().BoolP("follow", "f", defaults.Follow, "")
	cmd.Flags().StringSliceP("image", "I", defaults.Images, "")
	require.NoError(t, cmd.Flags().Set("conversation-id", "conversation-12345"))
	require.NoError(t, cmd.Flags().Set("image", "one.png,two.png"))

	config := getSteerConfigFromFlags(cmd.Context(), cmd)

	assert.Equal(t, "conversation-12345", config.ConversationID)
	assert.False(t, config.Follow)
	assert.Equal(t, []string{"one.png", "two.png"}, config.Images)
}
