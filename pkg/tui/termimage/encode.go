package termimage

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/iterm2"
	"github.com/charmbracelet/x/ansi/kitty"
)

// KittyUpload uploads PNG data as image id with a cols x rows virtual
// placement for Unicode placeholders. Responses are suppressed (q=2).
func KittyUpload(id uint32, png []byte, cols, rows int) string {
	encoded := base64.StdEncoding.EncodeToString(png)
	var b strings.Builder
	b.Grow(len(encoded) + (len(encoded)/kitty.MaxChunkSize+1)*16 + 64)
	for offset := 0; offset == 0 || offset < len(encoded); offset += kitty.MaxChunkSize {
		end := min(len(encoded), offset+kitty.MaxChunkSize)
		more := "m=0"
		if end < len(encoded) {
			more = "m=1"
		}
		chunk := []byte(encoded[offset:end])
		if offset == 0 {
			b.WriteString(ansi.KittyGraphics(
				chunk,
				"a=T",
				"U=1",
				"f=100",
				"t=d",
				"q=2",
				fmt.Sprintf("i=%d", id),
				fmt.Sprintf("c=%d", cols),
				fmt.Sprintf("r=%d", rows),
				more,
			))
			continue
		}
		b.WriteString(ansi.KittyGraphics(chunk, "q=2", more))
	}
	return b.String()
}

// KittyDelete returns the command that deletes image id and frees its data.
func KittyDelete(id uint32) string {
	return ansi.KittyGraphics(nil, "a=d", "d=I", fmt.Sprintf("i=%d", id), "q=2")
}

// ITerm2Image returns the command that draws encoded image data at the cursor,
// stretched to exactly cols x rows cells.
func ITerm2Image(data []byte, cols, rows int) string {
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
	base64.StdEncoding.Encode(encoded, data)
	return ansi.ITerm2(iterm2.File{
		Size:              int64(len(data)),
		Width:             iterm2.Cells(cols),
		Height:            iterm2.Cells(rows),
		IgnoreAspectRatio: true,
		Inline:            true,
		DoNotMoveCursor:   true,
		Content:           encoded,
	})
}
