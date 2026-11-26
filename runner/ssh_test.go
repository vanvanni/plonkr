package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("PLONK_TEST_FAKE_SSH"); mode != "" {
		os.Exit(runFakeSSH(mode))
	}
	os.Exit(m.Run())
}

type fakeSSHCapture struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
}

func runFakeSSH(mode string) int {
	switch mode {
	case "capture":
		input, _ := io.ReadAll(os.Stdin)
		data, err := json.Marshal(fakeSSHCapture{
			Args:  os.Args[1:],
			Stdin: string(input),
		})
		if err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			return 2
		}
		if path := os.Getenv("PLONK_TEST_CAPTURE_FILE"); path != "" {
			if err := os.WriteFile(path, data, 0600); err != nil {
				_, _ = io.WriteString(os.Stderr, err.Error())
				return 2
			}
		}
		if out := os.Getenv("PLONK_TEST_STDOUT"); out != "" {
			_, _ = io.WriteString(os.Stdout, out)
		}
		if errOut := os.Getenv("PLONK_TEST_STDERR"); errOut != "" {
			_, _ = io.WriteString(os.Stderr, errOut)
		}
		return 0
	case "sleep":
		time.Sleep(10 * time.Second)
		return 0
	case "ssh-G":
		realSSH := os.Getenv("PLONK_TEST_REAL_SSH")
		cfg := os.Getenv("PLONK_TEST_SSH_CONFIG")
		args := append([]string{"-F", cfg, "-G"}, os.Args[1:]...)
		cmd := exec.Command(realSSH, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
				return exitErr.ExitCode()
			}
			return 1
		}
		return 0
	default:
		return 99
	}
}

func installFakeSSH(t *testing.T, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	name := "ssh"
	if runtime.GOOS == "windows" {
		name = "ssh.exe"
	}
	dst := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		data, readErr := os.ReadFile(exe)
		if readErr != nil {
			t.Fatalf("read test executable: %v", readErr)
		}
		if writeErr := os.WriteFile(dst, data, 0700); writeErr != nil {
			t.Fatalf("write fake ssh: %v", writeErr)
		}
	} else if err := os.Link(exe, dst); err != nil {
		data, readErr := os.ReadFile(exe)
		if readErr != nil {
			t.Fatalf("read test executable: %v", readErr)
		}
		if writeErr := os.WriteFile(dst, data, 0700); writeErr != nil {
			t.Fatalf("write fake ssh: %v", writeErr)
		}
	}
	t.Setenv("PLONK_TEST_FAKE_SSH", mode)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestValidateHost(t *testing.T) {
	tests := []struct {
		host    string
		wantErr bool
	}{
		{host: "my-vm"},
		{host: "root@10.0.0.1"},
		{host: "user@example.com"},
		{host: "user@::1"},
		{host: "[::1]"},
		{host: "user@[::1]:2222"},
		{host: "_alias"},
		{host: "", wantErr: true},
		{host: "-oProxyCommand=touch /bad", wantErr: true},
		{host: "a b", wantErr: true},
		{host: "foo\nbar", wantErr: true},
		{host: "x;id", wantErr: true},
		{host: "foo'bar", wantErr: true},
	}

	for _, tc := range tests {
		err := ValidateHost(tc.host)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateHost(%q) = %v, wantErr = %v", tc.host, err, tc.wantErr)
		}
	}
}

