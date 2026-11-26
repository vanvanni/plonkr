package cmd

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func runCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	c := New(strings.NewReader(""), &stdout, &stderr)
	c.SetArgs(args)
	err := c.ExecuteContext(t.Context())
	return stdout.String(), stderr.String(), err
}

func TestVersionCommand(t *testing.T) {
	out, errOut, err := runCLI(t, "version")
	if err != nil {
		t.Fatalf("version failed: %v (stderr=%q)", err, errOut)
	}
	want := "plonk " + Version + "\n"
	if out != want {
		t.Fatalf("version output = %q, want %q", out, want)
	}
}

func TestValidateCommand(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "sample.hcl")
	validHCL := `
name = "Sample"
configuration {
  timezone = "UTC"
}
tasks {
  task "Check" {
    order = 10
    script = [
      "uname -a",
      flag("vm", "echo vm"),
    ]
  }
}
`
	if err := os.WriteFile(validPath, []byte(validHCL), 0644); err != nil {
		t.Fatal(err)
	}

	invalidPath := filepath.Join(dir, "bad.hcl")
	if err := os.WriteFile(invalidPath, []byte(`script "0" "legacy" { commands = ["echo hi"] }`), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		args      []string
		wantErr   bool
		wantInOut []string
	}{
		{
			name:      "valid by name",
			args:      []string{"validate", "sample", "--profiles-dir", dir},
			wantInOut: []string{"Profile: Sample", "Flags: --vm", "[config] Configuration", "[10] Check", "[script] uname -a"},
		},
		{
			name:    "legacy rejected",
			args:    []string{"validate", "bad", "--profiles-dir", dir},
			wantErr: true,
		},
		{
			name:    "empty profiles-dir rejected",
			args:    []string{"validate", "sample", "--profiles-dir", ""},
			wantErr: true,
		},
		{
			name:    "explicit path with missing profiles-dir rejected",
			args:    []string{"validate", validPath, "--profiles-dir", filepath.Join(dir, "missing")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := runCLI(t, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v (out=%q)", err, tt.wantErr, out)
			}
			for _, sub := range tt.wantInOut {
				if !strings.Contains(out, sub) {
					t.Errorf("output missing %q:\n%s", sub, out)
				}
			}
		})
	}
}

