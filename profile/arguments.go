package profile

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

const maxArgumentSize = 64 << 10

// Argument declares a typed input. A nil Default means the caller must supply it.
type Argument struct {
	Name    string
	Type    string
	Prompt  string
	Default *cty.Value
	rules   []argumentRule
}

type argumentRule struct {
	pattern              *regexp.Regexp
	choices              []cty.Value
	min, max             *int64
	minLength, maxLength *int64
	message              string
}

func (a Argument) valueType() cty.Type {
	switch a.Type {
	case "integer":
		return cty.Number
	case "bool":
		return cty.Bool
	default:
		return cty.String
	}
}

// ParseValue parses a CLI or prompt answer without coercing configuration values.
func (a Argument) ParseValue(text string) (cty.Value, error) {
	value := cty.StringVal(text)
	switch a.Type {
	case "integer":
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return cty.NilVal, fmt.Errorf("argument %q must be a signed 64-bit integer", a.Name)
		}
		value = cty.NumberIntVal(n)
	case "bool":
		if text != "true" && text != "false" {
			return cty.NilVal, fmt.Errorf("argument %q must be true or false", a.Name)
		}
		value = cty.BoolVal(text == "true")
	}
	return value, a.Validate(value)
}

// Validate checks a supplied value against this declaration, including its rules.
// Unknown typed values are used only during structural preflight.
func (a Argument) Validate(value cty.Value) error {
	if value.IsNull() || !value.Type().Equals(a.valueType()) {
		return fmt.Errorf("argument %q must be %s", a.Name, a.Type)
	}
	if !value.IsKnown() {
		return nil
	}
	var number int64
	var text string
	switch a.Type {
	case "integer":
		var accuracy big.Accuracy
		number, accuracy = value.AsBigFloat().Int64()
		if accuracy != big.Exact {
			return fmt.Errorf("argument %q must be a signed 64-bit integer", a.Name)
		}
	case "string":
		text = value.AsString()
		if strings.TrimSpace(text) == "" || len(text) > maxArgumentSize ||
			!utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return fmt.Errorf("argument %q must be non-blank UTF-8 text, at most 64 KiB, without NUL bytes", a.Name)
		}
	}
	for _, rule := range a.rules {
		message := "does not satisfy its validation rules"
		valid := true
		if rule.pattern != nil && !rule.pattern.MatchString(text) {
			valid, message = false, "does not match pattern "+rule.pattern.String()
		}
		if len(rule.choices) > 0 && !slices.ContainsFunc(rule.choices, value.RawEquals) {
			valid, message = false, "must be one of the declared choices"
		}
		if rule.min != nil && number < *rule.min || rule.max != nil && number > *rule.max {
			valid, message = false, "is outside the allowed range"
		}
		length := int64(utf8.RuneCountInString(text))
		if rule.minLength != nil && length < *rule.minLength || rule.maxLength != nil && length > *rule.maxLength {
			valid, message = false, "is outside the allowed length range"
		}
		if !valid {
			if rule.message != "" {
				message = rule.message
			}
			return fmt.Errorf("argument %q: %s", a.Name, message)
		}
	}
	return nil
}

func decodeArguments(body *hclsyntax.Body, snippet bool) ([]Argument, error) {
	result := []Argument{}
	seen := map[string]bool{}
	var found bool
	for _, block := range body.Blocks {
		if block.Type != "arguments" {
			continue
		}
		if found || len(block.Labels) != 0 {
			return nil, fmt.Errorf("%s: expected one unlabeled arguments block", block.DefRange())
		}
		found = true
		content, diags := block.Body.Content(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{{Type: "argument", LabelNames: []string{"name"}}},
		})
		if diags.HasErrors() {
			return nil, diags
		}
		for _, decl := range content.Blocks {
			name := decl.Labels[0]
			if !argumentName.MatchString(name) || seen[name] {
				return nil, fmt.Errorf("%s: invalid or duplicate argument %q", decl.DefRange, name)
			}
			seen[name] = true
			fields, diags := decl.Body.Content(&hcl.BodySchema{
				Attributes: []hcl.AttributeSchema{{Name: "type", Required: true}, {Name: "prompt"}, {Name: "default"}},
				Blocks:     []hcl.BlockHeaderSchema{{Type: "validation"}},
			})
			if diags.HasErrors() {
				return nil, diags
			}
			typeName := hcl.ExprAsKeyword(fields.Attributes["type"].Expr)
			if !slices.Contains([]string{"string", "integer", "bool"}, typeName) {
				return nil, fmt.Errorf("%s: argument %q type must be string, integer or bool", decl.DefRange, name)
			}
			a := Argument{Name: name, Type: typeName, Prompt: name, rules: []argumentRule{}}
			literals, err := evaluateLiterals(fields.Attributes, "prompt", "default")
			if err != nil {
				return nil, err
			}
			a.Prompt = literals.optionalString("prompt", name)
			if literals.err != nil {
				return nil, literals.err
			}
			if snippet && len(fields.Attributes) != 1 {
				return nil, fmt.Errorf("%s: snippet arguments cannot have prompt or default", decl.DefRange)
			}
			for _, validation := range fields.Blocks {
				rule, err := decodeArgumentRule(validation, a)
				if err != nil {
					return nil, fmt.Errorf("%s: argument %q: %w", validation.DefRange, name, err)
				}
				a.rules = append(a.rules, rule)
			}
			if value, ok := literals.values["default"]; ok {
				if err := a.Validate(value); err != nil {
					return nil, fmt.Errorf("%s: default: %w", decl.DefRange, err)
				}
				a.Default = &value
			}
			result = append(result, a)
		}
	}
	return result, nil
}

