//go:build !linux

package main

func protectProcessSecrets(...string) error {
	return nil
}

func protectRemoteACPProcessSecrets(...string) error {
	return nil
}
