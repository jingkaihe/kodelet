//go:build !linux

package main

func protectRemoteACPProcessSecrets(...string) error {
	return nil
}