func TestRunHelpAndDynamicFlags(t *testing.T) {
	out, _, err := runCLI(t, "run", "--help")
	if err != nil {
		t.Fatalf("run --help failed: %v", err)
	}
	for _, want := range []string{"HOST PROFILE", "--dry-run", "--bypass", "--profiles-dir"} {
		if !strings.Contains(out, want) {
			t.Errorf("run --help missing %q:\n%s", want, out)
		}
	}

	dir := t.TempDir()
	profileHCL := `
tasks {
  task "Prep" {
    order = 0
    script = [
      flag("vm", "echo vm"),
      flag("audit", "id"),
    ]
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "flags.hcl"), []byte(profileHCL), 0644); err != nil {
		t.Fatal(err)
	}

	out, _, err = runCLI(t, "run", "my-host", "flags", "--profiles-dir", dir, "--help")
	if err != nil {
		t.Fatalf("run profile --help failed: %v", err)
	}
	for _, want := range []string{"--audit", "--vm", "--bypass", "--dry-run"} {
		if !strings.Contains(out, want) {
			t.Errorf("profile --help missing %q:\n%s", want, out)
		}
	}
}

func TestRunDryRunAndBypass(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	t.Setenv("PLONK_LOG_DIR", logDir)

	missingKey := filepath.Join(dir, "does-not-exist.key")
	hcl := fmt.Sprintf(`
name = "Dry run test"

configuration {
  groups = [{ name = "app" }]
  timezone = "UTC"
}

repository "app-repo" {
  url  = "git@example.com:team/app.git"
  path = "/srv/app"
  user = "app"
  key  = %q
}

tasks {
  task "Profile bypassed" {
    order  = 5
    bypass = true
    script = ["echo should-not-run"]
  }

  task "CLI bypassed" {
    order  = 10
    setup  = [repo("app-repo")]
    script = ["echo bypassed-by-cli"]
  }

  task "Main setup" {
    order = 20
    script = [
      "echo always",
      flag("vm", "echo vm-only"),
      log("Dry log check", "cyan"),
    ]
  }
}
`, filepath.ToSlash(missingKey))

	if err := os.WriteFile(filepath.Join(dir, "dry.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	out, _, err := runCLI(t, "run", "lab-ct", "dry", "--profiles-dir", dir, "--dry-run", "--bypass", "CLI bypassed")
	if err != nil {
		t.Fatalf("dry-run failed: %v\nout:\n%s", err, out)
	}

	for _, want := range []string{
		"[planned] group app",
		"[planned] timezone UTC",
		"[bypassed] (bypassed in profile) echo should-not-run",
		"[bypassed] (bypassed via --bypass) repo app-repo",
		"[planned] echo always",
		"[skipped] (missing flags: --vm) echo vm-only",
		"Dry log check",
		"Dry run complete (no SSH connection made).",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("log files = %d, want 1", len(entries))
	}
}

func TestRunKeyPreflightRespectsSkipAndBypass(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	t.Setenv("PLONK_LOG_DIR", logDir)

	missingKey := filepath.Join(dir, "missing.key")
	hcl := fmt.Sprintf(`
repository "app-repo" {
  url  = "git@example.com:team/app.git"
  path = "/srv/app"
  user = "app"
  key  = %q
}

tasks {
  task "Repo step" {
    order = 10
    setup = [flag("deploy", repo("app-repo"))]
  }
}
`, filepath.ToSlash(missingKey))

	if err := os.WriteFile(filepath.Join(dir, "preflight.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	// Without --deploy, repo step is skipped so missing key is not loaded.
	out, _, err := runCLI(t, "run", "lab-ct", "preflight", "--profiles-dir", dir, "--no-tui")
	if err != nil {
		t.Fatalf("expected skip-only run to succeed, got %v\nout:\n%s", err, out)
	}
	if !strings.Contains(out, "Skipped.") {
		t.Fatalf("expected Skipped summary, got:\n%s", out)
	}

	// With --deploy enabled, preflight must fail before any SSH execution.
	_, _, err = runCLI(t, "run", "lab-ct", "preflight", "--profiles-dir", dir, "--no-tui", "--deploy")
	if err == nil || !strings.Contains(err.Error(), "load deploy key") {
		t.Fatalf("expected deploy key preflight failure, got %v", err)
	}

	// With --deploy AND --bypass, bypassed task must not load the missing key.
	out, _, err = runCLI(t, "run", "lab-ct", "preflight", "--profiles-dir", dir, "--no-tui", "--deploy", "--bypass", "Repo step")
	if err != nil {
		t.Fatalf("expected bypassed run to succeed, got %v\nout:\n%s", err, out)
	}
	if !strings.Contains(out, "Bypassed.") {
		t.Fatalf("expected Bypassed summary, got:\n%s", out)
	}
}

func TestReadKeySource(t *testing.T) {
	dir := t.TempDir()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plainBlock, err := ssh.MarshalPrivateKey(priv, "test-plain")
	if err != nil {
		t.Fatal(err)
	}
	plainBytes := pem.EncodeToMemory(plainBlock)

	encBlock, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "test-enc", []byte("secret-pass"))
	if err != nil {
		t.Fatal(err)
	}
	encBytes := pem.EncodeToMemory(encBlock)

	plainPath := filepath.Join(dir, "plain.key")
	if err := os.WriteFile(plainPath, plainBytes, 0600); err != nil {
		t.Fatal(err)
	}

	encPath := filepath.Join(dir, "encrypted.key")
	if err := os.WriteFile(encPath, encBytes, 0600); err != nil {
		t.Fatal(err)
	}

	badPath := filepath.Join(dir, "invalid.key")
	if err := os.WriteFile(badPath, []byte("not-a-private-key\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadKeySource(plainPath)
	if err != nil {
		t.Fatalf("ReadKeySource(plain) failed: %v", err)
	}
	if !bytes.Equal(got, plainBytes) {
		t.Fatalf("ReadKeySource(plain) altered file bytes")
	}

	if _, err := ReadKeySource(encPath); err == nil || !strings.Contains(err.Error(), "passphrase-protected") {
		t.Fatalf("expected passphrase-protected error, got %v", err)
	}
	if _, err := ReadKeySource(badPath); err == nil {
		t.Fatal("expected parse error for invalid key")
	}
	if _, err := ReadKeySource(filepath.Join(dir, "missing.key")); err == nil {
		t.Fatal("expected error for missing key file")
	}
	if _, err := ReadKeySource(dir); err == nil {
		t.Fatal("expected error for directory key path")
	}
	if runtime.GOOS != "windows" {
		openPath := filepath.Join(dir, "world.key")
		if err := os.WriteFile(openPath, plainBytes, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadKeySource(openPath); err == nil {
			t.Fatal("expected permission error on Unix for mode 0644")
		}
	}
}

func TestRunUnknownBypassAndSanitization(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	t.Setenv("PLONK_LOG_DIR", logDir)

	hcl := `
name = "Escaped\u001b[31mName"
tasks {
  task "Check\u001b[2JHost" {
    order = 10
    script = ["echo \u001b[32mhi"]
  }
}
`
	profilePath := filepath.Join(dir, "sanitize.hcl")
	if err := os.WriteFile(profilePath, []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	// Misspelled --bypass must fail before preflight/logging.
	_, _, err := runCLI(t, "run", "lab-ct", "sanitize", "--profiles-dir", dir, "--dry-run", "--bypass", "No Such Task")
	if err == nil || !strings.Contains(err.Error(), `unknown --bypass task "No Such Task"`) {
		t.Fatalf("expected unknown --bypass error, got %v", err)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unknown --bypass should not open a run log, found %d files", len(entries))
	}

	// validate and plain run must strip terminal escape sequences from plan/task text.
	valOut, _, err := runCLI(t, "validate", profilePath, "--profiles-dir", dir)
	if err != nil {
		t.Fatalf("validate sanitize failed: %v", err)
	}
	if strings.ContainsRune(valOut, '\x1b') {
		t.Fatalf("validate leaked ANSI escape: %q", valOut)
	}

	runOut, _, err := runCLI(t, "run", "lab-ct", profilePath, "--profiles-dir", dir, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run sanitize failed: %v", err)
	}
	if strings.ContainsRune(runOut, '\x1b') {
		t.Fatalf("plain run leaked ANSI escape: %q", runOut)
	}
}

func TestExitCode(t *testing.T) {
	exitErr := exec.Command("go", "tool", "nonexistent-plonk-subtool").Run()
	ee, ok := errors.AsType[*exec.ExitError](exitErr)
	if !ok {
		t.Skipf("expected *exec.ExitError, got %T", exitErr)
	}

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "canceled", err: fmt.Errorf("wrap: %w", context.Canceled), want: 130},
		{name: "exec exit", err: fmt.Errorf("remote failed: %w", exitErr), want: ee.ExitCode()},
		{name: "generic", err: errors.New("boom"), want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Fatalf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestExamplesValidateAndDryRun(t *testing.T) {
	logDir := t.TempDir()
	t.Setenv("PLONK_LOG_DIR", logDir)
	examplesDir, err := filepath.Abs(filepath.Join("..", "examples"))
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"minimal", "app-machine"} {
		t.Run("validate-"+name, func(t *testing.T) {
			out, errOut, err := runCLI(t, "validate", name, "--profiles-dir", examplesDir)
			if err != nil {
				t.Fatalf("validate %s failed: %v (stderr=%q)", name, err, errOut)
			}
			if out == "" {
				t.Fatalf("expected non-empty validate output for %s", name)
			}
		})
	}

	out, errOut, err := runCLI(t, "run", "example-host", filepath.Join("..", "examples", "app-machine.hcl"), "--dry-run", "--bypass", "Create reports account")
	if err != nil {
		t.Fatalf("app-machine dry-run failed: %v (stderr=%q)", err, errOut)
	}
	for _, want := range []string{
		"[planned] group app",
		"[planned] user app",
		"[planned] timezone America/Chicago",
		"[bypassed] (bypassed via --bypass) group reports",
		"[planned] repo your-repo",
		"[planned] unit example.service",
		"Dry run complete (no SSH connection made).",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("app-machine dry-run missing %q:\n%s", want, out)
		}
	}
}
