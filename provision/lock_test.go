package provision

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

// runLocalExecSSH stands in for ssh: it runs the remote command in a local POSIX
// shell, with fixture tools first on PATH, and passes stdin, stdout and the
// exit status through. The real scripts and their quoting run unchanged.
func runLocalExecSSH() int {
	sh := findPOSIXShell()
	if sh == "" || len(os.Args) < 2 {
		return 2
	}
	remote := os.Args[len(os.Args)-1]
	cmd := exec.Command(sh, "-c", withPOSIXTools(os.Getenv("PLONK_PROV_TOOLS"), remote))
	cmd.Env = posixEnv(sh)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return exitErr.ExitCode()
		}
		return 2
	}
	return 0
}

// lockFixture is a target made of a temporary directory.
type lockFixture struct {
	t        *testing.T
	dir      string
	stateDir string
	executor Executor
}

func newLockFixture(t *testing.T) *lockFixture {
	t.Helper()
	if findPOSIXShell() == "" {
		t.Skip("no POSIX sh available")
	}
	installFakeSSH(t)
	t.Setenv("PLONK_PROV_FAKE_SSH", "exec")

	tools := t.TempDir()
	// The fixture login is root, so managed scripts need no sudo.
	if err := os.WriteFile(filepath.Join(tools, "id"), []byte("#!/bin/sh\necho 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLONK_PROV_TOOLS", tools)

	dir := t.TempDir()
	f := &lockFixture{t: t, dir: dir, stateDir: filepath.Join(dir, "etc", ".states")}
	f.executor = Executor{
		SSH:      runner.SSH{Host: "fixture"},
		Plan:     &profile.Plan{Name: "Test profile"},
		StateDir: f.stateDir,
	}
	return f
}

// path returns a target path in POSIX form, which Git's sh also accepts on Windows.
func (f *lockFixture) path(name string) string {
	return filepath.ToSlash(filepath.Join(f.dir, name))
}

func (f *lockFixture) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *lockFixture) task(name string, lock profile.Lock) profile.Task {
	return profile.Task{Name: name, Lock: lock}
}

func (f *lockFixture) check(task profile.Task) (bool, string) {
	f.t.Helper()
	held, reason, err := f.executor.CheckLock(f.t.Context(), task)
	if err != nil {
		f.t.Fatalf("CheckLock(%s): %v", task.Name, err)
	}
	return held, reason
}

func (f *lockFixture) record(task profile.Task) {
	f.t.Helper()
	if err := f.executor.RecordLock(f.t.Context(), task); err != nil {
		f.t.Fatalf("RecordLock(%s): %v", task.Name, err)
	}
}

func TestLockExistsHoldsWithoutState(t *testing.T) {
	f := newLockFixture(t)
	task := f.task("exists", profile.Lock{Exists: []string{f.path("tool")}})

	if held, _ := f.check(task); held {
		t.Fatal("lock held although the path does not exist")
	}
	f.write("tool", "x")
	held, reason := f.check(task)
	if !held || reason != "exists" {
		t.Fatalf("held = %v, reason = %q; want held by exists", held, reason)
	}

	// Recording an exists-only lock stores nothing and needs no privileges.
	f.record(task)
	if _, err := os.Stat(f.stateDir); !os.IsNotExist(err) {
		t.Fatalf("exists-only lock touched the state directory: %v", err)
	}
}

func TestLockExistsRequiresEveryPath(t *testing.T) {
	f := newLockFixture(t)
	f.write("one", "x")
	task := f.task("all", profile.Lock{Exists: []string{f.path("one"), f.path("two")}})
	if held, _ := f.check(task); held {
		t.Fatal("lock held although one path is missing")
	}
	f.write("two", "x")
	if held, _ := f.check(task); !held {
		t.Fatal("lock not held although every path exists")
	}
}

func TestLockEvalHoldsOnlyWhenResultMatchesTheRecordedOne(t *testing.T) {
	f := newLockFixture(t)
	f.write("version", "go1.26\n")
	lock := profile.Lock{Eval: []string{"cat " + f.path("version")}}
	task := f.task("install", lock)

	if held, _ := f.check(task); held {
		t.Fatal("first run must not be held: nothing is recorded yet")
	}
	f.record(task)
	held, reason := f.check(task)
	if !held || reason != "eval unchanged" {
		t.Fatalf("held = %v, reason = %q; want held by unchanged eval", held, reason)
	}

	f.write("version", "go1.27\n")
	if held, _ := f.check(task); held {
		t.Fatal("lock held although the eval result changed")
	}
	f.record(task)
	if held, _ := f.check(task); !held {
		t.Fatal("lock not held after recording the new result")
	}
}

