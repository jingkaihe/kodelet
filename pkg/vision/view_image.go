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
	"io"
	"math"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	_ "golang.org/x/image/webp"

	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

var modelsSupportingOriginalImageDetail = map[string]struct{}{
	// OpenAI API or Codex platform models with an `original` image detail level.
	"gpt-6.2-astra": {},
	"gpt-6.2-sol":   {},
	"gpt-6.1-terra": {},
	"gpt-6.1-sol":   {},
	"gpt-6-astra":   {},
	"gpt-6-sol":     {},
	"gpt-6-luna":    {},
	"gpt-5.6-sol":   {},
	"gpt-5.6-terra": {},
	"gpt-5.6-luna":  {},
	"gpt-5.5-pro":   {},
	"gpt-5.5":       {},
	"gpt-5.4-pro":   {},
	"gpt-5.4":       {},
	"gpt-5.4-mini":  {},
	"gpt-5.3-codex": {},
	// Anthropic models with automatic high-resolution image input.
	"claude-fable-5-2":  {},
	"claude-mythos-5-2": {},
	"claude-sonnet-5-5": {},
	"claude-fable-5-1":  {},
	"claude-fable-5":    {},
	"claude-mythos-5":   {},
	"claude-opus-5-5":   {},
	"claude-opus-5":     {},
	"claude-opus-4-8":   {},
	"claude-opus-4-7":   {},
	"claude-sonnet-5":   {},
}

const (
	// MaxImageInputBytes limits encoded image input before resizing for the model.
	MaxImageInputBytes            = 32 << 20
	maxDecodedImagePixels         = 40_000_000
	ViewImageMaxWidth             = 2048
	ViewImageMaxHeight            = 768
	viewImagePatchSize            = 32
	viewImageOriginalMaxDimension = 6000
	viewImageOriginalMaxPatches   = 10_000
	viewImageMaxDownscaleAttempts = 7
)

// MaxAnthropicImageBase64Size is the direct Claude API limit for each base64-encoded image.
// https://platform.claude.com/docs/en/build-with-claude/vision#request-limits
const MaxAnthropicImageBase64Size = 10_000_000

// ErrImageInputTooLarge indicates the input exceeded the limit before resizing.
var ErrImageInputTooLarge = errors.Errorf("image file too large: %d MiB file limit", MaxImageInputBytes>>20)

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
	_, ok := modelsSupportingOriginalImageDetail[strings.ToLower(strings.TrimSpace(model))]
	return ok
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

	_, err := NormalizeViewImageDetail(detail, model)
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
	if info.Size() > MaxImageInputBytes {
		return nil, ErrImageInputTooLarge
	}

	file, err := os.Open(cleanPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open image file")
	}
	defer file.Close()
	fileBytes, err := io.ReadAll(io.LimitReader(file, MaxImageInputBytes+1))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read image file")
	}
	return MakeViewImageResultBytes(fileBytes, cleanPath, detail, model, provider)
}

