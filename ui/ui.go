// Package ui presents runner events in a live terminal view.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

const tailLimit = 32 * 1024

type finished struct{ err error }
type tick struct{}

type taskProgress struct {
	status runner.Status
	steps  []runner.Status
}

type model struct {
	host                 string
	plan                 *profile.Plan
	tasks                []taskProgress
	active               int
	command, tail        string
	width, height, frame int
	complete, cancelling bool
	err                  error
	cancel               context.CancelFunc
	events               <-chan tea.Msg
	stop                 <-chan struct{}
	waitStarted          time.Time
}

func newModel(host string, plan *profile.Plan, cancel context.CancelFunc) *model {
	m := &model{
		host: host, plan: plan, cancel: cancel, active: -1, width: 90, height: 28,
		tasks: make([]taskProgress, len(plan.Tasks)),
	}
	for i, task := range plan.Tasks {
		m.tasks[i].steps = make([]runner.Status, len(task.Steps))
	}
	return m
}

func (m *model) pulse() tea.Cmd {
	return func() tea.Msg {
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return tick{}
		case <-m.stop:
			return nil
		}
	}
}

func (m *model) receive() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-m.events:
			return msg
		case <-m.stop:
			return nil
		}
	}
}

func (m *model) Init() tea.Cmd { return tea.Batch(m.pulse(), m.receive()) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			m.cancelling = true
			m.cancel()
		}
	case tick:
		m.frame++
		return m, m.pulse()
	case runner.Event:
		m.event(msg)
		return m, m.receive()
	case finished:
		m.complete = true
		m.err = msg.err
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) event(e runner.Event) {
	switch e.Kind {
	case runner.Output:
		m.appendTail(clean(e.Text))
		return
	case runner.Message:
		if m.tail != "" && !strings.HasSuffix(m.tail, "\n") {
			m.appendTail("\n")
		}
		m.appendTail(clean(e.Text) + "\n")
		return
	}
	if e.Task < 0 || e.Task >= len(m.tasks) {
		return
	}
	task := &m.tasks[e.Task]
	switch e.Kind {
	case runner.TaskStarted:
		m.active = e.Task
		m.command = ""
		task.status = runner.StatusRunning
	case runner.StepStarted, runner.StepDone:
		if e.Step < 0 || e.Step >= len(task.steps) {
			return
		}
		if e.Kind == runner.StepStarted {
			m.waitStarted = time.Time{}
			op := m.plan.Tasks[e.Task].Steps[e.Step].Op
			if op == profile.OpWait || op == profile.OpWaitSSH || op == profile.OpWaitUntil {
				m.waitStarted = time.Now()
			}
			// log() messages are not commands; they already appear in the output.
			m.command = ""
			if m.plan.Tasks[e.Task].Steps[e.Step].Op != profile.OpLog {
				m.command = singleLine(e.Text)
			}
			task.steps[e.Step] = runner.StatusRunning
		} else {
			m.waitStarted = time.Time{}
			task.steps[e.Step] = e.Status
		}
	case runner.TaskDone:
		m.active = e.Task
		task.status = e.Status
	}
}

func (m *model) appendTail(text string) {
	tail := m.tail + text
	start := max(0, len(tail)-tailLimit)
	for start < len(tail) && !utf8.RuneStart(tail[start]) {
		start++
	}
	m.tail = tail
	if start > 0 {
		m.tail = strings.Clone(tail[start:])
	}
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(s))
}

func singleLine(s string) string { return strings.Join(strings.Fields(clean(s)), " ") }

func colorEnabled() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return !noColor && os.Getenv("TERM") != "dumb"
}

