// Package plugintest supplies offline helpers for first-party plugin tests.
package plugintest

import (
	"context"
	"fmt"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/vanvanni/plonkr/plugin"
	"github.com/vanvanni/plonkr/secret"
	"github.com/zclconf/go-cty/cty"
	"io"
)

// Decode uses HCL expressions with the same strict decoder as the host.
func Decode(source, label string, attrs []plugin.Attribute, ctx *hcl.EvalContext, redactor *secret.Redactor) (*plugin.Decoder, error) {
	file, diags := hclsyntax.ParseConfig([]byte(source), "plugin-test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	schema := &hcl.BodySchema{}
	for _, a := range attrs {
		schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: a.Name, Required: a.Required})
	}
	content, diags := file.Body.Content(schema)
	if diags.HasErrors() {
		return nil, diags
	}
	values := map[string]cty.Value{}
	for n, a := range content.Attributes {
		v, d := a.Expr.Value(ctx)
		if d.HasErrors() {
			return nil, fmt.Errorf("invalid attribute %s", n)
		}
		values[n] = v
	}
	return plugin.NewDecoder(label, values, attrs, redactor), nil
}

type Target struct {
	Scripts []string
	Inputs  []string
	Result  []byte
	Err     error
}

func (t *Target) Run(_ context.Context, script string, in io.Reader) error {
	t.Scripts = append(t.Scripts, script)
	if in != nil {
		b, err := io.ReadAll(in)
		if err != nil {
			return err
		}
		t.Inputs = append(t.Inputs, string(b))
	}
	return t.Err
}
func (t *Target) Output(ctx context.Context, script string, limit int) ([]byte, error) {
	if err := t.Run(ctx, script, nil); err != nil {
		return nil, err
	}
	if len(t.Result) > limit {
		return nil, fmt.Errorf("output exceeds limit")
	}
	return t.Result, nil
}

type Approver struct {
	Calls []plugin.Approval
	Err   error
}

func (a *Approver) Approve(_ context.Context, v plugin.Approval) error {
	a.Calls = append(a.Calls, v)
	return a.Err
}
