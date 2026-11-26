package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pkg/errors"
	"golang.design/x/clipboard"
)

// These read-v1 limits match the protocol table in README.md and the TUI reader.
const (
	maxImageBytes = 32 << 20
	maxTextBytes  = 16 << 20
	maxFilesBytes = 1 << 20
	maxErrorBytes = 4 << 10
)

var errImageTooLarge = errors.Errorf("image file too large: %d MiB file limit", maxImageBytes>>20)

type fileReader func(context.Context) ([]string, error)

type formatReader func(context.Context, clipboard.Format) ([]byte, error)

func readFinderFiles(ctx context.Context) ([]string, error) {
	if err := clipboard.Init(); err != nil {
		return nil, err
	}
	return clipboard.ReadFiles(ctx)
}

func readNativeClipboard(ctx context.Context, format clipboard.Format) ([]byte, error) {
	if err := clipboard.Init(); err != nil {
		return nil, err
	}
	return clipboard.Read(ctx, format)
}

func readSelection(ctx context.Context, files fileReader, read formatReader) response {
	// Finder can publish both the original file URL and its icon as an image.
	if files != nil {
		paths, err := files(ctx)
		if err != nil && !errors.Is(err, clipboard.ErrNoData) {
			return readFailure(err)
		}
		if len(paths) > 0 {
			return fileResponse(paths)
		}
	}
	data, err := read(ctx, clipboard.FmtImage)
	if err == nil {
		if len(data) > maxImageBytes {
			return readFailure(errImageTooLarge)
		}
		return response{kind: 'I', data: data}
	}
	if !errors.Is(err, clipboard.ErrNoData) {
		return readFailure(err)
	}
	data, err = read(ctx, clipboard.FmtText)
	if err != nil {
		return readFailure(err)
	}
	if len(data) > maxTextBytes {
		return readFailure(errors.New("clipboard text is too large"))
	}
	return response{kind: 'T', data: data}
}

func fileResponse(paths []string) response {
	for _, path := range paths {
		if !filepath.IsAbs(path) || !utf8.ValidString(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
			return readFailure(errors.New("invalid clipboard file path"))
		}
	}
	data, err := json.Marshal(paths)
	if err != nil {
		return readFailure(err)
	}
	if len(data) > maxFilesBytes {
		return readFailure(errors.New("clipboard file list is too large"))
	}
	return response{kind: 'F', data: data}
}

func readFailure(err error) response {
	if errors.Is(err, clipboard.ErrNoData) || errors.Is(err, clipboard.ErrUnavailable) {
		return response{kind: 'N'}
	}
	message := err.Error()
	if len(message) > maxErrorBytes {
		message = message[:maxErrorBytes]
	}
	message = strings.ToValidUTF8(message, "?")
	return response{kind: 'E', data: []byte(message)}
}
