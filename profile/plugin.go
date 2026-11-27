package profile

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/vanvanni/plonkr/plugin"
	"github.com/vanvanni/plonkr/secret"
	"github.com/zclconf/go-cty/cty"
)

// Definition is a bound plugin call. Value never appears in plans or journals.
type Definition struct {
	Action string
	Absent bool
	Value  any
	Range  hcl.Range
}
type pluginBlock struct {
	block  *hclsyntax.Block
	file   string
	action plugin.ActionInfo
	absent bool
}

func (p *Program) phase(phase Phase) *hcl.EvalContext {
	base := phaseContext(phase)
	base.Functions = maps.Clone(base.Functions)
	for _, name := range p.plugins.Names() {
		if phase != PhaseSetup {
			base.Functions[name] = wrongPhase(name, PhaseSetup, phase)
			continue
		}
		base.Functions[name] = actionFunction(1, true, func(values []cty.Value) (action, error) {
			children := []cty.Value{}
			for _, v := range values {
				if !isType(v, cty.String) || !plugin.Label.MatchString(v.AsString()) {
					return action{}, fmt.Errorf("%s requires definition names", name)
				}
				a := action{step: Step{Op: OpPlugin, Plugin: name, Resource: v.AsString(), Text: name + " " + v.AsString()}}
				children = append(children, cty.CapsuleVal(actionType, &a))
			}
			return action{children: children}, nil
		})
	}
	return base
}

func (p *Program) checkPlugins() error {
	loaded := map[string]bool{}
	p.definitions = map[string]map[string]pluginBlock{}
	for _, file := range slices.Sorted(maps.Keys(p.documents)) {
		doc := p.documents[file]
		for _, b := range doc.pluginBlocks {
			name := b.Type
			settings := name == "plugin"
			if len(b.Labels) != 1 {
				return fmt.Errorf("%s: plugin blocks need one label", b.DefRange())
			}
			if settings {
				name = b.Labels[0]
			}
			spec, ok := p.plugins.Get(name)
			if !ok {
				return fmt.Errorf("%s: unknown plugin %q (compiled in: %v)", b.DefRange(), name, p.plugins.Names())
			}
			attrs := spec.Settings
			entry := pluginBlock{block: b, file: file}
			if settings {
				loaded[name] = true
			} else {
				if !plugin.Label.MatchString(b.Labels[0]) {
					return fmt.Errorf("%s: invalid definition name", b.DefRange())
				}
				action, err := staticPluginString(b, "action", "")
				if err != nil {
					return err
				}
				info, ok := spec.Action(action)
				if !ok {
					return fmt.Errorf("%s: unknown %s action %q (valid: %v)", b.DefRange(), name, action, spec.ActionNames())
				}
				entry.action = info
				attrs = info.Attributes
				ensure, err := staticPluginString(b, "ensure", "present")
				if err != nil {
					return err
				}
				if (b.Body.Attributes["ensure"] != nil && !info.Object) || (ensure != "present" && ensure != "absent") {
					return fmt.Errorf("%s: ensure must be present or absent on an object", b.DefRange())
				}
				entry.absent = ensure == "absent"
				if p.definitions[name] == nil {
					p.definitions[name] = map[string]pluginBlock{}
				}
				if previous, exists := p.definitions[name][b.Labels[0]]; exists {
					return fmt.Errorf("%s: duplicate %s definition %q (first at %s)", b.DefRange(), name, b.Labels[0], previous.block.DefRange())
				}
				p.definitions[name][b.Labels[0]] = entry
			}
			schema := hcl.BodySchema{}
			for _, a := range attrs {
				schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: a.Name, Required: a.Required && !settings})
			}
			if !settings {
				schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: "action", Required: true}, hcl.AttributeSchema{Name: "ensure"})
			}
			if _, diags := b.Body.Content(&schema); diags.HasErrors() {
				return fmt.Errorf("%s: invalid %s attribute schema", b.DefRange(), name)
			}
			for _, a := range attrs {
				attr := b.Body.Attributes[a.Name]
				if attr == nil {
					continue
				}
				if a.Static {
					if _, diags := attr.Expr.Value(nil); diags.HasErrors() {
						return fmt.Errorf("%s: %s must be static", attr.Range(), a.Name)
					}
				}
				invalidCall := false
				hclsyntax.VisitAll(attr.Expr, func(n hclsyntax.Node) hcl.Diagnostics {
					if call, ok := n.(*hclsyntax.FunctionCallExpr); ok && call.Name != "shellquote" {
						invalidCall = true
					}
					return nil
				})
				if invalidCall {
					return fmt.Errorf("%s: plugin attributes permit only shellquote()", attr.Range())
				}
				if err := p.checkExpression(nil, attr.Expr, unknownArguments(doc.arguments), nil); err != nil {
					return fmt.Errorf("%s: invalid plugin attribute %s", attr.Range(), a.Name)
				}
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(p.definitions)) {
		if !loaded[name] {
			entry := p.definitions[name][slices.Sorted(maps.Keys(p.definitions[name]))[0]]
			return fmt.Errorf("%s: %s definitions need plugin %q loaded with plugin %q { … }", entry.block.DefRange(), name, name, name)
		}
	}
	for _, file := range slices.Sorted(maps.Keys(p.documents)) {
		var callErr error
		hclsyntax.VisitAll(p.documents[file].stripped(), func(n hclsyntax.Node) hcl.Diagnostics {
			if call, ok := n.(*hclsyntax.FunctionCallExpr); ok {
				if _, known := p.plugins.Get(call.Name); known && !loaded[call.Name] && callErr == nil {
					callErr = fmt.Errorf("%s: call needs plugin %q loaded with plugin %q { … }", call.Range(), call.Name, call.Name)
				}
			}
			return nil
		})
		if callErr != nil {
			return callErr
		}
	}
	return nil
}
func staticPluginString(b *hclsyntax.Block, name, fallback string) (string, error) {
	a := b.Body.Attributes[name]
	if a == nil {
		if name == "action" {
			return "", fmt.Errorf("%s: action is required", b.DefRange())
		}
		return fallback, nil
	}
	v, diags := a.Expr.Value(nil)
	if diags.HasErrors() || !isType(v, cty.String) {
		return "", fmt.Errorf("%s: %s must be a static string", a.Range(), name)
	}
	return v.AsString(), nil
}