func TestSSHExecuteAndExecuteInput(t *testing.T) {
	tests := []struct {
		name      string
		ssh       SSH
		useInput  bool
		command   string
		input     string
		wantArgs  []string
		wantStdin string
	}{
		{
			name:      "noninteractive execute quotes single quotes and newlines",
			ssh:       SSH{Host: "user@alias", Stdin: strings.NewReader("from-struct")},
			command:   "printf '%s\\n' \"it's ok\"\necho done",
			wantArgs:  []string{"-T", "-o", "BatchMode=yes", "--", "user@alias", "sh -ec 'printf '\"'\"'%s\\n'\"'\"' \"it'\"'\"'s ok\"\necho done'"},
			wantStdin: "from-struct",
		},
		{
			name:     "tty execute allocates pty",
			ssh:      SSH{Host: "my-vm", TTY: true},
			command:  "ls -la",
			wantArgs: []string{"-t", "--", "my-vm", "sh -ec 'ls -la'"},
		},
		{
			name:      "execute input disables tty and sends stdin",
			ssh:       SSH{Host: "my-vm", TTY: true},
			useInput:  true,
			command:   "cat > /tmp/file",
			input:     "secret-key-bytes\n",
			wantArgs:  []string{"-T", "-o", "BatchMode=yes", "--", "my-vm", "sh -ec 'cat > /tmp/file'"},
			wantStdin: "secret-key-bytes\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := installFakeSSH(t, "capture")
			capturePath := filepath.Join(dir, "capture.json")
			t.Setenv("PLONK_TEST_CAPTURE_FILE", capturePath)
			t.Setenv("PLONK_TEST_STDOUT", "out-data")
			t.Setenv("PLONK_TEST_STDERR", "err-data")

			var out, errOut bytes.Buffer
			var err error
			if tc.useInput {
				err = tc.ssh.ExecuteInput(t.Context(), tc.command, strings.NewReader(tc.input), &out, &errOut)
			} else {
				err = tc.ssh.Execute(t.Context(), tc.command, &out, &errOut)
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			raw, err := os.ReadFile(capturePath)
			if err != nil {
				t.Fatalf("read capture: %v", err)
			}
			var got fakeSSHCapture
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal capture: %v", err)
			}
			if !slices.Equal(got.Args, tc.wantArgs) {
				t.Errorf("args = %q\nwant = %q", got.Args, tc.wantArgs)
			}
			if got.Stdin != tc.wantStdin {
				t.Errorf("stdin = %q, want %q", got.Stdin, tc.wantStdin)
			}
			if out.String() != "out-data" || errOut.String() != "err-data" {
				t.Errorf("stdout=%q stderr=%q", out.String(), errOut.String())
			}
			if tc.useInput && !tc.ssh.TTY {
				t.Error("ExecuteInput mutated caller SSH.TTY")
			}
		})
	}
}

func TestSSHErrorsAndCancellation(t *testing.T) {
	t.Run("invalid host", func(t *testing.T) {
		err := (SSH{Host: "-bad"}).Execute(t.Context(), "true", &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error for invalid host")
		}
	})

	t.Run("pre-cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := (SSH{Host: "alias"}).Execute(ctx, "true", &bytes.Buffer{}, &bytes.Buffer{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})

	t.Run("cancelled while running", func(t *testing.T) {
		installFakeSSH(t, "sleep")
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		err := (SSH{Host: "alias"}).Execute(ctx, "sleep 10", &bytes.Buffer{}, &bytes.Buffer{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
	})
}

func TestSSHInputDisablesConfiguredPTY(t *testing.T) {
	realSSH, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("OpenSSH client not installed")
	}
	dir := installFakeSSH(t, "ssh-G")
	cfgPath := filepath.Join(dir, "config")
	if err := os.WriteFile(cfgPath, []byte("Host alias\n  RequestTTY force\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLONK_TEST_REAL_SSH", realSSH)
	t.Setenv("PLONK_TEST_SSH_CONFIG", cfgPath)

	var out, errOut bytes.Buffer
	transport := SSH{Host: "alias", TTY: true}
	if err := transport.ExecuteInput(t.Context(), "true", strings.NewReader("fixture"), &out, &errOut); err != nil {
		t.Fatalf("ssh -G failed: %v: %s", err, errOut.String())
	}
	if !strings.Contains(strings.ToLower(out.String()), "requesttty false") {
		t.Fatalf("expected requesttty false in effective config, got:\n%s", out.String())
	}
}
