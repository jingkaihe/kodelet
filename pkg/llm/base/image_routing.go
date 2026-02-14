package base

import "strings"

// ImageInputKind describes the source type for an image argument.
type ImageInputKind int

const (
	// ImageInputHTTPSURL is an HTTPS URL image source.
	ImageInputHTTPSURL ImageInputKind = iota
	// ImageInputDataURL is a data URL image source.
	ImageInputDataURL
	// ImageInputLocalFile is a local file path image source.
	ImageInputLocalFile
)

// ResolveImageInputPath classifies and normalizes an image input path.
// file:// paths are normalized into local file paths.
func ResolveImageInputPath(imagePath string) (ImageInputKind, string) {
	if strings.HasPrefix(imagePath, "https://") {
		return ImageInputHTTPSURL, imagePath
	}
	if strings.HasPrefix(imagePath, "data:") {
		return ImageInputDataURL, imagePath
	}
	if filePath, ok := strings.CutPrefix(imagePath, "file://"); ok {
		return ImageInputLocalFile, filePath
	}
	return ImageInputLocalFile, imagePath
}

// IsInsecureHTTPURL returns true when the image path starts with plain HTTP.
func IsInsecureHTTPURL(imagePath string) bool {
	return strings.HasPrefix(imagePath, "http://")
}
