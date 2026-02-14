package responses

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm/base"
	"github.com/pkg/errors"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// processImage processes an image path and returns a content part for the Responses API.
// Supports local files, URLs, and data URLs.
func processImage(imagePath string) (responses.ResponseInputContentUnionParam, error) {
	// Check if it's an HTTPS URL
	if strings.HasPrefix(imagePath, "https://") {
		return responses.ResponseInputContentUnionParam{
			OfInputImage: &responses.ResponseInputImageParam{
				ImageURL: param.NewOpt(imagePath),
			},
		}, nil
	}
	if strings.HasPrefix(imagePath, "http://") {
		return responses.ResponseInputContentUnionParam{}, errors.Errorf("only HTTPS URLs are supported for security: %s", imagePath)
	}

	// Check if it's already a data URL (e.g., from ACP)
	if strings.HasPrefix(imagePath, "data:") {
		return responses.ResponseInputContentUnionParam{
			OfInputImage: &responses.ResponseInputImageParam{
				ImageURL: param.NewOpt(imagePath),
			},
		}, nil
	}

	if filePath, ok := strings.CutPrefix(imagePath, "file://"); ok {
		imagePath = filePath
	}

	// Handle local file
	fileInfo, err := os.Stat(imagePath)
	if err != nil {
		return responses.ResponseInputContentUnionParam{}, errors.Wrap(err, "failed to stat image file")
	}
	if fileInfo.Size() > base.MaxImageFileSize {
		return responses.ResponseInputContentUnionParam{},
			errors.Errorf("image file too large: %d bytes (max: %d bytes)", fileInfo.Size(), base.MaxImageFileSize)
	}

	data, err := os.ReadFile(imagePath)
	if err != nil {
		return responses.ResponseInputContentUnionParam{}, errors.Wrap(err, "failed to read image file")
	}

	// Determine MIME type from extension
	mimeType, err := getMimeType(imagePath)
	if err != nil {
		return responses.ResponseInputContentUnionParam{}, err
	}

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
func getMimeType(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".png":
		return "image/png", nil
	case ".gif":
		return "image/gif", nil
	case ".webp":
		return "image/webp", nil
	default:
		return "", errors.Errorf("unsupported image format: %s (supported: .jpg, .jpeg, .png, .gif, .webp)", ext)
	}
}
