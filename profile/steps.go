package profile

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// HCL can carry actions returned by functions, but profile expressions cannot
// construct these values directly or disguise a shell command as managed setup.
type action struct {
	step     Step
	flag     string
	children []cty.Value
	snippets []string
}

var (
	actionType    = cty.Capsule("profile action", reflect.TypeFor[action]())
	flagName      = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	setupContext  = phaseContext(PhaseSetup)
	scriptContext = phaseContext(PhaseScript)
)

func phaseContext(phase Phase) *hcl.EvalContext {
	functions := map[string]function.Function{
		"flag": actionFunction(2, true, flagAction),
		"log":  actionFunction(1, true, logAction),
	}
	for _, op := range []Op{OpGroup, OpUser, OpRepo, OpUnit} {
		if phase == PhaseSetup {
			functions[string(op)] = actionFunction(1, false, func(args []cty.Value) (action, error) {
				if !isType(args[0], cty.String) {
					return action{}, fmt.Errorf("%s() requires a string resource name", op)
				}
				return action{step: setupStep(op, args[0].AsString())}, nil
			})
		} else {
			functions[string(op)] = wrongPhase(string(op), PhaseSetup, PhaseScript)
		}
	}
	if phase == PhaseScript {
		functions["run"] = actionFunction(1, true, runAction)
	} else {
		functions["run"] = wrongPhase("run", PhaseScript, PhaseSetup)
	}
	return &hcl.EvalContext{Functions: functions}
}

func actionFunction(required int, variadic bool, build func([]cty.Value) (action, error)) function.Function {
	params := []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType}}
	if required == 2 {
		params = append(params, function.Parameter{Name: "action", Type: cty.DynamicPseudoType})
	}
	returnType := function.StaticReturnType(actionType)
	spec := function.Spec{
		Params: params[:required],
		Type:   returnType,
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			result, err := build(args)
			if err != nil {
				return cty.NilVal, err
			}
			return cty.CapsuleVal(actionType, &result), nil
		},
	}
	if variadic {
		spec.VarParam = &function.Parameter{Name: "more", Type: cty.DynamicPseudoType, AllowNull: true}
	}
	return function.New(&spec)
}

func wrongPhase(name string, allowed, actual Phase) function.Function {
	return actionFunction(0, true, func([]cty.Value) (action, error) {
		return action{}, fmt.Errorf("%s() is only allowed in %s, not %s", name, allowed, actual)
	})
}

func flagAction(args []cty.Value) (action, error) {
	if !isType(args[0], cty.String) {
		return action{}, fmt.Errorf("flag name must be a string")
	}
	name := args[0].AsString()
	if !flagName.MatchString(name) {
		return action{}, fmt.Errorf("invalid flag name %q", name)
	}
	switch name {
	case "help", "tty", "dry-run", "no-tui", "profiles-dir", "bypass", "version":
		return action{}, fmt.Errorf("reserved flag name %q", name)
	}
	return action{flag: name, children: slices.Clone(args[1:])}, nil
}

func logAction(args []cty.Value) (action, error) {
	if len(args) > 2 || !isType(args[0], cty.String) || (len(args) == 2 && !isType(args[1], cty.String)) {
		return action{}, fmt.Errorf("log() requires a string message and one optional string color")
	}
	color := ""
	if len(args) == 2 {
		color = args[1].AsString()
	}
	switch color {
	case "", "default", "black", "red", "green", "yellow", "blue", "magenta", "cyan", "white", "gray":
	default:
		return action{}, fmt.Errorf("unsupported log color %q", color)
	}
	return action{step: Step{Op: OpLog, Text: args[0].AsString(), Color: color}}, nil
}

func runAction(args []cty.Value) (action, error) {
	names := make([]string, 0, len(args))
	for _, arg := range args {
		if !isType(arg, cty.String) || strings.TrimSpace(arg.AsString()) == "" {
			return action{}, fmt.Errorf("run() requires non-empty string snippet names")
		}
		names = append(names, arg.AsString())
	}
	return action{snippets: names}, nil
}

