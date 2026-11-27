package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vanvanni/plonkr/profile"
)

// These tests drive the real CLI, runner, provisioning and run log against a fake
// `ssh` executable (this test binary, re-invoked). The fake records every
// invocation and answers from a list of rules, so a test can assert both what
// the user sees and whether any connection was made at all.

func TestMain(m *testing.M) {
	if os.Getenv("PLONK_CMD_FAKE_SSH") == "1" {
		os.Exit(runFakeSSH())
	}
	os.Exit(m.Run())
}

type fakeRule struct {
	Contains string `json:"contains"` // matched against the remote command
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Exit     int    `json:"exit,omitempty"`
	SleepMS  int    `json:"sleep_ms,omitempty"`
}

type fakeCall struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
}

func runFakeSSH() int {
	stdin, _ := io.ReadAll(os.Stdin)
	call, _ := json.Marshal(fakeCall{Args: os.Args[1:], Stdin: string(stdin)})
	if path := os.Getenv("PLONK_CMD_CAPTURE"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.Write(append(call, '\n'))
			_ = f.Close()
		}
	}
	command := ""
	if len(os.Args) > 1 {
		command = os.Args[len(os.Args)-1]
	}
	var rules []fakeRule
	if raw, err := os.ReadFile(os.Getenv("PLONK_CMD_RULES")); err == nil {
		_ = json.Unmarshal(raw, &rules)
	}
	for _, rule := range rules {
		if !strings.Contains(command, rule.Contains) {
			continue
		}
		if rule.SleepMS > 0 {
			time.Sleep(time.Duration(rule.SleepMS) * time.Millisecond)
		}
		_, _ = io.WriteString(os.Stdout, rule.Stdout)
		_, _ = io.WriteString(os.Stderr, rule.Stderr)
		return rule.Exit
	}
	return 0
}

type e2e struct {
	t       *testing.T
	dir     string
	logDir  string
	capture string
}

// newE2E installs the fake ssh first on PATH and isolates the run log.
func newE2E(t *testing.T, rules ...fakeRule) *e2e {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "ssh"
	if runtime.GOOS == "windows" {
		name = "ssh.exe"
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), data, 0o700); err != nil {
		t.Fatal(err)
	}
	env := &e2e{t: t, dir: t.TempDir(), logDir: t.TempDir(), capture: filepath.Join(t.TempDir(), "calls.jsonl")}
	rulesPath := filepath.Join(t.TempDir(), "rules.json")
	raw, _ := json.Marshal(rules)
	if err := os.WriteFile(rulesPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLONK_CMD_FAKE_SSH", "1")
	t.Setenv("PLONK_CMD_CAPTURE", env.capture)
	t.Setenv("PLONK_CMD_RULES", rulesPath)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PLONK_LOG_DIR", env.logDir)
	return env
}

func (e *e2e) profile(source string) string {
	e.t.Helper()
	path := filepath.Join(e.dir, "p.hcl")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return path
}

func (e *e2e) calls() []fakeCall {
	e.t.Helper()
	raw, err := os.ReadFile(e.capture)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var calls []fakeCall
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var call fakeCall
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			e.t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}

func (e *e2e) noConnections() {
	e.t.Helper()
	if calls := e.calls(); len(calls) != 0 {
		e.t.Fatalf("expected no ssh connection, got %d: %+v", len(calls), calls)
	}
}

func (e *e2e) logs() []string {
	e.t.Helper()
	entries, err := os.ReadDir(e.logDir)
	if err != nil {
		e.t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		paths = append(paths, filepath.Join(e.logDir, entry.Name()))
	}
	return paths
}

func (e *e2e) onlyLog() string {
	e.t.Helper()
	logs := e.logs()
	if len(logs) != 1 {
		e.t.Fatalf("log files = %v, want exactly one", logs)
	}
	raw, err := os.ReadFile(logs[0])
	if err != nil {
		e.t.Fatal(err)
	}
	return string(raw)
}

