package responses

import (
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

	// Handle local file.
	mimeType, base64Data, err := base.ReadImageFileAsBase64(imagePath)
	if err != nil {
		if strings.Contains(err.Error(), "image file not found") {
			return responses.ResponseInputContentUnionParam{}, errors.Wrap(err, "failed to stat image file")
		}
		return responses.ResponseInputContentUnionParam{}, err
	}

	// Encode to base64 data URL.
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
	mimeType, err := base.ImageMIMETypeFromExtension(ext)
	if err != nil {
		return "", errors.Errorf("unsupported image format: %s (supported: .jpg, .jpeg, .png, .gif, .webp)", ext)
	}
	return mimeType, nil
}
