//go:build !linux

package tui

import (
	"context"

	"golang.design/x/clipboard"
)

func readDesktopClipboard(ctx context.Context, format clipboard.Format) ([]byte, error) {
	return readNativeClipboard(ctx, format)
}