func runWith(ctx context.Context, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	c := New(strings.NewReader(""), &stdout, &stderr)
	c.SetArgs(args)
	err := c.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func script(steps ...string) string {
	quoted := make([]string, len(steps))
	for i, step := range steps {
		quoted[i] = fmt.Sprintf("%q", step)
	}
	return "tasks {\n task \"Work\" {\n order = 1\n script = [" + strings.Join(quoted, ", ") + "]\n }\n}\n"
}

func TestE2ESuccessfulRun(t *testing.T) {
	env := newE2E(t, fakeRule{Contains: "echo one", Stdout: "one\n"}, fakeRule{Contains: "echo two", Stdout: "two\n", Stderr: "warn\n"})
	path := env.profile(script("echo one", "echo two"))
	out, errOut, err := runWith(t.Context(), "run", "web-1", path, "--no-tui")
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	for _, want := range []string{"[1] Work", "$ echo one", "one\n", "$ echo two", "two\n", "[completed]", "Complete."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "warn") {
		t.Errorf("remote stderr must reach stderr, got %q", errOut)
	}
	calls := env.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	for _, call := range calls {
		// Always non-interactive, never trusts a user's RequestTTY, always after `--`.
		if got := strings.Join(call.Args[:5], " "); got != "-T -o BatchMode=yes -- web-1" {
			t.Errorf("ssh args = %v", call.Args)
		}
		if !strings.HasPrefix(call.Args[5], "sh -ec '") {
			t.Errorf("remote command = %q", call.Args[5])
		}
	}
	log := env.onlyLog()
	for _, want := range []string{"plonk · web-1 · p", "$ echo one", "one\n", "[completed] echo two", "Complete."} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
}

func TestE2EFailedStepStopsTheRunAndKeepsTheExitCode(t *testing.T) {
	env := newE2E(t, fakeRule{Contains: "explode", Stderr: "kaboom\n", Exit: 7})
	path := env.profile(script("echo before", "explode", "echo after"))
	out, errOut, err := runWith(t.Context(), "run", "web-1", path, "--no-tui")
	if err == nil {
		t.Fatalf("expected a failure\n%s", out)
	}
	if got := ExitCode(err); got != 7 {
		t.Errorf("ExitCode = %d, want the remote status 7 (err = %v)", got, err)
	}
	for _, want := range []string{"[failed]", "Failed.", "1 completed, 1 failed, 1 pending"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "kaboom") {
		t.Errorf("stderr = %q", errOut)
	}
	if calls := env.calls(); len(calls) != 2 {
		t.Fatalf("a failure must stop the run: %d calls", len(calls))
	}
	log := env.onlyLog()
	if !strings.Contains(log, "Error: task \"Work\", step 2 (shell)") || strings.Contains(log, "echo after\n[") {
		t.Errorf("log = %s", log)
	}
}

func TestE2EConnectionFailureIsDistinctFromCommandFailure(t *testing.T) {
	env := newE2E(t, fakeRule{Contains: "", Stderr: "ssh: connect to host web-1 port 22: Connection refused\n", Exit: 255})
	path := env.profile(script("echo hi"))
	_, _, err := runWith(t.Context(), "run", "web-1", path, "--no-tui")
	if err == nil || ExitCode(err) != 255 {
		t.Fatalf("err = %v, exit code = %d, want 255", err, ExitCode(err))
	}
}

func TestE2ERemoteOutputCannotDriveTheTerminal(t *testing.T) {
	env := newE2E(t, fakeRule{Contains: "noisy", Stdout: "\x1b[2J\x1b]0;pwned\x07ok\r\nline\x00two\x1b[31m\n", Stderr: "\x1b[1Aerr\n"})
	path := env.profile(script("noisy"))
	out, errOut, err := runWith(t.Context(), "run", "web-1", path, "--no-tui")
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"stdout": out, "stderr": errOut, "log": env.onlyLog()} {
		if strings.ContainsAny(text, "\x1b\x07\x00") || strings.Contains(text, "pwned") {
			t.Errorf("%s leaked terminal control: %q", name, text)
		}
	}
	if !strings.Contains(out, "ok\n") || !strings.Contains(out, "linetwo") {
		t.Errorf("visible text must survive: %q", out)
	}
}

