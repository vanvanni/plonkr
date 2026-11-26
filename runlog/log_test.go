package runlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

func TestOpenValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  string
		plan *profile.Plan
	}{
		{"relative directory", "logs", &profile.Plan{}},
		{"nil plan", t.TempDir(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if l, err := Open(tc.dir, "host", "profile", tc.plan); l != nil || err == nil {
				t.Fatalf("Open returned %v, %v", l, err)
			}
		})
	}
}

func TestEventSanitizesStreams(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "logs"), "lab", "profile.hcl", &profile.Plan{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		stderr bool
		in     string
		want   string
	}{
		{false, "\x1b[3", ""},
		{true, "error\x00\x1b]52;c;", "error"},
		{false, "1mcaf\xc3", "caf"},
		{true, "secret", ""},
		{false, "\xa9\r", "é\n"},
		{false, "\n\x1b[0m\b\t", "\t"},
		{true, "\x1b", ""},
		{false, "stdout", "stdout"},
		{true, "\\ok\n", "ok\n"},
	} {
		e, err := l.Event(runner.Event{Kind: runner.Output, Text: tc.in, Stderr: tc.stderr})
		if err != nil || e.Text != tc.want || e.Stderr != tc.stderr {
			t.Errorf("Event(%q, stderr=%t) = %#v, %v; want text %q", tc.in, tc.stderr, e, err, tc.want)
		}
	}
	if err := l.Finish(nil, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.HasPrefix(got, "plonk · lab · profile.hcl\nerrorcafé\n\tstdoutok\n") ||
		strings.ContainsAny(got, "\x1b\r\x00") || strings.Contains(got, "secret") {
		t.Fatalf("unexpected file contents %q", got)
	}
}

func TestSummary(t *testing.T) {
	for _, tc := range []struct {
		status  runner.Status
		dry     bool
		runErr  error
		heading string
		reason  string
	}{
		{runner.StatusCompleted, false, nil, "Complete.", ""},
		{runner.StatusFailed, false, errors.New("\x1b[31mexit status 7"), "Failed.", ""},
		{runner.StatusSkipped, false, nil, "Skipped.", "missing flags: --vm"},
		{runner.StatusBypassed, false, nil, "Bypassed.", "bypassed via --bypass"},
		{runner.StatusPlanned, true, nil, "Dry run complete (no SSH connection made).", ""},
		{runner.StatusSkipped, true, nil, "Dry run skipped (no SSH connection made).", "missing flags: --vm"},
		{runner.StatusBypassed, true, nil, "Dry run bypassed (no SSH connection made).", "bypassed via --bypass"},
		{runner.StatusPending, false, nil, "Pending.", ""},
		{runner.StatusCancelled, false, context.Canceled, "Cancelled.", ""},
		{runner.StatusCancelled, false, nil, "Cancelled.", ""},
	} {
		t.Run(tc.heading+"/"+tc.status.String(), func(t *testing.T) {
			plan := &profile.Plan{Name: "Setup", Tasks: []profile.Task{
				{Name: "Machine", Baseline: true, Steps: make([]profile.Step, 1)},
				{Name: "Guests", Order: 42, Steps: make([]profile.Step, 1)},
			}}
			l, err := Open(t.TempDir(), "lab", "profile.hcl", plan)
			if err != nil {
				t.Fatal(err)
			}
			if tc.status != runner.StatusPending {
				for task := range plan.Tasks {
					for _, e := range []runner.Event{
						{Kind: runner.TaskStarted, Task: task},
						{Kind: runner.StepStarted, Task: task, Text: "echo output"},
						{Kind: runner.Message, Task: task, Text: "\x1b[31mmessage\a"},
						{Kind: runner.StepDone, Task: task, Status: tc.status, Text: "echo output", Reason: tc.reason},
						{Kind: runner.TaskDone, Task: task, Status: tc.status},
					} {
						if _, err := l.Event(e); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			status := tc.status.String()
			want := "plonk · lab · Setup · " + tc.heading + "\n" +
				"  [config] Machine: 1 " + status + " (" + status + ")\n" +
				"  [42] Guests: 1 " + status + " (" + status + ")\n" +
				"Total: 2 " + status + "\nLog: " + l.Path + "\n"
			if got := l.Summary(tc.runErr, tc.dry); got != want {
				t.Errorf("summary = %q; want %q", got, want)
			}
			if err := l.Finish(tc.runErr, tc.dry); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(l.Path)
			if err != nil {
				t.Fatal(err)
			}
			log := string(data)
			if !strings.Contains(log, want) || strings.ContainsAny(log, "\x1b\a") {
				t.Errorf("unexpected log %q", log)
			}
			if tc.status != runner.StatusPending {
				tag := "[" + status + "] "
				if tc.reason != "" {
					tag += "(" + tc.reason + ") "
				}
				if !strings.Contains(log, tag+"echo output\n") || !strings.Contains(log, "\nmessage\n") {
					t.Errorf("missing event details in %q", log)
				}
			}
			if tc.status == runner.StatusFailed && !strings.HasSuffix(log, "Error: exit status 7\n") {
				t.Errorf("missing sanitized error in %q", log)
			}
		})
	}
}
