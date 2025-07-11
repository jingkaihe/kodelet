package renderers

import (
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestImageRecognitionRenderer(t *testing.T) {
	renderer := &ImageRecognitionRenderer{}

	t.Run("Successful image recognition with all fields", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ImageRecognitionMetadata{
				ImagePath: "/test/image.png",
				ImageType: "screenshot",
				Prompt:    "Describe what you see in this image",
				Analysis:  "The image shows a desktop with multiple windows open, including a web browser and a text editor. The desktop has a blue wallpaper with cloud formations.",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Image Recognition: /test/image.png", "Expected image path in output")
		assert.Contains(t, output, "Type: screenshot", "Expected image type in output")
		assert.Contains(t, output, "Prompt: Describe what you see in this image", "Expected prompt in output")
		assert.Contains(t, output, "Analysis:\nThe image shows a desktop", "Expected analysis in output")
	})

	t.Run("Image recognition with diagram type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ImageRecognitionMetadata{
				ImagePath: "/test/diagram.jpg",
				ImageType: "diagram",
				Prompt:    "Explain the architecture shown in this diagram",
				Analysis:  "This is a system architecture diagram showing a microservices architecture with API Gateway, authentication service, and multiple backend services connected through a message queue.",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Image Recognition: /test/diagram.jpg", "Expected image path in output")
		assert.Contains(t, output, "Type: diagram", "Expected image type in output")
		assert.Contains(t, output, "microservices architecture", "Expected analysis content in output")
	})

	t.Run("Image recognition with photo type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ImageRecognitionMetadata{
				ImagePath: "/test/photo.jpg",
				ImageType: "photo",
				Prompt:    "What is happening in this photo?",
				Analysis:  "The photo shows a sunset over a mountain range with clouds in the sky. The lighting is golden and creates a dramatic silhouette effect.",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Image Recognition: /test/photo.jpg", "Expected image path in output")
		assert.Contains(t, output, "Type: photo", "Expected image type in output")
		assert.Contains(t, output, "sunset over a mountain range", "Expected analysis content in output")
	})

	t.Run("Image recognition with empty fields", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ImageRecognitionMetadata{
				ImagePath: "/test/empty.png",
				ImageType: "",
				Prompt:    "",
				Analysis:  "",
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Image Recognition: /test/empty.png", "Expected image path in output")
		assert.Contains(t, output, "Type: \n", "Expected empty type field in output")
		assert.Contains(t, output, "Prompt: \n", "Expected empty prompt field in output")
		assert.Contains(t, output, "Analysis:\n", "Expected empty analysis field in output")
	})

	t.Run("Image recognition with long analysis", func(t *testing.T) {
		longAnalysis := "This is a very detailed analysis of the image that contains multiple paragraphs and detailed descriptions. " +
			"It covers various aspects of the image including composition, colors, lighting, and subject matter. " +
			"The analysis demonstrates the capability to process complex visual information and provide comprehensive insights."

		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata: &tools.ImageRecognitionMetadata{
				ImagePath: "/test/complex.png",
				ImageType: "complex",
				Prompt:    "Provide a detailed analysis of this image",
				Analysis:  longAnalysis,
			},
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Image Recognition: /test/complex.png", "Expected image path in output")
		assert.Contains(t, output, "detailed analysis of the image", "Expected analysis content in output")
		assert.Contains(t, output, "comprehensive insights", "Expected full analysis content in output")
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   false,
			Error:     "Unable to process image: file not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Unable to process image: file not found", "Expected error message in output")
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for image_recognition", "Expected invalid metadata error")
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		assert.Contains(t, output, "Error: Invalid metadata type for image_recognition", "Expected invalid metadata error for nil metadata")
	})
}