func TestE2EDynamicFlagsGateSteps(t *testing.T) {
	env := newE2E(t)
	path := env.profile("tasks {\n task \"Work\" {\n order = 1\n script = [\"echo always\", flag(\"vm\", \"echo vm-only\")]\n }\n}\n")
	out, _, err := runWith(t.Context(), "run", "h", path, "--no-tui")
	if err != nil || len(env.calls()) != 1 || !strings.Contains(out, "[skipped] (missing flags: --vm) echo vm-only") {
		t.Fatalf("without --vm: err = %v, calls = %d\n%s", err, len(env.calls()), out)
	}
	if err := os.Remove(env.capture); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runWith(t.Context(), "run", "h", path, "--no-tui", "--vm"); err != nil || len(env.calls()) != 2 {
		t.Fatalf("with --vm: err = %v, calls = %d", err, len(env.calls()))
	}
}

func TestE2EDryRunNeverConnectsOrReadsKeys(t *testing.T) {
	env := newE2E(t)
	path := env.profile(`
repository "r" {
  url  = "git@example.com:a/b.git"
  path = "/srv/r"
  user = "app"
  key  = "/definitely/missing/key"
}
tasks {
  task "Locked" {
    order = 1
    script = ["echo hi"]
    lock {
      exists = ["/usr/bin/true"]
      eval   = ["true"]
    }
  }
  task "Clone" {
    order = 2
    setup = [repo("r")]
  }
}
`)
	out, _, err := runWith(t.Context(), "run", "h", path, "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "lock is evaluated when the task runs") || !strings.Contains(out, "Dry run complete") {
		t.Errorf("output:\n%s", out)
	}
	env.noConnections()
}

func TestE2EBypassedWorkMakesNoConnection(t *testing.T) {
	env := newE2E(t)
	path := env.profile(`
tasks {
  task "A" {
    order = 1
    bypass = true
    script = ["echo a"]
    lock { eval = ["true"] }
  }
  task "B" {
    order = 2
    script = ["echo b"]
    lock { eval = ["true"] }
  }
}
`)
	out, _, err := runWith(t.Context(), "run", "h", path, "--no-tui", "--bypass", "B", "--bypass", "B")
	if err != nil || !strings.Contains(out, "Bypassed.") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	env.noConnections()
}

func TestE2ELockHeldBypassesWithoutRunningTheTask(t *testing.T) {
	env := newE2E(t, fakeRule{Contains: "ok=1", Stdout: "exists 0 1\n"})
	path := env.profile(`
tasks {
  task "Install" {
    order = 1
    script = ["echo install"]
    lock { exists = ["/usr/bin/true"] }
  }
  task "After" {
    order = 2
    script = ["echo after"]
  }
}
`)
	out, _, err := runWith(t.Context(), "run", "h", path, "--no-tui")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "[bypassed] (bypassed by lock: exists) echo install") {
		t.Errorf("output:\n%s", out)
	}
	calls := env.calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want the probe and the following task", len(calls))
	}
	if strings.Contains(calls[0].Args[len(calls[0].Args)-1], "echo install") || !strings.Contains(calls[1].Args[len(calls[1].Args)-1], "echo after") {
		t.Errorf("unexpected calls: %+v", calls)
	}

	// --force runs the task without probing the lock.
	if err := os.Remove(env.capture); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runWith(t.Context(), "run", "h", path, "--no-tui", "--force"); err != nil {
		t.Fatal(err)
	}
	calls = env.calls()
	if len(calls) != 2 || !strings.Contains(calls[0].Args[len(calls[0].Args)-1], "echo install") {
		t.Fatalf("--force calls = %+v", calls)
	}
}

func TestE2EProbeFailureStopsBeforeTheTask(t *testing.T) {
	env := newE2E(t, fakeRule{Contains: "ok=1", Stderr: "sudo: a password is required\n", Exit: 1})
	path := env.profile(`
tasks {
  task "Install" {
    order = 1
    script = ["echo install"]
    lock { exists = ["/a"] }
  }
}
`)
	out, _, err := runWith(t.Context(), "run", "h", path, "--no-tui")
	if err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if len(env.calls()) != 1 {
		t.Fatalf("the task must not run after a failed probe: %d calls", len(env.calls()))
	}
}

