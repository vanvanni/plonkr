package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
	"github.com/vanvanni/plonkr/ui"
)

// plainPrinter streams runner events as line-oriented text for pipes, logs and
// --no-tui runs. It reports every step outcome with the same status words as the
// run log and the terminal UI.
type plainPrinter struct {
	plan           *profile.Plan
	dryRun         bool
	stdout, stderr io.Writer
	last           io.Writer
	lineStart      bool
}

func (p *plainPrinter) newline() {
	if !p.lineStart {
		fmt.Fprintln(p.last)
		p.lineStart = true
	}
}

// isLog reports whether the event belongs to a log() step, whose message is
// printed on its own.
func (p *plainPrinter) isLog(e runner.Event) bool {
	if e.Task < 0 || e.Task >= len(p.plan.Tasks) {
		return false
	}
	steps := p.plan.Tasks[e.Task].Steps
	return e.Step >= 0 && e.Step < len(steps) && steps[e.Step].Op == profile.OpLog
}

func (p *plainPrinter) emit(e runner.Event) {
	if e.Kind == runner.Output {
		if e.Text == "" {
			return
		}
		target := p.stdout
		if e.Stderr {
			target = p.stderr
		}
		if !p.lineStart && p.last != target {
			p.newline()
		}
		p.last = target
		fmt.Fprint(target, e.Text)
		p.lineStart = strings.HasSuffix(e.Text, "\n")
		return
	}

	p.newline()
	switch e.Kind {
	case runner.TaskStarted:
		if e.Task < 0 || e.Task >= len(p.plan.Tasks) {
			return
		}
		task := p.plan.Tasks[e.Task]
		name := strings.Join(strings.Fields(e.Text), " ")
		if task.Baseline {
			fmt.Fprintf(p.stdout, "\n[config] %s\n", name)
		} else {
			fmt.Fprintf(p.stdout, "\n[%d] %s\n", task.Order, name)
		}
	case runner.StepStarted:
		if p.dryRun || p.isLog(e) {
			return
		}
		fmt.Fprintf(p.stdout, "  $ %s\n", e.Text)
	case runner.Message:
		fmt.Fprintln(p.stdout, ui.MessageText(e.Text, e.Color, terminalWriter(p.stdout)))
	case runner.StepDone:
		switch e.Status {
		case runner.StatusPlanned, runner.StatusSkipped, runner.StatusBypassed:
			if e.Reason != "" {
				fmt.Fprintf(p.stdout, "  [%s] (%s) %s\n", e.Status, e.Reason, e.Text)
			} else {
				fmt.Fprintf(p.stdout, "  [%s] %s\n", e.Status, e.Text)
			}
		case runner.StatusCompleted, runner.StatusFailed, runner.StatusCancelled:
			// The command was already echoed; log() messages print themselves.
			if !p.isLog(e) {
				fmt.Fprintf(p.stdout, "  [%s]\n", e.Status)
			}
		}
	}
}

func terminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func terminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
