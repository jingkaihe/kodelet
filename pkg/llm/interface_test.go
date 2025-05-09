package llm

import (
	"testing"
)

func TestProviderFactory(t *testing.T) {
	// Test with Anthropic provider
	anthOptions := ProviderOptions{
		APIKey:    "test-api-key",
		Model:     "claude-3-7-sonnet-latest",
		MaxTokens: 1024,
	}

	anthProvider, err := NewProvider("anthropic", anthOptions)
	if err != nil {
		t.Fatalf("Failed to create Anthropic provider: %v", err)
	}

	if anthProvider == nil {
		t.Fatal("Anthropic provider is nil")
	}

	// Test with OpenAI provider
	openaiOptions := ProviderOptions{
		APIKey:    "test-api-key",
		Model:     "gpt-4o",
		MaxTokens: 1024,
	}

	openaiProvider, err := NewProvider("openai", openaiOptions)
	if err != nil {
		t.Fatalf("Failed to create OpenAI provider: %v", err)
	}

	if openaiProvider == nil {
		t.Fatal("OpenAI provider is nil")
	}

	// Test with unsupported provider
	_, err = NewProvider("unsupported", ProviderOptions{})
	if err == nil {
		t.Fatal("Expected error for unsupported provider, but got nil")
	}
}

func TestGetProviderFromConfig(t *testing.T) {
	// This test is a simple smoke test that doesn't actually connect
	// to any services, just verifies the function exists and returns
	// the expected error for missing API keys

	_, err := GetProviderFromConfig()

	// We expect an error since we don't have API keys in test environment
	if err == nil {
		t.Fatal("Expected error due to missing API keys, but got nil")
	}
}