func (p *Program) bindPlugins(plan *Plan, bindings map[string]cty.Value) error {
	plan.Definitions = map[string]map[string]Definition{}
	plan.PluginSessions = map[string]*plugin.Session{}
	plan.Secrets = &secret.Redactor{}
	fail := true
	defer func() {
		if fail {
			plan.ClosePlugins()
		}
	}()
	// Settings are merged after every document's effective arguments are known.
	settings := map[string]map[string]cty.Value{}
	for _, file := range slices.Sorted(maps.Keys(p.documents)) {
		for _, b := range p.documents[file].pluginBlocks {
			if b.Type != "plugin" {
				continue
			}
			name := b.Labels[0]
			if settings[name] == nil {
				settings[name] = map[string]cty.Value{}
			}
			spec, _ := p.plugins.Get(name)
			for n, a := range b.Body.Attributes {
				v, diags := a.Expr.Value(argumentContext(bindings[file].AsValueMap()))
				if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() {
					return fmt.Errorf("%s: invalid plugin %s setting %s", a.Range(), name, n)
				}
				if prev, ok := settings[name][n]; ok && !prev.RawEquals(v) {
					return fmt.Errorf("%s: conflicting plugin %s setting %s", a.Range(), name, n)
				}
				settings[name][n] = v
				registerSecret(plan.Secrets, spec.Settings, n, v)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(settings)) {
		spec, _ := p.plugins.Get(name)
		for _, a := range spec.Settings {
			if _, ok := settings[name][a.Name]; a.Required && !ok {
				return fmt.Errorf("plugin %s setting %s is required", name, a.Name)
			}
		}
		session, err := spec.Configure(plugin.NewDecoder(name, settings[name], spec.Settings, plan.Secrets))
		if err != nil {
			return fmt.Errorf("plugin %s Configure: %s", name, plan.Secrets.Text(err.Error()))
		}
		plan.PluginSessions[name] = session
	}
	for ti := range plan.Tasks {
		for si := range plan.Tasks[ti].Steps {
			step := &plan.Tasks[ti].Steps[si]
			if step.Op != OpPlugin {
				continue
			}
			entry, ok := p.definitions[step.Plugin][step.Resource]
			if !ok {
				return fmt.Errorf("unknown %s definition %q (valid: %v)", step.Plugin, step.Resource, slices.Sorted(maps.Keys(p.definitions[step.Plugin])))
			}
			if plan.PluginSessions[step.Plugin] == nil {
				return fmt.Errorf("add plugin %q { … } to load this plugin", step.Plugin)
			}
			if plan.Definitions[step.Plugin] == nil {
				plan.Definitions[step.Plugin] = map[string]Definition{}
			}
			step.Text = step.Plugin + " " + step.Resource + " (" + entry.action.Name + ")"
			if _, ok := plan.Definitions[step.Plugin][step.Resource]; ok {
				continue
			}
			values := map[string]cty.Value{}
			for n, a := range entry.block.Body.Attributes {
				if n == "action" || n == "ensure" {
					continue
				}
				v, diags := a.Expr.Value(argumentContext(bindings[entry.file].AsValueMap()))
				if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() {
					return fmt.Errorf("%s: invalid plugin attribute %s", a.Range(), n)
				}
				values[n] = v
				registerSecret(plan.Secrets, entry.action.Attributes, n, v)
			}
			v, err := entry.action.Decode(plugin.NewDecoder(step.Resource, values, entry.action.Attributes, plan.Secrets))
			if err != nil {
				return fmt.Errorf("%s: Decode: %s", entry.block.DefRange(), plan.Secrets.Text(err.Error()))
			}
			plan.Definitions[step.Plugin][step.Resource] = Definition{entry.action.Name, entry.absent, v, entry.block.DefRange()}
		}
	}
	fail = false
	return nil
}
func registerSecret(r *secret.Redactor, attrs []plugin.Attribute, name string, v cty.Value) {
	for _, a := range attrs {
		if a.Name == name && a.Sensitive && isType(v, cty.String) {
			r.Add(secret.New(v.AsString()))
		}
	}
}
func (p *Plan) ClosePlugins() error {
	if p == nil {
		return nil
	}
	err := p.CloseConnections()
	p.Secrets.Clear()
	return err
}

// CloseConnections leaves redaction active until final summaries are written.
func (p *Plan) CloseConnections() error {
	if p == nil {
		return nil
	}
	var err error
	for _, n := range slices.Sorted(maps.Keys(p.PluginSessions)) {
		err = errors.Join(err, p.PluginSessions[n].Close())
	}
	if err != nil {
		err = errors.New(p.Secrets.Text(err.Error()))
	}
	return err
}

// NeedsTarget includes disabled work: adding flags later must not change grammar.
func (p *Plan) NeedsTarget() bool {
	for _, t := range p.Tasks {
		if !t.Lock.IsZero() {
			return true
		}
		for _, s := range t.Steps {
			if s.Op != OpPlugin && s.Op != OpLog && s.Op != OpWait {
				return true
			}
		}
	}
	return false
}