var argumentName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func decodeArgumentRule(block *hcl.Block, a Argument) (argumentRule, error) {
	f, err := literalFields(block.Body, "pattern", "choices", "min", "max", "min_length", "max_length", "message")
	if err != nil {
		return argumentRule{}, err
	}
	r := argumentRule{message: f.optionalString("message", ""), choices: []cty.Value{}}
	if pattern, ok := f.values["pattern"]; ok {
		if a.Type != "string" || !isType(pattern, cty.String) {
			return r, fmt.Errorf("pattern requires a string argument and pattern")
		}
		r.pattern, err = regexp.Compile(pattern.AsString())
		if err != nil {
			return r, fmt.Errorf("invalid pattern: %w", err)
		}
	}
	if choices, ok := f.values["choices"]; ok {
		if a.Type == "bool" || !isSequence(choices) || choices.LengthInt() == 0 {
			return r, fmt.Errorf("choices requires a non-empty list for a string or integer argument")
		}
		r.choices = choices.AsValueSlice()
		for _, choice := range r.choices {
			plain := a
			plain.rules = nil
			if err := plain.Validate(choice); err != nil {
				return r, fmt.Errorf("choices: %w", err)
			}
		}
	}
	for _, item := range []struct {
		name   string
		target **int64
		kind   string
	}{
		{"min", &r.min, "integer"}, {"max", &r.max, "integer"},
		{"min_length", &r.minLength, "string"}, {"max_length", &r.maxLength, "string"},
	} {
		if value, ok := f.values[item.name]; ok {
			if a.Type != item.kind || !isType(value, cty.Number) {
				return r, fmt.Errorf("%s is invalid for %s", item.name, a.Type)
			}
			n, accuracy := value.AsBigFloat().Int64()
			if accuracy != big.Exact || item.kind == "string" && n < 0 {
				return r, fmt.Errorf("%s must be an integer in range", item.name)
			}
			*item.target = &n
		}
	}
	if r.min != nil && r.max != nil && *r.min > *r.max ||
		r.minLength != nil && r.maxLength != nil && *r.minLength > *r.maxLength {
		return r, fmt.Errorf("minimum must not exceed maximum")
	}
	_, messageOnly := f.values["message"]
	if len(f.values) == 0 || len(f.values) == 1 && messageOnly {
		return r, fmt.Errorf("validation requires at least one rule")
	}
	return r, f.err
}

func argumentContext(values map[string]cty.Value) *hcl.EvalContext {
	return &hcl.EvalContext{
		Variables: map[string]cty.Value{"args": cty.ObjectVal(values)},
		Functions: map[string]function.Function{"shellquote": function.New(&function.Spec{
			Params: []function.Parameter{{Name: "value", Type: cty.String}},
			Type:   function.StaticReturnType(cty.String),
			Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
				text := args[0].AsString()
				if strings.ContainsRune(text, 0) {
					return cty.NilVal, fmt.Errorf("shellquote cannot quote NUL bytes")
				}
				return cty.StringVal("'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'"), nil
			},
		})},
	}
}

func bindArguments(declarations []Argument, supplied map[string]cty.Value, defaults bool) (map[string]cty.Value, error) {
	values := map[string]cty.Value{}
	for _, a := range declarations {
		value, ok := supplied[a.Name]
		if !ok && defaults && a.Default != nil {
			value, ok = *a.Default, true
		}
		if !ok {
			return nil, fmt.Errorf("requires argument %q", a.Name)
		}
		if err := a.Validate(value); err != nil {
			return nil, err
		}
		values[a.Name] = value
	}
	for name := range supplied {
		if _, ok := values[name]; !ok {
			return nil, fmt.Errorf("unknown argument %q", name)
		}
	}
	return values, nil
}
