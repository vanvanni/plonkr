// Package runlog records execution events and a durable run summary.
package runlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

type taskProgress struct {
	status runner.Status
	steps  []runner.Status
}

// Log owns the run's file. Serialize calls to Event, Summary, and Finish.
type Log struct {
	Path      string
	file      *os.File
	heading   string
	plan      *profile.Plan
	tasks     []taskProgress
	stdout    *cleaner
	stderr    *cleaner
	lineStart bool
	err       error
}

// Open creates a private log before execution begins.
func Open(dir, host, profileRef string, plan *profile.Plan) (*Log, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("log directory must be absolute: %q", dir)
	}
	if plan == nil {
		return nil, errors.New("run log requires a plan")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	stem := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, host)
	stem = stem[:min(64, len(stem))]
	file, err := os.CreateTemp(dir, time.Now().UTC().Format("20060102T150405Z")+"-"+stem+"-*.log")
	if err != nil {
		return nil, fmt.Errorf("create run log: %w", err)
	}
	label := plan.Name
	if label == "" {
		label = profileRef
	}
	l := &Log{
		Path: file.Name(), file: file, plan: plan, lineStart: true,
		heading: fmt.Sprintf("plonk · %s · %s", singleLine(host), singleLine(label)),
		tasks:   make([]taskProgress, len(plan.Tasks)),
		stdout:  newCleaner(), stderr: newCleaner(),
	}
	for i, task := range plan.Tasks {
		l.tasks[i].steps = make([]runner.Status, len(task.Steps))
	}
	l.line(l.heading)
	if l.err != nil {
		return nil, errors.Join(l.err, file.Close())
	}
	return l, nil
}

func (l *Log) write(s string) {
	s = l.plan.Secrets.Text(s)
	if l.err != nil || s == "" {
		return
	}
	if _, err := l.file.WriteString(s); err != nil {
		l.err = fmt.Errorf("write run log %q: %w", l.Path, err)
	}
	l.lineStart = strings.HasSuffix(s, "\n")
}

func (l *Log) line(s string) {
	if !l.lineStart {
		l.write("\n")
	}
	l.write(s + "\n")
}

func taskLabel(task profile.Task) string {
	if task.Baseline {
		return "[config] " + singleLine(task.Name)
	}
	return fmt.Sprintf("[%d] %s", task.Order, singleLine(task.Name))
}

// Event records an event and returns a sanitized copy for display.
func (l *Log) Event(e runner.Event) (runner.Event, error) {
	e.Text = l.plan.Secrets.Text(e.Text)
	e.Reason = l.plan.Secrets.Text(e.Reason)
	if e.Err != nil {
		e.Err = errors.New(l.plan.Secrets.Text(e.Err.Error()))
	}
	e.Reason = singleLine(e.Reason)
	e.Color = singleLine(e.Color)
	if e.Kind == runner.Output {
		stream := l.stdout
		if e.Stderr {
			stream = l.stderr
		}
		e.Text = stream.clean(e.Text)
		l.write(e.Text)
		return e, l.err
	}
	e.Text = safe(e.Text)
	if e.Kind == runner.Message {
		l.line(e.Text)
		return e, l.err
	}
	if e.Task < 0 || e.Task >= len(l.tasks) {
		return e, l.err
	}
	task := &l.tasks[e.Task]
	switch e.Kind {
	case runner.TaskStarted:
		task.status = runner.StatusRunning
		l.line(taskLabel(l.plan.Tasks[e.Task]))
	case runner.StepStarted:
		l.stdout.reset()
		l.stderr.reset()
		isMessage := false
		if e.Step >= 0 && e.Step < len(task.steps) {
			task.steps[e.Step] = runner.StatusRunning
			isMessage = l.plan.Tasks[e.Task].Steps[e.Step].Op == profile.OpLog
		}
		if e.Text != "" && !isMessage {
			l.line("$ " + e.Text)
		}
	case runner.StepDone, runner.TaskDone:
		detail := singleLine(e.Text)
		if e.Kind == runner.TaskDone {
			task.status = e.Status
			if detail == "" {
				detail = taskLabel(l.plan.Tasks[e.Task])
			}
		} else if e.Step >= 0 && e.Step < len(task.steps) {
			task.steps[e.Step] = e.Status
		}
		if e.Reason != "" {
			detail = "(" + e.Reason + ") " + detail
		}
		l.line(strings.TrimSpace(fmt.Sprintf("[%s] %s", e.Status, detail)))
	}
	return e, l.err
}

