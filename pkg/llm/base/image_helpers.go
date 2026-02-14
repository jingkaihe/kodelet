package base

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

// ValidateHTTPSImageURL validates that an image URL uses HTTPS.
func ValidateHTTPSImageURL(url string) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.Errorf("only HTTPS URLs are supported for security: %s", url)
	}
	return nil
}

// ValidateDataURLPrefix validates that a string starts with "data:".
func ValidateDataURLPrefix(dataURL string) error {
	if !strings.HasPrefix(dataURL, "data:") {
		return errors.New("invalid data URL: must start with 'data:'")
	}
	return nil
}

// ParseBase64DataURL parses data URLs with the format: data:<mediatype>;base64,<data>.
func ParseBase64DataURL(dataURL string) (string, string, error) {
	if err := ValidateDataURLPrefix(dataURL); err != nil {
		return "", "", err
	}

	rest := strings.TrimPrefix(dataURL, "data:")
	parts := strings.SplitN(rest, ";base64,", 2)
	if len(parts) != 2 {
		return "", "", errors.New("invalid data URL: must contain ';base64,' separator")
	}

	return parts[0], parts[1], nil
}

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

// ReadImageFileAsDataURL validates an image file and returns a data URL.
func ReadImageFileAsDataURL(filePath string) (string, error) {
	mimeType, base64Data, err := ReadImageFileAsBase64(filePath)
	if err != nil {
		return "", err
	}

	return "data:" + mimeType + ";base64," + base64Data, nil
}
