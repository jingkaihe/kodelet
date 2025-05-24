package anthropic

import (
	"context"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"

	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestDeserializeMessages(t *testing.T) {
	thread := NewAnthropicThread(llmtypes.Config{
		Model: string(anthropic.ModelClaudeSonnet4_0),
	})
	messages, err := DeserializeMessages([]byte(`[]`))
	assert.NoError(t, err)
	assert.Equal(t, 0, len(messages))

	rawMessages := `[
    {
      "content": [
        {
          "text": "ls -la",
          "cache_control": {
            "type": "ephemeral"
          },
          "type": "text"
        }
      ],
      "role": "user"
    },
    {
      "content": [
        {
          "text": "I'll list all files in the current directory with detailed information.",
          "citations": [],
          "type": "text"
        },
        {
          "id": "toolu_01Nc9gURS9CrZjCcruQNCcna",
          "input": {
            "command": "ls -la",
            "description": "List all files with detailed information",
            "timeout": 10
          },
          "name": "bash",
          "type": "tool_use"
        }
      ],
      "role": "assistant"
    },
    {
      "content": [
        {
          "tool_use_id": "toolu_01Nc9gURS9CrZjCcruQNCcna",
          "is_error": false,
          "content": [
            {
              "text": "/root/foo/bar",
              "type": "text"
            }
          ],
          "type": "tool_result"
        }
      ],
      "role": "user"
    },
    {
      "content": [],
      "role": "assistant"
    }
  ]`

	messages, err = DeserializeMessages([]byte(rawMessages))
	assert.NoError(t, err)
	thread.messages = messages

	assert.Equal(t, 4, len(messages)) // remove the empty assistant message
	assert.Equal(t, anthropic.MessageParamRoleUser, messages[0].Role)
	assert.Equal(t, "ls -la", messages[0].Content[0].OfText.Text)
	assert.Equal(t, "text", *messages[0].Content[0].GetType())
	assert.Equal(t, anthropic.CacheControlEphemeralParam{Type: "ephemeral"}, messages[0].Content[0].OfText.CacheControl)

	assert.Equal(t, anthropic.MessageParamRoleAssistant, thread.messages[1].Role)
	assert.Equal(t, "I'll list all files in the current directory with detailed information.", thread.messages[1].Content[0].OfText.Text)
	assert.Equal(t, "text", *thread.messages[1].Content[0].GetType())
	assert.Equal(t, "toolu_01Nc9gURS9CrZjCcruQNCcna", thread.messages[1].Content[1].OfToolUse.ID)
	assert.Equal(t, "bash", thread.messages[1].Content[1].OfToolUse.Name)

	input := thread.messages[1].Content[1].OfToolUse.Input.(map[string]interface{})
	assert.Equal(t, "ls -la", input["command"])
	assert.Equal(t, "List all files with detailed information", input["description"])
	assert.Equal(t, float64(10), input["timeout"])

	assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[2].Role)
	assert.Equal(t, "toolu_01Nc9gURS9CrZjCcruQNCcna", thread.messages[2].Content[0].OfToolResult.ToolUseID)
	assert.Equal(t, false, thread.messages[2].Content[0].OfToolResult.IsError.Value)
	assert.Equal(t, "/root/foo/bar", thread.messages[2].Content[0].OfToolResult.Content[0].OfText.Text)
	assert.Equal(t, "text", *thread.messages[2].Content[0].OfToolResult.Content[0].GetType())

	assert.Equal(t, anthropic.MessageParamRoleAssistant, thread.messages[3].Role)
	assert.Equal(t, 0, len(thread.messages[3].Content))
}

func TestSaveAndLoadConversationWithFileLastAccess(t *testing.T) {
	// Create a thread with a unique conversation ID
	conversationID := "test-file-last-access"
	thread := NewAnthropicThread(llmtypes.Config{
		Model: string(anthropic.ModelClaudeSonnet4_0),
	})
	thread.SetConversationID(conversationID)

	// Setup state with file access data
	state := tools.NewBasicState(context.TODO())
	thread.SetState(state)

	// Enable persistence
	thread.EnablePersistence(true)

	// Set file access times
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	fileAccessMap := map[string]time.Time{
		"/path/to/file1.txt": now,
		"/path/to/file2.txt": yesterday,
	}
	state.SetFileLastAccess(fileAccessMap)

	// Save the conversation
	err := thread.SaveConversation(context.Background(), false)
	assert.NoError(t, err)

	// Create a new thread with the same conversation ID
	newThread := NewAnthropicThread(llmtypes.Config{
		Model: string(anthropic.ModelClaudeSonnet4_0),
	})
	newThread.SetConversationID(conversationID)
	newState := tools.NewBasicState(context.TODO())
	newThread.SetState(newState)

	// Enable persistence to load the conversation
	newThread.EnablePersistence(true)

	// Verify the file last access data was preserved
	loadedState := newThread.GetState()
	loadedFileAccess := loadedState.FileLastAccess()

	assert.Equal(t, 2, len(loadedFileAccess))
	assert.Equal(t, now.Unix(), loadedFileAccess["/path/to/file1.txt"].Unix())
	assert.Equal(t, yesterday.Unix(), loadedFileAccess["/path/to/file2.txt"].Unix())
}
