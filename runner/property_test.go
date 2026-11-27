package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/vanvanni/plonkr/profile"
)

// propertyRun is one randomly generated scenario plus everything observed while
// running it, so invariants can be checked against what actually happened.
type propertyRun struct {
	plan     *profile.Plan
	opts     Options
	failAt   [2]int // task, step; {-1,-1} for none
	cancelAt [2]int // cancel the context when this step starts executing
	lockHeld map[string]bool
	lockErr  string // task whose probe fails
	recErr   string // task whose record fails

	executed [][2]int
	checked  []string
	recorded []string
	events   []Event
	summary  Summary
	err      error
}

type scriptedLocker struct{ run *propertyRun }

func (l scriptedLocker) CheckLock(_ context.Context, task profile.Task) (bool, string, error) {
	l.run.checked = append(l.run.checked, task.Name)
	if l.run.lockErr == task.Name {
		return false, "", errors.New("probe failed")
	}
	return l.run.lockHeld[task.Name], "exists", nil
}

func (l scriptedLocker) RecordLock(_ context.Context, task profile.Task) error {
	l.run.recorded = append(l.run.recorded, task.Name)
	if l.run.recErr == task.Name {
		return errors.New("record failed")
	}
	return nil
}

func generateRun(rng *rand.Rand) *propertyRun {
	run := &propertyRun{failAt: [2]int{-1, -1}, cancelAt: [2]int{-1, -1}, lockHeld: map[string]bool{}}
	flags := []string{"a", "b"}
	plan := &profile.Plan{}
	for i := range 1 + rng.IntN(5) {
		task := profile.Task{Name: fmt.Sprintf("t%d", i), Order: i, Bypass: rng.IntN(6) == 0}
		if rng.IntN(3) == 0 {
			task.Lock = profile.Lock{Exists: []string{"/x"}}
			run.lockHeld[task.Name] = rng.IntN(2) == 0
		}
		for j := range 1 + rng.IntN(5) {
			step := profile.Step{Phase: profile.PhaseScript, Text: fmt.Sprintf("t%d-s%d", i, j)}
			if rng.IntN(5) == 0 {
				step.Op = profile.OpLog
			}
			for _, flag := range flags {
				if rng.IntN(5) == 0 {
					step.Flags = append(step.Flags, flag)
				}
			}
			task.Steps = append(task.Steps, step)
		}
		plan.Tasks = append(plan.Tasks, task)
	}
	run.plan = plan
	run.opts = Options{
		DryRun: rng.IntN(5) == 0, Force: rng.IntN(5) == 0,
		Flags: map[string]bool{"a": rng.IntN(2) == 0, "b": rng.IntN(2) == 0},
	}
	if rng.IntN(3) == 0 {
		run.opts.BypassTasks = map[string]bool{plan.Tasks[rng.IntN(len(plan.Tasks))].Name: true}
	}
	pick := func() [2]int {
		i := rng.IntN(len(plan.Tasks))
		return [2]int{i, rng.IntN(len(plan.Tasks[i].Steps))}
	}
	if rng.IntN(3) == 0 {
		run.failAt = pick()
	}
	if rng.IntN(6) == 0 {
		run.cancelAt = pick()
	}
	if rng.IntN(8) == 0 {
		run.lockErr = plan.Tasks[rng.IntN(len(plan.Tasks))].Name
	}
	if rng.IntN(8) == 0 {
		run.recErr = plan.Tasks[rng.IntN(len(plan.Tasks))].Name
	}
	return run
}

func (r *propertyRun) execute(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.opts.Locks = scriptedLocker{r}
	exec := func(_ context.Context, step profile.Step, _, _ io.Writer) error {
		task, index := taskAndStep(r.plan, step)
		r.executed = append(r.executed, [2]int{task, index})
		if [2]int{task, index} == r.cancelAt {
			cancel()
		}
		if [2]int{task, index} == r.failAt {
			return errors.New("step failed")
		}
		return nil
	}
	r.summary, r.err = Run(ctx, r.plan, r.opts, exec, func(e Event) { r.events = append(r.events, e) })
}