// MessageText sanitizes a message and optionally wraps it in trusted SGR color.
func MessageText(text, color string, colored bool) string {
	text = clean(text)
	if !colored || !colorEnabled() {
		return text
	}
	code := map[string]string{
		"black": "30", "red": "31", "green": "32", "yellow": "33",
		"blue": "34", "magenta": "35", "cyan": "36", "white": "37", "gray": "90",
	}[color]
	if code == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func style(color string) lipgloss.Style {
	s := lipgloss.NewStyle()
	if colorEnabled() {
		s = s.Foreground(lipgloss.Color(color))
	}
	return s
}

func taskIcon(status runner.Status, frame int) string {
	switch status {
	case runner.StatusRunning:
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		return style("99").Render(frames[frame%len(frames)])
	case runner.StatusCompleted:
		return style("78").Render("✓")
	case runner.StatusFailed:
		return style("203").Render("✗")
	case runner.StatusSkipped:
		return style("220").Render("⊘")
	case runner.StatusBypassed:
		return style("214").Render("↷")
	case runner.StatusPlanned:
		return style("75").Render("◎")
	case runner.StatusCancelled:
		return style("203").Render("⊗")
	default:
		return style("243").Render("○")
	}
}

func taskCounts(steps []runner.Status) string {
	var c runner.Counts
	for _, status := range steps {
		c.Add(status)
	}
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
	return strings.Join(parts, " · ")
}

func (m *model) View() string {
	accent, muted := style("99"), style("243")
	status := "Executing"
	switch {
	case m.complete:
		status = "Run finished"
		if m.err != nil {
			status = "Failed"
		}
		if errors.Is(m.err, context.Canceled) || errors.Is(m.err, context.DeadlineExceeded) {
			status = "Cancelled"
		}
	case m.cancelling:
		status = "Cancelling"
	}
	heading := accent.Bold(true).Render("plonk") + " · " + singleLine(m.host)
	if m.plan.Name != "" {
		heading += " · " + singleLine(m.plan.Name)
	}
	lines := []string{heading + " · " + status, ""}
	budget := max(1, m.height/3)
	start := max(0, m.active-budget+1)
	end := min(len(m.tasks), start+budget)
	if start > 0 {
		lines = append(lines, muted.Render(fmt.Sprintf("  … %d earlier tasks", start)))
	}
	for i := start; i < end; i++ {
		task, progress := m.plan.Tasks[i], m.tasks[i]
		label := fmt.Sprintf("[%d]", task.Order)
		if task.Baseline {
			label = "[config]"
		}
		line := fmt.Sprintf("%s %s %s", taskIcon(progress.status, m.frame), label, singleLine(task.Name))
		if counts := taskCounts(progress.steps); counts != "" {
			line += "  " + muted.Render(counts)
		}
		lines = append(lines, line)
	}
	if end < len(m.tasks) {
		lines = append(lines, muted.Render(fmt.Sprintf("  … %d later tasks", len(m.tasks)-end)))
	}
	if m.command != "" {
		command := m.command
		if !m.waitStarted.IsZero() {
			command += " (" + time.Since(m.waitStarted).Truncate(time.Second).String() + " elapsed)"
		}
		lines = append(lines, "", accent.Render("$ ")+command)
	}
	output := strings.Split(strings.TrimSuffix(m.tail, "\n"), "\n")
	available := max(1, m.height-len(lines)-3)
	lines = append(lines, output[max(0, len(output)-available):]...)
	if m.err != nil {
		lines = append(lines, style("203").Render(singleLine(m.err.Error())))
	}
	if !m.complete {
		lines = append(lines, "", muted.Render("q / ctrl+c  cancel · no rollback"))
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(1, m.width-1), "…")
	}
	return strings.Join(lines, "\n") + "\n"
}

// Run displays work's events and waits for work to stop before returning.
// Work must honor cancellation of its context.
func Run(ctx context.Context, host string, plan *profile.Plan, work func(context.Context, func(runner.Event)) error, options ...tea.ProgramOption) error {
	if plan == nil || work == nil {
		return errors.New("terminal UI requires a plan and work callback")
	}
	workCtx, cancel := context.WithCancel(ctx)
	events := make(chan tea.Msg, 128)
	stop := make(chan struct{})
	done := make(chan struct{})
	m := newModel(host, plan, cancel)
	m.events, m.stop = events, stop
	var workErr error
	go func() {
		defer close(done)
		workErr = work(workCtx, func(e runner.Event) {
			select {
			case events <- e:
			case <-stop:
			}
		})
		select {
		case events <- finished{workErr}:
		case <-stop:
		}
	}()
	_, uiErr := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithoutSignalHandler()}, options...)...).Run()
	cancel()
	close(stop)
	<-done
	if uiErr != nil {
		return errors.Join(uiErr, workErr)
	}
	return workErr
}
