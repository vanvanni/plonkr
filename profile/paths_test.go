package profile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirectoryOverrides(t *testing.T) {
	for _, tc := range []struct {
		env string
		get func() (string, error)
	}{
		{"PLONK_PROFILES_DIR", DefaultProfilesDir},
		{"PLONK_LOG_DIR", DefaultLogDir},
	} {
		t.Run(tc.env, func(t *testing.T) {
			base := t.TempDir()
			unclean := base + string(os.PathSeparator) + "." + string(os.PathSeparator) + "data"
			t.Setenv(tc.env, unclean)
			checkDirectory(t, tc.get, filepath.Join(base, "data"))
			for _, value := range []string{"relative/data", ""} {
				t.Setenv(tc.env, value)
				_, err := tc.get()
				checkError(t, err, tc.env+" must be an absolute path")
			}
		})
	}
}

func TestDefaultDirectories(t *testing.T) {
	unsetEnv(t, "PLONK_PROFILES_DIR")
	unsetEnv(t, "PLONK_LOG_DIR")
	base := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("ProgramData", base)
		t.Setenv("LocalAppData", base)
		checkDirectory(t, DefaultProfilesDir, filepath.Join(base, "plonkr"))
		checkDirectory(t, DefaultLogDir, filepath.Join(base, "plonkr", "logs"))
		for _, value := range []string{"relative", ""} {
			t.Setenv("ProgramData", value)
			_, err := DefaultProfilesDir()
			checkError(t, err, "cannot determine ProgramData directory; set PLONK_PROFILES_DIR")
		}
		return
	}

	checkDirectory(t, DefaultProfilesDir, "/etc/plonkr")
	t.Setenv("XDG_STATE_HOME", base)
	checkDirectory(t, DefaultLogDir, filepath.Join(base, "plonkr", "logs"))
	t.Setenv("XDG_STATE_HOME", "relative")
	_, err := DefaultLogDir()
	checkError(t, err, "XDG_STATE_HOME must be an absolute path")
	unsetEnv(t, "XDG_STATE_HOME")
	t.Setenv("HOME", base)
	checkDirectory(t, DefaultLogDir, filepath.Join(base, ".local", "state", "plonkr", "logs"))
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func checkDirectory(t *testing.T, get func() (string, error), want string) {
	t.Helper()
	got, err := get()
	if err != nil || got != want {
		t.Fatalf("directory = %q, %v; want %q", got, err, want)
	}
}
