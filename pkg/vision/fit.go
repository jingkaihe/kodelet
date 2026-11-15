package vision

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"math"
	"slices"
	"strings"

	"github.com/pkg/errors"
)

// Base64Image is a base64-encoded image payload and its media type.
type Base64Image struct {
	MimeType string
	Data     string
}

// ImageLimits bounds a fitted image. A zero field means no limit.
type ImageLimits struct {
	// MaxDimension caps both the width and the height in pixels.
	MaxDimension int
	// MaxBase64Size caps the length of the base64 payload in bytes.
	MaxBase64Size int
}

// FitBase64Image downscales img, preserving its aspect ratio, until it fits limits.
// It reads only the image header when img already fits and then returns it unchanged with false.
// On error it returns img unchanged. A resized WebP image is re-encoded as PNG.
func FitBase64Image(img Base64Image, limits ImageLimits) (Base64Image, bool, error) {
	if limits.MaxDimension < 0 || limits.MaxBase64Size < 0 {
		return img, false, errors.Errorf(
			"invalid image limits: max dimension %d, max base64 size %d",
			limits.MaxDimension,
			limits.MaxBase64Size,
		)
	}
	config, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(img.Data)))
	if err != nil {
		return img, false, errors.Wrap(err, "failed to read image dimensions")
	}
	if config.Width <= 0 || config.Height <= 0 {
		return img, false, errors.Errorf("invalid image dimensions: %dx%d", config.Width, config.Height)
	}
	if fitsDimension(config.Width, config.Height, limits.MaxDimension) &&
		fitsBase64Size(len(img.Data), limits.MaxBase64Size) {
		return img, false, nil
	}
	if config.Width > maxDecodedImagePixels/config.Height {
		return img, false, errors.New("image exceeds the decoded pixel limit")
	}

	decoded, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		return img, false, errors.Wrap(err, "failed to decode base64 image")
	}
	src, format, err := decodeImage(decoded)
	if err != nil {
		return img, false, errors.Wrap(err, "failed to decode image")
	}
	mimeType, err := mimeTypeForDecodedImage(format)
	if err != nil {
		return img, false, err
	}

	width, height := src.Bounds().Dx(), src.Bounds().Dy()
	if !fitsDimension(width, height, limits.MaxDimension) {
		width, height = fitDimensions(width, height, limits.MaxDimension)
	}
	output, err := encodeResized(src, width, height, mimeType, limits.MaxBase64Size)
	if err != nil {
		return img, false, err
	}

	return Base64Image{
		MimeType: output.mimeType,
		Data:     base64.StdEncoding.EncodeToString(output.data),
	}, true, nil
}

type encodedImage struct {
	data     []byte
	mimeType string
	width    int
	height   int
}

// encodeResized resizes src to width x height and encodes it, shrinking it
// further until its base64 payload fits maxBase64Size. Zero means no limit.
func encodeResized(src image.Image, width, height int, mimeType string, maxBase64Size int) (encodedImage, error) {
	for attempt := 0; ; attempt++ {
		data, outputMimeType, err := encodeImageBytes(resizeImageNearest(src, width, height), mimeType)
		if err != nil {
			return encodedImage{}, err
		}
		encodedSize := base64.StdEncoding.EncodedLen(len(data))
		if fitsBase64Size(encodedSize, maxBase64Size) {
			return encodedImage{data: data, mimeType: outputMimeType, width: width, height: height}, nil
		}
		if attempt >= viewImageMaxDownscaleAttempts {
			return encodedImage{}, errors.Errorf("image could not fit within the %d byte base64 limit", maxBase64Size)
		}

		// Reduce pixel area in proportion to the excess bytes, with encoding headroom.
		scale := math.Min(0.9, math.Sqrt(float64(maxBase64Size)/float64(encodedSize))*0.95)
		width = maxInt(1, int(math.Floor(float64(width)*scale)))
		height = maxInt(1, int(math.Floor(float64(height)*scale)))
	}
}

func fitsDimension(width, height, maxDimension int) bool {
	return maxDimension == 0 || (width <= maxDimension && height <= maxDimension)
}

func fitsBase64Size(size, maxBase64Size int) bool {
	return maxBase64Size == 0 || size <= maxBase64Size
}

// fitDimensions scales the longer side to exactly maxDimension, using integer
// arithmetic so rounding cannot leave it one pixel short.
func fitDimensions(width, height, maxDimension int) (int, int) {
	if width >= height {
		return maxDimension, maxInt(1, height*maxDimension/width)
	}
	return maxInt(1, width*maxDimension/height), maxDimension
}

// decodeImage decodes the first frame of an image. image.Decode returns a GIF's
// first frame at its own offset and size, so the frame is placed on the GIF's
// logical screen to keep the image's canvas and composition.
func decodeImage(data []byte) (image.Image, string, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	frame, ok := img.(*image.Paletted)
	if format != "gif" || !ok {
		return img, format, nil
	}
	config, err := gif.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to read gif dimensions")
	}
	screen := image.Rect(0, 0, config.Width, config.Height)
	if frame.Rect == screen || screen.Empty() || !frame.Rect.In(screen) {
		return frame, format, nil
	}
	return gifScreen(frame, screen, gifBackgroundIndex(data)), format, nil
}

// gifScreen draws frame on a canvas the size of screen. The area outside the
// frame is transparent, as browsers render it, when the palette has or can take
// a transparent entry; otherwise it uses the GIF's background colour index.
func gifScreen(frame *image.Paletted, screen image.Rectangle, background uint8) *image.Paletted {
	palette := frame.Palette
	fill := slices.IndexFunc(palette, func(c color.Color) bool {
		_, _, _, alpha := c.RGBA()
		return alpha == 0
	})
	if fill < 0 && len(palette) < 256 {
		palette = append(slices.Clip(palette), color.Transparent)
		fill = len(palette) - 1
	}
	if fill < 0 {
		fill = int(background)
	}

	canvas := image.NewPaletted(screen, palette)
	if fill != 0 {
		for i := range canvas.Pix {
			canvas.Pix[i] = uint8(fill)
		}
	}
	for y := frame.Rect.Min.Y; y < frame.Rect.Max.Y; y++ {
		row := frame.Pix[frame.PixOffset(frame.Rect.Min.X, y):frame.PixOffset(frame.Rect.Max.X, y)]
		copy(canvas.Pix[canvas.PixOffset(frame.Rect.Min.X, y):], row)
	}
	return canvas
}

// gifBackgroundIndex reads the background colour index from the GIF logical
// screen descriptor, which follows the six-byte signature and version.
func gifBackgroundIndex(data []byte) uint8 {
	const backgroundIndexOffset = 11
	if len(data) <= backgroundIndexOffset {
		return 0
	}
	return data[backgroundIndexOffset]
}
