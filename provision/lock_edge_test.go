package provision

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
)

func TestLockPathsAreDataNotShellSyntax(t *testing.T) {
	f := newLockFixture(t)
	names := []string{
		"with space", "it's", "dollar $HOME", "sub $(touch pwned)", "back `touch pwned`", "semi;touch pwned", "bracket[0]", "-dash", "uni✓é",
	}
	if runtime.GOOS != "windows" { // Windows forbids * in file names
		names = append(names, "glob*")
	}
	for _, name := range names {
		f.write(name, "x")
	}
	for _, name := range names {
		task := f.task("exists "+name, profile.Lock{Exists: []string{f.path(name)}})
		if held, _ := f.check(task); !held {
			t.Errorf("%q exists but the lock was not held", name)
		}
		missing := f.task("missing "+name, profile.Lock{Exists: []string{f.path(name) + ".missing"}})
		if held, _ := f.check(missing); held {
			t.Errorf("%q.missing does not exist but the lock was held", name)
		}
	}
	for _, evidence := range []string{"pwned", "touch"} {
		if _, err := os.Stat(filepath.Join(f.dir, evidence)); err == nil {
			t.Fatalf("a path was executed as shell code: %s exists", evidence)
		}
	}
	// A glob is a literal name, so it does not match unrelated files.
	f.write("global", "x")
	if held, _ := f.check(f.task("glob", profile.Lock{Exists: []string{f.path("nomatch*")}})); held {
		t.Error("a glob character must not expand")
	}
}

func TestLockEvalCommandsAreShellButStayIsolated(t *testing.T) {
	f := newLockFixture(t)
	f.write("marker", "v1")
	cmd := "cat " + f.path("marker") + ` | tr 'a-z' 'A-Z'; echo "it's fine" && true`
	task := f.task("pipeline", profile.Lock{Eval: []string{cmd}})
	if held, _ := f.check(task); held {
		t.Fatal("no record yet, the lock must not hold")
	}
	f.record(task)
	if held, reason := f.check(task); !held || reason != "eval unchanged" {
		t.Fatalf("held = %t, reason = %q", held, reason)
	}
	f.write("marker", "v2")
	if held, _ := f.check(task); held {
		t.Fatal("changed output must release the lock")
	}
}

func TestLockEvalResultSemantics(t *testing.T) {
	f := newLockFixture(t)
	hash := func(command string) string {
		t.Helper()
		obs, err := f.executor.probeLock(t.Context(), nil, []string{command})
		if err != nil {
			t.Fatalf("probe %q: %v", command, err)
		}
		return obs.Eval[0]
	}
	same := func(a, b string) bool { return hash(a) == hash(b) }

	for _, tc := range []struct {
		name, a, b string
		equal      bool
	}{
		{"trailing newlines are not significant", "printf 'out'", "printf 'out\\n\\n'", true},
		{"internal newlines are significant", "printf 'a\\nb'", "printf 'a b'", false},
		{"leading whitespace is significant", "printf ' out'", "printf 'out'", false},
		{"stderr is ignored", "echo out; echo noise >&2", "echo out", true},
		{"exit status is significant", "echo out; exit 3", "echo out", false},
		{"different statuses differ", "exit 3", "exit 4", false},
		{"a failing command is a stable state", "false", "false", true},
		{"missing command is a stable state", "no-such-command-plonk 2>/dev/null", "no-such-command-plonk 2>/dev/null", true},
		{"empty output with success", "true", ":", true},
		{"status and output cannot be confused", "printf '0\\nx'; exit 0", "printf 'x'; exit 0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := same(tc.a, tc.b); got != tc.equal {
				t.Fatalf("hash equality of %q and %q = %t, want %t", tc.a, tc.b, got, tc.equal)
			}
		})
	}

	t.Run("stdin is closed", func(t *testing.T) {
		if hash("cat; echo done") != hash("echo done") {
			t.Fatal("an eval command must not be able to read the script that carries it")
		}
	})
	t.Run("each command runs in its own shell", func(t *testing.T) {
		obs, err := f.executor.probeLock(t.Context(), nil, []string{"X=1; exit 0", `printf '%s' "${X:-unset}"`})
		if err != nil {
			t.Fatal(err)
		}
		if want := hash(`printf '%s' unset`); obs.Eval[1] != want {
			t.Fatal("shell state leaked from one eval command to the next")
		}
	})
	t.Run("a command that exits the shell does not end the probe", func(t *testing.T) {
		obs, err := f.executor.probeLock(t.Context(), nil, []string{"exit 5", "echo after"})
		if err != nil || len(obs.Eval) != 2 {
			t.Fatalf("%+v, %v", obs, err)
		}
	})
}