func TestLockEditedDefinitionInvalidatesRecordedState(t *testing.T) {
	f := newLockFixture(t)
	f.write("version", "go1.26\n")
	task := f.task("install", profile.Lock{Eval: []string{"cat " + f.path("version")}})
	f.record(task)

	// The same output under a different command is not the state that was recorded.
	edited := f.task("install", profile.Lock{Eval: []string{"cat " + f.path("version") + " | cat"}})
	if held, _ := f.check(edited); held {
		t.Fatal("lock held although its definition changed")
	}
}

func TestLockExistsAndEvalMustBothHold(t *testing.T) {
	f := newLockFixture(t)
	f.write("version", "go1.26\n")
	lock := profile.Lock{Exists: []string{f.path("tool")}, Eval: []string{"cat " + f.path("version")}}
	task := f.task("both", lock)
	f.record(task)

	if held, _ := f.check(task); held {
		t.Fatal("lock held with the same eval result but a missing path")
	}
	f.write("tool", "x")
	held, reason := f.check(task)
	if !held || reason != "exists and eval unchanged" {
		t.Fatalf("held = %v, reason = %q; want both directives named", held, reason)
	}
	f.write("version", "go1.27\n")
	if held, _ := f.check(task); held {
		t.Fatal("lock held with the path present but a changed eval result")
	}
}

func TestLockEvalSeesExitStatusAndIgnoresStderr(t *testing.T) {
	f := newLockFixture(t)
	marker := f.path("marker")
	// The command prints nothing to stdout; only its exit status can differ.
	task := f.task("status", profile.Lock{Eval: []string{"echo noise >&2; test -e " + marker}})
	f.record(task)
	if held, _ := f.check(task); !held {
		t.Fatal("a failing command is a state like any other and must hold when unchanged")
	}
	f.write("marker", "x")
	if held, _ := f.check(task); held {
		t.Fatal("lock held although the exit status changed")
	}
}

func TestLockEvalRecordedAfterTheTaskChangedTheMachine(t *testing.T) {
	f := newLockFixture(t)
	version := f.path("version")
	task := f.task("install", profile.Lock{Eval: []string{"cat " + version + " 2>/dev/null"}})

	// Before the task, the command fails; the task then installs the file.
	if held, _ := f.check(task); held {
		t.Fatal("must run on a fresh machine")
	}
	f.write("version", "go1.26\n")
	f.record(task)
	if held, _ := f.check(task); !held {
		t.Fatal("the state recorded after the task must hold on the next run")
	}
}

func TestLockEvalIsSkippedWhileAPathIsMissing(t *testing.T) {
	f := newLockFixture(t)
	marker := f.path("ran")
	task := f.task("gated", profile.Lock{
		Exists: []string{f.path("absent")},
		Eval:   []string{"touch " + marker},
	})
	if held, _ := f.check(task); held {
		t.Fatal("lock held although a path is missing")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "ran")); !os.IsNotExist(err) {
		t.Fatalf("eval ran although a missing path already decided the outcome: %v", err)
	}
}

