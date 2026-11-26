package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"plonk/profile"
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

	summary := Summary{
		DryRun: opts.DryRun,
		Tasks:  make([]TaskResult, len(plan.Tasks)),
	}
	for i, task := range plan.Tasks {
		steps := make([]StepResult, len(task.Steps))
		for j, step := range task.Steps {
			steps[j] = StepResult{Step: step, Status: StatusPending}
		}
		summary.Tasks[i] = TaskResult{
			Task:   task,
			Status: StatusPending,
			Steps:  steps,
		}
	}

	var mu sync.Mutex
	send := func(e Event) {
		if emit == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		emit(e)
	}

	for i, task := range plan.Tasks {
		if err := ctx.Err(); err != nil {
			finalizeCounts(&summary)
			summary.Status = StatusCancelled
			summary.Err = err
			return summary, err
		}

		send(Event{Kind: TaskStarted, Task: i, Status: StatusRunning, Text: task.Name})

		if task.Bypass || opts.BypassTasks[task.Name] {
			reason := "bypassed via --bypass"
			if task.Bypass {
				reason = "bypassed in profile"
			}
			for j, step := range task.Steps {
				summary.Tasks[i].Steps[j].Status = StatusBypassed
				summary.Tasks[i].Steps[j].Reason = reason
				summary.Tasks[i].Counts.Add(StatusBypassed)
				send(Event{
					Kind:   StepDone,
					Task:   i,
					Step:   j,
					Status: StatusBypassed,
					Text:   step.Text,
					Reason: reason,
				})
			}
			summary.Tasks[i].Status = StatusBypassed
			summary.Tasks[i].Reason = reason
			send(Event{
				Kind:   TaskDone,
				Task:   i,
				Status: StatusBypassed,
				Text:   task.Name,
				Reason: reason,
			})
			continue
		}

		for j, step := range task.Steps {
			if err := ctx.Err(); err != nil {
				summary.Tasks[i].Steps[j].Status = StatusCancelled
				summary.Tasks[i].Steps[j].Err = err
				send(Event{
					Kind:   StepDone,
					Task:   i,
					Step:   j,
					Status: StatusCancelled,
					Text:   step.Text,
					Err:    err,
				})
				summary.Tasks[i].Status = StatusCancelled
				send(Event{
					Kind:   TaskDone,
					Task:   i,
					Status: StatusCancelled,
					Text:   task.Name,
					Err:    err,
				})
				finalizeCounts(&summary)
				summary.Status = StatusCancelled
				summary.Err = err
				return summary, err
			}

			if missing := step.MissingFlags(opts.Flags); len(missing) > 0 {
				reason := "missing flags: --" + strings.Join(missing, ", --")
				summary.Tasks[i].Steps[j].Status = StatusSkipped
				summary.Tasks[i].Steps[j].Reason = reason
				summary.Tasks[i].Counts.Add(StatusSkipped)
				send(Event{
					Kind:   StepDone,
					Task:   i,
					Step:   j,
					Status: StatusSkipped,
					Text:   step.Text,
					Reason: reason,
				})
				continue
			}

			send(Event{
				Kind:   StepStarted,
				Task:   i,
				Step:   j,
				Status: StatusRunning,
				Text:   step.Text,
			})

			if opts.DryRun {
				if step.Op == profile.OpLog {
					send(Event{
						Kind:  Message,
						Task:  i,
						Step:  j,
						Text:  step.Text,
						Color: step.Color,
					})
				}
				summary.Tasks[i].Steps[j].Status = StatusPlanned
				summary.Tasks[i].Counts.Add(StatusPlanned)
				send(Event{
					Kind:   StepDone,
					Task:   i,
					Step:   j,
					Status: StatusPlanned,
					Text:   step.Text,
				})
				continue
			}

			if step.Op == profile.OpLog {
				send(Event{
					Kind:  Message,
					Task:  i,
					Step:  j,
					Text:  step.Text,
					Color: step.Color,
				})
				summary.Tasks[i].Steps[j].Status = StatusCompleted
				summary.Tasks[i].Counts.Add(StatusCompleted)
				send(Event{
					Kind:   StepDone,
					Task:   i,
					Step:   j,
					Status: StatusCompleted,
					Text:   step.Text,
				})
				continue
			}

			var err error
			if exec == nil {
				err = errors.New("step executor is nil")
			} else {
				out := eventWriter{emit: send, event: Event{Kind: Output, Task: i, Step: j}}
				errOut := eventWriter{emit: send, event: Event{Kind: Output, Task: i, Step: j, Stderr: true}}
				err = exec(ctx, step, out, errOut)
			}

			if err != nil {
				st := StatusFailed
				stepErr := fmt.Errorf("task %q, step %d (%s): %w", task.Name, j+1, step.Op.String(), err)
				if ctxErr := ctx.Err(); ctxErr != nil {
					st = StatusCancelled
					stepErr = ctxErr
				}
				summary.Tasks[i].Steps[j].Status = st
				summary.Tasks[i].Steps[j].Err = stepErr
				summary.Tasks[i].Status = st
				send(Event{
					Kind:   StepDone,
					Task:   i,
					Step:   j,
					Status: st,
					Text:   step.Text,
					Err:    stepErr,
				})
				send(Event{
					Kind:   TaskDone,
					Task:   i,
					Status: st,
					Text:   task.Name,
					Err:    stepErr,
				})
				finalizeCounts(&summary)
				summary.Status = st
				summary.Err = stepErr
				return summary, stepErr
			}

			summary.Tasks[i].Steps[j].Status = StatusCompleted
			summary.Tasks[i].Counts.Add(StatusCompleted)
			send(Event{
				Kind:   StepDone,
				Task:   i,
				Step:   j,
				Status: StatusCompleted,
				Text:   step.Text,
			})
		}

		taskStatus := aggregateStatus(opts.DryRun, summary.Tasks[i].Counts)
		summary.Tasks[i].Status = taskStatus
		send(Event{
			Kind:   TaskDone,
			Task:   i,
			Status: taskStatus,
			Text:   task.Name,
		})
	}

	totalSteps := finalizeCounts(&summary)
	if totalSteps == 0 {
		summary.Status = StatusPending
	} else {
		summary.Status = aggregateStatus(opts.DryRun, summary.Counts)
	}
	return summary, nil
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
