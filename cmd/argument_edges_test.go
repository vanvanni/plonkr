package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
	"github.com/zclconf/go-cty/cty"
)

func TestArgumentInputLineEndingsAndHandoff(t *testing.T) {
	for _, separator := range []string{"\n", "\r\n", "\r"} {
		for _, copyMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%q/copy=%t", separator, copyMode), func(t *testing.T) {
				input := &argumentInput{Reader: bufio.NewReader(strings.NewReader("first" + separator + "second" + separator + "remaining\n"))}
				for _, want := range []string{"first", "second"} {
					got, err := readArgumentAnswer(input, io.Discard)
					if err != nil || got != want {
						t.Fatalf("answer=%q, error=%v", got, err)
					}
				}
				var remaining bytes.Buffer
				var err error
				if copyMode {
					_, err = io.Copy(&remaining, input)
				} else {
					data, readErr := io.ReadAll(input)
					err = readErr
					remaining.Write(data)
				}
				if err != nil || remaining.String() != "remaining\n" {
					t.Fatalf("handoff=%q, error=%v", remaining.String(), err)
				}
			})
		}
	}
}

func TestArgumentAnswerEditingAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
		err   error
	}{
		{name: "empty", input: "\n"},
		{name: "CR enter", input: "text\r", want: "text"},
		{name: "backspace", input: "wrong\b\b\b\b\bright\n", want: "right"},
		{name: "delete", input: "abc\x7fd\n", want: "abd"},
		{name: "Unicode delete", input: "界\bword\n", want: "word"},
		{name: "empty delete", input: "\b\x7fok\n", want: "ok"},
		{name: "Ctrl D", input: "\x04", err: io.EOF},
		{name: "partial EOF", input: "unfinished", err: io.EOF},
		{name: "Ctrl C", input: "partial\x03", err: context.Canceled},
		{name: "limit", input: strings.Repeat("x", 64<<10) + "\n", want: strings.Repeat("x", 64<<10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &argumentInput{Reader: bufio.NewReader(strings.NewReader(tc.input))}
			got, err := readArgumentAnswer(reader, io.Discard)
			if !errors.Is(err, tc.err) || got != tc.want {
				t.Fatalf("length=%d, error=%v; want length=%d, error=%v", len(got), err, len(tc.want), tc.err)
			}
		})
	}
	for _, answer := range []string{"word\n", "\n", "a\b\n"} {
		reader := &argumentInput{Reader: bufio.NewReader(strings.NewReader(answer))}
		_, err := readArgumentAnswer(reader, &argumentFailWriter{failAt: 1})
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("echo error was lost: %v", err)
		}
	}
}

type argumentFailWriter struct{ calls, failAt int }

func (w *argumentFailWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.failAt {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestArgumentCollectionFailurePaths(t *testing.T) {
	p, err := profile.Prepare([]byte(inputProfile), "inputs.hcl", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		input  string
		failAt int
	}{
		{name: "question output", input: "app\n\n", failAt: 1},
		{name: "validation output", input: "Bad\n", failAt: 2},
		{name: "second question", input: "app\n\n", failAt: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cleanup, err := collectArguments(
				t.Context(), p, map[string]cty.Value{}, strings.NewReader(tc.input),
				&argumentFailWriter{failAt: tc.failAt}, true,
			)
			cleanup()
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal(err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, cleanup, err := collectArguments(ctx, p, map[string]cty.Value{}, strings.NewReader(""), io.Discard, true)
	cleanup()
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	values := map[string]cty.Value{"name": cty.StringVal("provided"), "port": cty.NumberIntVal(9000)}
	input := strings.NewReader("untouched")
	remaining, cleanup, err := collectArguments(t.Context(), p, values, input, io.Discard, true)
	cleanup()
	if err != nil || remaining != input || input.Len() != len("untouched") {
		t.Fatalf("fully supplied inputs consumed stdin: %v", err)
	}
}

func TestArgumentPromptTextIsSanitized(t *testing.T) {
	source := `arguments {
  argument "value" {
    type = string
    prompt = "Question\u001b[31m\nnext"
    validation {
      pattern = "^ok$"
      message = "Try again\u001b[31m"
    }
  }
}
tasks {
  task "Check" {
    order = 0
    script = ["echo ${shellquote(args.value)}"]
  }
}`
	p, err := profile.Prepare([]byte(source), "inputs.hcl", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_, cleanup, err := collectArguments(
		t.Context(), p, map[string]cty.Value{}, strings.NewReader("bad\nok\n"), &output, true,
	)
	cleanup()
	if err != nil || strings.Contains(output.String(), "\x1b") || !strings.Contains(output.String(), "Try again") {
		t.Fatalf("unsafe prompt output: %q, %v", output.String(), err)
	}
}

func TestArgumentCLIValuesAndValidateIsolation(t *testing.T) {
	env := newE2E(t)
	source := `arguments {
  argument "text" { type = string }
  argument "n" {
    type = integer
    default = 7
  }
  argument "enabled" {
    type = bool
    default = true
  }
}
tasks {
  task "Values" {
    order = 0
    script = [flag("details", log(args.text)), "echo ${args.n} ${args.enabled}"]
  }
}`
	path := env.profile(source)
	for _, text := range []string{"a=b=c", "--dry-run", "hello world", "a'b; $(id)", "${args.n}"} {
		out, _, err := runWith(t.Context(), "validate", path, "--arg=text="+text, "--arg=n=0", "--arg=enabled=false")
		if err != nil || !strings.Contains(out, text) || !strings.Contains(out, "echo 0 false") {
			t.Fatalf("value %q: %v\n%s", text, err, out)
		}
	}
	for _, value := range []string{"TRUE", "1", "yes", ""} {
		_, _, err := runWith(t.Context(), "validate", path, "--arg=text=x", "--arg=enabled="+value)
		if err == nil || !strings.Contains(err.Error(), "true or false") {
			t.Fatal(err)
		}
	}
	out, _, err := runWith(t.Context(), "run", "h", path, "--help")
	if err != nil || !strings.Contains(out, "--details") {
		t.Fatalf("unresolved inputs hid dynamic flags: %s, %v", out, err)
	}
	env.noConnections()
	if len(env.logs()) != 0 {
		t.Fatal("validation/help must not open a run journal")
	}
}

func TestResolvedConfigurationErrorsNeverStartEarlierTasks(t *testing.T) {
	env := newE2E(t)
	for _, tc := range []struct{ name, extra, script string }{
		{name: "Linux user", extra: "configuration { users = [{name = args.value}] }", script: `["echo valid"]`},
		{name: "repository path", extra: "repository \"app\" {\nurl = \"https://example.com/app.git\"\npath = args.value\nuser = \"app\"\n}", script: `["echo valid"]`},
		{name: "lock path", script: `["echo valid"]`},
		{name: "undeclared late reference", script: `["echo ${args.missing}"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lock := ""
			if tc.name == "lock path" {
				lock = "\nlock { exists = [args.value] }"
			}
			source := "arguments {\nargument \"value\" { type = string }\n}\n" + tc.extra + `
tasks {
  task "First must never start" {
    order = 0
    script = ["echo FIRST_TASK_RAN"]
  }
  task "Invalid later task" {
    order = 1
    script = ` + tc.script + lock + "\n}\n}"
			path := env.profile(source)
			_, _, err := runWith(t.Context(), "run", "h", path, "--arg=value=../Bad!", "--no-tui")
			if err == nil {
				t.Fatal("invalid resolved configuration was accepted")
			}
			env.noConnections()
			if len(env.logs()) != 0 {
				t.Fatal("preflight error opened a journal")
			}
		})
	}
}