func TestLockStateIsPrivateAndKeepsOtherTasks(t *testing.T) {
	f := newLockFixture(t)
	f.write("a", "1")
	f.write("b", "2")
	a := f.task("A", profile.Lock{Eval: []string{"cat " + f.path("a")}})
	b := f.task("B", profile.Lock{Eval: []string{"cat " + f.path("b")}})
	f.record(a)
	f.record(b)

	if held, _ := f.check(a); !held {
		t.Fatal("recording a second task dropped the first task's state")
	}
	if held, _ := f.check(b); !held {
		t.Fatal("second task's state was not recorded")
	}

	files, err := os.ReadDir(f.stateDir)
	if err != nil || len(files) != 1 {
		t.Fatalf("state directory = %v, %v; want one file and no temporary leftovers", files, err)
	}
	if name := files[0].Name(); name != stateFileName("Test profile") {
		t.Fatalf("state file = %q, want %q", name, stateFileName("Test profile"))
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{f.stateDir: 0700, filepath.Join(f.stateDir, files[0].Name()): 0600} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("%s mode = %v (%v), want %v", path, info.Mode().Perm(), err, want)
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(f.stateDir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var state lockState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if state.Profile != "Test profile" || len(state.Tasks) != 2 {
		t.Fatalf("state = %+v", state)
	}
}

func TestLockStateIsPerProfile(t *testing.T) {
	f := newLockFixture(t)
	f.write("v", "1")
	task := f.task("install", profile.Lock{Eval: []string{"cat " + f.path("v")}})
	f.record(task)

	other := f.executor
	other.Plan = &profile.Plan{Name: "Another profile"}
	held, _, err := other.CheckLock(t.Context(), task)
	if err != nil || held {
		t.Fatalf("held = %v, err = %v; state from one profile must not satisfy another", held, err)
	}
}

func TestLockRejectsAnUnreadableStateFile(t *testing.T) {
	f := newLockFixture(t)
	f.write("v", "1")
	task := f.task("install", profile.Lock{Eval: []string{"cat " + f.path("v")}})
	f.record(task)

	if err := os.WriteFile(filepath.Join(f.stateDir, stateFileName("Test profile")), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.executor.CheckLock(t.Context(), task); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("error = %v, want a clear message instead of silently running or skipping", err)
	}
}

func TestParseLockProbe(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name           string
		out            string
		exists, evals  int
		wantExistsAll  bool
		wantEval       []string
		wantErrContain string
	}{
		{"all present", "exists 0 1\nexists 1 1\neval 0 " + hash + "\n", 2, 1, true, []string{hash}, ""},
		{"one missing hides eval", "exists 0 1\nexists 1 0\n", 2, 1, false, nil, ""},
		{"exists only", "exists 0 1\n", 1, 0, true, nil, ""},
		{"eval only", "eval 0 " + hash + "\neval 1 " + hash + "\n", 0, 2, true, []string{hash, hash}, ""},
		{"missing exists line", "exists 0 1\n", 2, 0, false, nil, "incomplete"},
		{"missing eval line", "eval 0 " + hash + "\n", 0, 2, false, nil, "incomplete"},
		{"bad hash", "eval 0 xyz\n", 0, 1, false, nil, "unexpected"},
		{"index out of range", "exists 3 1\n", 1, 0, false, nil, "unexpected"},
		{"unknown kind", "noise 0 1\n", 1, 0, false, nil, "unexpected"},
		{"stray text", "warning: something\n", 1, 0, false, nil, "unexpected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs, err := parseLockProbe(tc.out, tc.exists, tc.evals)
			if tc.wantErrContain != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrContain) {
					t.Fatalf("error = %v, want %q", err, tc.wantErrContain)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if obs.ExistsAll != tc.wantExistsAll || !slices.Equal(obs.Eval, tc.wantEval) {
				t.Fatalf("observation = %+v, want exists %v eval %v", obs, tc.wantExistsAll, tc.wantEval)
			}
		})
	}
}

func TestLockStateParsing(t *testing.T) {
	empty, err := parseLockState(nil, "p")
	if err != nil || len(empty.Tasks) != 0 {
		t.Fatalf("empty state = %+v, %v", empty, err)
	}
	if _, err := parseLockState([]byte(`{"version": 99, "tasks": {}}`), "p"); err == nil {
		t.Fatal("an unknown state version must be refused rather than overwritten")
	}
	raw, err := empty.with("p", "t", lockEntry{Lock: "d", Eval: []string{"h"}})
	if err != nil {
		t.Fatal(err)
	}
	round, err := parseLockState(raw, "p")
	if err != nil {
		t.Fatal(err)
	}
	if entry := round.Tasks["t"]; entry.Lock != "d" || !slices.Equal(entry.Eval, []string{"h"}) {
		t.Fatalf("round trip = %+v", entry)
	}

	lock := profile.Lock{Eval: []string{"x"}}
	held := lockState{Tasks: map[string]lockEntry{"t": {Lock: lock.Digest(), Eval: []string{"h"}}}}
	if !held.holds("t", lock, []string{"h"}) {
		t.Fatal("matching digest and result must hold")
	}
	if held.holds("t", lock, []string{"other"}) || held.holds("t", profile.Lock{Eval: []string{"y"}}, []string{"h"}) || held.holds("missing", lock, []string{"h"}) {
		t.Fatal("a different result, lock digest or task must not hold")
	}
}

func TestLockScriptsAreValidShell(t *testing.T) {
	sh := findPOSIXShell()
	if sh == "" {
		t.Skip("no POSIX sh available")
	}
	for name, script := range map[string]string{
		"probe":      lockProbeScript([]string{"/a b", "/it's"}, []string{"echo 'quoted'", "printf \"x\"\nfalse"}),
		"probe-none": lockProbeScript(nil, nil),
		"read":       stateReadScript(DefaultStateDir, "abc.json"),
		"write":      stateWriteScript(DefaultStateDir, "abc.json"),
		"read-root":  asRoot(stateReadScript(DefaultStateDir, "abc.json")),
		"write-root": asRoot(stateWriteScript(DefaultStateDir, "abc.json")),
	} {
		cmd := exec.CommandContext(t.Context(), sh, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: sh -n: %v\n%s", name, err, out)
		}
	}
}

func TestStateFileNameIsFixedWidthAndSafe(t *testing.T) {
	a, b := stateFileName("../../etc/passwd; rm -rf /"), stateFileName("Minimal machine check")
	if a == b || strings.ContainsAny(a+b, "/\\ ;") || len(a) != len(b) {
		t.Fatalf("state file names %q and %q must be distinct, fixed width and free of path characters", a, b)
	}
}
