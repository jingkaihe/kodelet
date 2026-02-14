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
	if base.IsInsecureHTTPURL(imagePath) {
		return responses.ResponseInputContentUnionParam{}, errors.Errorf("only HTTPS URLs are supported for security: %s", imagePath)
	}

	fromURL := func(path string) (responses.ResponseInputContentUnionParam, error) {
		return responses.ResponseInputContentUnionParam{
			OfInputImage: &responses.ResponseInputImageParam{
				ImageURL: param.NewOpt(path),
			},
		}, nil
	}

	return base.RouteImageInput(
		imagePath,
		fromURL,
		fromURL,
		func(path string) (responses.ResponseInputContentUnionParam, error) {
			dataURL, err := base.ReadImageFileAsDataURL(path)
			if err != nil {
				if strings.Contains(err.Error(), "image file not found") {
					return responses.ResponseInputContentUnionParam{}, errors.Wrap(err, "failed to stat image file")
				}
				return responses.ResponseInputContentUnionParam{}, err
			}

			return responses.ResponseInputContentUnionParam{
				OfInputImage: &responses.ResponseInputImageParam{
					ImageURL: param.NewOpt(dataURL),
				},
			}, nil
		},
	)
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
