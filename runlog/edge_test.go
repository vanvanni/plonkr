package runlog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

func onePlan() *profile.Plan {
	return &profile.Plan{Name: "P", Tasks: []profile.Task{{Name: "T", Order: 1, Steps: []profile.Step{{Text: "echo hi"}}}}}
}

func TestOpenNamesFilesSafelyFromAnyHost(t *testing.T) {
	for _, host := range []string{
		"web-1", "root@10.0.0.1", "../../../etc/passwd", `..\..\windows`, "a/b/c", "host with spaces", "uni✓é", "[::1]:22",
		"", strings.Repeat("x", 500), "con", "nul", "a\x00b", "a\nb", "-rf", "*", "..", ".",
	} {
		t.Run(host, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "logs")
			l, err := Open(dir, host, "profile", onePlan())
			if err != nil {
				t.Fatalf("Open(%q): %v", host, err)
			}
			defer l.Finish(nil, false)
			if filepath.Dir(l.Path) != dir {
				t.Fatalf("log escaped its directory: %s", l.Path)
			}
			name := filepath.Base(l.Path)
			if !strings.HasSuffix(name, ".log") || len(name) > 64+len("20060102T150405Z--")+len("*.log")+30 {
				t.Fatalf("unexpected file name %q", name)
			}
			if strings.ContainsAny(name, `/\:*?"<>|`+"\x00\n") {
				t.Fatalf("file name %q contains unsafe characters", name)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Fatalf("log directory holds %d entries", len(entries))
			}
		})
	}
}

func TestOpenCreatesPrivateFilesAndNeverReusesOne(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "logs")
	seen := map[string]bool{}
	for range 20 {
		l, err := Open(dir, "host", "profile", onePlan())
		if err != nil {
			t.Fatal(err)
		}
		if seen[l.Path] {
			t.Fatalf("log path %s reused", l.Path)
		}
		seen[l.Path] = true
		if runtime.GOOS != "windows" {
			if info, err := os.Stat(l.Path); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("log mode = %v, %v; want 0600", info.Mode().Perm(), err)
			}
		}
		if err := l.Finish(nil, false); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("log directory mode = %v, %v; want 0700", info.Mode().Perm(), err)
		}
	}
}

func TestOpenFailsClearlyWhenTheDirectoryIsUnusable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"a file": file, "under a file": filepath.Join(file, "logs")} {
		if _, err := Open(dir, "host", "profile", onePlan()); err == nil || !strings.Contains(err.Error(), "create log directory") {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if _, err := Open("", "host", "profile", onePlan()); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("empty directory: %v", err)
	}
}

func TestOpenHeadingIsSanitizedAndFallsBackToTheReference(t *testing.T) {
	plan := onePlan()
	plan.Name = ""
	l, err := Open(t.TempDir(), "host\x1b[31m\nname", "ref\tname", plan)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Finish(nil, false)
	if l.heading != "plonk · host name · ref name" {
		t.Fatalf("heading = %q", l.heading)
	}
}

