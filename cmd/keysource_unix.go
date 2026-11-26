//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"syscall"
)

func checkKeyPermissions(path string, mode os.FileMode) error {
	if mode.Perm()&0077 != 0 {
		return fmt.Errorf("key source %q permissions %#o are too permissive; expected mode 0600 or 0400", path, mode.Perm())
	}
	return nil
}

func openReadOnly(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
