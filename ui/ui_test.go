package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

func TestMessageText(t *testing.T) {
	for _, tc := range []struct {
		name, color, term, want string
		colored, noColor        bool
	}{
		{"trusted palette", "cyan", "xterm", "\x1b[36mhello\tworld\n\x1b[0m", true, false},
		{"gray", "gray", "xterm", "\x1b[90mhello\tworld\n\x1b[0m", true, false},
		{"plain", "red", "xterm", "hello\tworld\n", false, false},
		{"no color", "red", "xterm", "hello\tworld\n", true, true},
		{"dumb terminal", "red", "dumb", "hello\tworld\n", true, false},
		{"untrusted color", "\x1b[2J", "xterm", "hello\tworld\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM", tc.term)
			t.Setenv("NO_COLOR", "")
			if !tc.noColor {
				if err := os.Unsetenv("NO_COLOR"); err != nil {
					t.Fatal(err)
				}
			}
			raw := "\x1b[2Jhello\x00\t\x1b]52;c;secret\aworld\x7f\r\n"
			if got := MessageText(raw, tc.color, tc.colored); got != tc.want {
				t.Errorf("MessageText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWaitElapsedDisplay(t *testing.T) {
	plan := &profile.Plan{Tasks: []profile.Task{{Name: "Ready", Steps: []profile.Step{{Op: profile.OpWaitSSH}}}}}
	m := newModel("host", plan, func() {})
	m.event(runner.Event{Kind: runner.TaskStarted, Task: 0})
	m.event(runner.Event{Kind: runner.StepStarted, Task: 0, Step: 0, Text: "wait_ssh 1m"})
	m.waitStarted = time.Now().Add(-3 * time.Second)
	if !strings.Contains(m.View(), "3s elapsed") {
		t.Fatal(m.View())
	}
	m.event(runner.Event{Kind: runner.StepDone, Task: 0, Step: 0, Status: runner.StatusCompleted})
	if strings.Contains(m.View(), "elapsed") {
		t.Fatal("completed timer is live")
	}
}

func TestViewStatusAndCounts(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, tc := range []struct {
		status runner.Status
		icon   string
		count  string
	}{
		{runner.StatusPending, "○", "pending"},
		{runner.StatusRunning, "⠋", "pending"},
		{runner.StatusCompleted, "✓", "completed"},
		{runner.StatusFailed, "✗", "failed"},
		{runner.StatusSkipped, "⊘", "skipped"},
		{runner.StatusBypassed, "↷", "bypassed"},
		{runner.StatusPlanned, "◎", "planned"},
		{runner.StatusCancelled, "⊗", "cancelled"},
	} {
		t.Run(tc.status.String(), func(t *testing.T) {
			plan := &profile.Plan{Name: "Setup", Tasks: []profile.Task{
				{Name: "Machine", Baseline: true, Steps: make([]profile.Step, 1)},
			}}
			m := newModel("lab", plan, func() {})
			if tc.status != runner.StatusPending {
				m.Update(runner.Event{Kind: runner.TaskStarted})
				m.Update(runner.Event{Kind: runner.StepStarted, Text: "echo hello"})
			}
			if tc.status > runner.StatusRunning {
				m.Update(runner.Event{Kind: runner.StepDone, Status: tc.status})
				m.Update(runner.Event{Kind: runner.TaskDone, Status: tc.status})
			}
			m.Update(runner.Event{Kind: runner.Output, Text: "hello\x1b[2J\x00\n"})
			view := m.View()
			for _, want := range []string{
				"plonk · lab · Setup", tc.icon + " [config] Machine  1 " + tc.count,
				"hello\n", "q / ctrl+c  cancel · no rollback",
			} {
				if !strings.Contains(view, want) {
					t.Errorf("view missing %q in %q", want, view)
				}
			}
			if strings.ContainsAny(view, "\x1b\x00") {
				t.Errorf("control sequence reached view: %q", view)
			}
			if tc.status == runner.StatusRunning {
				m.Update(tick{})
				if !strings.Contains(m.View(), "⠙") {
					t.Error("spinner did not advance")
				}
			}
		})
	}
}

func TestTailLimit(t *testing.T) {
	m := newModel("lab", &profile.Plan{}, func() {})
	m.Update(runner.Event{Kind: runner.Output, Text: "old" + strings.Repeat("界", tailLimit)})
	m.Update(runner.Event{Kind: runner.Message, Text: "last\x1b[2J"})
	if len(m.tail) > tailLimit || !utf8.ValidString(m.tail) || !strings.HasSuffix(m.tail, "\nlast\n") {
		t.Fatalf("invalid tail of %d bytes", len(m.tail))
	}
}

func TestRun(t *testing.T) {
	failure := errors.New("command failed")
	for _, tc := range []struct {
		name   string
		input  string
		cancel bool
		want   error
	}{
		{"complete", "", false, nil},
		{"failure", "", false, failure},
		{"q", "q", false, context.Canceled},
		{"ctrl+c", "\x03", false, context.Canceled},
		{"parent cancellation", "", true, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deadline, stop := context.WithTimeout(t.Context(), 5*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(deadline)
			defer cancel()
			stopped := make(chan struct{})
			var input io.Reader
			if tc.input != "" {
				input = strings.NewReader(tc.input)
			}
			err := Run(ctx, "lab", &profile.Plan{}, func(ctx context.Context, emit func(runner.Event)) error {
				defer close(stopped)
				emit(runner.Event{Kind: runner.Output, Text: "started"})
				if tc.cancel {
					cancel()
				}
				if tc.input != "" || tc.cancel {
					<-ctx.Done()
					// More than the channel capacity, so shutdown also exercises
					// event delivery while cancellation is in progress.
					for range 256 {
						emit(runner.Event{Kind: runner.Output, Text: "stopping\n"})
					}
					return ctx.Err()
				}
				return tc.want
			}, tea.WithInput(input), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
			if !errors.Is(err, tc.want) {
				t.Errorf("Run = %v, want %v", err, tc.want)
			}
			select {
			case <-stopped:
			default:
				t.Error("Run returned before work stopped")
			}
		})
	}
}
