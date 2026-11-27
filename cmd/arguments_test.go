package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vanvanni/plonkr/profile"
	"github.com/zclconf/go-cty/cty"
)

const inputProfile = `arguments {
  argument "name" {
    type = string
    prompt = "Application name"
    validation { pattern = "^[a-z]+$" }
  }
  argument "port" {
    type = integer
    default = 8080
    validation {
      min = 1024
      max = 65535
    }
  }
}
tasks {
  task "Inputs" {
    order = 1
    script = ["printf '%s:%s' ${shellquote(args.name)} ${args.port}"]
  }
}`

func TestArgumentsCLI(t *testing.T) {
	env := newE2E(t)
	path := env.profile(inputProfile)
	for _, tc := range []struct {
		name    string
		options []string
		want    string
	}{
		{"missing", nil, "missing profile arguments"},
		{"invalid", []string{"--arg", "name=ok", "--arg", "port=80"}, "range"},
		{"unknown", []string{"--arg", "typo=x"}, "unknown profile argument"},
		{"duplicate", []string{"--arg", "name=one", "--arg", "name=two"}, "duplicate"},
		{"syntax", []string{"--arg", "name"}, "name=value"},
		{"auto approval", []string{"--auto-approve"}, "missing profile arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runWith(t.Context(), append([]string{"run", "h", path}, tc.options...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			env.noConnections()
			if len(env.logs()) != 0 {
				t.Fatal("input failures must precede opening the journal")
			}
		})
	}
	out, _, err := runWith(t.Context(), "validate", path)
	if err != nil || !strings.Contains(out, "Unresolved inputs: name") {
		t.Fatalf("validate: %s, %v", out, err)
	}
	out, _, err = runWith(t.Context(), "validate", path, "--arg", "name=app")
	if err != nil || !strings.Contains(out, "'app' 8080") {
		t.Fatalf("resolved validate: %s, %v", out, err)
	}
	out, _, err = runWith(t.Context(), "run", "h", path, "--help")
	if err != nil || !strings.Contains(out, "--arg") {
		t.Fatalf("help: %s, %v", out, err)
	}
	out, _, err = runWith(t.Context(), "run", "h", path, "--dry-run", "--arg", "name=app")
	if err != nil || !strings.Contains(out, "'app' 8080") {
		t.Fatalf("dry run: %s, %v", out, err)
	}
	env.noConnections()
	_, _, err = runWith(t.Context(), "run", "h", path, "--no-tui", "--arg", "name=app", "--arg", "port=9000")
	if err != nil {
		t.Fatal(err)
	}
	calls := env.calls()
	if len(calls) != 1 || !strings.Contains(strings.Join(calls[0].Args, " "), "app") || !strings.Contains(strings.Join(calls[0].Args, " "), "9000") {
		t.Fatalf("resolved execution: %+v", calls)
	}
}

func TestMissingForwardingAndLateValidationNeverConnect(t *testing.T) {
	for _, tc := range []struct{ mapping, want string }{
		{"{}", `requires argument "port"`},
		{"{port = args.port}", "range"},
	} {
		env := newE2E(t)
		if err := os.Mkdir(filepath.Join(env.dir, "snippets"), 0700); err != nil {
			t.Fatal(err)
		}
		snippet := `snippet "s" {
  arguments {
    argument "port" {
      type = integer
      validation { min = 9000 }
    }
  }
  commands = ["echo ${args.port}"]
}`
		if err := os.WriteFile(filepath.Join(env.dir, "snippets/s.hcl"), []byte(snippet), 0600); err != nil {
			t.Fatal(err)
		}
		path := env.profile(strings.Replace(inputProfile, `"printf '%s:%s' ${shellquote(args.name)} ${args.port}"`, `run("s", `+tc.mapping+`)`, 1))
		_, _, err := runWith(t.Context(), "run", "h", path, "--arg", "name=app", "--no-tui")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("error = %v, want %s", err, tc.want)
		}
		env.noConnections()
		if len(env.logs()) != 0 {
			t.Fatal("failed binding must not create a journal")
		}
	}
}

func TestCollectArguments(t *testing.T) {
	p, err := profile.Prepare([]byte(inputProfile), "inputs.hcl", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]cty.Value{}
	var out bytes.Buffer
	input, cleanup, err := collectArguments(t.Context(), p, values, strings.NewReader("Bad\r\napp\r\n\r\nremaining\n"), &out, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if values["name"].AsString() != "app" || !values["port"].RawEquals(cty.NumberIntVal(8080)) {
		t.Fatal(values)
	}
	if strings.Count(out.String(), "Application name:") != 2 || !strings.Contains(out.String(), "[8080]") {
		t.Fatal(out.String())
	}
	rest, err := io.ReadAll(input)
	if err != nil || string(rest) != "remaining\n" {
		t.Fatalf("lost subsequent input: %q, %v", rest, err)
	}
	for _, tc := range []struct {
		input string
		want  error
	}{
		{"", io.EOF}, {"\x03", context.Canceled},
	} {
		_, closeInput, err := collectArguments(t.Context(), p, map[string]cty.Value{}, strings.NewReader(tc.input), io.Discard, true)
		closeInput()
		if !errors.Is(err, tc.want) {
			t.Fatalf("error = %v, want %v", err, tc.want)
		}
	}
	_, closeInput, err := collectArguments(t.Context(), p, map[string]cty.Value{}, strings.NewReader(strings.Repeat("x", (64<<10)+1)), io.Discard, true)
	closeInput()
	if err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatal(err)
	}
}

func TestArgumentPromptCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cancelreader only supports the real stdin console on Windows; pipes use its uncancellable fallback")
	}
	p, err := profile.Prepare([]byte(inputProfile), "inputs.hcl", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	prompted := make(chan struct{}, 1)
	go func() {
		_, cleanup, err := collectArguments(ctx, p, map[string]cty.Value{}, r, promptSignal{prompted}, true)
		cleanup()
		done <- err
	}()
	<-prompted
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled prompt did not return")
	}
}

type promptSignal struct{ ready chan struct{} }

func (w promptSignal) Write(p []byte) (int, error) {
	select {
	case w.ready <- struct{}{}:
	default:
	}
	return len(p), nil
}
