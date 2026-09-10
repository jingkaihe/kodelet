package artifacts

import (
	"bufio"
	"encoding/binary"
	"image"
	"image/gif"
	"io"
	"os"

	"github.com/pkg/errors"
)

func validateImage(file *os.File) (image.Config, string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return image.Config{}, "", errors.Wrap(err, "failed to rewind image")
	}
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return image.Config{}, "", errors.Wrap(err, "invalid image header")
	}
	switch format {
	case "png", "jpeg", "gif", "webp":
	default:
		return image.Config{}, "", errors.New("only PNG, JPEG, GIF and WebP images are supported")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > MaxPixels/int64(config.Height) {
		return image.Config{}, "", errors.Errorf("image exceeds %d decoded pixel limit", MaxPixels)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return image.Config{}, "", errors.Wrap(err, "failed to rewind image")
	}
	if format == "gif" {
		// DecodeAll validates every frame; preflight their cumulative allocation
		// so an animation cannot bypass the single-image pixel limit.
		if err := checkGIFBudget(file); err != nil {
			return image.Config{}, "", errors.Wrap(err, "invalid GIF image")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return image.Config{}, "", errors.Wrap(err, "failed to rewind GIF")
		}
		_, err = gif.DecodeAll(file)
	} else {
		_, _, err = image.Decode(file)
	}
	if err != nil {
		return image.Config{}, "", errors.Wrap(err, "invalid image data")
	}
	return config, format, nil
}

func checkGIFBudget(source io.Reader) error {
	r := bufio.NewReader(source)
	var header [13]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	skipColorTable := func(flags byte) error {
		if flags&0x80 == 0 {
			return nil
		}
		_, err := io.CopyN(io.Discard, r, 3<<((flags&7)+1))
		return err
	}
	if err := skipColorTable(header[10]); err != nil {
		return err
	}
	var pixels int64
	frames := 0
	for {
		marker, err := r.ReadByte()
		if err != nil {
			return err
		}
		switch marker {
		case 0x3b: // Trailer.
			return nil
		case 0x21: // Extension label, followed by length-prefixed sub-blocks.
			if _, err := r.ReadByte(); err != nil {
				return err
			}
		case 0x2c: // Image descriptor and optional local color table.
			var descriptor [9]byte
			if _, err := io.ReadFull(r, descriptor[:]); err != nil {
				return err
			}
			pixels += int64(binary.LittleEndian.Uint16(descriptor[4:6])) * int64(binary.LittleEndian.Uint16(descriptor[6:8]))
			frames++
			if pixels > MaxPixels || frames > 256 {
				return errors.New("GIF exceeds decoded pixel or 256-frame limit")
			}
			if err := skipColorTable(descriptor[8]); err != nil {
				return err
			}
			if _, err := r.ReadByte(); err != nil { // LZW minimum code size.
				return err
			}
		default:
			return errors.New("unexpected GIF block")
		}
		for {
			size, err := r.ReadByte()
			if err != nil {
				return err
			}
			if size == 0 {
				break
			}
			if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
				return err
			}
		}
	}
}
