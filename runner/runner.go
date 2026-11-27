package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/vanvanni/plonkr/profile"
)

type eventWriter struct {
	emit  func(Event)
	event Event
}

func (w eventWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	e := w.event
	e.Text = string(p)
	w.emit(e)
	return len(p), nil
}

// Run executes a validated plan sequentially, stopping on the first failure or cancellation.
// All event emissions are serialized, including concurrent stdout and stderr writes.
func Run(ctx context.Context, plan *profile.Plan, opts Options, exec StepExecutor, emit func(Event)) (Summary, error) {
	if plan == nil {
		return Summary{}, errors.New("runner: execution plan is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r := newRun(plan, opts, emit)
	for i, task := range plan.Tasks {
		if err := ctx.Err(); err != nil {
			return r.abort(StatusCancelled, err)
		}

		r.send(Event{Kind: TaskStarted, Task: i, Status: StatusRunning, Text: task.Name})

		if task.Bypass || opts.BypassTasks[task.Name] {
			reason := "bypassed via --bypass"
			if task.Bypass {
				reason = "bypassed in profile"
			}
			for j := range task.Steps {
				r.finishStep(i, j, StatusBypassed, reason, nil)
			}
			r.finishTask(i, StatusBypassed, reason, nil)
			continue
		}

		// A task whose steps are all flag-gated off does no work, so its lock is
		// neither probed nor recorded.
		locked := !task.Lock.IsZero() && hasRunnableStep(task, opts.Flags)
		if locked && !opts.DryRun && !opts.Force {
			held, why, err := checkLock(ctx, opts.Locks, task)
			if err != nil {
				status, lockErr := StatusFailed, fmt.Errorf("task %q: lock: %w", task.Name, err)
				if ctxErr := ctx.Err(); ctxErr != nil {
					status, lockErr = StatusCancelled, ctxErr
				}
				r.finishStep(i, 0, status, "", lockErr)
				r.finishTask(i, status, "", lockErr)
				return r.abort(status, lockErr)
			}
			if held {
				reason := "bypassed by lock: " + why
				for j := range task.Steps {
					r.finishStep(i, j, StatusBypassed, reason, nil)
				}
				r.finishTask(i, StatusBypassed, reason, nil)
				continue
			}
		}

		// A dry run never connects, so it can only say that the lock is pending.
		plannedNote := ""
		if locked && opts.DryRun {
			plannedNote = "lock is evaluated when the task runs"
		}

		for j, step := range task.Steps {
			if err := ctx.Err(); err != nil {
				r.finishStep(i, j, StatusCancelled, "", err)
				r.finishTask(i, StatusCancelled, "", err)
				return r.abort(StatusCancelled, err)
			}

			if missing := step.MissingFlags(opts.Flags); len(missing) > 0 {
				r.finishStep(i, j, StatusSkipped, "missing flags: --"+strings.Join(missing, ", --"), nil)
				continue
			}

			r.send(Event{Kind: StepStarted, Task: i, Step: j, Status: StatusRunning, Text: step.Text})

			if step.Op == profile.OpLog {
				r.send(Event{Kind: Message, Task: i, Step: j, Text: step.Text, Color: step.Color})
			}
			if opts.DryRun {
				r.finishStep(i, j, StatusPlanned, plannedNote, nil)
				continue
			}
			if step.Op == profile.OpLog {
				r.finishStep(i, j, StatusCompleted, "", nil)
				continue
			}

			var err error
			if exec == nil {
				err = errors.New("step executor is nil")
			} else {
				out := eventWriter{emit: r.send, event: Event{Kind: Output, Task: i, Step: j}}
				errOut := eventWriter{emit: r.send, event: Event{Kind: Output, Task: i, Step: j, Stderr: true}}
				err = exec(ctx, step, out, errOut)
			}
			if err != nil {
				status := StatusFailed
				stepErr := fmt.Errorf("task %q, step %d (%s): %w", task.Name, j+1, step.Op.String(), err)
				if ctxErr := ctx.Err(); ctxErr != nil {
					status, stepErr = StatusCancelled, ctxErr
				}
				r.finishStep(i, j, status, "", stepErr)
				r.finishTask(i, status, "", stepErr)
				return r.abort(status, stepErr)
			}
			r.finishStep(i, j, StatusCompleted, "", nil)
		}

		counts := r.summary.Tasks[i].Counts
		if locked && !opts.DryRun && counts.Completed > 0 {
			if err := recordLock(ctx, opts.Locks, task); err != nil {
				status, lockErr := StatusFailed, fmt.Errorf("task %q: record lock state: %w", task.Name, err)
				if ctxErr := ctx.Err(); ctxErr != nil {
					status, lockErr = StatusCancelled, ctxErr
				}
				r.finishTask(i, status, "", lockErr)
				return r.abort(status, lockErr)
			}
		}
		r.finishTask(i, aggregateStatus(opts.DryRun, counts), plannedNote, nil)
	}

	if totalSteps := finalizeCounts(&r.summary); totalSteps == 0 {
		r.summary.Status = StatusPending
	} else {
		r.summary.Status = aggregateStatus(opts.DryRun, r.summary.Counts)
	}
	return r.summary, nil
}

var errNoLocker = errors.New("runner: no lock store configured")

func checkLock(ctx context.Context, locks Locker, task profile.Task) (bool, string, error) {
	if locks == nil {
		return false, "", errNoLocker
	}
	return locks.CheckLock(ctx, task)
}

func recordLock(ctx context.Context, locks Locker, task profile.Task) error {
	if locks == nil {
		return errNoLocker
	}
	return locks.RecordLock(ctx, task)
}

// hasRunnableStep reports whether any step of task has all its flags enabled.
func hasRunnableStep(task profile.Task, flags map[string]bool) bool {
	return slices.ContainsFunc(task.Steps, func(step profile.Step) bool {
		return len(step.MissingFlags(flags)) == 0
	})
}

// execution tracks one Run: the summary being built and the serialized emitter.
type execution struct {
	plan    *profile.Plan
	summary Summary
	emit    func(Event)
	mu      sync.Mutex
}

func newRun(plan *profile.Plan, opts Options, emit func(Event)) *execution {
	r := &execution{
		plan:    plan,
		emit:    emit,
		summary: Summary{DryRun: opts.DryRun, Tasks: make([]TaskResult, len(plan.Tasks))},
	}
	for i, task := range plan.Tasks {
		steps := make([]StepResult, len(task.Steps))
		for j, step := range task.Steps {
			steps[j] = StepResult{Step: step, Status: StatusPending}
		}
		r.summary.Tasks[i] = TaskResult{Task: task, Status: StatusPending, Steps: steps}
	}
	return r
}

// send serializes event delivery; stdout and stderr writers call it concurrently.
func (r *execution) send(e Event) {
	if r.emit == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emit(e)
}

// finishStep records a step outcome and reports it.
func (r *execution) finishStep(task, step int, status Status, reason string, err error) {
	result := &r.summary.Tasks[task].Steps[step]
	result.Status, result.Reason, result.Err = status, reason, err
	r.summary.Tasks[task].Counts.Add(status)
	r.send(Event{
		Kind: StepDone, Task: task, Step: step, Status: status,
		Text: r.plan.Tasks[task].Steps[step].Text, Reason: reason, Err: err,
	})
}

// finishTask records a task outcome and reports it.
func (r *execution) finishTask(task int, status Status, reason string, err error) {
	result := &r.summary.Tasks[task]
	result.Status, result.Reason = status, reason
	r.send(Event{Kind: TaskDone, Task: task, Status: status, Text: r.plan.Tasks[task].Name, Reason: reason, Err: err})
}

// abort ends the run early with status and err.
func (r *execution) abort(status Status, err error) (Summary, error) {
	finalizeCounts(&r.summary)
	r.summary.Status = status
	r.summary.Err = err
	return r.summary, err
}

func finalizeCounts(summary *Summary) int {
	var total Counts
	totalSteps := 0
	for i := range summary.Tasks {
		var taskCounts Counts
		for _, step := range summary.Tasks[i].Steps {
			taskCounts.Add(step.Status)
			total.Add(step.Status)
			totalSteps++
		}
		summary.Tasks[i].Counts = taskCounts
	}
	summary.Counts = total
	return totalSteps
}

func aggregateStatus(dryRun bool, counts Counts) Status {
	if dryRun {
		switch {
		case counts.Planned > 0:
			return StatusPlanned
		case counts.Bypassed > 0 && counts.Skipped == 0:
			return StatusBypassed
		default:
			return StatusSkipped
		}
	}
	switch {
	case counts.Completed > 0:
		return StatusCompleted
	case counts.Bypassed > 0:
		return StatusBypassed
	default:
		return StatusSkipped
	}
}
