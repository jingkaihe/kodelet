package sqlite

// SQL schema definitions for SQLite conversation store

const (
	// Schema version constants
	SchemaVersion1       = 1
	SchemaVersion2       = 2
	SchemaVersion3       = 3
	SchemaVersion4       = 4
	CurrentSchemaVersion = SchemaVersion4
)

// createSchemaVersionTable creates the schema version tracking table
const createSchemaVersionTable = `
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at DATETIME NOT NULL,
    description TEXT
);
`

// createConversationsTable creates the main conversations table
const createConversationsTable = `
CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    raw_messages TEXT NOT NULL,
    provider TEXT NOT NULL,
    file_last_access TEXT,
    usage TEXT NOT NULL,
    summary TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    metadata TEXT,
    tool_results TEXT
);
`

// createConversationSummariesTable creates the denormalized summaries table
const createConversationSummariesTable = `
CREATE TABLE IF NOT EXISTS conversation_summaries (
    id TEXT PRIMARY KEY,
    message_count INTEGER NOT NULL,
    first_message TEXT NOT NULL,
    summary TEXT,
    usage TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
`

// Schema version 2 indexes
const createIndexConversationsCreatedAt = `
CREATE INDEX IF NOT EXISTS idx_conversations_created_at ON conversations(created_at DESC);
`

const createIndexConversationsUpdatedAt = `
CREATE INDEX IF NOT EXISTS idx_conversations_updated_at ON conversations(updated_at DESC);
`

const createIndexConversationsProvider = `
CREATE INDEX IF NOT EXISTS idx_conversations_provider ON conversations(provider);
`

const createIndexSummariesCreatedAt = `
CREATE INDEX IF NOT EXISTS idx_summaries_created_at ON conversation_summaries(created_at DESC);
`

const createIndexSummariesUpdatedAt = `
CREATE INDEX IF NOT EXISTS idx_summaries_updated_at ON conversation_summaries(updated_at DESC);
`

const createIndexSummariesMessageCount = `
CREATE INDEX IF NOT EXISTS idx_summaries_message_count ON conversation_summaries(message_count);
`

const createIndexSummariesFirstMessage = `
CREATE INDEX IF NOT EXISTS idx_summaries_first_message ON conversation_summaries(first_message);
`

const createIndexSummariesSummary = `
CREATE INDEX IF NOT EXISTS idx_summaries_summary ON conversation_summaries(summary);
`

// Schema version 3 changes
const addProviderToSummariesTable = `
ALTER TABLE conversation_summaries ADD COLUMN provider TEXT;
`

const createIndexSummariesProvider = `
CREATE INDEX IF NOT EXISTS idx_summaries_provider ON conversation_summaries(provider);
`

// Drop indexes for rollback
const dropIndexConversationsCreatedAt = `DROP INDEX IF EXISTS idx_conversations_created_at;`
const dropIndexConversationsUpdatedAt = `DROP INDEX IF EXISTS idx_conversations_updated_at;`
const dropIndexConversationsProvider = `DROP INDEX IF EXISTS idx_conversations_provider;`
const dropIndexSummariesCreatedAt = `DROP INDEX IF EXISTS idx_summaries_created_at;`
const dropIndexSummariesUpdatedAt = `DROP INDEX IF EXISTS idx_summaries_updated_at;`
const dropIndexSummariesMessageCount = `DROP INDEX IF EXISTS idx_summaries_message_count;`
const dropIndexSummariesFirstMessage = `DROP INDEX IF EXISTS idx_summaries_first_message;`
const dropIndexSummariesSummary = `DROP INDEX IF EXISTS idx_summaries_summary;`
const dropIndexSummariesProvider = `DROP INDEX IF EXISTS idx_summaries_provider;`

// Schema version 4 changes - Add background_processes column
const addBackgroundProcessesToConversationsTable = `
ALTER TABLE conversations ADD COLUMN background_processes TEXT;
`
