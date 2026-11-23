package termimage

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var kittyCommandPattern = regexp.MustCompile(`\x1b_G([^;\x1b]*)(?:;([^\x1b]*))?\x1b\\`)

func TestKittyUploadChunksPayload(t *testing.T) {
	data := bytes.Repeat([]byte{0xab}, kitty.MaxChunkSize) // Encodes to more than two chunks.
	commands := kittyCommandPattern.FindAllStringSubmatch(KittyUpload(42, data, 8, 3), -1)
	require.Len(t, commands, 2)

	assert.Equal(t, "a=T,U=1,f=100,t=d,q=2,i=42,c=8,r=3,m=1", commands[0][1])
	assert.Equal(t, "q=2,m=0", commands[1][1])
	assert.Len(t, commands[0][2], kitty.MaxChunkSize)
	decoded, err := base64.StdEncoding.DecodeString(commands[0][2] + commands[1][2])
	require.NoError(t, err)
	assert.Equal(t, data, decoded)
}

func TestKittyUploadSingleChunk(t *testing.T) {
	commands := kittyCommandPattern.FindAllStringSubmatch(KittyUpload(7, []byte("png"), 1, 1), -1)
	require.Len(t, commands, 1)
	assert.Equal(t, "a=T,U=1,f=100,t=d,q=2,i=7,c=1,r=1,m=0", commands[0][1])
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("png")), commands[0][2])
}

func TestKittyDelete(t *testing.T) {
	assert.Equal(t, "\x1b_Ga=d,d=I,i=9,q=2\x1b\\", KittyDelete(9))
}

func TestITerm2Image(t *testing.T) {
	got := ITerm2Image([]byte("png"), 12, 4)
	assert.Equal(
		t,
		"\x1b]1337;File=size=3;width=12;height=4;preserveAspectRatio=0;inline=1;doNotMoveCursor=1:"+
			base64.StdEncoding.EncodeToString([]byte("png"))+"\a",
		got,
	)
	assert.False(t, strings.Contains(got, "\n"))
}
