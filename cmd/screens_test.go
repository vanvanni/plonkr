//go:build screens

package cmd

// Records the real plain output (--no-tui and piped runs) of the website demo
// profile, with the same journal summary a real run prints. It drives the real
// runner with scripted target output; no SSH connection is made. See
// ui/screens_test.go and docs/design/terminal/build.mjs.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runlog"
	"github.com/vanvanni/plonkr/runner"
)

type screenLocks map[string]string

func (d screenLocks) CheckLock(_ context.Context, task profile.Task) (bool, string, error) {
	why, held := d[task.Name]
	return held, why, nil
}

func (screenLocks) RecordLock(context.Context, profile.Task) error { return nil }

func TestRecordPlainScreens(t *testing.T) {
	out := os.Getenv("PLONK_SCREENS_OUT")
	if out == "" {
		t.Skip("set PLONK_SCREENS_OUT to record the website screens")
	}
	dir := filepath.Join("..", "docs", "design", "terminal")
	source, err := os.ReadFile(filepath.Join(dir, "demo-profile.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := profile.Parse(source, "web.hcl")
	if err != nil {
		t.Fatal(err)
	}
	var demo struct {
		Steps map[string]struct {
			Lines []string `json:"lines"`
		} `json:"steps"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "demo-output.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &demo); err != nil {
		t.Fatal(err)
	}

	result := map[string]string{}
	for name, locks := range map[string]screenLocks{
		"run1": {},
		"run2": {"Install Go": "exists and eval unchanged", "Fetch application": "exists"},
	} {
		var buf bytes.Buffer
		journal, err := runlog.Open(t.TempDir(), "lab-ct", "web", plan)
		if err != nil {
			t.Fatal(err)
		}
		printer := plainPrinter{plan: plan, stdout: &buf, stderr: &buf, last: &buf, lineStart: true}
		exec := func(_ context.Context, step profile.Step, stdout, _ io.Writer) error {
			for _, line := range demo.Steps[step.Text].Lines {
				if _, err := io.WriteString(stdout, line+"\n"); err != nil {
					return err
				}
			}
			return nil
		}
		_, runErr := runner.Run(context.Background(), plan, runner.Options{Locks: locks}, exec, func(e runner.Event) {
			clean, err := journal.Event(e)
			if err != nil {
				t.Fatal(err)
			}
			printer.emit(clean)
		})
		printer.newline()
		logErr := journal.Finish(runErr, false)
		buf.WriteString("\n" + journal.Summary(runErr, false))
		if runErr != nil || logErr != nil {
			t.Fatal(runErr, logErr)
		}
		// The journal prints this machine's log path. Show a typical Linux one.
		result[name] = regexp.MustCompile(`(?m)^Log: .*$`).ReplaceAllString(buf.String(),
			"Log: ~/.local/state/plonkr/logs/20261127T141502Z-lab-ct-web.log")
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "plain-output.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
