package runner

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/vanvanni/plonkr/profile"
)

// fakeLocker records every call so tests can check what the runner asked for.
type fakeLocker struct {
	held      bool
	why       string
	checkErr  error
	recordErr error
	checked   []string
	recorded  []string
}

func (f *fakeLocker) CheckLock(_ context.Context, task profile.Task) (bool, string, error) {
	f.checked = append(f.checked, task.Name)
	return f.held, f.why, f.checkErr
}

func (f *fakeLocker) RecordLock(_ context.Context, task profile.Task) error {
	f.recorded = append(f.recorded, task.Name)
	return f.recordErr
}

func lockedPlan() *profile.Plan {
	return &profile.Plan{Tasks: []profile.Task{
		{
			Name:  "locked",
			Lock:  profile.Lock{Exists: []string{"/usr/bin/something"}},
			Steps: []profile.Step{{Op: profile.OpShell, Text: "one"}, {Op: profile.OpShell, Text: "two"}},
		},
		{Name: "after", Steps: []profile.Step{{Op: profile.OpShell, Text: "three"}}},
	}}
}

type commandLog struct{ ran []string }

func (c *commandLog) exec(_ context.Context, step profile.Step, _, _ io.Writer) error {
	c.ran = append(c.ran, step.Text)
	return nil
}

func TestLockHeldBypassesTaskAndContinues(t *testing.T) {
	locks := &fakeLocker{held: true, why: "exists"}
	cmds := &commandLog{}
	summary, err := Run(t.Context(), lockedPlan(), Options{Locks: locks}, cmds.exec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cmds.ran, []string{"three"}) {
		t.Fatalf("ran %v, want only the task after the lock", cmds.ran)
	}
	task := summary.Tasks[0]
	if task.Status != StatusBypassed || task.Reason != "bypassed by lock: exists" {
		t.Fatalf("task = %s (%q), want bypassed by lock", task.Status, task.Reason)
	}
	for _, step := range task.Steps {
		if step.Status != StatusBypassed || step.Reason != task.Reason {
			t.Fatalf("step = %s (%q), want bypassed with the task reason", step.Status, step.Reason)
		}
	}
	if summary.Counts.Bypassed != 2 || summary.Counts.Completed != 1 {
		t.Fatalf("counts = %+v", summary.Counts)
	}
	if len(locks.recorded) != 0 {
		t.Fatalf("a bypassed task must not record state, recorded %v", locks.recorded)
	}
}

func TestLockNotHeldRunsTaskThenRecords(t *testing.T) {
	locks := &fakeLocker{}
	cmds := &commandLog{}
	summary, err := Run(t.Context(), lockedPlan(), Options{Locks: locks}, cmds.exec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cmds.ran, []string{"one", "two", "three"}) {
		t.Fatalf("ran %v", cmds.ran)
	}
	if summary.Tasks[0].Status != StatusCompleted {
		t.Fatalf("status = %s", summary.Tasks[0].Status)
	}
	if !slices.Equal(locks.checked, []string{"locked"}) || !slices.Equal(locks.recorded, []string{"locked"}) {
		t.Fatalf("checked %v, recorded %v; only the locked task should be probed and recorded", locks.checked, locks.recorded)
	}
}

func TestLockRecordsOnlyAfterTheTaskSucceeds(t *testing.T) {
	locks := &fakeLocker{}
	failing := func(_ context.Context, step profile.Step, _, _ io.Writer) error {
		if step.Text == "two" {
			return errors.New("boom")
		}
		return nil
	}
	if _, err := Run(t.Context(), lockedPlan(), Options{Locks: locks}, failing, nil); err == nil {
		t.Fatal("expected the failing step to stop the run")
	}
	if len(locks.recorded) != 0 {
		t.Fatalf("a failed task must not record state, recorded %v", locks.recorded)
	}
}

func TestLockForceRunsHeldTaskAndStillRecords(t *testing.T) {
	locks := &fakeLocker{held: true, why: "exists"}
	cmds := &commandLog{}
	if _, err := Run(t.Context(), lockedPlan(), Options{Locks: locks, Force: true}, cmds.exec, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cmds.ran, []string{"one", "two", "three"}) {
		t.Fatalf("ran %v, want every task under --force", cmds.ran)
	}
	if len(locks.checked) != 0 {
		t.Fatalf("--force must not evaluate the lock, checked %v", locks.checked)
	}
	if !slices.Equal(locks.recorded, []string{"locked"}) {
		t.Fatalf("--force should refresh recorded state, recorded %v", locks.recorded)
	}
}

