// Package runner executes validated plans sequentially with unified action status reporting.
package runner

import (
	"context"
	"io"

	"github.com/vanvanni/plonkr/profile"
)

// Status represents the unified lifecycle and outcome state for steps, tasks, and runs.
type Status uint8

const (
	StatusPending Status = iota
	StatusRunning
	StatusCompleted
	StatusFailed
	StatusSkipped
	StatusBypassed
	StatusPlanned
	StatusCancelled
)

// String returns the canonical lowercase status label.
func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusSkipped:
		return "skipped"
	case StatusBypassed:
		return "bypassed"
	case StatusPlanned:
		return "planned"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// EventKind classifies streaming execution events emitted by Run.
type EventKind uint8

const (
	TaskStarted EventKind = iota
	StepStarted
	Output
	Message
	StepDone
	TaskDone
)

// Event describes a single state transition or output chunk from Run.
type Event struct {
	Kind   EventKind
	Task   int
	Step   int
	Status Status
	Text   string
	Reason string
	Stderr bool
	Color  string
	Err    error
}

// Options configures plan execution.
type Options struct {
	DryRun      bool
	Flags       map[string]bool
	BypassTasks map[string]bool
	// Force runs locked tasks even when their lock would bypass them. Their lock
	// state is still recorded after they complete.
	Force bool
	// Locks evaluates and records task locks. It is required for tasks with a
	// lock unless the run is a dry run.
	Locks Locker
}

// Locker evaluates task locks on the target and records their state.
type Locker interface {
	// CheckLock reports whether task's lock holds, so the task can be bypassed.
	// reason briefly names the directives that held.
	CheckLock(ctx context.Context, task profile.Task) (held bool, reason string, err error)
	// RecordLock stores the lock state observed after task completed.
	RecordLock(ctx context.Context, task profile.Task) error
}

// StepExecutor runs one non-log executable step and streams output to stdout and stderr.
type StepExecutor func(ctx context.Context, step profile.Step, stdout, stderr io.Writer) error

// Counts tallies step outcomes across a task or whole run.
type Counts struct {
	Pending   int
	Completed int
	Failed    int
	Skipped   int
	Bypassed  int
	Planned   int
	Cancelled int
}

// Add increments the counter corresponding to status.
func (c *Counts) Add(status Status) {
	switch status {
	case StatusPending, StatusRunning:
		c.Pending++
	case StatusCompleted:
		c.Completed++
	case StatusFailed:
		c.Failed++
	case StatusSkipped:
		c.Skipped++
	case StatusBypassed:
		c.Bypassed++
	case StatusPlanned:
		c.Planned++
	case StatusCancelled:
		c.Cancelled++
	}
}

// StepResult captures the final outcome of a single step.
type StepResult struct {
	Step   profile.Step
	Status Status
	Reason string
	Err    error
}

// TaskResult captures the aggregate outcome of a task and its steps.
type TaskResult struct {
	Task   profile.Task
	Status Status
	Reason string
	Counts Counts
	Steps  []StepResult
}

// Summary captures the final outcome of an entire plan execution.
type Summary struct {
	Status Status
	DryRun bool
	Counts Counts
	Tasks  []TaskResult
	Err    error
}
