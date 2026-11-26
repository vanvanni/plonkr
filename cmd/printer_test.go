package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

func TestPlainPrinterReportsEveryOutcome(t *testing.T) {
	plan := &profile.Plan{Tasks: []profile.Task{{
		Order: 1, Name: "Deploy",
		Steps: []profile.Step{{Text: "echo hi"}, {Op: profile.OpLog, Text: "Done"}},
	}}}
	var out, errOut bytes.Buffer
	p := plainPrinter{plan: plan, stdout: &out, stderr: &errOut, last: &out, lineStart: true}
	for _, e := range []runner.Event{
		{Kind: runner.TaskStarted, Task: 0, Text: "Deploy"},
		{Kind: runner.StepStarted, Task: 0, Step: 0, Text: "echo hi"},
		{Kind: runner.Output, Task: 0, Step: 0, Text: "hi\n"},
		{Kind: runner.StepDone, Task: 0, Step: 0, Status: runner.StatusCompleted, Text: "echo hi"},
		{Kind: runner.StepStarted, Task: 0, Step: 1, Text: "Done"},
		{Kind: runner.Message, Task: 0, Step: 1, Text: "Done"},
		{Kind: runner.StepDone, Task: 0, Step: 1, Status: runner.StatusCompleted, Text: "Done"},
		{Kind: runner.StepDone, Task: 0, Step: 0, Status: runner.StatusFailed, Text: "echo hi"},
		{Kind: runner.StepDone, Task: 0, Step: 0, Status: runner.StatusCancelled, Text: "echo hi"},
	} {
		p.emit(e)
	}
	want := "\n[1] Deploy\n  $ echo hi\nhi\n  [completed]\nDone\n  [failed]\n  [cancelled]\n"
	if out.String() != want {
		t.Fatalf("output = %q\nwant     %q", out.String(), want)
	}
	if strings.Contains(out.String(), "[completed]\n  [completed]") {
		t.Fatal("log step was reported twice")
	}
}

func TestErrorTextStripsTerminalControls(t *testing.T) {
	got := ErrorText(errors.New("bad \x1b[2Jprofile\x1b]52;c;AAAA\x07 text"))
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 7) || got != "bad profile text" {
		t.Fatalf("ErrorText = %q", got)
	}
}
