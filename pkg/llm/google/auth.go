package google

import (
	"os"
	"strings"

	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func detectBackend(config llmtypes.Config) string {
	// 1. Explicit configuration takes precedence
	if config.Google != nil && config.Google.Backend != "" {
		return strings.ToLower(config.Google.Backend)
	}

	// 2. Check environment variable for explicit backend preference
	if envBackend := os.Getenv("GOOGLE_GENAI_USE_VERTEXAI"); envBackend != "" {
		if strings.ToLower(envBackend) == "true" || envBackend == "1" {
			return "vertexai"
		}
		return "gemini"
	}

	// 3. Auto-detect based on available configuration and environment
	
	// Check for Vertex AI indicators
	hasVertexAIConfig := false
	if config.Google != nil {
		hasVertexAIConfig = config.Google.Project != "" || config.Google.Location != ""
	}
	
	// Check for Vertex AI environment variables
	hasVertexAIEnv := os.Getenv("GOOGLE_CLOUD_PROJECT") != "" ||
		os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" ||
		os.Getenv("GCLOUD_PROJECT") != ""
		
	// Check for Gemini API key
	hasGeminiAPIKey := config.Google != nil && config.Google.APIKey != ""
	
	// Check for Gemini API key in environment
	hasGeminiAPIKeyEnv := os.Getenv("GOOGLE_API_KEY") != "" ||
		os.Getenv("GEMINI_API_KEY") != ""

	// Decision logic:
	// - If explicit API key is provided, prefer Gemini API (user choice)
	// - If Vertex AI config is provided, use Vertex AI
	// - If only environment variables exist, prefer Vertex AI (enterprise grade)
	// - If neither is explicitly configured, default to Gemini API
	if hasGeminiAPIKey {
		return "gemini"
	}
	
	if hasVertexAIConfig {
		return "vertexai"
	}
	
	if hasVertexAIEnv {
		return "vertexai"
	}
	
	if hasGeminiAPIKeyEnv {
		return "gemini"
	}

	// Default to Gemini API if no clear indicators
	return "gemini"
}