//go:build windows

package cmd

import "os"

func checkKeyPermissions(_ string, _ os.FileMode) error {
	// Regular files on Windows report synthetic 0666 mode bits via os.Stat.
	return nil
}

func openReadOnly(path string) (*os.File, error) {
	return os.Open(path)
}