func TestLockStateSurvivesHostileNames(t *testing.T) {
	f := newLockFixture(t)
	f.executor.Plan = &profile.Plan{Name: "Weird \"profile\" ✓ 'name' \\ $x"}
	names := []string{"plain", "with space", `quo"te`, "it's", `back\slash`, "uni✓日本語", "$(touch pwned)", strings.Repeat("long ", 200)}
	for _, name := range names {
		task := f.task(name, profile.Lock{Eval: []string{"echo " + name[:min(len(name), 5)]}})
		f.record(task)
	}
	for _, name := range names {
		task := f.task(name, profile.Lock{Eval: []string{"echo " + name[:min(len(name), 5)]}})
		if held, _ := f.check(task); !held {
			t.Errorf("state for %q was lost", name)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, "pwned")); err == nil {
		t.Fatal("a task name was executed")
	}
	entries, err := os.ReadDir(f.stateDir)
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") || strings.HasPrefix(entries[0].Name(), ".tmp") {
		t.Fatalf("state directory = %v, %v; want exactly one finished state file", entries, err)
	}
}

func TestLockRecordReplacesOnlyItsOwnTask(t *testing.T) {
	f := newLockFixture(t)
	f.write("a", "1")
	f.write("b", "1")
	taskA := f.task("A", profile.Lock{Eval: []string{"cat " + f.path("a")}})
	taskB := f.task("B", profile.Lock{Eval: []string{"cat " + f.path("b")}})
	f.record(taskA)
	f.record(taskB)
	f.write("a", "2")
	f.record(taskA) // re-recording A must not disturb B
	if held, _ := f.check(taskB); !held {
		t.Fatal("recording one task dropped another task's state")
	}
	f.write("b", "2")
	if held, _ := f.check(taskA); !held {
		t.Fatal("A was re-recorded against the new value and must hold")
	}
	if held, _ := f.check(taskB); held {
		t.Fatal("B's output changed and must not hold")
	}
}

func TestLockDigestChangeAfterEditNeverHoldsUntilRerecorded(t *testing.T) {
	f := newLockFixture(t)
	f.write("v", "1")
	old := f.task("T", profile.Lock{Eval: []string{"cat " + f.path("v")}})
	f.record(old)
	edited := f.task("T", profile.Lock{Eval: []string{"cat " + f.path("v") + " # edited"}})
	if held, _ := f.check(edited); held {
		t.Fatal("an edited lock definition must not reuse the old record")
	}
	f.record(edited)
	if held, _ := f.check(edited); !held {
		t.Fatal("the edited lock must hold once recorded")
	}
	if held, _ := f.check(old); held {
		t.Fatal("the original definition was replaced and must no longer hold")
	}
}

func TestLockStateFileEdgeCases(t *testing.T) {
	task := profile.Task{Name: "T", Lock: profile.Lock{Eval: []string{"echo x"}}}
	for _, tc := range []struct {
		name    string
		state   string
		wantErr string
	}{
		{"empty file is no state", "", ""},
		{"whitespace file is no state", " \n", ""},
		{"not JSON", "{oops", "not valid JSON"},
		{"truncated JSON", `{"version":1,"tasks":{"T":`, "not valid JSON"},
		{"future version", `{"version":2,"tasks":{}}`, "version 2 is not supported"},
		{"missing version is the current version", `{"tasks":{}}`, ""},
		{"explicit zero version", `{"version":0,"tasks":{}}`, "version 0 is not supported"},
		{"null tasks", `{"version":1,"tasks":null}`, ""},
		{"unknown fields are ignored", `{"version":1,"tasks":{},"extra":true}`, ""},
		{"wrong shape", `{"version":1,"tasks":[]}`, "not valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLockFixture(t)
			if err := os.MkdirAll(f.stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.stateDir, stateFileName(f.executor.Plan.Name)), []byte(tc.state), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := f.executor.CheckLock(t.Context(), task)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}

	t.Run("a corrupt state file is never silently replaced", func(t *testing.T) {
		f := newLockFixture(t)
		if err := os.MkdirAll(f.stateDir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(f.stateDir, stateFileName(f.executor.Plan.Name))
		if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := f.executor.RecordLock(t.Context(), task); err == nil {
			t.Fatal("recording over a corrupt state file must fail rather than discard it")
		}
		if got, _ := os.ReadFile(path); string(got) != "{oops" {
			t.Fatalf("the corrupt file was modified: %q", got)
		}
	})
}

func TestLockNeedsAPlanForStateButNotForExists(t *testing.T) {
	f := newLockFixture(t)
	f.write("here", "x")
	f.executor.Plan = nil
	if held, _, err := f.executor.CheckLock(t.Context(), f.task("T", profile.Lock{Exists: []string{f.path("here")}})); err != nil || !held {
		t.Fatalf("exists-only lock: held = %t, error = %v", held, err)
	}
	if _, _, err := f.executor.CheckLock(t.Context(), f.task("T", profile.Lock{Eval: []string{"true"}})); err == nil {
		t.Fatal("an eval lock cannot be checked without a plan to name its state")
	}
	if err := f.executor.RecordLock(t.Context(), f.task("T", profile.Lock{Eval: []string{"true"}})); err == nil {
		t.Fatal("an eval lock cannot be recorded without a plan to name its state")
	}
	if err := f.executor.RecordLock(t.Context(), f.task("T", profile.Lock{Exists: []string{"/x"}})); err != nil {
		t.Fatalf("exists-only locks keep no state: %v", err)
	}
}
