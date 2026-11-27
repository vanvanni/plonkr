package runner

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
)

func TestValidateHostEdges(t *testing.T) {
	for host, valid := range map[string]bool{
		"web-1": true, "web_1": true, "a.b.c": true, "WEB": true, "10.0.0.1": true, "user@host": true, "user.name@host": true,
		"a@b@c": true, "[::1]": true, "user@[fe80::1%eth0]": true, "host%eth0": true, "a+b@host": true, "9host": true,
		"": false, "-": false, "--": false, "-host": false, "@host": false, ".host": false, "[": true,
		"host name": false, "host\t": false, "host\n": false, "host\r": false, " host": false, "host ": false,
		"ho$st": false, "ho|st": false, "ho;st": false, "ho&st": false, "ho`st": false, "ho(st": false, "ho'st": false, `ho"st`: false,
		"ho\\st": false, "ho/st": false, "ho*st": false, "ho?st": false, "ho=st": false, "ho,st": false, "host\x00": false, "hôst": false,
		"ssh://host": false, "-oProxyCommand=id": false, "-J jump": false,
	} {
		if err := ValidateHost(host); (err == nil) != valid {
			t.Errorf("ValidateHost(%q) = %v, want valid = %t", host, err, valid)
		}
	}
}

func TestRunWithoutAnExecutorFailsTheStepInsteadOfPanicking(t *testing.T) {
	plan := &profile.Plan{Tasks: []profile.Task{{Name: "T", Steps: []profile.Step{{Text: "x"}}}}}
	summary, err := Run(t.Context(), plan, Options{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "executor is nil") || summary.Status != StatusFailed {
		t.Fatalf("status = %v, error = %v", summary.Status, err)
	}
	// Log-only and fully skipped work needs no executor.
	plan = &profile.Plan{Tasks: []profile.Task{{Name: "T", Steps: []profile.Step{
		{Op: profile.OpLog, Text: "hello"}, {Text: "gated", Flags: []string{"x"}},
	}}}}
	if _, err := Run(t.Context(), plan, Options{}, nil, nil); err != nil {
		t.Fatalf("no executor is needed for log and skipped steps: %v", err)
	}
}

func TestRunAcceptsANilContextAndNilEmit(t *testing.T) {
	plan := &profile.Plan{Tasks: []profile.Task{{Name: "T", Steps: []profile.Step{{Text: "x"}}}}}
	ran := false
	//lint:ignore SA1012 a nil context is deliberately tolerated
	summary, err := Run(nil, plan, Options{}, func(context.Context, profile.Step, io.Writer, io.Writer) error { ran = true; return nil }, nil) //nolint:staticcheck
	if err != nil || !ran || summary.Status != StatusCompleted {
		t.Fatalf("ran = %t, status = %v, error = %v", ran, summary.Status, err)
	}
}

func TestRunWithACancelledContextStartsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	plan := &profile.Plan{Tasks: []profile.Task{{Name: "T", Steps: []profile.Step{{Text: "x"}, {Text: "y"}}}}}
	called := false
	summary, err := Run(ctx, plan, Options{}, func(context.Context, profile.Step, io.Writer, io.Writer) error { called = true; return nil }, nil)
	if !errors.Is(err, context.Canceled) || called || summary.Status != StatusCancelled || summary.Counts.Pending != 2 {
		t.Fatalf("status = %v, counts = %+v, error = %v, called = %t", summary.Status, summary.Counts, err, called)
	}
}

func TestRunCancellationDuringALockProbeIsCancelledNotFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	locker := probeFunc(func(context.Context, profile.Task) (bool, string, error) {
		cancel()
		return false, "", errors.New("signal: killed")
	})
	plan := lockedPlan()
	summary, err := Run(ctx, plan, Options{Locks: locker}, (&commandLog{}).exec, nil)
	if !errors.Is(err, context.Canceled) || summary.Status != StatusCancelled {
		t.Fatalf("status = %v, error = %v", summary.Status, err)
	}
	if summary.Tasks[0].Status != StatusCancelled || summary.Tasks[1].Status != StatusPending {
		t.Fatalf("task statuses = %v, %v", summary.Tasks[0].Status, summary.Tasks[1].Status)
	}
}

func TestRunCancellationDuringRecordIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	locker := &recordFunc{record: func(context.Context, profile.Task) error {
		cancel()
		return errors.New("signal: killed")
	}}
	summary, err := Run(ctx, lockedPlan(), Options{Locks: locker}, (&commandLog{}).exec, nil)
	if !errors.Is(err, context.Canceled) || summary.Status != StatusCancelled {
		t.Fatalf("status = %v, error = %v", summary.Status, err)
	}
	if summary.Counts.Completed != 2 {
		t.Fatalf("steps that finished before the cancellation stay completed: %+v", summary.Counts)
	}
}

func TestOutputEventsKeepStreamAndPosition(t *testing.T) {
	plan := &profile.Plan{Tasks: []profile.Task{{Name: "A", Steps: []profile.Step{{Text: "one"}}}, {Name: "B", Steps: []profile.Step{{Text: "x"}, {Text: "two"}}}}}
	var outputs []Event
	_, err := Run(t.Context(), plan, Options{}, func(_ context.Context, step profile.Step, stdout, stderr io.Writer) error {
		_, _ = io.WriteString(stdout, "out:"+step.Text)
		_, _ = io.WriteString(stderr, "err:"+step.Text)
		_, _ = stdout.Write(nil) // empty writes are not events
		return nil
	}, func(e Event) {
		if e.Kind == Output {
			outputs = append(outputs, e)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 6 {
		t.Fatalf("output events = %d, want 6", len(outputs))
	}
	last := outputs[len(outputs)-2]
	if last.Task != 1 || last.Step != 1 || last.Stderr || last.Text != "out:two" {
		t.Fatalf("event = %+v", last)
	}
	if e := outputs[len(outputs)-1]; !e.Stderr || e.Text != "err:two" {
		t.Fatalf("event = %+v", e)
	}
}

func TestStatusAndCountsHelpers(t *testing.T) {
	for status, want := range map[Status]string{
		StatusPending: "pending", StatusRunning: "running", StatusCompleted: "completed", StatusFailed: "failed",
		StatusSkipped: "skipped", StatusBypassed: "bypassed", StatusPlanned: "planned", StatusCancelled: "cancelled", Status(99): "unknown",
	} {
		if status.String() != want {
			t.Errorf("Status(%d) = %q, want %q", status, status.String(), want)
		}
	}
	var c Counts
	for _, s := range []Status{StatusPending, StatusRunning, StatusCompleted, StatusFailed, StatusSkipped, StatusBypassed, StatusPlanned, StatusCancelled, Status(99)} {
		c.Add(s)
	}
	if c != (Counts{Pending: 2, Completed: 1, Failed: 1, Skipped: 1, Bypassed: 1, Planned: 1, Cancelled: 1}) {
		t.Fatalf("counts = %+v (an unknown status must not be counted)", c)
	}
}

func TestAggregateStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dry    bool
		counts Counts
		want   Status
	}{
		{"completed beats bypassed", false, Counts{Completed: 1, Bypassed: 3, Skipped: 2}, StatusCompleted},
		{"bypassed beats skipped", false, Counts{Bypassed: 1, Skipped: 2}, StatusBypassed},
		{"only skipped", false, Counts{Skipped: 2}, StatusSkipped},
		{"nothing at all", false, Counts{}, StatusSkipped},
		{"dry run planned", true, Counts{Planned: 1, Bypassed: 1, Skipped: 1}, StatusPlanned},
		{"dry run only bypassed", true, Counts{Bypassed: 2}, StatusBypassed},
		{"dry run bypassed and skipped", true, Counts{Bypassed: 2, Skipped: 1}, StatusSkipped},
		{"dry run only skipped", true, Counts{Skipped: 1}, StatusSkipped},
	} {
		if got := aggregateStatus(tc.dry, tc.counts); got != tc.want {
			t.Errorf("%s: aggregateStatus = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// probeFunc and recordFunc adapt functions to Locker for single-purpose tests.
type probeFunc func(context.Context, profile.Task) (bool, string, error)

func (f probeFunc) CheckLock(ctx context.Context, t profile.Task) (bool, string, error) {
	return f(ctx, t)
}
func (probeFunc) RecordLock(context.Context, profile.Task) error { return nil }

type recordFunc struct {
	record func(context.Context, profile.Task) error
}

func (recordFunc) CheckLock(context.Context, profile.Task) (bool, string, error) {
	return false, "", nil
}
func (r *recordFunc) RecordLock(ctx context.Context, t profile.Task) error { return r.record(ctx, t) }
