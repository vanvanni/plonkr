//go:build screens

package ui

// Records the real terminal UI, frame by frame, for the website screenshots and
// promo. It drives the real runner and the real model with scripted target
// output; no SSH connection is made. Run it through docs/design/terminal/build.mjs
// or directly:
//
//	PLONK_SCREENS_OUT=<dir> go test -tags screens -run TestRecordScreens ./ui

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muesli/termenv"

	"github.com/charmbracelet/lipgloss"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

type demoSteps struct {
	Steps map[string]struct {
		Work  int      `json:"work"`
		Lines []string `json:"lines"`
	} `json:"steps"`
	Stills map[string]string `json:"stills"`
}

type demoLocks map[string]string

func (d demoLocks) CheckLock(_ context.Context, task profile.Task) (bool, string, error) {
	why, held := d[task.Name]
	return held, why, nil
}

func (demoLocks) RecordLock(context.Context, profile.Task) error { return nil }

type screenFrame struct {
	Ms   int    `json:"ms"`
	View string `json:"view"`
}

type screenScene struct {
	Frames []screenFrame  `json:"frames"`
	Stills map[string]int `json:"stills"`
}

func TestRecordScreens(t *testing.T) {
	out := os.Getenv("PLONK_SCREENS_OUT")
	if out == "" {
		t.Skip("set PLONK_SCREENS_OUT to record the website screens")
	}
	// Tests run without a terminal, so colors would otherwise be stripped.
	// ANSI256 is what the CLI emits on a typical terminal.
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	os.Unsetenv("NO_COLOR")

	dir := filepath.Join("..", "docs", "design", "terminal")
	source, err := os.ReadFile(filepath.Join(dir, "demo-profile.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := profile.Parse(source, "web.hcl")
	if err != nil {
		t.Fatal(err)
	}
	var demo demoSteps
	raw, err := os.ReadFile(filepath.Join(dir, "demo-output.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &demo); err != nil {
		t.Fatal(err)
	}

	scenes := map[string]screenScene{
		// A first run does everything.
		"run1": record(t, plan, demo, demoLocks{}),
		// A repeat run bypasses what the target already has.
		"run2": record(t, plan, demo, demoLocks{
			"Install Go":        "exists and eval unchanged",
			"Fetch application": "exists",
		}),
	}
	data, err := json.MarshalIndent(scenes, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "ui-frames.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func record(t *testing.T, plan *profile.Plan, demo demoSteps, locks runner.Locker) screenScene {
	t.Helper()
	m := newModel("lab-ct", plan, func() {})
	m.width, m.height = 100, 22
	scene := screenScene{Stills: map[string]int{}}
	snap := func(ms int) { scene.Frames = append(scene.Frames, screenFrame{Ms: ms, View: m.View()}) }

	snap(400)
	emit := func(e runner.Event) {
		m.event(e)
		ms := 100
		switch e.Kind {
		case runner.TaskStarted:
			ms = 120
		case runner.StepStarted:
			ms = 150
		case runner.Output:
			ms = 300
			for name, marker := range demo.Stills {
				if strings.Contains(e.Text, marker) {
					scene.Stills[name] = len(scene.Frames)
				}
			}
		case runner.StepDone:
			ms = 120
		case runner.TaskDone:
			ms = 300
		}
		snap(ms)
	}
	exec := func(_ context.Context, step profile.Step, stdout, _ io.Writer) error {
		spec, ok := demo.Steps[step.Text]
		if !ok {
			t.Fatalf("demo-output.json has no entry for step %q", step.Text)
		}
		for range spec.Work {
			m.frame++
			snap(100)
		}
		for _, line := range spec.Lines {
			if _, err := io.WriteString(stdout, line+"\n"); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := runner.Run(context.Background(), plan, runner.Options{Locks: locks}, exec, emit); err != nil {
		t.Fatal(err)
	}
	m.complete = true
	scene.Stills["finished"] = len(scene.Frames)
	snap(3500)
	return scene
}
