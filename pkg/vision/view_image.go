// Package vision provides provider-neutral image preprocessing helpers.
package vision

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	_ "golang.org/x/image/webp"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

const (
	maxImageFileSize   = 5 * 1024 * 1024
	ViewImageMaxWidth  = 2048
	ViewImageMaxHeight = 768
)

// Result stores the processed image payload used across providers.
type Result struct {
	Path      string
	ImageURL  string
	MimeType  string
	Width     int
	Height    int
	Detail    string
	Assistant string
}

// SupportsViewImageOriginalDetail returns true when the model supports the optional original detail mode.
func SupportsViewImageOriginalDetail(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "gpt-5.3-codex")
}

// SupportsImageInputs reports whether the current provider/model combination can accept image inputs.
func SupportsImageInputs(provider, model string) bool {
	_ = provider
	_ = model
	return true
}

// NormalizeViewImageDetail validates and normalizes the optional detail field.
func NormalizeViewImageDetail(detail, model string) (string, error) {
	normalized := strings.TrimSpace(detail)
	if normalized == "" {
		return "", nil
	}
	if normalized != "original" {
		return "", errors.Errorf("view_image.detail only supports `original`; omit `detail` for default resized behavior, got %q", normalized)
	}
	if !SupportsViewImageOriginalDetail(model) {
		return "", errors.New("view_image.detail only supports `original` on compatible models; omit `detail` for default resized behavior")
	}
	return normalized, nil
}

// MakeViewImageResult validates and preprocesses a local image path into a provider-neutral data URL plus dimensions metadata.
func MakeViewImageResult(path string, detail string, model string, provider string) (*Result, error) {
	if !SupportsImageInputs(provider, model) {
		return nil, errors.New("view_image is not allowed because the current model may not support image inputs")
	}

	normalizedDetail, err := NormalizeViewImageDetail(detail, model)
	if err != nil {
		return nil, err
	}

	cleanPath := strings.TrimSpace(strings.TrimPrefix(path, "file://"))
	if cleanPath == "" {
		return nil, errors.New("unable to locate image at ``")
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.Errorf("unable to locate image at `%s`", cleanPath)
		}
		return nil, errors.Wrap(err, "failed to stat image path")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.Errorf("image path `%s` is not a file", cleanPath)
	}

	fileBytes, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read image file")
	}
	if len(fileBytes) > maxImageFileSize {
		return nil, errors.Errorf("image file too large: %d bytes (max: %d bytes)", len(fileBytes), maxImageFileSize)
	}

	img, format, err := image.Decode(bytes.NewReader(fileBytes))
	if err != nil {
		guessedMime := mime.TypeByExtension(strings.ToLower(filepath.Ext(cleanPath)))
		if guessedMime == "" {
			guessedMime = "unknown"
		}
		return nil, errors.Wrapf(err, "unable to process image at `%s`: unsupported image `%s`", cleanPath, guessedMime)
	}

	bounds := img.Bounds()
	originalWidth := bounds.Dx()
	originalHeight := bounds.Dy()
	mimeType, err := mimeTypeForDecodedImage(format)
	if err != nil {
		return nil, err
	}
	if _, err := base64ImageSourceMediaType(mimeType); err != nil {
		return nil, err
	}

	outputBytes := fileBytes
	outputWidth := originalWidth
	outputHeight := originalHeight
	useOriginal := normalizedDetail == "original"

	if !useOriginal && (originalWidth > ViewImageMaxWidth || originalHeight > ViewImageMaxHeight) {
		scale := math.Min(float64(ViewImageMaxWidth)/float64(originalWidth), float64(ViewImageMaxHeight)/float64(originalHeight))
		outputWidth = maxInt(1, int(math.Floor(float64(originalWidth)*scale)))
		outputHeight = maxInt(1, int(math.Floor(float64(originalHeight)*scale)))
		resized := resizeImageNearest(img, outputWidth, outputHeight)
		outputBytes, mimeType, err = encodeImageBytes(resized, mimeType)
		if err != nil {
			return nil, err
		}
	}

	encoded := base64.StdEncoding.EncodeToString(outputBytes)
	dataURL := dataURLFromBase64Payload(mimeType, encoded)
	result := &Result{
		Path:     cleanPath,
		ImageURL: dataURL,
		MimeType: mimeType,
		Width:    outputWidth,
		Height:   outputHeight,
		Detail:   normalizedDetail,
	}
	result.Assistant = fmt.Sprintf("Viewed image %s (%dx%d, %s)", cleanPath, result.Width, result.Height, mimeType)
	return result, nil
}

// MetadataFromResult converts a processed image result into structured metadata.
func MetadataFromResult(result *Result) *tooltypes.ViewImageMetadata {
	if result == nil {
		return nil
	}
	return &tooltypes.ViewImageMetadata{
		Path:     result.Path,
		MimeType: result.MimeType,
		Detail:   result.Detail,
		ImageSize: tooltypes.ImageDimensions{
			Width:  result.Width,
			Height: result.Height,
		},
	}
}

func mimeTypeForDecodedImage(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg", "jpg":
		return "image/jpeg", nil
	case "png":
		return "image/png", nil
	case "gif":
		return "image/gif", nil
	case "webp":
		return "image/webp", nil
	default:
		return "", errors.Errorf("unsupported decoded image format: %s", strings.TrimSpace(format))
	}
}

func encodeImageBytes(img image.Image, mimeType string) ([]byte, string, error) {
	var buf bytes.Buffer
	switch mimeType {
	case "image/jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", errors.Wrap(err, "failed to encode resized jpeg image")
		}
		return buf.Bytes(), mimeType, nil
	case "image/gif":
		if err := gif.Encode(&buf, img, nil); err != nil {
			return nil, "", errors.Wrap(err, "failed to encode resized gif image")
		}
		return buf.Bytes(), mimeType, nil
	case "image/png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", errors.Wrap(err, "failed to encode resized png image")
		}
		return buf.Bytes(), mimeType, nil
	case "image/webp":
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", errors.Wrap(err, "failed to encode resized png image")
		}
		return buf.Bytes(), "image/png", nil
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", errors.Wrap(err, "failed to encode resized image")
		}
		return buf.Bytes(), "image/png", nil
	}
}

func resizeImageNearest(src image.Image, width int, height int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	srcBounds := src.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()
	for y := 0; y < height; y++ {
		srcY := srcBounds.Min.Y + (y*srcHeight)/height
		for x := 0; x < width; x++ {
			srcX := srcBounds.Min.X + (x*srcWidth)/width
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func dataURLFromBase64Payload(mimeType, base64Data string) string {
	return "data:" + mimeType + ";base64," + base64Data
}

func base64ImageSourceMediaType(mimeType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return strings.ToLower(strings.TrimSpace(mimeType)), nil
	default:
		return "", errors.Errorf("unsupported image mime type: %s", mimeType)
	}
}
