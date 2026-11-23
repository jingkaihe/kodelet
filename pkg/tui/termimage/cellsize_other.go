//go:build !unix

package termimage

// QueryCellSize reports no cell size on platforms without TIOCGWINSZ.
func QueryCellSize(uintptr) (CellSize, bool) {
	return CellSize{}, false
}
