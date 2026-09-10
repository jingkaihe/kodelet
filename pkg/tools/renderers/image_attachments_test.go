package renderers

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
)

func TestImageAttachmentLines(t *testing.T) {
	for _, tc := range []struct {
		name       string
		toolName   string
		baseURL    string
		attachment tools.ToolAttachment
		want       []string
	}{
		{
			name:     "generated image uses public URL",
			toolName: "draw_chart",
			baseURL:  "https://internal.example",
			attachment: tools.ToolAttachment{
				Type:       "image",
				ArtifactID: "internal",
				ShortCode:  "public_code",
				ViewURL:    "https://images.example/i/public_code",
				Filename:   "chart.png",
				Width:      1536,
				Height:     1024,
			},
			want: []string{"Generated image - https://images.example/i/public_code"},
		},
		{
			name:     "view_image keeps original artifact link",
			toolName: "view_image",
			attachment: tools.ToolAttachment{
				Type:       "image",
				ArtifactID: "internal",
				ShortCode:  "public",
				ViewURL:    "https://images.example/i/public",
			},
			want: []string{"Viewed image - https://images.example/i/public"},
		},
		{
			name:    "relative URL uses connected server and prefix",
			baseURL: "https://internal.example/kodelet/",
			attachment: tools.ToolAttachment{
				Type:       "image",
				ArtifactID: "internal",
				ShortCode:  "public",
				ViewURL:    "/i/public",
			},
			want: []string{"Generated image - https://internal.example/kodelet/i/public"},
		},
		{
			name:    "history without a decorated URL",
			baseURL: "http://localhost:8080",
			attachment: tools.ToolAttachment{
				Type:       "image",
				ArtifactID: "internal",
				ShortCode:  "public",
			},
			want: []string{"Generated image - http://localhost:8080/i/public"},
		},
		{
			name: "no known server leaves a relative link",
			attachment: tools.ToolAttachment{
				Type:       "image",
				ArtifactID: "internal",
				ShortCode:  "public",
			},
			want: []string{"Generated image - /i/public"},
		},
		{
			name:       "failed attachment",
			attachment: tools.ToolAttachment{Type: "image", Error: "Upload\ninterrupted"},
			want:       []string{"Image unavailable - Upload interrupted"},
		},
		{
			name:       "local path is not a display URL",
			attachment: tools.ToolAttachment{Type: "image", Path: "/tmp/chart.png"},
			want:       []string{"Image unavailable"},
		},
		{
			name: "unsafe URL is not displayed",
			attachment: tools.ToolAttachment{
				Type:       "image",
				ArtifactID: "internal",
				ShortCode:  "public",
				ViewURL:    "javascript:alert(1)/i/public",
			},
			want: []string{"Image unavailable"},
		},
		{
			name: "foreign path cannot become an image URL",
			attachment: tools.ToolAttachment{
				Type:       "image",
				ArtifactID: "internal",
				ShortCode:  "../private",
			},
			want: []string{"Image unavailable"},
		},
		{
			name: "terminal controls rejected",
			attachment: tools.ToolAttachment{
				Type:       "image",
				ArtifactID: "internal",
				ShortCode:  "public",
				ViewURL:    "https://images.example/\x1b[31m/i/public",
			},
			want: []string{"Image unavailable"},
		},
		{
			name: "protocol-relative URL rejected",
			attachment: tools.ToolAttachment{
				Type:       "image",
				ArtifactID: "internal",
				ShortCode:  "public",
				ViewURL:    "//images.example/i/public",
			},
			want: []string{"Image unavailable"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := tools.StructuredToolResult{
				ToolName:    tc.toolName,
				Success:     true,
				Attachments: []tools.ToolAttachment{tc.attachment},
			}
			assert.Equal(t, tc.want, ImageAttachmentLines(result, tc.baseURL))
		})
	}
}

func TestRendererRegistryImageAttachmentsPreserveOutput(t *testing.T) {
	result := tools.StructuredToolResult{
		ToolName: "draw_chart",
		Success:  true,
		Metadata: &tools.ExtensionToolMetadata{
			ToolName: "draw_chart",
			Output:   "Chart generated from 20 measurements.",
		},
		Attachments: []tools.ToolAttachment{
			{
				Type:       "image",
				ArtifactID: "first",
				ShortCode:  "first",
				ViewURL:    "https://example.com/i/first",
			},
			{
				Type:       "image",
				ArtifactID: "second",
				ShortCode:  "second",
				ViewURL:    "https://example.com/i/second",
			},
		},
	}
	output := NewRendererRegistry().Render(result)
	assert.Contains(t, output, "Generated image - https://example.com/i/first\nGenerated image - https://example.com/i/second")
	assert.Contains(t, output, "Chart generated from 20 measurements.")
}
