package conversations

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/pkg/errors"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// JSONField is a generic type for handling JSON marshaling/unmarshaling in database
type JSONField[T any] struct {
	Data T
}

// Scan implements the sql.Scanner interface for reading from database
func (j *JSONField[T]) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		str, ok := value.(string)
		if !ok {
			return errors.Errorf("cannot scan %T into JSONField", value)
		}
		bytes = []byte(str)
	}

	return json.Unmarshal(bytes, &j.Data)
}

// Value implements the driver.Valuer interface for writing to database
func (j JSONField[T]) Value() (driver.Value, error) {
	return json.Marshal(j.Data)
}

// dbConversationRecord represents the conversations table structure
type dbConversationRecord struct {
	ID             string                                                 `db:"id"`
	RawMessages    json.RawMessage                                        `db:"raw_messages"`
	ModelType      string                                                 `db:"model_type"`
	FileLastAccess JSONField[map[string]time.Time]                       `db:"file_last_access"`
	Usage          JSONField[llmtypes.Usage]                              `db:"usage"`
	Summary        *string                                                `db:"summary"` // NULL in database
	CreatedAt      time.Time                                              `db:"created_at"`
	UpdatedAt      time.Time                                              `db:"updated_at"`
	Metadata       JSONField[map[string]interface{}]                      `db:"metadata"`
	ToolResults    JSONField[map[string]tools.StructuredToolResult]       `db:"tool_results"`
}

// dbConversationSummary represents the conversation_summaries table structure
type dbConversationSummary struct {
	ID           string                        `db:"id"`
	MessageCount int                           `db:"message_count"`
	FirstMessage string                        `db:"first_message"`
	Summary      *string                       `db:"summary"` // NULL in database
	Usage        JSONField[llmtypes.Usage]     `db:"usage"`
	CreatedAt    time.Time                     `db:"created_at"`
	UpdatedAt    time.Time                     `db:"updated_at"`
}

// ToConversationRecord converts database record to domain model
func (dbr *dbConversationRecord) ToConversationRecord() ConversationRecord {
	record := ConversationRecord{
		ID:             dbr.ID,
		RawMessages:    dbr.RawMessages,
		ModelType:      dbr.ModelType,
		FileLastAccess: dbr.FileLastAccess.Data,
		Usage:          dbr.Usage.Data,
		CreatedAt:      dbr.CreatedAt,
		UpdatedAt:      dbr.UpdatedAt,
		Metadata:       dbr.Metadata.Data,
		ToolResults:    dbr.ToolResults.Data,
	}

	if dbr.Summary != nil {
		record.Summary = *dbr.Summary
	}

	return record
}

// ToConversationSummary converts database summary to domain model
func (dbs *dbConversationSummary) ToConversationSummary() ConversationSummary {
	summary := ConversationSummary{
		ID:           dbs.ID,
		MessageCount: dbs.MessageCount,
		FirstMessage: dbs.FirstMessage,
		Usage:        dbs.Usage.Data,
		CreatedAt:    dbs.CreatedAt,
		UpdatedAt:    dbs.UpdatedAt,
	}

	if dbs.Summary != nil {
		summary.Summary = *dbs.Summary
	}

	return summary
}

// FromConversationRecord converts domain model to database record
func FromConversationRecord(record ConversationRecord) *dbConversationRecord {
	dbRecord := &dbConversationRecord{
		ID:             record.ID,
		RawMessages:    record.RawMessages,
		ModelType:      record.ModelType,
		FileLastAccess: JSONField[map[string]time.Time]{Data: record.FileLastAccess},
		Usage:          JSONField[llmtypes.Usage]{Data: record.Usage},
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
		Metadata:       JSONField[map[string]interface{}]{Data: record.Metadata},
		ToolResults:    JSONField[map[string]tools.StructuredToolResult]{Data: record.ToolResults},
	}

	if record.Summary != "" {
		dbRecord.Summary = &record.Summary
	}

	return dbRecord
}

// FromConversationSummary converts domain model to database summary
func FromConversationSummary(summary ConversationSummary) *dbConversationSummary {
	dbSummary := &dbConversationSummary{
		ID:           summary.ID,
		MessageCount: summary.MessageCount,
		FirstMessage: summary.FirstMessage,
		Usage:        JSONField[llmtypes.Usage]{Data: summary.Usage},
		CreatedAt:    summary.CreatedAt,
		UpdatedAt:    summary.UpdatedAt,
	}

	if summary.Summary != "" {
		dbSummary.Summary = &summary.Summary
	}

	return dbSummary
}