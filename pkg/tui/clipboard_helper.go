package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/jingkaihe/kodelet/pkg/vision"
	"github.com/pkg/errors"
)

type clipboardContents struct {
	kind  byte
	data  []byte
	files []string
}

// readClipboardHelper runs on the TUI host, never the runner. One process per
// gesture lets cancellation stop even native clipboard reads that ignore it.
func readClipboardHelper(ctx context.Context) (clipboardContents, error) {
	cmd := exec.CommandContext(ctx, "kodelet-clipboard", "read-v1")
	if errors.Is(cmd.Err, exec.ErrNotFound) {
		return clipboardContents{}, errors.New("install kodelet-clipboard on PATH to use this paste shortcut")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return clipboardContents{}, err
	}
	if err := cmd.Start(); err != nil {
		return clipboardContents{}, errors.Wrap(err, "starting kodelet-clipboard")
	}
	stop := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer stop()
	result, readErr := readClipboardResponse(bufio.NewReader(stdout))
	if readErr != nil {
		_ = cmd.Process.Kill() // Do not wait for an invalid or oversized transfer.
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return clipboardContents{}, ctx.Err()
	}
	if readErr != nil {
		return clipboardContents{}, readErr
	}
	return result, errors.Wrap(waitErr, "running kodelet-clipboard")
}

// read-v1 returns a one-byte kind followed by the payload to EOF. Limit reads
// by kind before parsing; image bytes travel unchanged, without base64.
func readClipboardResponse(r *bufio.Reader) (clipboardContents, error) {
	kind, err := r.ReadByte()
	if err != nil {
		return clipboardContents{}, errors.Wrap(err, "reading kodelet-clipboard response")
	}
	var limit int
	switch kind {
	case 'I':
		limit = vision.MaxImageInputBytes
	case 'T':
		limit = 16 << 20
	case 'F':
		limit = 1 << 20
	case 'E':
		limit = 4 << 10
	case 'N': // Empty selection: no payload.
	default:
		return clipboardContents{}, errors.New("unrecognized kodelet-clipboard response")
	}
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return clipboardContents{}, errors.Wrap(err, "reading clipboard data")
	}
	if len(data) > limit {
		if kind == 'I' {
			return clipboardContents{}, vision.ErrImageInputTooLarge
		}
		return clipboardContents{}, errors.New("clipboard response exceeds its size limit")
	}
	switch kind {
	case 'E':
		return clipboardContents{}, errors.Errorf("reading clipboard: %s", data)
	case 'F':
		var files []string
		if err := json.Unmarshal(data, &files); err != nil || len(files) == 0 {
			return clipboardContents{}, errors.New("invalid clipboard file list")
		}
		for _, path := range files {
			if !filepath.IsAbs(path) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
				return clipboardContents{}, errors.New("invalid clipboard file path")
			}
		}
		return clipboardContents{kind: kind, files: files}, nil
	default:
		return clipboardContents{kind: kind, data: data}, nil
	}
}
