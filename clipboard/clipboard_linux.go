package main

import (
	"context"
	"io"
	"os"
	"slices"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/pkg/errors"
	"golang.design/x/clipboard"
	"golang.org/x/text/encoding/charmap"
)

func init() {
	// XGB logs even a missing Xauthority file on successful reads.
	xgb.Logger.SetOutput(io.Discard)
}

var errX11SelectionTooLarge = errors.New("clipboard selection is too large")

var clipboardImageMIMEs = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

func readDesktopClipboard(ctx context.Context, format clipboard.Format) ([]byte, error) {
	if os.Getenv("DISPLAY") == "" {
		return readWaylandClipboard(ctx, format)
	}
	// WAYLAND_DISPLAY does not mean the compositor offers data-control. Use
	// the same X11 selection for discovery and transfer, including XWayland.
	c, err := xgb.NewConn()
	if err != nil {
		// A stale DISPLAY need not prevent a working native Wayland paste.
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return readWaylandClipboard(ctx, format)
		}
		return nil, clipboard.ErrUnavailable
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, c.Close)
	defer stop()
	return readX11Selection(c, format)
}

func readWaylandClipboard(ctx context.Context, format clipboard.Format) ([]byte, error) {
	if format != clipboard.FmtImage {
		return readNativeClipboard(ctx, format)
	}
	if err := clipboard.Init(); err != nil {
		return nil, err
	}
	// FmtImage requests only PNG; the library exposes the other advertised
	// MIME types as custom formats that return the original encoded bytes.
	offered, err := clipboard.Formats(ctx)
	if err != nil {
		return nil, err
	}
	for _, mime := range clipboardImageMIMEs {
		for _, f := range offered {
			if f.MIME() == mime {
				return clipboard.Read(ctx, f)
			}
		}
	}
	return nil, clipboard.ErrNoData
}

func x11Atom(c *xgb.Conn, name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(c, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return reply.Atom, nil
}

func readX11Selection(c *xgb.Conn, format clipboard.Format) ([]byte, error) {
	selection, err := x11Atom(c, "CLIPBOARD")
	if err != nil {
		return nil, err
	}
	owner, err := xproto.GetSelectionOwner(c, selection).Reply()
	if err != nil {
		return nil, err
	}
	if owner.Owner == xproto.WindowNone {
		return nil, clipboard.ErrNoData
	}
	win, err := xproto.NewWindowId(c)
	if err != nil {
		return nil, err
	}
	root := xproto.Setup(c).Roots[0].Root
	err = xproto.CreateWindowChecked(
		c, 0, win, root, 0, 0, 1, 1, 0, xproto.WindowClassInputOnly, 0,
		xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange},
	).Check()
	if err != nil {
		return nil, err
	}
	incr, err := x11Atom(c, "INCR")
	if err != nil {
		return nil, err
	}
	s := x11Selection{conn: c, win: win, selection: selection, incr: incr}
	targets, err := x11Atom(c, "TARGETS")
	if err != nil {
		return nil, err
	}
	data, err := s.read(targets, 32, 64<<10)
	if err != nil {
		return nil, err
	}
	var offered []xproto.Atom
	for len(data) >= 4 {
		offered = append(offered, xproto.Atom(xgb.Get32(data)))
		data = data[4:]
	}
	candidates := []string{"UTF8_STRING", "text/plain;charset=utf-8", "text/plain", "STRING"}
	limit := maxTextBytes
	if format == clipboard.FmtImage {
		candidates = clipboardImageMIMEs
		limit = maxImageBytes
	}
	for _, name := range candidates {
		target, err := x11Atom(c, name)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(offered, target) {
			continue
		}
		data, err = s.read(target, 8, limit)
		if format == clipboard.FmtImage && errors.Is(err, errX11SelectionTooLarge) {
			return nil, errImageTooLarge
		}
		if err != nil {
			return nil, err
		}
		if target == xproto.AtomString { // ICCCM STRING is ISO-8859-1, not UTF-8.
			return charmap.ISO8859_1.NewDecoder().Bytes(data)
		}
		return data, nil
	}
	return nil, clipboard.ErrNoData
}

type x11Selection struct {
	conn      *xgb.Conn
	win       xproto.Window
	selection xproto.Atom
	incr      xproto.Atom
}

// read implements the ICCCM requestor side of ConvertSelection. The reply's
// type, not its length, identifies an incremental transfer. Each property
// deletion acknowledges the chunk and invites the owner to write the next.
func (s x11Selection) read(target xproto.Atom, format byte, limit int) ([]byte, error) {
	err := xproto.ConvertSelectionChecked(
		s.conn, s.win, s.selection, target, target, xproto.TimeCurrentTime,
	).Check()
	if err != nil {
		return nil, err
	}
	transfer := x11Transfer{format: format, limit: limit}
	for {
		event, xerr := s.conn.WaitForEvent()
		if xerr != nil {
			return nil, xerr
		}
		switch e := event.(type) {
		case nil:
			return nil, io.EOF
		case xproto.SelectionNotifyEvent:
			if e.Requestor != s.win || e.Target != target || transfer.incremental {
				continue
			}
			if e.Property == xproto.AtomNone {
				return nil, clipboard.ErrNoData
			}
		case xproto.PropertyNotifyEvent:
			if !transfer.incremental || e.Window != s.win || e.Atom != target || e.State != xproto.PropertyNewValue {
				continue
			}
		default:
			continue
		}
		remaining := limit - len(transfer.data)
		words := uint32((remaining + 3) / 4)
		reply, err := xproto.GetProperty(s.conn, true, s.win, target, xproto.AtomAny, 0, words).Reply()
		if err != nil {
			return nil, err
		}
		done, err := transfer.accept(reply, s.incr)
		if err != nil {
			return nil, err
		}
		if done {
			return transfer.data, nil
		}
	}
}

type x11Transfer struct {
	format      byte
	limit       int
	incremental bool
	data        []byte
}

func (t *x11Transfer) accept(reply *xproto.GetPropertyReply, incr xproto.Atom) (done bool, err error) {
	if reply.Type == incr {
		if t.incremental || reply.BytesAfter != 0 || len(reply.Value) > 4 {
			return false, errors.New("invalid X11 incremental clipboard header")
		}
		t.incremental = true
		return false, nil
	}
	if reply.BytesAfter != 0 || len(reply.Value) > t.limit-len(t.data) {
		return false, errX11SelectionTooLarge
	}
	if reply.Format != t.format {
		return false, errors.New("invalid X11 clipboard property format")
	}
	t.data = append(t.data, reply.Value...)
	return !t.incremental || len(reply.Value) == 0, nil
}