func TestLockBypassedTaskIsNeverProbed(t *testing.T) {
	locks := &fakeLocker{}
	plan := lockedPlan()
	plan.Tasks[0].Bypass = true
	if _, err := Run(t.Context(), plan, Options{Locks: locks}, (&commandLog{}).exec, nil); err != nil {
		t.Fatal(err)
	}
	if len(locks.checked) != 0 || len(locks.recorded) != 0 {
		t.Fatalf("checked %v, recorded %v; a bypassed task has no lock activity", locks.checked, locks.recorded)
	}
}

func TestLockSkippedByFlagsIsNeverProbedOrRecorded(t *testing.T) {
	locks := &fakeLocker{}
	plan := &profile.Plan{Tasks: []profile.Task{{
		Name:  "gated",
		Lock:  profile.Lock{Eval: []string{"true"}},
		Steps: []profile.Step{{Op: profile.OpShell, Text: "one", Flags: []string{"vm"}}},
	}}}
	summary, err := Run(t.Context(), plan, Options{Locks: locks}, (&commandLog{}).exec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tasks[0].Status != StatusSkipped {
		t.Fatalf("status = %s, want skipped", summary.Tasks[0].Status)
	}
	if len(locks.checked) != 0 || len(locks.recorded) != 0 {
		t.Fatalf("checked %v, recorded %v; a task that does no work must leave no state", locks.checked, locks.recorded)
	}
}

func TestLockDryRunDoesNotConnect(t *testing.T) {
	locks := &fakeLocker{held: true}
	summary, err := Run(t.Context(), lockedPlan(), Options{Locks: locks, DryRun: true}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks.checked) != 0 || len(locks.recorded) != 0 {
		t.Fatalf("dry run touched the lock: checked %v, recorded %v", locks.checked, locks.recorded)
	}
	if summary.Tasks[0].Status != StatusPlanned || summary.Tasks[0].Reason == "" {
		t.Fatalf("task = %s (%q), want planned with a note about the lock", summary.Tasks[0].Status, summary.Tasks[0].Reason)
	}
	if got := summary.Tasks[0].Steps[0].Reason; got != summary.Tasks[0].Reason {
		t.Fatalf("planned step reason = %q, want the task's lock note", got)
	}
	if summary.Tasks[1].Reason != "" {
		t.Fatalf("unlocked task reason = %q", summary.Tasks[1].Reason)
	}
}

func TestLockProbeFailureFailsTheRun(t *testing.T) {
	sentinel := errors.New("ssh down")
	locks := &fakeLocker{checkErr: sentinel}
	cmds := &commandLog{}
	summary, err := Run(t.Context(), lockedPlan(), Options{Locks: locks}, cmds.exec, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}
	if len(cmds.ran) != 0 {
		t.Fatalf("ran %v after a failed probe", cmds.ran)
	}
	if summary.Tasks[0].Status != StatusFailed || summary.Status != StatusFailed {
		t.Fatalf("task %s, run %s; want failed", summary.Tasks[0].Status, summary.Status)
	}
}

func TestLockRecordFailureFailsTheRun(t *testing.T) {
	sentinel := errors.New("read-only filesystem")
	locks := &fakeLocker{recordErr: sentinel}
	cmds := &commandLog{}
	summary, err := Run(t.Context(), lockedPlan(), Options{Locks: locks}, cmds.exec, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}
	if slices.Contains(cmds.ran, "three") {
		t.Fatalf("ran %v; the run should stop when state cannot be recorded", cmds.ran)
	}
	if summary.Tasks[0].Status != StatusFailed {
		t.Fatalf("task status = %s, want failed", summary.Tasks[0].Status)
	}
}

func TestLockWithoutLockerIsAnError(t *testing.T) {
	_, err := Run(t.Context(), lockedPlan(), Options{}, (&commandLog{}).exec, nil)
	if !errors.Is(err, errNoLocker) {
		t.Fatalf("error = %v, want %v", err, errNoLocker)
	}
}
