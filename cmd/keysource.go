package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/ssh"
)

const maxKeySize = 1 << 20

// ReadKeySource validates and reads an unencrypted SSH private key file without
// modifying or re-encoding its bytes. Callers must clear the returned slice.
func ReadKeySource(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("key source path must not be empty")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect key source %q: %w", path, err)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("key source %q must be a regular file, not a symlink or directory", path)
	}
	if before.Size() <= 0 || before.Size() > maxKeySize {
		return nil, fmt.Errorf("key source %q must be between 1 byte and 1 MiB", path)
	}
	if err := checkKeyPermissions(path, before.Mode()); err != nil {
		return nil, err
	}

	file, err := openReadOnly(path)
	if err != nil {
		return nil, fmt.Errorf("open key source %q: %w", path, err)
	}
	defer file.Close()

	after, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat key source %q: %w", path, err)
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return nil, fmt.Errorf("key source %q changed during read or is not a regular file", path)
	}
	if after.Size() <= 0 || after.Size() > maxKeySize {
		return nil, fmt.Errorf("key source %q must be between 1 byte and 1 MiB", path)
	}
	if err := checkKeyPermissions(path, after.Mode()); err != nil {
		return nil, err
	}

	raw, err := io.ReadAll(io.LimitReader(file, maxKeySize+1))
	if err != nil {
		clear(raw)
		return nil, fmt.Errorf("read key source %q: %w", path, err)
	}
	if len(raw) == 0 || len(raw) > maxKeySize {
		clear(raw)
		return nil, fmt.Errorf("key source %q must be between 1 byte and 1 MiB", path)
	}

	if _, err := ssh.ParseRawPrivateKey(raw); err != nil {
		clear(raw)
		if _, ok := errors.AsType[*ssh.PassphraseMissingError](err); ok {
			return nil, fmt.Errorf("key source %q is passphrase-protected; v0.1.0 requires an unencrypted deploy key file for non-interactive cloning", path)
		}
		return nil, fmt.Errorf("parse deploy key %q: %w", path, err)
	}
	return raw, nil
}
