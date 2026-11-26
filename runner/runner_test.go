package runner

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vanvanni/plonkr/profile"
)

func TestRunNilAndEmptyPlan(t *testing.T) {
	t.Run("nil plan", func(t *testing.T) {
		summary, err := Run(t.Context(), nil, Options{}, nil, nil)
		if err == nil {
			t.Fatal("expected error for nil plan")
		}
		if summary.Status != StatusPending || len(summary.Tasks) != 0 {
			t.Fatalf("expected empty summary, got %+v", summary)
		}
	})

	t.Run("empty plan", func(t *testing.T) {
		var events []Event
		summary, err := Run(t.Context(), &profile.Plan{}, Options{}, nil, func(e Event) {
			events = append(events, e)
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary.Status != StatusPending {
			t.Fatalf("status = %v, want %v", summary.Status, StatusPending)
		}
		if len(events) != 0 {
			t.Fatalf("expected no events, got %d", len(events))
		}
	})
}

func TestRunScenarios(t *testing.T) {
	tests := []struct {
		name           string
		plan           *profile.Plan
		opts           Options
		exec           StepExecutor
		wantStatus     Status
		wantCounts     Counts
		wantTaskStatus []Status
		wantErr        bool
		wantMessages   []Event
		wantOutputs    []Event
	}{
		{
			name: "live run with shell log and output",
			plan: &profile.Plan{
				Tasks: []profile.Task{
					{
						Name: "setup",
						Steps: []profile.Step{
							{Op: profile.OpLog, Text: "starting", Color: "cyan"},
							{Op: profile.OpShell, Text: "echo hi"},
						},
					},
				},
			},
			exec: func(_ context.Context, _ profile.Step, stdout, stderr io.Writer) error {
				_, _ = io.WriteString(stdout, "hi\n")
				_, _ = io.WriteString(stderr, "warn\n")
				return nil
			},
			wantStatus:     StatusCompleted,
			wantCounts:     Counts{Completed: 2},
			wantTaskStatus: []Status{StatusCompleted},
			wantMessages:   []Event{{Kind: Message, Task: 0, Step: 0, Text: "starting", Color: "cyan"}},
			wantOutputs: []Event{
				{Kind: Output, Task: 0, Step: 1, Text: "hi\n", Stderr: false},
				{Kind: Output, Task: 0, Step: 1, Text: "warn\n", Stderr: true},
			},
		},
		{
			name: "dry run never executes and emits log message",
			plan: &profile.Plan{
				Tasks: []profile.Task{
					{
						Name: "preview",
						Steps: []profile.Step{
							{Op: profile.OpLog, Text: "note", Color: "yellow"},
							{Op: profile.OpShell, Text: "rm -rf /"},
						},
					},
				},
			},
			opts: Options{DryRun: true},
			exec: func(context.Context, profile.Step, io.Writer, io.Writer) error {
				t.Error("executor must not run during dry-run")
				return nil
			},
			wantStatus:     StatusPlanned,
			wantCounts:     Counts{Planned: 2},
			wantTaskStatus: []Status{StatusPlanned},
			wantMessages:   []Event{{Kind: Message, Task: 0, Step: 0, Text: "note", Color: "yellow"}},
		},
		{
			name: "all skipped task in live run is skipped not completed",
			plan: &profile.Plan{
				Tasks: []profile.Task{
					{
						Name: "optional",
						Steps: []profile.Step{
							{Op: profile.OpShell, Text: "echo a", Flags: []string{"vm", "extra"}},
							{Op: profile.OpShell, Text: "echo b", Flags: []string{"extra"}},
						},
					},
				},
			},
			opts:           Options{Flags: map[string]bool{"vm": false}},
			wantStatus:     StatusSkipped,
			wantCounts:     Counts{Skipped: 2},
			wantTaskStatus: []Status{StatusSkipped},
		},
		{
			name: "all skipped task in dry run is skipped",
			plan: &profile.Plan{
				Tasks: []profile.Task{
					{
						Name: "optional",
						Steps: []profile.Step{
							{Op: profile.OpShell, Text: "echo a", Flags: []string{"vm"}},
						},
					},
				},
			},
			opts:           Options{DryRun: true},
			wantStatus:     StatusSkipped,
			wantCounts:     Counts{Skipped: 1},
			wantTaskStatus: []Status{StatusSkipped},
		},
		{
			name: "bypassed in profile and via flag",
			plan: &profile.Plan{
				Tasks: []profile.Task{
					{
						Name:   "profile-bypass",
						Bypass: true,
						Steps:  []profile.Step{{Op: profile.OpShell, Text: "echo 1"}},
					},
					{
						Name:  "cli-bypass",
						Steps: []profile.Step{{Op: profile.OpShell, Text: "echo 2"}},
					},
				},
			},
			opts:           Options{BypassTasks: map[string]bool{"cli-bypass": true}},
			wantStatus:     StatusBypassed,
			wantCounts:     Counts{Bypassed: 2},
			wantTaskStatus: []Status{StatusBypassed, StatusBypassed},
		},
		{
			name: "mixed bypassed and skipped in dry run yields skipped summary",
			plan: &profile.Plan{
				Tasks: []profile.Task{
					{Name: "b", Bypass: true, Steps: []profile.Step{{Op: profile.OpShell, Text: "echo 1"}}},
					{Name: "s", Steps: []profile.Step{{Op: profile.OpShell, Text: "echo 2", Flags: []string{"x"}}}},
				},
			},
			opts:           Options{DryRun: true},
			wantStatus:     StatusSkipped,
			wantCounts:     Counts{Bypassed: 1, Skipped: 1},
			wantTaskStatus: []Status{StatusBypassed, StatusSkipped},
		},
		{
			name: "mixed bypassed and skipped in live run yields bypassed summary",
			plan: &profile.Plan{
				Tasks: []profile.Task{
					{Name: "b", Bypass: true, Steps: []profile.Step{{Op: profile.OpShell, Text: "echo 1"}}},
					{Name: "s", Steps: []profile.Step{{Op: profile.OpShell, Text: "echo 2", Flags: []string{"x"}}}},
				},
			},
			wantStatus:     StatusBypassed,
			wantCounts:     Counts{Bypassed: 1, Skipped: 1},
			wantTaskStatus: []Status{StatusBypassed, StatusSkipped},
		},
		{
			name: "nil executor fails on live non-log step",
			plan: &profile.Plan{
				Tasks: []profile.Task{
					{Name: "t", Steps: []profile.Step{{Op: profile.OpShell, Text: "echo 1"}}},
				},
			},
			exec:           nil,
			wantStatus:     StatusFailed,
			wantCounts:     Counts{Failed: 1},
			wantTaskStatus: []Status{StatusFailed},
			wantErr:        true,
		},
		{
			name: "nil executor succeeds on log-only live plan",
			plan: &profile.Plan{
				Tasks: []profile.Task{
					{Name: "t", Steps: []profile.Step{{Op: profile.OpLog, Text: "hello"}}},
				},
			},
			exec:           nil,
			wantStatus:     StatusCompleted,
			wantCounts:     Counts{Completed: 1},
			wantTaskStatus: []Status{StatusCompleted},
			wantMessages:   []Event{{Kind: Message, Task: 0, Step: 0, Text: "hello"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var messages, outputs []Event
			summary, err := Run(t.Context(), tc.plan, tc.opts, tc.exec, func(e Event) {
				switch e.Kind {
				case Message:
					messages = append(messages, e)
				case Output:
					outputs = append(outputs, e)
				}
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if summary.DryRun != tc.opts.DryRun {
				t.Fatalf("summary.DryRun = %v, want %v", summary.DryRun, tc.opts.DryRun)
			}
			if summary.Status != tc.wantStatus {
				t.Fatalf("summary.Status = %v, want %v", summary.Status, tc.wantStatus)
			}
			if summary.Counts != tc.wantCounts {
				t.Fatalf("summary.Counts = %+v, want %+v", summary.Counts, tc.wantCounts)
			}
			if len(summary.Tasks) != len(tc.wantTaskStatus) {
				t.Fatalf("len(summary.Tasks) = %d, want %d", len(summary.Tasks), len(tc.wantTaskStatus))
			}
			for i, want := range tc.wantTaskStatus {
				if summary.Tasks[i].Status != want {
					t.Errorf("task[%d].Status = %v, want %v", i, summary.Tasks[i].Status, want)
				}
			}
			if !slices.Equal(messages, tc.wantMessages) {
				t.Errorf("messages = %+v, want %+v", messages, tc.wantMessages)
			}
			if !slices.Equal(outputs, tc.wantOutputs) {
				t.Errorf("outputs = %+v, want %+v", outputs, tc.wantOutputs)
			}
		})
	}
}

func TestRunBypassAndSkipReasons(t *testing.T) {
	plan := &profile.Plan{
		Tasks: []profile.Task{
			{
				Name:   "both-bypass",
				Bypass: true,
				Steps:  []profile.Step{{Op: profile.OpShell, Text: "cmd1"}},
			},
			{
				Name:  "cli-only",
				Steps: []profile.Step{{Op: profile.OpShell, Text: "cmd2"}},
			},
			{
				Name: "skip-flags",
				Steps: []profile.Step{
					{Op: profile.OpShell, Text: "cmd3", Flags: []string{"alpha", "beta", "gamma"}},
				},
			},
		},
	}

	var events []Event
	summary, err := Run(t.Context(), plan, Options{
		Flags:       map[string]bool{"beta": true},
		BypassTasks: map[string]bool{"both-bypass": true, "cli-only": true},
	}, nil, func(e Event) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := summary.Tasks[0].Reason; got != "bypassed in profile" {
		t.Errorf("task[0].Reason = %q, want %q", got, "bypassed in profile")
	}
	if got := summary.Tasks[0].Steps[0].Reason; got != "bypassed in profile" {
		t.Errorf("task[0].Steps[0].Reason = %q, want %q", got, "bypassed in profile")
	}
	if got := summary.Tasks[1].Reason; got != "bypassed via --bypass" {
		t.Errorf("task[1].Reason = %q, want %q", got, "bypassed via --bypass")
	}
	wantSkip := "missing flags: --alpha, --gamma"
	if got := summary.Tasks[2].Steps[0].Reason; got != wantSkip {
		t.Errorf("task[2].Steps[0].Reason = %q, want %q", got, wantSkip)
	}

	// Ensure bypassed and skipped steps never emit StepStarted.
	for _, e := range events {
		if e.Kind == StepStarted {
			t.Errorf("unexpected StepStarted event: %+v", e)
		}
	}
}

func TestRunFailureStopsAndPreservesPendingCounts(t *testing.T) {
	sentinel := errors.New("boom")
	plan := &profile.Plan{
		Tasks: []profile.Task{
			{
				Name: "deploy",
				Steps: []profile.Step{
					{Op: profile.OpShell, Text: "step1"},
					{Op: profile.OpRepo, Text: "repo app", Resource: "app"},
					{Op: profile.OpShell, Text: "step3"},
				},
			},
			{
				Name: "after",
				Steps: []profile.Step{
					{Op: profile.OpShell, Text: "step4"},
					{Op: profile.OpShell, Text: "step5"},
				},
			},
		},
	}

	var events []Event
	exec := func(_ context.Context, step profile.Step, _, _ io.Writer) error {
		if step.Op == profile.OpRepo {
			return sentinel
		}
		return nil
	}

	summary, err := Run(t.Context(), plan, Options{}, exec, func(e Event) {
		events = append(events, e)
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v does not wrap sentinel", err)
	}
	wantSubstr := `task "deploy", step 2 (repo): boom`
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("err = %q, want substring %q", err.Error(), wantSubstr)
	}
	if summary.Status != StatusFailed || summary.Err != err {
		t.Fatalf("summary status=%v err=%v", summary.Status, summary.Err)
	}
	if want := (Counts{Completed: 1, Failed: 1, Pending: 3}); summary.Counts != want {
		t.Fatalf("summary.Counts = %+v, want %+v", summary.Counts, want)
	}
	if want := (Counts{Completed: 1, Failed: 1, Pending: 1}); summary.Tasks[0].Counts != want {
		t.Fatalf("task[0].Counts = %+v, want %+v", summary.Tasks[0].Counts, want)
	}
	if summary.Tasks[0].Steps[2].Status != StatusPending {
		t.Fatalf("unreached step status = %v, want pending", summary.Tasks[0].Steps[2].Status)
	}
	if summary.Tasks[1].Status != StatusPending || summary.Tasks[1].Counts != (Counts{Pending: 2}) {
		t.Fatalf("unreached task = %+v", summary.Tasks[1])
	}

	// Verify last two events are StepDone(Failed) and TaskDone(Failed) with Err set.
	if len(events) < 2 {
		t.Fatalf("expected events, got %+v", events)
	}
	stepDone := events[len(events)-2]
	taskDone := events[len(events)-1]
	if stepDone.Kind != StepDone || stepDone.Status != StatusFailed || stepDone.Err != err {
		t.Errorf("stepDone = %+v", stepDone)
	}
	if taskDone.Kind != TaskDone || taskDone.Status != StatusFailed || taskDone.Err != err {
		t.Errorf("taskDone = %+v", taskDone)
	}
}

func TestRunCancellation(t *testing.T) {
	t.Run("cancelled before first task", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		plan := &profile.Plan{
			Tasks: []profile.Task{
				{Name: "t1", Steps: []profile.Step{{Op: profile.OpShell, Text: "a"}, {Op: profile.OpShell, Text: "b"}}},
			},
		}
		var events []Event
		summary, err := Run(ctx, plan, Options{}, nil, func(e Event) { events = append(events, e) })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if summary.Status != StatusCancelled || summary.Counts != (Counts{Pending: 2}) {
			t.Fatalf("summary = %+v", summary)
		}
		if len(events) != 0 {
			t.Fatalf("expected no events, got %+v", events)
		}
	})

	t.Run("cancelled before step inside task", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		plan := &profile.Plan{
			Tasks: []profile.Task{
				{
					Name: "t1",
					Steps: []profile.Step{
						{Op: profile.OpShell, Text: "s1"},
						{Op: profile.OpShell, Text: "s2"},
						{Op: profile.OpShell, Text: "s3"},
					},
				},
			},
		}
		exec := func(_ context.Context, step profile.Step, _, _ io.Writer) error {
			if step.Text == "s1" {
				cancel()
			}
			return nil
		}
		summary, err := Run(ctx, plan, Options{}, exec, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if summary.Status != StatusCancelled {
			t.Fatalf("summary.Status = %v, want cancelled", summary.Status)
		}
		if want := (Counts{Completed: 1, Cancelled: 1, Pending: 1}); summary.Counts != want {
			t.Fatalf("summary.Counts = %+v, want %+v", summary.Counts, want)
		}
		if summary.Tasks[0].Steps[1].Status != StatusCancelled || !errors.Is(summary.Tasks[0].Steps[1].Err, context.Canceled) {
			t.Fatalf("step[1] = %+v", summary.Tasks[0].Steps[1])
		}
		if summary.Tasks[0].Steps[2].Status != StatusPending {
			t.Fatalf("step[2] = %+v", summary.Tasks[0].Steps[2])
		}
	})

	t.Run("cancelled during step execution", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		plan := &profile.Plan{
			Tasks: []profile.Task{
				{
					Name: "t1",
					Steps: []profile.Step{
						{Op: profile.OpShell, Text: "s1"},
						{Op: profile.OpShell, Text: "s2"},
					},
				},
			},
		}
		exec := func(_ context.Context, _ profile.Step, _, _ io.Writer) error {
			cancel()
			return errors.New("killed")
		}
		summary, err := Run(ctx, plan, Options{}, exec, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if summary.Status != StatusCancelled || summary.Tasks[0].Status != StatusCancelled {
			t.Fatalf("summary=%v task=%v", summary.Status, summary.Tasks[0].Status)
		}
		if want := (Counts{Cancelled: 1, Pending: 1}); summary.Counts != want {
			t.Fatalf("summary.Counts = %+v, want %+v", summary.Counts, want)
		}
	})
}

func TestRunConcurrentOutputSerialization(t *testing.T) {
	plan := &profile.Plan{
		Tasks: []profile.Task{
			{Name: "stream", Steps: []profile.Step{{Op: profile.OpShell, Text: "concurrent"}}},
		},
	}

	var inEmit atomic.Bool
	var overlap atomic.Bool
	var stdoutCount, stderrCount int

	exec := func(_ context.Context, _ profile.Step, stdout, stderr io.Writer) error {
		var wg sync.WaitGroup
		wg.Go(func() {
			for range 50 {
				_, _ = io.WriteString(stdout, "out")
			}
		})
		wg.Go(func() {
			for range 50 {
				_, _ = io.WriteString(stderr, "err")
			}
		})
		wg.Wait()
		return nil
	}

	_, err := Run(t.Context(), plan, Options{}, exec, func(e Event) {
		if !inEmit.CompareAndSwap(false, true) {
			overlap.Store(true)
		}
		defer inEmit.Store(false)

		if e.Kind == Output {
			if e.Stderr {
				stderrCount++
			} else {
				stdoutCount++
			}
		}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if overlap.Load() {
		t.Fatal("emit was invoked concurrently")
	}
	if stdoutCount != 50 || stderrCount != 50 {
		t.Fatalf("stdout=%d stderr=%d, want 50 each", stdoutCount, stderrCount)
	}
}
