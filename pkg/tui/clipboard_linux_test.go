package tui

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/stretchr/testify/require"
)

func TestX11ClipboardTransfer(t *testing.T) {
	const incr xproto.Atom = 100
	text := &xproto.GetPropertyReply{Type: xproto.AtomString, Format: 8, Value: []byte("test")}
	header := &xproto.GetPropertyReply{Type: incr, Format: 32, Value: []byte{4, 0, 0, 0}}
	end := &xproto.GetPropertyReply{Type: xproto.AtomString, Format: 8}

	t.Run("literal four bytes", func(t *testing.T) {
		transfer := x11Transfer{format: 8, limit: 4}
		done, err := transfer.accept(text, incr)
		require.NoError(t, err)
		require.True(t, done)
		require.Equal(t, "test", string(transfer.data))
	})

	t.Run("incremental header is not pasted", func(t *testing.T) {
		transfer := x11Transfer{format: 8, limit: 8}
		for _, property := range []*xproto.GetPropertyReply{header, text, text} {
			done, err := transfer.accept(property, incr)
			require.NoError(t, err)
			require.False(t, done)
		}
		done, err := transfer.accept(end, incr)
		require.NoError(t, err)
		require.True(t, done)
		require.Equal(t, "testtest", string(transfer.data))
	})

	t.Run("oversize chunk is not partially pasted", func(t *testing.T) {
		transfer := x11Transfer{format: 8, limit: 5}
		_, err := transfer.accept(header, incr)
		require.NoError(t, err)
		_, err = transfer.accept(text, incr)
		require.NoError(t, err)
		_, err = transfer.accept(text, incr)
		require.ErrorContains(t, err, "exceeds 5 bytes")
	})
}