// ImageInputFormat checks the byte and pixel limits without decoding the pixel buffer.
func ImageInputFormat(data []byte) (string, error) {
	if len(data) > MaxImageInputBytes {
		return "", ErrImageInputTooLarge
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.Wrap(err, "unsupported image")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxDecodedImagePixels/config.Height {
		return "", errors.New("image exceeds the decoded pixel limit")
	}
	return format, nil
}

// MakeViewImageResultBytes preprocesses a persisted image without a runner filesystem round trip.
// Artifact payloads are bounded to 32 MiB and 40 million decoded pixels before decoding.
// The model-facing copy also respects Anthropic's base64 size limit, including original detail.
func MakeViewImageResultBytes(fileBytes []byte, label, detail, model, provider string) (*Result, error) {
	if !SupportsImageInputs(provider, model) {
		return nil, errors.New("view_image is not allowed because the current model may not support image inputs")
	}
	normalizedDetail, err := NormalizeViewImageDetail(detail, model)
	if err != nil {
		return nil, err
	}
	if _, err := ImageInputFormat(fileBytes); err != nil {
		return nil, errors.Wrapf(err, "unable to process image %s", label)
	}

	img, format, err := decodeImage(fileBytes)
	if err != nil {
		guessedMime := mime.TypeByExtension(strings.ToLower(filepath.Ext(label)))
		if guessedMime == "" {
			guessedMime = "unknown"
		}
		return nil, errors.Wrapf(err, "unable to process image at `%s`: unsupported image `%s`", label, guessedMime)
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

	if useOriginal {
		outputWidth, outputHeight = viewImageOutputDimensionsForLimits(
			originalWidth,
			originalHeight,
			viewImageOriginalMaxDimension,
			viewImageOriginalMaxPatches,
		)
	} else if originalWidth > ViewImageMaxWidth || originalHeight > ViewImageMaxHeight {
		scale := math.Min(float64(ViewImageMaxWidth)/float64(originalWidth), float64(ViewImageMaxHeight)/float64(originalHeight))
		outputWidth = maxInt(1, int(math.Floor(float64(originalWidth)*scale)))
		outputHeight = maxInt(1, int(math.Floor(float64(originalHeight)*scale)))
	}

	maxBase64Size := 0
	if strings.EqualFold(strings.TrimSpace(provider), "anthropic") {
		maxBase64Size = MaxAnthropicImageBase64Size
	}
	needsResize := outputWidth != originalWidth || outputHeight != originalHeight
	if needsResize || !fitsBase64Size(base64.StdEncoding.EncodedLen(len(outputBytes)), maxBase64Size) {
		// Re-encoding strips padding/metadata and converts to eight bits per
		// channel without losing transparency.
		output, err := encodeResized(img, outputWidth, outputHeight, mimeType, maxBase64Size)
		if err != nil {
			return nil, err
		}
		outputBytes, mimeType = output.data, output.mimeType
		outputWidth, outputHeight = output.width, output.height
	}

	encoded := base64.StdEncoding.EncodeToString(outputBytes)
	dataURL := dataURLFromBase64Payload(mimeType, encoded)
	result := &Result{
		Path:     label,
		ImageURL: dataURL,
		MimeType: mimeType,
		Width:    outputWidth,
		Height:   outputHeight,
		Detail:   normalizedDetail,
	}
	result.Assistant = fmt.Sprintf("Viewed image %s (%dx%d, %s)", label, result.Width, result.Height, mimeType)
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

// resizeImageNearest keeps paletted and grayscale images in their compact
// formats, since converting them to RGBA can enlarge the encoded image several times.
func resizeImageNearest(src image.Image, width int, height int) image.Image {
	srcBounds := src.Bounds()
	srcXs := make([]int, width)
	for x := range srcXs {
		srcXs[x] = srcBounds.Min.X + (x*srcBounds.Dx())/width
	}
	srcY := func(y int) int {
		return srcBounds.Min.Y + (y*srcBounds.Dy())/height
	}
	rect := image.Rect(0, 0, width, height)

	switch src := src.(type) {
	case *image.Paletted:
		dst := image.NewPaletted(rect, src.Palette)
		resizePixelBytesNearest(dst.Pix, dst.Stride, src.Pix, src.PixOffset, srcXs, height, srcY)
		return dst
	case *image.Gray:
		dst := image.NewGray(rect)
		resizePixelBytesNearest(dst.Pix, dst.Stride, src.Pix, src.PixOffset, srcXs, height, srcY)
		return dst
	}

	dst := image.NewRGBA(rect)
	for y := 0; y < height; y++ {
		sy := srcY(y)
		for x, sx := range srcXs {
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func resizePixelBytesNearest(
	dst []uint8,
	dstStride int,
	src []uint8,
	srcOffset func(x, y int) int,
	srcXs []int,
	height int,
	srcY func(int) int,
) {
	for y := 0; y < height; y++ {
		sy := srcY(y)
		row := dst[y*dstStride:]
		for x, sx := range srcXs {
			row[x] = src[srcOffset(sx, sy)]
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func viewImageOutputDimensionsForLimits(width, height, maxDimension, maxPatches int) (int, int) {
	width = maxInt(1, width)
	height = maxInt(1, height)
	if viewImageDimensionsFit(width, height, maxDimension, maxPatches) {
		return width, height
	}

	maxDimensionScale := math.Min(float64(maxDimension)/float64(maxInt(width, height)), 1)
	width = maxInt(1, int(math.Round(float64(width)*maxDimensionScale)))
	height = maxInt(1, int(math.Round(float64(height)*maxDimensionScale)))
	if viewImageDimensionsFit(width, height, maxDimension, maxPatches) {
		return width, height
	}

	widthFloat := float64(width)
	heightFloat := float64(height)
	patchSize := float64(viewImagePatchSize)
	scale := math.Sqrt(patchSize * patchSize * float64(maxPatches) / widthFloat / heightFloat)
	scaledPatchesWide := widthFloat * scale / patchSize
	scaledPatchesHigh := heightFloat * scale / patchSize
	scale *= math.Min(
		math.Floor(scaledPatchesWide)/scaledPatchesWide,
		math.Floor(scaledPatchesHigh)/scaledPatchesHigh,
	)

	return maxInt(1, int(math.Floor(widthFloat*scale))), maxInt(1, int(math.Floor(heightFloat*scale)))
}

func viewImageDimensionsFit(width, height, maxDimension, maxPatches int) bool {
	patchesWide := (uint64(width) + viewImagePatchSize - 1) / viewImagePatchSize
	patchesHigh := (uint64(height) + viewImagePatchSize - 1) / viewImagePatchSize
	return width <= maxDimension &&
		height <= maxDimension &&
		patchesWide*patchesHigh <= uint64(maxPatches)
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
