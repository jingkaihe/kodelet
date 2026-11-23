//go:build unix

package termimage

import "golang.org/x/sys/unix"

// QueryCellSize returns the terminal's cell size in pixels, if it reports one.
func QueryCellSize(fd uintptr) (CellSize, bool) {
	size, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
	if err != nil || size.Col == 0 || size.Row == 0 {
		return CellSize{}, false
	}
	cell := CellSize{Width: int(size.Xpixel) / int(size.Col), Height: int(size.Ypixel) / int(size.Row)}
	return cell, cell.valid()
}