// Summary reports the task outcomes and step totals recorded so far.
func (l *Log) Summary(runErr error, dry bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s\n", l.heading, l.overall(runErr, dry))
	var total runner.Counts
	for i, task := range l.tasks {
		var counts runner.Counts
		for _, status := range task.steps {
			counts.Add(status)
			total.Add(status)
		}
		status := task.status
		if status == runner.StatusRunning {
			status = runner.StatusPending
		}
		fmt.Fprintf(&b, "  %s: %s (%s)\n", taskLabel(l.plan.Tasks[i]), countText(counts), status)
	}
	fmt.Fprintf(&b, "Total: %s\nLog: %s\n", countText(total), safe(l.Path))
	return l.plan.Secrets.Text(b.String())
}

func (l *Log) overall(runErr error, dry bool) string {
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return "Cancelled."
	}
	var tasks runner.Counts
	for _, task := range l.tasks {
		tasks.Add(task.status)
	}
	switch {
	case runErr != nil || tasks.Failed > 0:
		return "Failed."
	case tasks.Cancelled > 0:
		return "Cancelled."
	case tasks.Pending > 0:
		return "Pending."
	case dry:
		switch {
		case tasks.Planned > 0:
			return "Dry run complete (no SSH connection made)."
		case tasks.Bypassed > 0:
			return "Dry run bypassed (no SSH connection made)."
		default:
			return "Dry run skipped (no SSH connection made)."
		}
	case tasks.Completed > 0 || len(l.tasks) == 0:
		return "Complete."
	case tasks.Planned > 0:
		return "Planned."
	case tasks.Bypassed > 0:
		return "Bypassed."
	default:
		return "Skipped."
	}
}

func countText(c runner.Counts) string {
	var parts []string
	for _, count := range []struct {
		status runner.Status
		n      int
	}{
		{runner.StatusCompleted, c.Completed}, {runner.StatusPlanned, c.Planned},
		{runner.StatusSkipped, c.Skipped}, {runner.StatusBypassed, c.Bypassed},
		{runner.StatusFailed, c.Failed}, {runner.StatusCancelled, c.Cancelled},
		{runner.StatusPending, c.Pending},
	} {
		if count.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count.n, count.status))
		}
	}
	if len(parts) == 0 {
		return "0 steps"
	}
	return strings.Join(parts, ", ")
}

// Finish writes the summary, syncs the log, and closes it.
func (l *Log) Finish(runErr error, dry bool) error {
	l.line("")
	l.write(l.Summary(runErr, dry))
	if runErr != nil {
		l.line("Error: " + singleLine(runErr.Error()))
	}
	return errors.Join(l.err, l.file.Sync(), l.file.Close())
}

func singleLine(s string) string { return strings.Join(strings.Fields(safe(s)), " ") }

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(s))
}

type cleaner struct {
	parser *ansi.Parser
	text   strings.Builder
	cr     bool
}

func newCleaner() *cleaner {
	c := &cleaner{parser: ansi.NewParser()}
	c.parser.SetDataSize(1)
	c.parser.SetHandler(ansi.Handler{
		Print: func(r rune) {
			c.cr = false
			if !unicode.IsControl(r) {
				c.text.WriteRune(r)
			}
		},
		Execute: func(b byte) {
			switch b {
			case '\r':
				c.text.WriteByte('\n')
				c.cr = true
			case '\n':
				if !c.cr {
					c.text.WriteByte('\n')
				}
				c.cr = false
			case '\t':
				c.text.WriteByte('\t')
				c.cr = false
			}
		},
	})
	return c
}

func (c *cleaner) clean(s string) string {
	c.text.Reset()
	c.parser.Parse([]byte(s))
	return c.text.String()
}

func (c *cleaner) reset() {
	c.parser.Reset()
	c.cr = false
}
