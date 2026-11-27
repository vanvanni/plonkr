package provision

import (
	"bytes"
	"context"
	"github.com/vanvanni/plonkr/plugin"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
	"github.com/vanvanni/plonkr/secret"
	"io"
	"strings"
	"testing"
)

func TestPluginPreviewDeniesTargetSSH(t *testing.T) {
	mutations := 0
	obj := plugin.NewObject(plugin.ObjectSpec[int, int]{Name: "machine", Decode: func(*plugin.Decoder) (int, error) { return 0, nil }, Read: func(ctx context.Context, r plugin.Run[int], _ int) (int, bool, error) {
		return 0, false, r.Target.Run(ctx, "echo never", nil)
	}, Create: func(context.Context, plugin.Run[int], int) error { mutations++; return nil }, Delete: func(context.Context, plugin.Run[int], int) error { mutations++; return nil }, Diff: func(int, int) []plugin.Change { return nil }})
	p := plugin.New(plugin.Spec[int]{Name: "fake", Configure: func(*plugin.Decoder) (int, error) { return 0, nil }, Objects: []plugin.Object[int]{obj}})
	session, err := p.Configure(plugin.NewDecoder("", nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	plan := &profile.Plan{Secrets: &secret.Redactor{}, PluginSessions: map[string]*plugin.Session{"fake": session}, Definitions: map[string]map[string]profile.Definition{"fake": {"web": {Action: "machine", Value: 0}}}}
	e := Executor{Plan: plan, SSH: runner.SSH{Host: "must-not-connect"}}
	err = e.Preview(t.Context(), profile.Step{Op: profile.OpPlugin, Plugin: "fake", Resource: "web"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unavailable") || mutations != 0 {
		t.Fatal(err, mutations)
	}
}

func TestPluginDiffShownBeforeApproval(t *testing.T) {
	mutated := 0
	var out bytes.Buffer
	obj := plugin.NewObject(plugin.ObjectSpec[int, int]{Name: "machine", Attributes: []plugin.Attribute{{Name: "image", Replace: true, Sensitive: true}}, Decode: func(*plugin.Decoder) (int, error) { return 0, nil }, Read: func(context.Context, plugin.Run[int], int) (int, bool, error) { return 1, true, nil }, Create: func(context.Context, plugin.Run[int], int) error { mutated++; return nil }, Delete: func(context.Context, plugin.Run[int], int) error { mutated++; return nil }, Diff: func(int, int) []plugin.Change {
		return []plugin.Change{{Attribute: "image", From: "old-secret", To: "new-secret"}}
	}})
	p := plugin.New(plugin.Spec[int]{Name: "fake", Configure: func(*plugin.Decoder) (int, error) { return 0, nil }, Objects: []plugin.Object[int]{obj}})
	s, _ := p.Configure(plugin.NewDecoder("", nil, nil, nil))
	defer s.Close()
	plan := &profile.Plan{Secrets: &secret.Redactor{}, PluginSessions: map[string]*plugin.Session{"fake": s}, Definitions: map[string]map[string]profile.Definition{"fake": {"web": {Action: "machine", Value: 0}}}}
	approved := false
	e := Executor{Plan: plan, ApprovePlugin: func(context.Context, plugin.Approval) error {
		if !strings.Contains(out.String(), "-/+ replace") || !strings.Contains(out.String(), "forces replacement") || strings.Contains(out.String(), "secret") {
			t.Fatalf("unsafe or missing diff before approval: %s", out.String())
		}
		if mutated != 0 {
			t.Fatal("mutated before approval")
		}
		approved = true
		return nil
	}}
	if err := e.Execute(t.Context(), profile.Step{Op: profile.OpPlugin, Plugin: "fake", Resource: "web"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !approved || mutated != 2 {
		t.Fatal(approved, mutated)
	}
}
