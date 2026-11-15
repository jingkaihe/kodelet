package vision

import (
	"bytes"
	"encoding/base64"
	"image"
	"strings"

	"github.com/pkg/errors"
)

// Base64Image is a base64-encoded image payload and its media type.
type Base64Image struct {
	MimeType string
	Data     string
}

// FitBase64Image downscales img, preserving its aspect ratio, so neither side exceeds maxDimension.
// It reads only the image header when img already fits and then returns it unchanged with false.
// A resized WebP image is re-encoded as PNG.
func FitBase64Image(img Base64Image, maxDimension int) (Base64Image, bool, error) {
	if maxDimension <= 0 {
		return img, false, errors.Errorf("invalid maximum image dimension: %d", maxDimension)
	}
	config, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(img.Data)))
	if err != nil {
		return img, false, errors.Wrap(err, "failed to read image dimensions")
	}
	if config.Width <= 0 || config.Height <= 0 {
		return img, false, errors.Errorf("invalid image dimensions: %dx%d", config.Width, config.Height)
	}
	if config.Width <= maxDimension && config.Height <= maxDimension {
		return img, false, nil
	}
	if config.Width > maxDecodedImagePixels/config.Height {
		return img, false, errors.New("image exceeds the decoded pixel limit")
	}

	decoded, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		return img, false, errors.Wrap(err, "failed to decode base64 image")
	}
	src, format, err := image.Decode(bytes.NewReader(decoded))
	if err != nil {
		return img, false, errors.Wrap(err, "failed to decode image")
	}
	mimeType, err := mimeTypeForDecodedImage(format)
	if err != nil {
		return img, false, err
	}

	width, height := fitDimensions(src.Bounds().Dx(), src.Bounds().Dy(), maxDimension)
	output, mimeType, err := encodeImageBytes(resizeImageNearest(src, width, height), mimeType)
	if err != nil {
		return img, false, err
	}

	return Base64Image{
		MimeType: mimeType,
		Data:     base64.StdEncoding.EncodeToString(output),
	}, true, nil
}

// fitDimensions scales the longer side to exactly maxDimension, using integer
// arithmetic so rounding cannot leave it one pixel short.
func fitDimensions(width, height, maxDimension int) (int, int) {
	if width >= height {
		return maxDimension, maxInt(1, height*maxDimension/width)
	}
	return maxInt(1, width*maxDimension/height), maxDimension
}
