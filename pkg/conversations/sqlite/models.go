package sqlite

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/types/conversations"
)

// JSONField is a generic type for handling JSON marshaling/unmarshaling in database
type JSONField[T any] struct {
	Data T
}

// Scan implements the sql.Scanner interface for reading from database
func (j *JSONField[T]) Scan(value any) error {
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
	ID                  string                                           `db:"id"`
	RawMessages         json.RawMessage                                  `db:"raw_messages"`
	Provider            string                                           `db:"provider"`
	FileLastAccess      JSONField[map[string]time.Time]                  `db:"file_last_access"`
	Usage               JSONField[llmtypes.Usage]                        `db:"usage"`
	Summary             *string                                          `db:"summary"` // NULL in database
	CreatedAt           time.Time                                        `db:"created_at"`
	UpdatedAt           time.Time                                        `db:"updated_at"`
	Metadata            JSONField[map[string]any]                        `db:"metadata"`
	ToolResults         JSONField[map[string]tools.StructuredToolResult] `db:"tool_results"`
	BackgroundProcesses JSONField[[]tools.BackgroundProcess]             `db:"background_processes"`
}

// dbConversationSummary represents the conversation_summaries table structure
type dbConversationSummary struct {
	ID           string                    `db:"id"`
	MessageCount int                       `db:"message_count"`
	FirstMessage string                    `db:"first_message"`
	Summary      *string                   `db:"summary"` // NULL in database
	Provider     string                    `db:"provider"`
	Metadata     JSONField[map[string]any] `db:"metadata"`
	Usage        JSONField[llmtypes.Usage] `db:"usage"`
	CreatedAt    time.Time                 `db:"created_at"`
	UpdatedAt    time.Time                 `db:"updated_at"`
}

// ToConversationRecord converts database record to domain model
func (dbr *dbConversationRecord) ToConversationRecord() conversations.ConversationRecord {
	record := conversations.ConversationRecord{
		ID:             dbr.ID,
		RawMessages:    dbr.RawMessages,
		Provider:       dbr.Provider,
		FileLastAccess: dbr.FileLastAccess.Data,
		Usage:          dbr.Usage.Data,
		CreatedAt:      dbr.CreatedAt,
		UpdatedAt:      dbr.UpdatedAt,
		Metadata:       dbr.Metadata.Data,
		ToolResults:    dbr.ToolResults.Data,
	}

	// Ensure BackgroundProcesses is always a non-nil slice
	if dbr.BackgroundProcesses.Data == nil {
		record.BackgroundProcesses = []tools.BackgroundProcess{}
	} else {
		record.BackgroundProcesses = dbr.BackgroundProcesses.Data
	}

	if dbr.Summary != nil {
		record.Summary = *dbr.Summary
	}

	return record
}

// ToConversationSummary converts database summary to domain model
func (dbs *dbConversationSummary) ToConversationSummary() conversations.ConversationSummary {
	summary := conversations.ConversationSummary{
		ID:           dbs.ID,
		MessageCount: dbs.MessageCount,
		FirstMessage: dbs.FirstMessage,
		Provider:     dbs.Provider,
		Metadata:     dbs.Metadata.Data,
		Usage:        dbs.Usage.Data,
		CreatedAt:    dbs.CreatedAt,
		UpdatedAt:    dbs.UpdatedAt,
	}

	if dbs.Summary != nil {
		summary.Summary = *dbs.Summary
	}

	return summary
}

// fromConversationRecord converts domain model to database record
func fromConversationRecord(record conversations.ConversationRecord) *dbConversationRecord {
	dbRecord := &dbConversationRecord{
		ID:                  record.ID,
		RawMessages:         record.RawMessages,
		Provider:            record.Provider,
		FileLastAccess:      JSONField[map[string]time.Time]{Data: record.FileLastAccess},
		Usage:               JSONField[llmtypes.Usage]{Data: record.Usage},
		CreatedAt:           record.CreatedAt,
		UpdatedAt:           record.UpdatedAt,
		Metadata:            JSONField[map[string]any]{Data: record.Metadata},
		ToolResults:         JSONField[map[string]tools.StructuredToolResult]{Data: record.ToolResults},
		BackgroundProcesses: JSONField[[]tools.BackgroundProcess]{Data: record.BackgroundProcesses},
	}

	if record.Summary != "" {
		dbRecord.Summary = &record.Summary
	}

	return dbRecord
}

// fromConversationSummary converts domain model to database summary
func fromConversationSummary(summary conversations.ConversationSummary) *dbConversationSummary {
	dbSummary := &dbConversationSummary{
		ID:           summary.ID,
		MessageCount: summary.MessageCount,
		FirstMessage: summary.FirstMessage,
		Provider:     summary.Provider,
		Metadata:     JSONField[map[string]any]{Data: summary.Metadata},
		Usage:        JSONField[llmtypes.Usage]{Data: summary.Usage},
		CreatedAt:    summary.CreatedAt,
		UpdatedAt:    summary.UpdatedAt,
	}

	if summary.Summary != "" {
		dbSummary.Summary = &summary.Summary
	}

	return dbSummary
}
