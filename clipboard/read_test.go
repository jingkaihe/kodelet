package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"golang.design/x/clipboard"
)

func TestReadSelection(t *testing.T) {
	paths := []string{"/one file", "/two"}
	image := []byte{0x89, 0, 0xff}
	for _, tt := range []struct {
		name  string
		paths []string
		image []byte
		text  string
		want  string
	}{
		{"Finder files before icon", paths, image, "text", `F["/one file","/two"]`},
		{"image before text", nil, image, "text", "I" + string(image)},
		{"multiline text", nil, nil, "hello\nworld", "Thello\nworld"},
		{"empty", nil, nil, "", "N"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := func(context.Context) ([]string, error) { return tt.paths, nil }
			read := func(_ context.Context, format clipboard.Format) ([]byte, error) {
				if format == clipboard.FmtImage && tt.image != nil {
					return tt.image, nil
				}
				if format == clipboard.FmtText && tt.text != "" {
					return []byte(tt.text), nil
				}
				return nil, clipboard.ErrNoData
			}
			var out bytes.Buffer
			err := readSelection(t.Context(), files, read).writeTo(&out)
			require.NoError(t, err)
			require.Equal(t, tt.want, out.String())
		})
	}
}

func TestReadFailures(t *testing.T) {
	require.Equal(t, byte('N'), readFailure(errors.Wrap(clipboard.ErrUnavailable, "display")).kind)
	require.Equal(t, byte('E'), fileResponse([]string{"relative"}).kind)
	r := readFailure(errors.New(strings.Repeat("界", maxErrorBytes)))
	require.Equal(t, byte('E'), r.kind)
	require.LessOrEqual(t, len(r.data), maxErrorBytes)
	require.True(t, utf8.Valid(r.data))
}
