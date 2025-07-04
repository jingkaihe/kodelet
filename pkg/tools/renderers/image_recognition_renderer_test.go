package renderers

import (
	"strings"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
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

		if !strings.Contains(output, "Image Recognition: /test/image.png") {
			t.Errorf("Expected image path in output, got: %s", output)
		}
		if !strings.Contains(output, "Type: screenshot") {
			t.Errorf("Expected image type in output, got: %s", output)
		}
		if !strings.Contains(output, "Prompt: Describe what you see in this image") {
			t.Errorf("Expected prompt in output, got: %s", output)
		}
		if !strings.Contains(output, "Analysis:\nThe image shows a desktop") {
			t.Errorf("Expected analysis in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Image Recognition: /test/diagram.jpg") {
			t.Errorf("Expected image path in output, got: %s", output)
		}
		if !strings.Contains(output, "Type: diagram") {
			t.Errorf("Expected image type in output, got: %s", output)
		}
		if !strings.Contains(output, "microservices architecture") {
			t.Errorf("Expected analysis content in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Image Recognition: /test/photo.jpg") {
			t.Errorf("Expected image path in output, got: %s", output)
		}
		if !strings.Contains(output, "Type: photo") {
			t.Errorf("Expected image type in output, got: %s", output)
		}
		if !strings.Contains(output, "sunset over a mountain range") {
			t.Errorf("Expected analysis content in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Image Recognition: /test/empty.png") {
			t.Errorf("Expected image path in output, got: %s", output)
		}
		if !strings.Contains(output, "Type: \n") {
			t.Errorf("Expected empty type field in output, got: %s", output)
		}
		if !strings.Contains(output, "Prompt: \n") {
			t.Errorf("Expected empty prompt field in output, got: %s", output)
		}
		if !strings.Contains(output, "Analysis:\n") {
			t.Errorf("Expected empty analysis field in output, got: %s", output)
		}
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

		if !strings.Contains(output, "Image Recognition: /test/complex.png") {
			t.Errorf("Expected image path in output, got: %s", output)
		}
		if !strings.Contains(output, "detailed analysis of the image") {
			t.Errorf("Expected analysis content in output, got: %s", output)
		}
		if !strings.Contains(output, "comprehensive insights") {
			t.Errorf("Expected full analysis content in output, got: %s", output)
		}
	})

	t.Run("Error handling", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   false,
			Error:     "Unable to process image: file not found",
			Timestamp: time.Now(),
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Unable to process image: file not found") {
			t.Errorf("Expected error message in output, got: %s", output)
		}
	})

	t.Run("Invalid metadata type", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  &tools.FileReadMetadata{}, // Wrong type
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for image_recognition") {
			t.Errorf("Expected invalid metadata error, got: %s", output)
		}
	})

	t.Run("Nil metadata", func(t *testing.T) {
		result := tools.StructuredToolResult{
			ToolName:  "image_recognition",
			Success:   true,
			Timestamp: time.Now(),
			Metadata:  nil,
		}

		output := renderer.RenderCLI(result)

		if !strings.Contains(output, "Error: Invalid metadata type for image_recognition") {
			t.Errorf("Expected invalid metadata error for nil metadata, got: %s", output)
		}
	})
}