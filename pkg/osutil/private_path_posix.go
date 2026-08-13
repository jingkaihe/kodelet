//go:build !windows

package osutil

func restrictPrivatePath(string, bool) error {
	return nil
}
