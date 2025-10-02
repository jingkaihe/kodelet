package sqlite

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

func TestJSONField_Scan_Success(t *testing.T) {
	tests := []struct {
		name     string
		field    JSONField[map[string]interface{}]
		input    interface{}
		expected map[string]interface{}
	}{
		{
			name:  "scan from byte slice",
			field: JSONField[map[string]interface{}]{},
			input: []byte(`{"key": "value", "number": 42}`),
			expected: map[string]interface{}{
				"key":    "value",
				"number": float64(42), // JSON numbers become float64
			},
		},
		{
			name:  "scan from string",
			field: JSONField[map[string]interface{}]{},
			input: `{"test": true}`,
			expected: map[string]interface{}{
				"test": true,
			},
		},
		{
			name:     "scan nil value",
			field:    JSONField[map[string]interface{}]{},
			input:    nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.field.Scan(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, tt.field.Data)
		})
	}
}

func TestJSONField_Scan_Error(t *testing.T) {
	tests := []struct {
		name  string
		field JSONField[map[string]interface{}]
		input interface{}
	}{
		{
			name:  "invalid type",
			field: JSONField[map[string]interface{}]{},
			input: 123,
		},
		{
			name:  "invalid JSON bytes",
			field: JSONField[map[string]interface{}]{},
			input: []byte(`{"invalid": json}`),
		},
		{
			name:  "invalid JSON string",
			field: JSONField[map[string]interface{}]{},
			input: `{"incomplete":`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.field.Scan(tt.input)
			assert.Error(t, err)
		})
	}
}

