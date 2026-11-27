package provision

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/vanvanni/plonkr/plugin"
	"github.com/vanvanni/plonkr/profile"
)

// Plugin steps buffer output until the call finishes so split secret writes cannot
// cross the journal/redaction boundary. A bounded writer prevents API output from
// exhausting the controller's memory.
type pluginOutput struct{ bytes.Buffer }

// Preview permits plugin reads, with an unavailable SSH target and no mutations.
func (e Executor) Preview(ctx context.Context, s profile.Step, out, errOut io.Writer) error {
	if s.Op != profile.OpPlugin {
		return errors.New("preview accepts plugin steps only")
	}
	e.DryRun = true
	return e.Execute(ctx, s, out, errOut)
}
func (e Executor) StepReason(s profile.Step) string {
	if s.Op == profile.OpPlugin {
		return e.Plan.PluginSessions[s.Plugin].Reason()
	}
	return ""
}

func (w *pluginOutput) Write(p []byte) (int, error) {
	if len(p) > 1<<20-w.Len() {
		return 0, errors.New("plugin output exceeds 1 MiB")
	}
	return w.Buffer.Write(p)
}
func (e Executor) executePlugin(ctx context.Context, s profile.Step, out io.Writer) error {
	session := e.Plan.PluginSessions[s.Plugin]
	d, ok := e.Plan.Definitions[s.Plugin][s.Resource]
	if session == nil || !ok {
		return errors.New("unknown plugin definition")
	}
	var buffer pluginOutput
	defer func() { clear(buffer.Bytes()) }()
	var target plugin.Target = pluginTarget{e: e, out: &buffer}
	if e.DryRun || e.SSH.Host == "" {
		target = unavailableTarget{}
	}
	approve := func(ctx context.Context, a plugin.Approval) error {
		if e.DryRun {
			return errors.New("dry-run cannot request destructive approval")
		}
		// Show the sanitized change list before requesting destructive approval.
		if _, err := io.WriteString(out, e.Plan.Secrets.Text(buffer.String())); err != nil {
			return err
		}
		clear(buffer.Bytes())
		buffer.Reset()
		if e.ApprovePlugin == nil {
			return errors.New("destructive plugin action requires approval")
		}
		return e.ApprovePlugin(ctx, a)
	}
	err := session.Execute(ctx, d.Action, d.Value, d.Absent, e.DryRun, &buffer, target, approve, plugin.Approval{Plugin: s.Plugin, Definition: s.Resource})
	_, writeErr := io.WriteString(out, e.Plan.Secrets.Text(buffer.String()))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		err = errors.New(e.Plan.Secrets.Text(err.Error()))
	}
	return errors.Join(err, writeErr)
}

type pluginTarget struct {
	e   Executor
	out io.Writer
}

func (t pluginTarget) Run(ctx context.Context, script string, in io.Reader) error {
	return t.e.SSH.ExecuteInput(ctx, script, in, t.out, t.out)
}
func (t pluginTarget) Output(ctx context.Context, script string, limit int) ([]byte, error) {
	if limit <= 0 || limit > 1<<20 {
		return nil, errors.New("invalid plugin target output limit")
	}
	var b pluginOutput
	err := t.e.SSH.ExecuteInput(ctx, script, nil, &b, t.out)
	if b.Len() > limit {
		return nil, errors.New("plugin target output exceeds limit")
	}
	return b.Bytes(), err
}

type unavailableTarget struct{}

func (unavailableTarget) Run(context.Context, string, io.Reader) error {
	return errors.New("plugin target SSH unavailable: provide HOST; dry-run never uses SSH")
}
func (unavailableTarget) Output(context.Context, string, int) ([]byte, error) {
	return nil, errors.New("plugin target SSH unavailable: provide HOST; dry-run never uses SSH")
}