func setupStep(op Op, name string) Step {
	return Step{Phase: PhaseSetup, Op: op, Text: string(op) + " " + name, Resource: name}
}

func decodeSetupSteps(value cty.Value, plan *Plan, limit int) ([]Step, error) {
	decoder := stepDecoder{phase: PhaseSetup, plan: plan, limit: limit}
	err := decoder.decodeList(value)
	return decoder.steps, err
}

func (l *profileLoader) decodeScriptSteps(value cty.Value, stack []string, limit int) ([]Step, int, error) {
	decoder := stepDecoder{phase: PhaseScript, loader: l, stack: stack, limit: limit}
	err := decoder.decodeList(value)
	return decoder.steps, decoder.depth, err
}

type stepDecoder struct {
	phase  Phase
	plan   *Plan
	loader *profileLoader
	stack  []string
	limit  int
	depth  int
	steps  []Step
}

func (d *stepDecoder) decodeList(value cty.Value) error {
	if !isSequence(value) {
		return fmt.Errorf("expected a list or tuple of steps")
	}
	for it := value.ElementIterator(); it.Next(); {
		_, item := it.Element()
		if err := d.decode(item, nil); err != nil {
			return err
		}
	}
	return nil
}

func (d *stepDecoder) decode(value cty.Value, flags []string) error {
	if isType(value, cty.String) {
		if d.phase != PhaseScript {
			return fmt.Errorf("raw shell strings are only allowed in script, not setup")
		}
		text := value.AsString()
		if strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) {
			return fmt.Errorf("shell command must be non-empty and contain no NUL bytes")
		}
		return d.append(Step{Text: text}, flags)
	}
	if !isType(value, actionType) {
		return fmt.Errorf("invalid %s step", d.phase)
	}
	item := value.EncapsulatedValue().(*action)
	switch {
	case item.flag != "":
		if !slices.Contains(flags, item.flag) {
			flags = append(slices.Clone(flags), item.flag)
		}
		for _, child := range item.children {
			if err := d.decode(child, flags); err != nil {
				return err
			}
		}
		return nil
	case len(item.snippets) != 0:
		if d.phase != PhaseScript {
			return fmt.Errorf("run() is only allowed in script, not setup")
		}
		for _, name := range item.snippets {
			snippet, err := d.loader.expandSnippet(name, d.stack)
			if err != nil {
				return err
			}
			d.depth = max(d.depth, snippet.depth)
			for _, step := range snippet.steps {
				if err := d.append(step, flags); err != nil {
					return err
				}
			}
		}
		return nil
	case item.step.Op == OpLog:
		return d.append(item.step, flags)
	default:
		if d.phase != PhaseSetup {
			return fmt.Errorf("%s() is only allowed in setup, not script", item.step.Op)
		}
		step, err := resolveSetupStep(d.plan, item.step)
		if err != nil {
			return err
		}
		return d.append(step, flags)
	}
}

func (d *stepDecoder) append(step Step, flags []string) error {
	if len(d.steps) >= d.limit {
		return fmt.Errorf("profile exceeds %d steps", maxSteps)
	}
	step.Phase = d.phase
	required := slices.Clone(flags)
	for _, flag := range step.Flags {
		if !slices.Contains(required, flag) {
			required = append(required, flag)
		}
	}
	step.Flags = required
	d.steps = append(d.steps, step)
	return nil
}

func resolveSetupStep(plan *Plan, step Step) (Step, error) {
	var name string
	id := step.Resource
	switch step.Op {
	case OpGroup:
		name = plan.Groups[id].Name
	case OpUser:
		name = plan.Users[id].Name
	case OpRepo:
		name = plan.Repositories[id].Name
	case OpUnit:
		name = plan.Units[id].Name
	}
	if name == "" {
		return Step{}, fmt.Errorf("unknown %s %q", step.Op, id)
	}
	step.Text = string(step.Op) + " " + name
	return step, nil
}