// taskAndStep recovers the position of a step from its unique text.
func taskAndStep(plan *profile.Plan, step profile.Step) (int, int) {
	for i, task := range plan.Tasks {
		for j, candidate := range task.Steps {
			if candidate.Text == step.Text {
				return i, j
			}
		}
	}
	return -1, -1
}

func TestRunProperties(t *testing.T) {
	for seed := uint64(0); seed < 3000; seed++ {
		run := generateRun(rand.New(rand.NewPCG(seed, seed*7919+1)))
		run.execute(t)
		if t.Failed() {
			return
		}
		checkRunInvariants(t, seed, run)
		if t.Failed() {
			return
		}
	}
}

func checkRunInvariants(t *testing.T, seed uint64, r *propertyRun) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Helper()
		t.Errorf("seed %d: %s\nplan: %+v\nopts: dry=%t force=%t flags=%v bypass=%v failAt=%v cancelAt=%v lockHeld=%v lockErr=%q recErr=%q\nsummary: status=%v counts=%+v err=%v\nexecuted=%v checked=%v recorded=%v",
			seed, fmt.Sprintf(format, args...), r.plan.Tasks, r.opts.DryRun, r.opts.Force, r.opts.Flags, r.opts.BypassTasks,
			r.failAt, r.cancelAt, r.lockHeld, r.lockErr, r.recErr, r.summary.Status, r.summary.Counts, r.err, r.executed, r.checked, r.recorded)
	}

	// 1. The summary accounts for every step exactly once.
	total, sum := 0, Counts{}
	for i, task := range r.plan.Tasks {
		var taskSum Counts
		for _, step := range r.summary.Tasks[i].Steps {
			taskSum.Add(step.Status)
			sum.Add(step.Status)
			total++
		}
		if taskSum != r.summary.Tasks[i].Counts {
			fail("task %d counts %+v != recomputed %+v", i, r.summary.Tasks[i].Counts, taskSum)
		}
		if len(r.summary.Tasks[i].Steps) != len(task.Steps) {
			fail("task %d lost steps", i)
		}
	}
	if sum != r.summary.Counts {
		fail("run counts %+v != recomputed %+v", r.summary.Counts, sum)
	}

	// 2. Error and status agree, and an error leaves nothing running.
	if (r.err != nil) != (r.summary.Err != nil) {
		fail("returned error and summary error disagree")
	}
	if r.err != nil {
		if r.summary.Status != StatusFailed && r.summary.Status != StatusCancelled {
			fail("an error must end Failed or Cancelled")
		}
		if (r.summary.Status == StatusCancelled) != errors.Is(r.err, context.Canceled) {
			fail("Cancelled must correspond exactly to context cancellation")
		}
	} else if r.summary.Counts.Failed != 0 || r.summary.Counts.Cancelled != 0 {
		fail("a successful run reports failed or cancelled steps")
	}
	if r.summary.Counts.Failed+r.summary.Counts.Cancelled > 1 {
		fail("at most one step can fail or be cancelled")
	}
	for _, task := range r.summary.Tasks {
		for _, step := range task.Steps {
			if step.Status == StatusRunning {
				fail("a step is still running after Run returned")
			}
		}
	}

	// 3. Nothing runs after the point of failure: every later step is pending.
	failedAt := [2]int{-1, -1}
	for i, task := range r.summary.Tasks {
		for j, step := range task.Steps {
			if step.Status == StatusFailed || step.Status == StatusCancelled {
				failedAt = [2]int{i, j}
			}
		}
	}
	if r.err != nil && failedAt[0] >= 0 {
		for i, task := range r.summary.Tasks {
			for j, step := range task.Steps {
				after := i > failedAt[0] || (i == failedAt[0] && j > failedAt[1])
				if after && step.Status != StatusPending {
					fail("step %d.%d ran after the failure at %v: %v", i, j, failedAt, step.Status)
				}
			}
		}
		for _, call := range r.executed {
			if call[0] > failedAt[0] || (call[0] == failedAt[0] && call[1] > failedAt[1]) {
				fail("executor called for %v after the failure at %v", call, failedAt)
			}
		}
	}
	if r.err == nil {
		for _, task := range r.summary.Tasks {
			for _, step := range task.Steps {
				if step.Status == StatusPending {
					fail("a successful run left a step pending")
				}
			}
		}
	}

	// 4. The executor only sees steps that should really run.
	for _, call := range r.executed {
		task, step := r.plan.Tasks[call[0]], r.plan.Tasks[call[0]].Steps[call[1]]
		switch {
		case r.opts.DryRun:
			fail("dry run executed %v", call)
		case task.Bypass || r.opts.BypassTasks[task.Name]:
			fail("bypassed task executed %v", call)
		case len(step.MissingFlags(r.opts.Flags)) > 0:
			fail("step with missing flags executed %v", call)
		case step.Op == profile.OpLog:
			fail("log step executed %v", call)
		case !r.opts.Force && r.lockHeld[task.Name] && !task.Lock.IsZero():
			fail("held lock did not bypass %v", call)
		}
	}

	// 5. Locks are probed and recorded only when they matter.
	for _, name := range r.checked {
		task := r.plan.Tasks[taskIndex(r.plan, name)]
		if r.opts.DryRun || r.opts.Force || task.Bypass || r.opts.BypassTasks[name] || task.Lock.IsZero() {
			fail("lock of %q was probed", name)
		}
		if !slices.ContainsFunc(task.Steps, func(s profile.Step) bool { return len(s.MissingFlags(r.opts.Flags)) == 0 }) {
			fail("lock of %q was probed although every step is flag-gated off", name)
		}
	}
	for _, name := range r.recorded {
		index := taskIndex(r.plan, name)
		task := r.plan.Tasks[index]
		if r.opts.DryRun || task.Bypass || r.opts.BypassTasks[name] || task.Lock.IsZero() || r.summary.Tasks[index].Counts.Completed == 0 {
			fail("lock of %q was recorded", name)
		}
		if r.summary.Tasks[index].Counts.Failed != 0 {
			fail("a failed task recorded lock state for %q", name)
		}
	}

	// 6. Dry runs only ever plan, skip or bypass.
	if r.opts.DryRun && (r.summary.Counts.Completed != 0 || r.summary.Counts.Failed != 0 || len(r.checked) != 0 || len(r.recorded) != 0) {
		fail("a dry run did real work")
	}

	// 7. The event stream is well formed.
	checkEventStream(t, seed, r, fail)
}

