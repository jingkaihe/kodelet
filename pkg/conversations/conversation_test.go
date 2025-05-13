package conversations

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewConversationRecord(t *testing.T) {
	// Test creation with provided ID
	providedID := "test-conversation-123"
	record := NewConversationRecord(providedID)

	assert.Equal(t, providedID, record.ID, "ID should match provided ID")
	assert.Equal(t, json.RawMessage("[]"), record.RawMessages, "RawMessages should be initialized as empty array")
	assert.NotZero(t, record.CreatedAt, "CreatedAt should be set")
	assert.NotZero(t, record.UpdatedAt, "UpdatedAt should be set")
	assert.NotNil(t, record.Metadata, "Metadata should be initialized")

	// Test creation with generated ID
	record = NewConversationRecord("")
	assert.NotEmpty(t, record.ID, "ID should be generated")
}

func TestToSummary(t *testing.T) {
	record := NewConversationRecord("test-id")
	record.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"This is a test message"}]},{"role":"assistant"}]`)
	record.Summary = "Test conversation summary"

	summary := record.ToSummary()

	assert.Equal(t, record.ID, summary.ID, "ID should match")
	assert.Equal(t, 2, summary.MessageCount, "Message count should be 2 based on role count")
	assert.Equal(t, "This is a test message", summary.FirstMessage, "First message content should match")
	assert.Equal(t, "Test conversation summary", summary.Summary, "Summary should match")
	assert.Equal(t, record.CreatedAt, summary.CreatedAt, "CreatedAt should match")
	assert.Equal(t, record.UpdatedAt, summary.UpdatedAt, "UpdatedAt should match")

	// Test truncation of long first message
	longMessage := "This is a very long message that should be truncated when converted to a summary. It contains more than 100 characters to test the truncation logic."
	record = NewConversationRecord("test-id-2")
	record.RawMessages = json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"` + longMessage + `"}]}]`)

	summary = record.ToSummary()
	assert.Equal(t, 100, len(summary.FirstMessage), "Long first message should be truncated to 100 chars")
	assert.Equal(t, longMessage[:97]+"...", summary.FirstMessage, "Should truncate and add ellipsis")
}
