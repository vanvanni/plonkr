package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin how a PROFILE argument turns into a file. Each case names the
// profile after where it must have been found, so the output proves which copy won.

func writeProfile(t *testing.T, path, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	source := "name = " + `"` + name + `"` + "\ntasks {\n task \"T\" {\n order = 1\n script = [\"true\"]\n }\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func resolvedName(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := runCLI(t, append([]string{"validate"}, args...)...)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(out, "\n")
	return strings.TrimPrefix(first, "Profile: "), nil
}

func TestProfileResolution(t *testing.T) {
	envDir, flagDir, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	writeProfile(t, filepath.Join(envDir, "web.hcl"), "from-env-dir")
	writeProfile(t, filepath.Join(flagDir, "web.hcl"), "from-flag-dir")
	writeProfile(t, filepath.Join(cwd, "web.hcl"), "from-cwd")
	t.Setenv("PLONK_PROFILES_DIR", envDir)
	t.Chdir(cwd)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"bare name uses the profiles directory, never the working directory", []string{"web"}, "from-env-dir"},
		{"bare name with .hcl prefers the working directory", []string{"web.hcl"}, "from-cwd"},
		{"--profiles-dir beats the environment", []string{"web", "--profiles-dir", flagDir}, "from-flag-dir"},
		{".hcl is a path even with --profiles-dir", []string{"web.hcl", "--profiles-dir", flagDir}, "from-cwd"},
		{"--profiles-dir=value form", []string{"web", "--profiles-dir=" + flagDir}, "from-flag-dir"},
		{"explicit relative path", []string{"./web.hcl"}, "from-cwd"},
		{"explicit absolute path", []string{filepath.Join(flagDir, "web.hcl")}, "from-flag-dir"},
		{"explicit path ignores the profiles directory for the file itself", []string{filepath.Join(flagDir, "web.hcl"), "--profiles-dir", envDir}, "from-flag-dir"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvedName(t, tc.args...)
			if err != nil || got != tc.want {
				t.Fatalf("resolved %q, error %v; want %q", got, err, tc.want)
			}
		})
	}

	// An explicit file never falls back to the profiles directory.
	t.Chdir(t.TempDir())
	if _, err := resolvedName(t, "web.hcl"); err == nil {
		t.Fatal("missing explicit file resolved as a name")
	}
}

func TestProfileResolutionErrors(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, filepath.Join(dir, "ok.hcl"), "ok")
	t.Setenv("PLONK_PROFILES_DIR", dir)
	t.Chdir(t.TempDir())
	if err := os.Mkdir("dir.hcl", 0o700); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(t.TempDir(), "huge.hcl")
	if err := os.WriteFile(huge, []byte("# "+strings.Repeat("x", maxProfileFileSize)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing name", []string{"missing"}, "read profile"},
		{"missing explicit file", []string{filepath.Join(dir, "missing.hcl")}, "read profile"},
		{"empty reference", []string{""}, "invalid profile reference"},
		{"invalid characters", []string{"we b"}, "invalid profile reference"},
		{"leading dash", []string{"-x"}, "unknown shorthand flag"},
		{"parent traversal by name", []string{".." + string(filepath.Separator) + "ok"}, "read profile"},
		{"directory instead of a file", []string{"./dir.hcl"}, "regular file"},
		{"bare .hcl directory in the working directory", []string{"dir.hcl"}, "regular file"},
		{"oversized profile", []string{huge}, "1 MiB"},
		{"two profiles", []string{"ok", "ok"}, "accepts 1 arg"},
		{"no profile", nil, "accepts 1 arg"},
		{"profiles dir is a file", []string{"ok", "--profiles-dir", filepath.Join(dir, "ok.hcl")}, "not a directory"},
		{"profiles dir missing", []string{"ok", "--profiles-dir", filepath.Join(dir, "nope")}, "open profiles directory"},
		{"profiles dir blank", []string{"ok", "--profiles-dir", " "}, "must not be empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolvedName(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}

	t.Run("relative PLONK_PROFILES_DIR", func(t *testing.T) {
		t.Setenv("PLONK_PROFILES_DIR", "relative")
		_, err := resolvedName(t, "ok")
		if err == nil || !strings.Contains(err.Error(), "PLONK_PROFILES_DIR must be an absolute path") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("symlinked profile", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link.hcl")
		if err := os.Symlink(filepath.Join(dir, "ok.hcl"), link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		_, err := resolvedName(t, link)
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestIncludesResolveFromTheSelectedBase(t *testing.T) {
	fileDir, baseDir := t.TempDir(), t.TempDir()
	for dir, label := range map[string]string{fileDir: "beside-the-file", baseDir: "in-the-base"} {
		writeProfile(t, filepath.Join(dir, "child.hcl"), label)
		if err := os.WriteFile(filepath.Join(dir, "child.hcl"), []byte("tasks {\n task \""+label+"\" {\n order = 1\n script = [\"true\"]\n }\n}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	main := "include = [\"child\"]\ntasks {\n task \"Main\" {\n order = 1\n script = [\"true\"]\n }\n}\n"
	if err := os.WriteFile(filepath.Join(fileDir, "main.hcl"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	out, _, err := runCLI(t, "validate", filepath.Join(fileDir, "main.hcl"))
	if err != nil || !strings.Contains(out, "beside-the-file") || strings.Contains(out, "in-the-base") {
		t.Fatalf("without --profiles-dir includes resolve beside the file: %v\n%s", err, out)
	}
	out, _, err = runCLI(t, "validate", filepath.Join(fileDir, "main.hcl"), "--profiles-dir", baseDir)
	if err != nil || !strings.Contains(out, "in-the-base") || strings.Contains(out, "beside-the-file") {
		t.Fatalf("with --profiles-dir includes resolve from the base: %v\n%s", err, out)
	}
}

func TestValidateOutputShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shape.hcl")
	source := `
name = "  Spaced   name  "
configuration { groups = [{name = "g"}] }
tasks {
  task "Bypassed" {
    order = 3
    bypass = true
    script = ["true"]
  }
  task "Locked" {
    order = 2
    script = [flag("zeta", "a"), flag("alpha", "b"), log("hi")]
    lock { exists = ["/x"] }
  }
}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := runCLI(t, "validate", path)
	if err != nil {
		t.Fatal(err)
	}
	want := "Profile: Spaced name\n" +
		"Flags: --alpha, --zeta\n" +
		"\n[config] Configuration\n  [setup] group g\n" +
		"\n[2] Locked (lock)\n  [script] a [flags: --zeta]\n  [script] b [flags: --alpha]\n  [script] hi\n" +
		"\n[3] Bypassed (bypass)\n  [script] true\n"
	if out != want {
		t.Fatalf("validate output:\n%s\nwant:\n%s", out, want)
	}
}

func TestValidateNeverReadsKeysOrConnects(t *testing.T) {
	env := newE2E(t)
	path := env.profile(`
repository "r" {
  url  = "git@example.com:a/b.git"
  path = "/srv/r"
  user = "app"
  key  = "/definitely/missing/key"
}
tasks {
  task "Clone" {
    order = 1
    setup = [repo("r")]
  }
}
`)
	if _, _, err := runCLI(t, "validate", path); err != nil {
		t.Fatal(err)
	}
	env.noConnections()
	if len(env.logs()) != 0 {
		t.Fatal("validate must not write a run log")
	}
}