func taskIndex(plan *profile.Plan, name string) int {
	for i, task := range plan.Tasks {
		if task.Name == name {
			return i
		}
	}
	return -1
}

func checkEventStream(t *testing.T, seed uint64, r *propertyRun, fail func(string, ...any)) {
	t.Helper()
	currentTask, openStep := -1, -1
	done := map[int]bool{}
	for n, e := range r.events {
		switch e.Kind {
		case TaskStarted:
			if e.Task <= currentTask && currentTask != -1 && !done[currentTask] {
				fail("event %d: task %d started before task %d finished", n, e.Task, currentTask)
			}
			if e.Task < currentTask {
				fail("event %d: tasks went backwards", n)
			}
			currentTask, openStep = e.Task, -1
		case StepStarted:
			if e.Task != currentTask || openStep != -1 {
				fail("event %d: step started out of order (%+v)", n, e)
			}
			openStep = e.Step
		case Output, Message:
			if e.Task != currentTask || openStep != e.Step {
				fail("event %d: output outside its step (%+v)", n, e)
			}
		case StepDone:
			// Skipped and bypassed steps finish without starting.
			if e.Task != currentTask {
				fail("event %d: step finished in the wrong task (%+v)", n, e)
			}
			if openStep != -1 && openStep != e.Step {
				fail("event %d: step %d finished while step %d was open", n, e.Step, openStep)
			}
			openStep = -1
		case TaskDone:
			if e.Task != currentTask || openStep != -1 {
				fail("event %d: task finished with a step open (%+v)", n, e)
			}
			if done[e.Task] {
				fail("event %d: task %d finished twice", n, e.Task)
			}
			done[e.Task] = true
		}
	}
	if r.err == nil {
		for i := range r.plan.Tasks {
			if !done[i] {
				fail("task %d never finished in a successful run", i)
			}
		}
	}
}
