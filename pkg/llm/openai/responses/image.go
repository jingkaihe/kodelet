package responses

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// processImage processes an image path and returns a content part for the Responses API.
// Supports local files, URLs, and data URLs.
func processImage(imagePath string) (responses.ResponseInputContentUnionParam, error) {
	// Check if it's a URL
	if strings.HasPrefix(imagePath, "http://") || strings.HasPrefix(imagePath, "https://") {
		return responses.ResponseInputContentUnionParam{
			OfInputImage: &responses.ResponseInputImageParam{
				ImageURL: param.NewOpt(imagePath),
			},
		}, nil
	}

	// Check if it's already a data URL (e.g., from ACP)
	if strings.HasPrefix(imagePath, "data:") {
		return responses.ResponseInputContentUnionParam{
			OfInputImage: &responses.ResponseInputImageParam{
				ImageURL: param.NewOpt(imagePath),
			},
		}, nil
	}

	// Handle local file
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return responses.ResponseInputContentUnionParam{}, errors.Wrap(err, "failed to read image file")
	}

	// Determine MIME type from extension
	mimeType := getMimeType(imagePath)

	// Encode to base64 data URL
	base64Data := base64.StdEncoding.EncodeToString(data)
	dataURL := "data:" + mimeType + ";base64," + base64Data

	return responses.ResponseInputContentUnionParam{
		OfInputImage: &responses.ResponseInputImageParam{
			ImageURL: param.NewOpt(dataURL),
		},
	}, nil
}

// getMimeType returns the MIME type for an image file based on its extension.
func getMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		// Default to JPEG for unknown types
		return "image/jpeg"
	}
}
