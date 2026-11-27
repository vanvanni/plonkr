package cmd

import (
	"bytes"
	"context"
	"errors"
	"github.com/vanvanni/plonkr/plugin"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/secret"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginApproval(t *testing.T) {
	for _, auto := range []bool{false, true} {
		var out bytes.Buffer
		err := approvePlugin(t.Context(), plugin.Approval{Plugin: "fake", Definition: "web", Action: "replace"}, strings.NewReader("yes\n"), &out, auto)
		if (err == nil) != auto {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "destructive") {
			t.Fatal(out.String())
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := approvePlugin(ctx, plugin.Approval{}, strings.NewReader(""), io.Discard, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestTargetlessRun(t *testing.T) {
	env := newE2E(t)
	// A controller-only wait/log profile needs no placeholder HOST.
	file := filepath.Join(env.dir, "local.hcl")
	if err := os.WriteFile(file, []byte(`tasks {
 task "Local" {
  order = 1
  setup = [wait("1s"), log("controller only")]
 }
}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := runWith(t.Context(), "run", file, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	env.noConnections()
}

func TestPlanSummaryRedactsPluginSecrets(t *testing.T) {
	r := &secret.Redactor{}
	r.Add(secret.New("credential"))
	defer r.Clear()
	plan := &profile.Plan{Name: "credential", Secrets: r, Tasks: []profile.Task{{Name: "credential", Steps: []profile.Step{{Text: "echo credential"}}}}}
	var out bytes.Buffer
	if err := writePlanSummary(&out, plan); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "credential") {
		t.Fatal("plan disclosed secret")
	}
}