func TestE2EInvalidInputNeverConnects(t *testing.T) {
	env := newE2E(t)
	good := env.profile(script("echo hi"))
	keyed := filepath.Join(env.dir, "keyed.hcl")
	if err := os.WriteFile(keyed, []byte(`
repository "r" {
  url  = "git@example.com:a/b.git"
  path = "/srv/r"
  user = "app"
  key  = "`+filepath.ToSlash(filepath.Join(env.dir, "missing-key"))+`"
}
tasks {
  task "Clone" {
    order = 1
    setup = [repo("r")]
  }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"flag before host", []string{"run", "--dry-run", "h", good}, "invalid SSH destination"},
		{"option-like host", []string{"run", "-oProxyCommand=x", good}, "invalid SSH destination"},
		{"empty host", []string{"run", "", good}, "invalid SSH destination"},
		{"host with space", []string{"run", "a b", good}, "invalid SSH destination"},
		{"omitted host with SSH work", []string{"run", good}, "needs an SSH target"},
		{"missing profile file", []string{"run", "h", filepath.Join(env.dir, "nope.hcl")}, "read profile"},
		{"unknown profile name", []string{"run", "h", "nope", "--profiles-dir", env.dir}, "read profile"},
		{"unknown flag", []string{"run", "h", good, "--nope"}, "unknown flag"},
		{"unknown shorthand", []string{"run", "h", good, "-x"}, "unknown shorthand flag"},
		{"extra positional", []string{"run", "h", good, "extra"}, `unknown command "extra"`},
		{"bypass without value", []string{"run", "h", good, "--bypass"}, "flag needs an argument"},
		{"bypass unknown task", []string{"run", "h", good, "--bypass", "Nope"}, "unknown --bypass task"},
		{"bypass is case-sensitive", []string{"run", "h", good, "--bypass", "work"}, "unknown --bypass task"},
		{"bool flag with junk value", []string{"run", "h", good, "--force=maybe"}, "invalid argument"},
		{"profiles-dir without value", []string{"run", "h", good, "--profiles-dir"}, "requires a value"},
		{"profiles-dir empty", []string{"run", "h", good, "--profiles-dir="}, "must not be empty"},
		{"profiles-dir blank", []string{"run", "h", good, "--profiles-dir", "  "}, "must not be empty"},
		{"profiles-dir missing", []string{"run", "h", good, "--profiles-dir", filepath.Join(env.dir, "nope")}, "open profiles directory"},
		{"profiles-dir is a file", []string{"run", "h", good, "--profiles-dir", good}, "not a directory"},
		{"missing deploy key", []string{"run", "h", keyed, "--no-tui"}, "load deploy key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runWith(t.Context(), tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
			if ExitCode(err) != 1 {
				t.Errorf("ExitCode = %d, want 1", ExitCode(err))
			}
			env.noConnections()
		})
	}
}

func TestE2EHelpForms(t *testing.T) {
	newE2E(t)
	for _, args := range [][]string{{"run"}, {"run", "-h"}, {"run", "--help"}, {"run", "help"}} {
		out, _, err := runWith(t.Context(), args...)
		if err != nil || !strings.Contains(out, "run HOST PROFILE") {
			t.Errorf("%v: err = %v, out = %q", args, err, out)
		}
	}
	if _, _, err := runWith(t.Context(), "nope"); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown command: %v", err)
	}
}

func TestE2EUnusableLogDirectoryFailsBeforeConnecting(t *testing.T) {
	env := newE2E(t)
	file := filepath.Join(env.dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLONK_LOG_DIR", file)
	path := env.profile(script("echo hi"))
	_, _, err := runWith(t.Context(), "run", "h", path, "--no-tui")
	if err == nil || !strings.Contains(err.Error(), "log directory") {
		t.Fatalf("err = %v", err)
	}
	env.noConnections()

	t.Setenv("PLONK_LOG_DIR", "relative/logs")
	_, _, err = runWith(t.Context(), "run", "h", path, "--no-tui")
	if err == nil || !strings.Contains(err.Error(), "PLONK_LOG_DIR must be an absolute path") {
		t.Fatalf("relative log dir: %v", err)
	}
	env.noConnections()
}

func TestE2ECancellationExitsWithSignalConvention(t *testing.T) {
	env := newE2E(t, fakeRule{Contains: "slow", SleepMS: 20000})
	path := env.profile(script("slow", "echo never"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if raw, err := os.ReadFile(env.capture); err == nil && len(raw) > 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	out, _, err := runWith(ctx, "run", "h", path, "--no-tui")
	if !errors.Is(err, context.Canceled) || ExitCode(err) != 130 {
		t.Fatalf("err = %v, exit code = %d", err, ExitCode(err))
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("cancellation must not wait for the remote command to finish")
	}
	if !strings.Contains(out, "Cancelled.") || strings.Contains(out, "echo never\n  [") {
		t.Errorf("output:\n%s", out)
	}
	if len(env.calls()) != 1 {
		t.Errorf("calls after cancellation: %d", len(env.calls()))
	}
	if log := env.onlyLog(); !strings.Contains(log, "Cancelled.") {
		t.Errorf("log:\n%s", log)
	}
}

func TestE2EForceIsAReservedFlagName(t *testing.T) {
	env := newE2E(t)
	path := env.profile("tasks {\n task \"Work\" {\n order = 1\n script = [flag(\"force\", \"echo hi\")]\n }\n}\n")
	for _, args := range [][]string{{"validate", path}, {"run", "h", path, "--dry-run"}} {
		_, _, err := runWith(t.Context(), args...)
		if err == nil || !strings.Contains(err.Error(), `reserved flag name "force"`) {
			t.Errorf("%v: err = %v, want a validation error instead of a crash", args, err)
		}
	}
	env.noConnections()
}

func TestE2EEveryBuiltInFlagIsReserved(t *testing.T) {
	// A profile flag named like a built-in would redefine it and crash flag
	// registration, so every flag shown by --help must be a reserved name.
	env := newE2E(t)
	plan, err := loadProfileRef(env.profile(script("echo hi")), "", false)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := executeProfile(t.Context(), "h", "p", plan, []string{"--help"}, strings.NewReader(""), &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	var builtin []string
	for _, match := range regexp.MustCompile(`(?m)^\s+(?:-\w, )?--([a-z][a-z-]*)`).FindAllStringSubmatch(stdout.String(), -1) {
		builtin = append(builtin, match[1])
	}
	if len(builtin) < 8 {
		t.Fatalf("could not read the built-in flags from --help: %v\n%s", builtin, stdout.String())
	}
	for _, name := range builtin {
		source := fmt.Sprintf("tasks {\n task \"Work\" {\n order = 1\n script = [flag(%q, \"echo hi\")]\n }\n}\n", name)
		if _, err := profile.Parse([]byte(source), "x.hcl"); err == nil || !strings.Contains(err.Error(), "reserved flag name") {
			t.Errorf("built-in flag --%s is not reserved: %v", name, err)
		}
	}
}

func TestE2ESameNamedTasksAreBypassedTogether(t *testing.T) {
	env := newE2E(t)
	path := env.profile(`
tasks {
  task "Same" {
    order = 1
    script = ["echo one"]
  }
  task "Same" {
    order = 2
    script = ["echo two"]
  }
}
`)
	out, _, err := runWith(t.Context(), "run", "h", path, "--no-tui", "--bypass", "Same")
	if err != nil || strings.Count(out, "[bypassed]") != 2 {
		t.Fatalf("err = %v\n%s", err, out)
	}
	env.noConnections()
}

func TestE2EProfileFlagValuesAreBooleans(t *testing.T) {
	env := newE2E(t)
	path := env.profile("tasks {\n task \"Work\" {\n order = 1\n script = [flag(\"vm\", \"echo vm\")]\n }\n}\n")
	for value, wantCalls := range map[string]int{"--vm": 1, "--vm=true": 1, "--vm=false": 0, "--vm=1": 1, "--vm=0": 0} {
		_ = os.Remove(env.capture)
		if _, _, err := runWith(t.Context(), "run", "h", path, "--no-tui", value); err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if got := len(env.calls()); got != wantCalls {
			t.Errorf("%s: calls = %d, want %d", value, got, wantCalls)
		}
	}
}