func TestEventToleratesOutOfRangeIndexes(t *testing.T) {
	l, err := Open(t.TempDir(), "host", "profile", onePlan())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []runner.Event{
		{Kind: runner.TaskStarted, Task: -1}, {Kind: runner.TaskStarted, Task: 99},
		{Kind: runner.StepStarted, Task: 0, Step: -1, Text: "x"}, {Kind: runner.StepStarted, Task: 0, Step: 99, Text: "x"},
		{Kind: runner.StepDone, Task: 0, Step: 99, Status: runner.StatusCompleted},
		{Kind: runner.TaskDone, Task: -5}, {Kind: runner.EventKind(200), Task: 0},
	} {
		if _, err := l.Event(e); err != nil {
			t.Fatalf("Event(%+v): %v", e, err)
		}
	}
	if err := l.Finish(nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestEventFormatting(t *testing.T) {
	l, err := Open(t.TempDir(), "host", "profile", &profile.Plan{Name: "P", Tasks: []profile.Task{
		{Name: "Multi\nline", Order: 7, Steps: []profile.Step{{Text: "echo a\necho b"}, {Op: profile.OpLog, Text: "note"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []runner.Event{
		{Kind: runner.TaskStarted, Task: 0, Status: runner.StatusRunning, Text: "Multi\nline"},
		{Kind: runner.StepStarted, Task: 0, Step: 0, Text: "echo a\necho b"},
		{Kind: runner.Output, Task: 0, Step: 0, Text: "a\r\nb\rc\n"},
		{Kind: runner.Output, Task: 0, Step: 0, Text: "err\n", Stderr: true},
		{Kind: runner.StepDone, Task: 0, Step: 0, Status: runner.StatusFailed, Text: "echo a\necho b", Err: os.ErrClosed},
		{Kind: runner.StepStarted, Task: 0, Step: 1, Text: "note"},
		{Kind: runner.Message, Task: 0, Step: 1, Text: "\x1b[1mhello\x1b[0m\x07 world"},
		{Kind: runner.StepDone, Task: 0, Step: 1, Status: runner.StatusCompleted, Text: "note"},
		{Kind: runner.TaskDone, Task: 0, Status: runner.StatusFailed, Text: "Multi\nline"},
	} {
		if _, err := l.Event(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Finish(os.ErrClosed, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(l.Path)
	log := string(raw)
	for _, want := range []string{
		"[7] Multi line\n", // task label is one line
		"$ echo a\necho b\n",
		"a\nb\nc\n", // CRLF is one line break, a lone CR starts a new line
		"err\n",
		"[failed] echo a echo b\n",
		"hello world\n", // log() message, escapes and BEL stripped, and no "$ note" echo
		"[completed] note\n",
		"[failed] Multi line\n",
		"Failed.\n",
		"Error: file already closed\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "$ note") || strings.ContainsAny(log, "\x1b\x07\r") {
		t.Errorf("unexpected content in log:\n%q", log)
	}
}

func TestOutputWithoutTrailingNewlineIsTerminatedBeforeTheNextLine(t *testing.T) {
	l, err := Open(t.TempDir(), "host", "profile", onePlan())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = l.Event(runner.Event{Kind: runner.TaskStarted, Task: 0})
	_, _ = l.Event(runner.Event{Kind: runner.StepStarted, Task: 0, Step: 0, Text: "echo -n x"})
	_, _ = l.Event(runner.Event{Kind: runner.Output, Task: 0, Step: 0, Text: "partial"})
	_, _ = l.Event(runner.Event{Kind: runner.StepDone, Task: 0, Step: 0, Status: runner.StatusCompleted, Text: "echo -n x"})
	if err := l.Finish(nil, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(l.Path)
	if !strings.Contains(string(raw), "partial\n[completed] echo -n x\n") {
		t.Fatalf("log = %q", raw)
	}
}

func TestLogWriteFailureIsReportedNotSwallowed(t *testing.T) {
	l, err := Open(t.TempDir(), "host", "profile", onePlan())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.file.Close(); err != nil { // simulate the disk going away
		t.Fatal(err)
	}
	if _, err := l.Event(runner.Event{Kind: runner.TaskStarted, Task: 0}); err == nil || !strings.Contains(err.Error(), "write run log") {
		t.Fatalf("Event after a failed write: %v", err)
	}
	// The failure sticks: later events keep reporting it instead of pretending to log.
	if _, err := l.Event(runner.Event{Kind: runner.Output, Text: "x"}); err == nil {
		t.Fatal("the error must persist")
	}
	if err := l.Finish(nil, false); err == nil {
		t.Fatal("Finish must report the write failure")
	}
}

func TestFinishTwiceReportsAnErrorInsteadOfPanicking(t *testing.T) {
	l, err := Open(t.TempDir(), "host", "profile", onePlan())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(nil, false); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(nil, false); err == nil {
		t.Fatal("finishing a closed log must report an error")
	}
}

func TestSummaryEdges(t *testing.T) {
	t.Run("no tasks", func(t *testing.T) {
		l, err := Open(t.TempDir(), "h", "p", &profile.Plan{Name: "Empty"})
		if err != nil {
			t.Fatal(err)
		}
		defer l.Finish(nil, false)
		if got := l.Summary(nil, false); !strings.HasPrefix(got, "plonk · h · Empty · Complete.\nTotal: 0 steps\n") {
			t.Fatalf("summary = %q", got)
		}
	})
	t.Run("running tasks are reported as pending", func(t *testing.T) {
		l, err := Open(t.TempDir(), "h", "p", onePlan())
		if err != nil {
			t.Fatal(err)
		}
		defer l.Finish(nil, false)
		_, _ = l.Event(runner.Event{Kind: runner.TaskStarted, Task: 0})
		got := l.Summary(nil, false)
		if !strings.Contains(got, "(pending)") || strings.Contains(got, "(running)") {
			t.Fatalf("summary = %q", got)
		}
	})
	t.Run("mixed outcomes", func(t *testing.T) {
		plan := &profile.Plan{Name: "M", Tasks: []profile.Task{{Name: "T", Steps: make([]profile.Step, 6)}}}
		l, err := Open(t.TempDir(), "h", "p", plan)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Finish(nil, false)
		_, _ = l.Event(runner.Event{Kind: runner.TaskStarted, Task: 0})
		for i, status := range []runner.Status{runner.StatusCompleted, runner.StatusCompleted, runner.StatusSkipped, runner.StatusBypassed, runner.StatusFailed} {
			_, _ = l.Event(runner.Event{Kind: runner.StepDone, Task: 0, Step: i, Status: status, Text: "x"})
		}
		got := l.Summary(os.ErrClosed, false)
		if !strings.Contains(got, "2 completed, 1 skipped, 1 bypassed, 1 failed, 1 pending") || !strings.Contains(got, "Failed.") {
			t.Fatalf("summary = %q", got)
		}
	})
}

func TestSanitizersStripControlSequences(t *testing.T) {
	for _, tc := range []struct{ in, safeWant, lineWant string }{
		{"plain", "plain", "plain"},
		{"a\tb\nc", "a\tb\nc", "a b c"},
		{"\x1b[31mred\x1b[0m", "red", "red"},
		{"\x1b]0;title\x07text", "text", "text"},
		{"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "link", "link"},
		{"a\x00b\x07c\x08d\x7fe", "abcde", "abcde"},
		{"\x1b", "", ""},
		{"\x1b[", "", ""},
		{"  many   spaces \n\n here ", "  many   spaces \n\n here ", "many spaces here"},
		{"é✓日本", "é✓日本", "é✓日本"},
		{"bad \xff utf8", "bad  utf8", "bad utf8"}, // invalid bytes are dropped, never echoed raw
	} {
		if got := safe(tc.in); got != tc.safeWant {
			t.Errorf("safe(%q) = %q, want %q", tc.in, got, tc.safeWant)
		}
		if got := singleLine(tc.in); got != tc.lineWant {
			t.Errorf("singleLine(%q) = %q, want %q", tc.in, got, tc.lineWant)
		}
	}
}

func FuzzCleanerNeverEmitsControlCharacters(f *testing.F) {
	for _, seed := range []string{"", "plain", "\x1b[31mx", "\x1b]0;t\x07", "a\r\nb", "\x00\x01\x7f", "\xff\xfe", "\x1b[", "\x9b31m", "\x1bP1;2|data\x1b\\"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		c := newCleaner()
		// Feed in two arbitrary pieces: a sequence may straddle a chunk boundary.
		half := len(input) / 2
		got := c.clean(input[:half]) + c.clean(input[half:])
		for _, r := range got {
			if r != '\n' && r != '\t' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0)) {
				t.Fatalf("clean(%q) emitted control character %U in %q", input, r, got)
			}
		}
		if strings.ContainsRune(got, '\x1b') {
			t.Fatalf("clean(%q) kept an escape: %q", input, got)
		}
	})
}