func TestJSONField_Value_Success(t *testing.T) {
	tests := []struct {
		name     string
		field    JSONField[map[string]interface{}]
		expected string
	}{
		{
			name: "marshal simple map",
			field: JSONField[map[string]interface{}]{
				Data: map[string]interface{}{
					"key":    "value",
					"number": 42,
				},
			},
			expected: `{"key":"value","number":42}`,
		},
		{
			name: "marshal empty map",
			field: JSONField[map[string]interface{}]{
				Data: map[string]interface{}{},
			},
			expected: `{}`,
		},
		{
			name: "marshal nil map",
			field: JSONField[map[string]interface{}]{
				Data: nil,
			},
			expected: `null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := tt.field.Value()
			require.NoError(t, err)

			// Convert driver.Value to string for comparison
			jsonBytes, ok := value.([]byte)
			require.True(t, ok)
			assert.JSONEq(t, tt.expected, string(jsonBytes))
		})
	}
}

func TestJSONField_Value_Error(t *testing.T) {
	// Create a field with a type that can't be marshaled to JSON
	field := JSONField[func()]{
		Data: func() {}, // functions can't be marshaled to JSON
	}

	_, err := field.Value()
	assert.Error(t, err)
}

func TestJSONField_ComplexTypes(t *testing.T) {
	// Test with LLM Usage type
	t.Run("llm usage type", func(t *testing.T) {
		usage := llmtypes.Usage{
			InputTokens:  100,
			OutputTokens: 50,
			InputCost:    0.001,
			OutputCost:   0.002,
		}

		field := JSONField[llmtypes.Usage]{Data: usage}

		// Test Value() method
		value, err := field.Value()
		require.NoError(t, err)

		// Test Scan() method
		var newField JSONField[llmtypes.Usage]
		err = newField.Scan(value)
		require.NoError(t, err)
		assert.Equal(t, usage, newField.Data)
	})

	// Test with time map
	t.Run("time map type", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second) // Truncate for JSON compatibility
		timeMap := map[string]time.Time{
			"created": now,
			"updated": now.Add(time.Hour),
		}

		field := JSONField[map[string]time.Time]{Data: timeMap}

		// Test Value() method
		value, err := field.Value()
		require.NoError(t, err)

		// Test Scan() method
		var newField JSONField[map[string]time.Time]
		err = newField.Scan(value)
		require.NoError(t, err)

		// Compare times with RFC3339 precision (JSON time format)
		for key, expectedTime := range timeMap {
			actualTime, exists := newField.Data[key]
			require.True(t, exists)
			assert.Equal(t, expectedTime.Format(time.RFC3339), actualTime.Format(time.RFC3339))
		}
	})
}

func TestConversationRecord_ToConversations(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	summary := "Test summary"

	dbRecord := &dbConversationRecord{
		ID:          "test-id",
		RawMessages: json.RawMessage(`[{"role": "user", "content": "test"}]`),
		Provider:    "anthropic",
		FileLastAccess: JSONField[map[string]time.Time]{
			Data: map[string]time.Time{"file.txt": now},
		},
		Usage: JSONField[llmtypes.Usage]{
			Data: llmtypes.Usage{InputTokens: 100, OutputTokens: 50},
		},
		Summary:   &summary,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Hour),
		Metadata: JSONField[map[string]interface{}]{
			Data: map[string]interface{}{"key": "value"},
		},
		ToolResults: JSONField[map[string]tools.StructuredToolResult]{
			Data: map[string]tools.StructuredToolResult{
				"call1": {
					ToolName:  "test_tool",
					Success:   true,
					Timestamp: now,
				},
			},
		},
	}

	record := dbRecord.ToConversationRecord()

	assert.Equal(t, "test-id", record.ID)
	assert.Equal(t, `[{"role": "user", "content": "test"}]`, string(record.RawMessages))
	assert.Equal(t, "anthropic", record.Provider)
	assert.Equal(t, "Test summary", record.Summary)
	assert.Equal(t, now, record.CreatedAt)
	assert.Equal(t, now.Add(time.Hour), record.UpdatedAt)
	assert.Equal(t, map[string]interface{}{"key": "value"}, record.Metadata)
	assert.Contains(t, record.FileLastAccess, "file.txt")
	assert.Equal(t, 100, record.Usage.InputTokens)
	assert.Equal(t, 50, record.Usage.OutputTokens)
	assert.Contains(t, record.ToolResults, "call1")
}

func TestDb_ConversationRecord_ToConversationRecord_NullSummary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	dbRecord := &dbConversationRecord{
		ID:          "test-id",
		RawMessages: json.RawMessage(`[]`),
		Provider:    "anthropic",
		FileLastAccess: JSONField[map[string]time.Time]{
			Data: map[string]time.Time{},
		},
		Usage: JSONField[llmtypes.Usage]{
			Data: llmtypes.Usage{},
		},
		Summary:   nil, // NULL in database
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: JSONField[map[string]interface{}]{
			Data: map[string]interface{}{},
		},
		ToolResults: JSONField[map[string]tools.StructuredToolResult]{
			Data: map[string]tools.StructuredToolResult{},
		},
	}

	record := dbRecord.ToConversationRecord()

	assert.Equal(t, "", record.Summary) // Should be empty string, not nil
}

func TestDbConversationSummary_ToConversationSummary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	summary := "Test summary"

	dbSummary := &dbConversationSummary{
		ID:           "test-id",
		MessageCount: 5,
		FirstMessage: "Hello world",
		Summary:      &summary,
		Usage: JSONField[llmtypes.Usage]{
			Data: llmtypes.Usage{InputTokens: 100, OutputTokens: 50},
		},
		CreatedAt: now,
		UpdatedAt: now.Add(time.Hour),
	}

	domainSummary := dbSummary.ToConversationSummary()

	assert.Equal(t, "test-id", domainSummary.ID)
	assert.Equal(t, 5, domainSummary.MessageCount)
	assert.Equal(t, "Hello world", domainSummary.FirstMessage)
	assert.Equal(t, "Test summary", domainSummary.Summary)
	assert.Equal(t, 100, domainSummary.Usage.InputTokens)
	assert.Equal(t, 50, domainSummary.Usage.OutputTokens)
	assert.Equal(t, now, domainSummary.CreatedAt)
	assert.Equal(t, now.Add(time.Hour), domainSummary.UpdatedAt)
}

func TestFromConversationRecord(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	record := conversations.ConversationRecord{
		ID:          "test-id",
		RawMessages: json.RawMessage(`[{"role": "user", "content": "test"}]`),
		Provider:    "anthropic",
		FileLastAccess: map[string]time.Time{
			"file.txt": now,
		},
		Usage: llmtypes.Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
		Summary:   "Test summary",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Hour),
		Metadata: map[string]interface{}{
			"key": "value",
		},
		ToolResults: map[string]tools.StructuredToolResult{
			"call1": {
				ToolName:  "test_tool",
				Success:   true,
				Timestamp: now,
			},
		},
		BackgroundProcesses: []tools.BackgroundProcess{
			{
				PID:       123,
				StartTime: now,
				LogPath:   "/tmp/test.log",
				Command:   "echo hello",
			},
		},
	}

	dbRecord := fromConversationRecord(record)

	assert.Equal(t, "test-id", dbRecord.ID)
	assert.Equal(t, `[{"role": "user", "content": "test"}]`, string(dbRecord.RawMessages))
	assert.Equal(t, "anthropic", dbRecord.Provider)
	assert.Equal(t, "Test summary", *dbRecord.Summary)
	assert.Equal(t, now, dbRecord.CreatedAt)
	assert.Equal(t, now.Add(time.Hour), dbRecord.UpdatedAt)
	assert.Equal(t, map[string]time.Time{"file.txt": now}, dbRecord.FileLastAccess.Data)
	assert.Equal(t, llmtypes.Usage{InputTokens: 100, OutputTokens: 50}, dbRecord.Usage.Data)
	assert.Equal(t, map[string]interface{}{"key": "value"}, dbRecord.Metadata.Data)
	assert.Contains(t, dbRecord.ToolResults.Data, "call1")
	assert.Len(t, dbRecord.BackgroundProcesses.Data, 1)
	assert.Equal(t, 123, dbRecord.BackgroundProcesses.Data[0].PID)
}

func TestFromConversationRecord_EmptySummary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	record := conversations.ConversationRecord{
		ID:                  "test-id",
		RawMessages:         json.RawMessage(`[]`),
		Provider:            "anthropic",
		FileLastAccess:      map[string]time.Time{},
		Usage:               llmtypes.Usage{},
		Summary:             "", // Empty summary
		CreatedAt:           now,
		UpdatedAt:           now,
		Metadata:            map[string]interface{}{},
		ToolResults:         map[string]tools.StructuredToolResult{},
		BackgroundProcesses: []tools.BackgroundProcess{},
	}

	dbRecord := fromConversationRecord(record)

	assert.Nil(t, dbRecord.Summary) // Should be nil for database storage
}

func TestFromConversationSummary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	summary := conversations.ConversationSummary{
		ID:           "test-id",
		MessageCount: 5,
		FirstMessage: "Hello world",
		Summary:      "Test summary",
		Usage: llmtypes.Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
		CreatedAt: now,
		UpdatedAt: now.Add(time.Hour),
	}

	dbSummary := fromConversationSummary(summary)

	assert.Equal(t, "test-id", dbSummary.ID)
	assert.Equal(t, 5, dbSummary.MessageCount)
	assert.Equal(t, "Hello world", dbSummary.FirstMessage)
	assert.Equal(t, "Test summary", *dbSummary.Summary)
	assert.Equal(t, llmtypes.Usage{InputTokens: 100, OutputTokens: 50}, dbSummary.Usage.Data)
	assert.Equal(t, now, dbSummary.CreatedAt)
	assert.Equal(t, now.Add(time.Hour), dbSummary.UpdatedAt)
}

func TestFromConversationSummary_EmptySummary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	summary := conversations.ConversationSummary{
		ID:           "test-id",
		MessageCount: 5,
		FirstMessage: "Hello world",
		Summary:      "", // Empty summary
		Usage:        llmtypes.Usage{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	dbSummary := fromConversationSummary(summary)

	assert.Nil(t, dbSummary.Summary) // Should be nil for database storage
}

func TestRoundTripConversion(t *testing.T) {
	// Test that converting from domain -> db -> domain preserves data
	now := time.Now().UTC().Truncate(time.Second)

	originalRecord := conversations.ConversationRecord{
		ID:          "test-id",
		RawMessages: json.RawMessage(`[{"role": "user", "content": [{"type": "text", "text": "Hello"}]}]`),
		Provider:    "anthropic",
		FileLastAccess: map[string]time.Time{
			"file1.txt": now,
			"file2.txt": now.Add(time.Hour),
		},
		Usage: llmtypes.Usage{
			InputTokens:              100,
			OutputTokens:             50,
			CacheCreationInputTokens: 10,
			CacheReadInputTokens:     5,
			InputCost:                0.001,
			OutputCost:               0.002,
			CurrentContextWindow:     8192,
			MaxContextWindow:         32768,
		},
		Summary:   "Conversation about greetings",
		CreatedAt: now,
		UpdatedAt: now.Add(time.Hour),
		Metadata: map[string]interface{}{
			"source":   "test",
			"priority": float64(1),
			"tags":     []interface{}{"greeting", "test"},
		},
		ToolResults: map[string]tools.StructuredToolResult{
			"call1": {
				ToolName:  "file_read",
				Success:   true,
				Timestamp: now,
				Metadata: tools.FileReadMetadata{
					FilePath: "/test/file.txt",
					Offset:   1,
					Lines:    []string{"line1", "line2"},
					Language: "text",
				},
			},
		},
		BackgroundProcesses: []tools.BackgroundProcess{
			{
				PID:       456,
				StartTime: now.Add(30 * time.Minute),
				LogPath:   "/tmp/bg_process.log",
				Command:   "long_running_command",
			},
		},
	}

	// Convert to database model and back
	dbRecord := fromConversationRecord(originalRecord)
	convertedRecord := dbRecord.ToConversationRecord()

	// Compare all fields
	assert.Equal(t, originalRecord.ID, convertedRecord.ID)
	assert.Equal(t, string(originalRecord.RawMessages), string(convertedRecord.RawMessages))
	assert.Equal(t, originalRecord.Provider, convertedRecord.Provider)
	assert.Equal(t, originalRecord.Summary, convertedRecord.Summary)
	assert.Equal(t, originalRecord.CreatedAt, convertedRecord.CreatedAt)
	assert.Equal(t, originalRecord.UpdatedAt, convertedRecord.UpdatedAt)

	// Compare Usage (all fields)
	assert.Equal(t, originalRecord.Usage.InputTokens, convertedRecord.Usage.InputTokens)
	assert.Equal(t, originalRecord.Usage.OutputTokens, convertedRecord.Usage.OutputTokens)
	assert.Equal(t, originalRecord.Usage.CacheCreationInputTokens, convertedRecord.Usage.CacheCreationInputTokens)
	assert.Equal(t, originalRecord.Usage.CacheReadInputTokens, convertedRecord.Usage.CacheReadInputTokens)
	assert.Equal(t, originalRecord.Usage.InputCost, convertedRecord.Usage.InputCost)
	assert.Equal(t, originalRecord.Usage.OutputCost, convertedRecord.Usage.OutputCost)
	assert.Equal(t, originalRecord.Usage.CurrentContextWindow, convertedRecord.Usage.CurrentContextWindow)
	assert.Equal(t, originalRecord.Usage.MaxContextWindow, convertedRecord.Usage.MaxContextWindow)

	// Compare FileLastAccess
	assert.Equal(t, len(originalRecord.FileLastAccess), len(convertedRecord.FileLastAccess))
	for file, originalTime := range originalRecord.FileLastAccess {
		convertedTime, exists := convertedRecord.FileLastAccess[file]
		assert.True(t, exists)
		assert.Equal(t, originalTime.Format(time.RFC3339), convertedTime.Format(time.RFC3339))
	}

	// Compare Metadata
	assert.Equal(t, originalRecord.Metadata, convertedRecord.Metadata)

	// Compare ToolResults
	assert.Equal(t, len(originalRecord.ToolResults), len(convertedRecord.ToolResults))
	for callID, originalResult := range originalRecord.ToolResults {
		convertedResult, exists := convertedRecord.ToolResults[callID]
		assert.True(t, exists)
		assert.Equal(t, originalResult.ToolName, convertedResult.ToolName)
		assert.Equal(t, originalResult.Success, convertedResult.Success)
		assert.Equal(t, originalResult.Timestamp.Format(time.RFC3339), convertedResult.Timestamp.Format(time.RFC3339))
	}

	// Compare BackgroundProcesses
	assert.Equal(t, len(originalRecord.BackgroundProcesses), len(convertedRecord.BackgroundProcesses))
	for i, originalProcess := range originalRecord.BackgroundProcesses {
		convertedProcess := convertedRecord.BackgroundProcesses[i]
		assert.Equal(t, originalProcess.PID, convertedProcess.PID)
		assert.Equal(t, originalProcess.StartTime.Format(time.RFC3339), convertedProcess.StartTime.Format(time.RFC3339))
		assert.Equal(t, originalProcess.LogPath, convertedProcess.LogPath)
		assert.Equal(t, originalProcess.Command, convertedProcess.Command)
	}
}

func TestBackgroundProcesses_DefaultToEmptySlice(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	// Test with nil BackgroundProcesses field (simulating old database records)
	dbRecord := &dbConversationRecord{
		ID:          "test-id",
		RawMessages: json.RawMessage(`[]`),
		Provider:    "anthropic",
		FileLastAccess: JSONField[map[string]time.Time]{
			Data: map[string]time.Time{},
		},
		Usage: JSONField[llmtypes.Usage]{
			Data: llmtypes.Usage{},
		},
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: JSONField[map[string]interface{}]{
			Data: map[string]interface{}{},
		},
		ToolResults: JSONField[map[string]tools.StructuredToolResult]{
			Data: map[string]tools.StructuredToolResult{},
		},
		// BackgroundProcesses field is zero-initialized (nil slice)
		BackgroundProcesses: JSONField[[]tools.BackgroundProcess]{},
	}

	// Convert to domain model
	record := dbRecord.ToConversationRecord()

	// Should get empty slice, not nil
	assert.NotNil(t, record.BackgroundProcesses)
	assert.Empty(t, record.BackgroundProcesses)
	assert.Equal(t, []tools.BackgroundProcess{}, record.BackgroundProcesses)
}

func TestBackgroundProcesses_NullDatabaseValue(t *testing.T) {
	// Test JSONField behavior with NULL database value
	var field JSONField[[]tools.BackgroundProcess]

	// Simulate scanning a NULL value from database
	err := field.Scan(nil)
	assert.NoError(t, err)

	// Should have nil slice (this is the issue we need to fix)
	assert.Nil(t, field.Data)
}

func TestBackgroundProcesses_EmptyJsonArray(t *testing.T) {
	// Test JSONField behavior with empty JSON array
	var field JSONField[[]tools.BackgroundProcess]

	// Simulate scanning an empty JSON array from database
	err := field.Scan([]byte("[]"))
	assert.NoError(t, err)

	// Should have empty slice
	assert.NotNil(t, field.Data)
	assert.Empty(t, field.Data)
	assert.Equal(t, []tools.BackgroundProcess{}, field.Data)
}

func TestBackgroundProcesses_ValidData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	// Test JSONField behavior with valid data
	var field JSONField[[]tools.BackgroundProcess]

	jsonData := `[{"pid":123,"start_time":"` + now.Format(time.RFC3339) + `","log_path":"/tmp/test.log","command":"echo hello"}]`

	err := field.Scan([]byte(jsonData))
	assert.NoError(t, err)

	// Should have the correct data
	assert.NotNil(t, field.Data)
	assert.Len(t, field.Data, 1)
	assert.Equal(t, 123, field.Data[0].PID)
	assert.Equal(t, "/tmp/test.log", field.Data[0].LogPath)
	assert.Equal(t, "echo hello", field.Data[0].Command)
	assert.Equal(t, now.Format(time.RFC3339), field.Data[0].StartTime.Format(time.RFC3339))
}
