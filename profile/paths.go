package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultProfilesDir returns the configured or platform-default profiles directory.
func DefaultProfilesDir() (string, error) {
	if dir, set, err := absoluteEnv("PLONK_PROFILES_DIR"); set {
		return dir, err
	}
	if runtime.GOOS == "windows" {
		dir := os.Getenv("ProgramData")
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("cannot determine ProgramData directory; set PLONK_PROFILES_DIR")
		}
		return filepath.Join(dir, "plonkr"), nil
	}
	return "/etc/plonkr", nil
}

// DefaultLogDir returns the configured or platform-default log directory.
func DefaultLogDir() (string, error) {
	if dir, set, err := absoluteEnv("PLONK_LOG_DIR"); set {
		return dir, err
	}
	if runtime.GOOS == "windows" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("determine Windows cache directory: %w", err)
		}
		return filepath.Join(dir, "plonkr", "logs"), nil
	}
	if dir, set, err := absoluteEnv("XDG_STATE_HOME"); set {
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "plonkr", "logs"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "plonkr", "logs"), nil
}

func absoluteEnv(name string) (string, bool, error) {
	dir, set := os.LookupEnv(name)
	if !set {
		return "", false, nil
	}
	if !filepath.IsAbs(dir) {
		return "", true, fmt.Errorf("%s must be an absolute path", name)
	}
	return filepath.Clean(dir), true, nil
}
