package anthropic

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/stretchr/testify/assert"
)

func TestDeserializeMessages(t *testing.T) {
	thread := NewAnthropicThread(types.Config{
		Model: anthropic.ModelClaude3_7SonnetLatest,
	})
	thread.DeserializeMessages([]byte(``))

	assert.Equal(t, 0, len(thread.messages))

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

	thread.DeserializeMessages([]byte(rawMessages))

	assert.Equal(t, 3, len(thread.messages)) // remove the empty assistant message
	assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[0].Role)
	assert.Equal(t, "ls -la", thread.messages[0].Content[0].OfRequestTextBlock.Text)
	assert.Equal(t, "text", *thread.messages[0].Content[0].GetType())
	assert.Equal(t, anthropic.CacheControlEphemeralParam{}, thread.messages[0].Content[0].OfRequestTextBlock.CacheControl, "prompt cache config is ignored")

	assert.Equal(t, anthropic.MessageParamRoleAssistant, thread.messages[1].Role)
	assert.Equal(t, "I'll list all files in the current directory with detailed information.", thread.messages[1].Content[0].OfRequestTextBlock.Text)
	assert.Equal(t, "text", *thread.messages[1].Content[0].GetType())
	assert.Equal(t, "toolu_01Nc9gURS9CrZjCcruQNCcna", thread.messages[1].Content[1].OfRequestToolUseBlock.ID)
	assert.Equal(t, "bash", thread.messages[1].Content[1].OfRequestToolUseBlock.Name)

	input := thread.messages[1].Content[1].OfRequestToolUseBlock.Input.(map[string]interface{})
	assert.Equal(t, "ls -la", input["command"])
	assert.Equal(t, "List all files with detailed information", input["description"])
	assert.Equal(t, float64(10), input["timeout"])

	assert.Equal(t, anthropic.MessageParamRoleUser, thread.messages[2].Role)
	assert.Equal(t, "toolu_01Nc9gURS9CrZjCcruQNCcna", thread.messages[2].Content[0].OfRequestToolResultBlock.ToolUseID)
	assert.Equal(t, false, thread.messages[2].Content[0].OfRequestToolResultBlock.IsError.Value)
	assert.Equal(t, "/root/foo/bar", thread.messages[2].Content[0].OfRequestToolResultBlock.Content[0].OfRequestTextBlock.Text)
	assert.Equal(t, "text", *thread.messages[2].Content[0].OfRequestToolResultBlock.Content[0].GetType())
	// assert.Equal(t, "ls -la", thread.messages[1].Content[1].OfRequestToolUseBlock.Input.Command)
	// assert.Equal(t, "List all files with detailed information", thread.messages[1].Content[1].OfRequestToolUseBlock.Input.Description)
	// assert.Equal(t, 10, thread.messages[1].Content[1].OfRequestToolUseBlock.Input.Timeout)
}
