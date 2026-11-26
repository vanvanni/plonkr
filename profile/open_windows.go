//go:build windows

package profile

import "os"

func openRootRegular(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
