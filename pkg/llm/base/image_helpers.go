package base

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

// ImageMIMETypeFromExtension returns the MIME type for supported image extensions.
func ImageMIMETypeFromExtension(ext string) (string, error) {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".png":
		return "image/png", nil
	case ".gif":
		return "image/gif", nil
	case ".webp":
		return "image/webp", nil
	default:
		return "", errors.New("unsupported format")
	}
}

// ReadImageFileAsBase64 validates an image file and returns its MIME type and base64 payload.
func ReadImageFileAsBase64(filePath string) (string, string, error) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", "", errors.Errorf("image file not found: %s", filePath)
	}

	// Determine media type from file extension first.
	mimeType, err := ImageMIMETypeFromExtension(filepath.Ext(filePath))
	if err != nil {
		return "", "", errors.Errorf(
			"unsupported image format: %s (supported: .jpg, .jpeg, .png, .gif, .webp)",
			filepath.Ext(filePath),
		)
	}

	// Check file size.
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to get file info")
	}
	if fileInfo.Size() > MaxImageFileSize {
		return "", "", errors.Errorf("image file too large: %d bytes (max: %d bytes)", fileInfo.Size(), MaxImageFileSize)
	}

	imageData, err := os.ReadFile(filePath)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to read image file")
	}

	return mimeType, base64.StdEncoding.EncodeToString(imageData), nil
}
