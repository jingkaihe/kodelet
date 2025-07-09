package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func BenchmarkJSONStore_Save(b *testing.B) {
	ctx := context.Background()
	tempDir := b.TempDir()

	store, err := NewJSONConversationStore(ctx, tempDir)
	if err != nil {
		b.Fatalf("Failed to create JSON store: %v", err)
	}
	defer store.Close()

	record := createBenchmarkRecord()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		record.ID = fmt.Sprintf("bench-json-save-%d", i)
		if err := store.Save(record); err != nil {
			b.Fatalf("Failed to save record: %v", err)
		}
	}
}

func BenchmarkBBoltStore_Save(b *testing.B) {
	ctx := context.Background()
	tempDir := b.TempDir()
	dbPath := filepath.Join(tempDir, "benchmark.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		b.Fatalf("Failed to create BBolt store: %v", err)
	}
	defer store.Close()

	record := createBenchmarkRecord()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		record.ID = fmt.Sprintf("bench-bbolt-save-%d", i)
		if err := store.Save(record); err != nil {
			b.Fatalf("Failed to save record: %v", err)
		}
	}
}

func BenchmarkJSONStore_Load(b *testing.B) {
	ctx := context.Background()
	tempDir := b.TempDir()

	store, err := NewJSONConversationStore(ctx, tempDir)
	if err != nil {
		b.Fatalf("Failed to create JSON store: %v", err)
	}
	defer store.Close()

	// Pre-populate with test data
	numRecords := 1000
	for i := 0; i < numRecords; i++ {
		record := createBenchmarkRecord()
		record.ID = fmt.Sprintf("bench-json-load-%d", i)
		if err := store.Save(record); err != nil {
			b.Fatalf("Failed to save record: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("bench-json-load-%d", i%numRecords)
		if _, err := store.Load(id); err != nil {
			b.Fatalf("Failed to load record: %v", err)
		}
	}
}

func BenchmarkBBoltStore_Load(b *testing.B) {
	ctx := context.Background()
	tempDir := b.TempDir()
	dbPath := filepath.Join(tempDir, "benchmark.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		b.Fatalf("Failed to create BBolt store: %v", err)
	}
	defer store.Close()

	// Pre-populate with test data
	numRecords := 1000
	for i := 0; i < numRecords; i++ {
		record := createBenchmarkRecord()
		record.ID = fmt.Sprintf("bench-bbolt-load-%d", i)
		if err := store.Save(record); err != nil {
			b.Fatalf("Failed to save record: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("bench-bbolt-load-%d", i%numRecords)
		if _, err := store.Load(id); err != nil {
			b.Fatalf("Failed to load record: %v", err)
		}
	}
}

func BenchmarkJSONStore_List(b *testing.B) {
	ctx := context.Background()
	tempDir := b.TempDir()

	store, err := NewJSONConversationStore(ctx, tempDir)
	if err != nil {
		b.Fatalf("Failed to create JSON store: %v", err)
	}
	defer store.Close()

	// Pre-populate with test data
	numRecords := 1000
	for i := 0; i < numRecords; i++ {
		record := createBenchmarkRecord()
		record.ID = fmt.Sprintf("bench-json-list-%d", i)
		if err := store.Save(record); err != nil {
			b.Fatalf("Failed to save record: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.List(); err != nil {
			b.Fatalf("Failed to list records: %v", err)
		}
	}
}

func BenchmarkBBoltStore_List(b *testing.B) {
	ctx := context.Background()
	tempDir := b.TempDir()
	dbPath := filepath.Join(tempDir, "benchmark.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		b.Fatalf("Failed to create BBolt store: %v", err)
	}
	defer store.Close()

	// Pre-populate with test data
	numRecords := 1000
	for i := 0; i < numRecords; i++ {
		record := createBenchmarkRecord()
		record.ID = fmt.Sprintf("bench-bbolt-list-%d", i)
		if err := store.Save(record); err != nil {
			b.Fatalf("Failed to save record: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.List(); err != nil {
			b.Fatalf("Failed to list records: %v", err)
		}
	}
}

func BenchmarkJSONStore_Search(b *testing.B) {
	ctx := context.Background()
	tempDir := b.TempDir()

	store, err := NewJSONConversationStore(ctx, tempDir)
	if err != nil {
		b.Fatalf("Failed to create JSON store: %v", err)
	}
	defer store.Close()

	// Pre-populate with test data
	numRecords := 1000
	for i := 0; i < numRecords; i++ {
		record := createBenchmarkRecord()
		record.ID = fmt.Sprintf("bench-json-search-%d", i)
		if i%10 == 0 {
			record.Summary = "This is a special searchable record"
		}
		if err := store.Save(record); err != nil {
			b.Fatalf("Failed to save record: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.Query(QueryOptions{SearchTerm: "searchable"}); err != nil {
			b.Fatalf("Failed to search records: %v", err)
		}
	}
}

func BenchmarkBBoltStore_Search(b *testing.B) {
	ctx := context.Background()
	tempDir := b.TempDir()
	dbPath := filepath.Join(tempDir, "benchmark.db")

	store, err := NewBBoltConversationStore(ctx, dbPath)
	if err != nil {
		b.Fatalf("Failed to create BBolt store: %v", err)
	}
	defer store.Close()

	// Pre-populate with test data
	numRecords := 1000
	for i := 0; i < numRecords; i++ {
		record := createBenchmarkRecord()
		record.ID = fmt.Sprintf("bench-bbolt-search-%d", i)
		if i%10 == 0 {
			record.Summary = "This is a special searchable record"
		}
		if err := store.Save(record); err != nil {
			b.Fatalf("Failed to save record: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.Query(QueryOptions{SearchTerm: "searchable"}); err != nil {
			b.Fatalf("Failed to search records: %v", err)
		}
	}
}

func createBenchmarkRecord() ConversationRecord {
	record := NewConversationRecord("")
	record.Summary = "This is a benchmark conversation about various topics"
	record.ModelType = "anthropic"
	record.Usage = llmtypes.Usage{
		InputTokens:  500,
		OutputTokens: 200,
		InputCost:    0.001,
		OutputCost:   0.002,
	}
	record.RawMessages = json.RawMessage(`[
		{"role": "user", "content": [{"type": "text", "text": "Hello, I need help with a complex programming task involving database optimization and performance tuning."}]},
		{"role": "assistant", "content": [{"type": "text", "text": "I'd be happy to help you with database optimization and performance tuning. This is a complex topic that involves understanding query patterns, indexing strategies, and system architecture considerations."}]},
		{"role": "user", "content": [{"type": "text", "text": "Can you explain different indexing strategies and when to use them?"}]},
		{"role": "assistant", "content": [{"type": "text", "text": "Certainly! There are several key indexing strategies: B-tree indexes are great for range queries and equality lookups, hash indexes are optimal for exact matches, and composite indexes can optimize queries with multiple WHERE conditions."}]}
	]`)
	return record
}

// Benchmark function to compare overall performance
func BenchmarkStoreComparison(b *testing.B) {
	// This benchmark will show relative performance between stores
	numRecords := 100

	b.Run("JSON_Overall", func(b *testing.B) {
		ctx := context.Background()
		tempDir := b.TempDir()
		store, err := NewJSONConversationStore(ctx, tempDir)
		if err != nil {
			b.Fatalf("Failed to create JSON store: %v", err)
		}
		defer store.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Save some records
			for j := 0; j < numRecords; j++ {
				record := createBenchmarkRecord()
				record.ID = fmt.Sprintf("overall-json-%d-%d", i, j)
				store.Save(record)
			}
			// List all records
			store.List()
			// Search for records
			store.Query(QueryOptions{SearchTerm: "programming"})
		}
	})

	b.Run("BBolt_Overall", func(b *testing.B) {
		ctx := context.Background()
		tempDir := b.TempDir()
		dbPath := filepath.Join(tempDir, "benchmark.db")
		store, err := NewBBoltConversationStore(ctx, dbPath)
		if err != nil {
			b.Fatalf("Failed to create BBolt store: %v", err)
		}
		defer store.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Save some records
			for j := 0; j < numRecords; j++ {
				record := createBenchmarkRecord()
				record.ID = fmt.Sprintf("overall-bbolt-%d-%d", i, j)
				store.Save(record)
			}
			// List all records
			store.List()
			// Search for records
			store.Query(QueryOptions{SearchTerm: "programming"})
		}
	})
}
