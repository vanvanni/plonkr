package profile

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/quick"

	"github.com/zclconf/go-cty/cty"
)

func TestArgumentConfigurationEdges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "two blocks", source: "arguments {}\narguments {}", want: "one unlabeled"},
		{name: "labeled block", source: `arguments "inputs" {}`, want: "unlabeled"},
		{name: "attribute in block", source: "arguments { typo = true }", want: "Unsupported argument"},
		{name: "unlabeled input", source: "arguments {\nargument { type = string }\n}", want: "label"},
		{name: "missing type", source: "arguments {\nargument \"v\" {}\n}", want: "type"},
		{name: "quoted type", source: "arguments {\nargument \"v\" { type = \"string\" }\n}", want: "type must be"},
		{name: "unknown field", source: "arguments {\nargument \"v\" {\ntype = string\nrequired = true\n}\n}", want: "Unsupported argument"},
		{name: "nonliteral default", source: "arguments {\nargument \"v\" {\ntype = string\ndefault = args.other\n}\n}", want: "Variables may not be used"},
		{name: "null default", source: "arguments {\nargument \"v\" {\ntype = bool\ndefault = null\n}\n}", want: "must be bool"},
		{name: "numeric prompt", source: "arguments {\nargument \"v\" {\ntype = string\nprompt = 3\n}\n}", want: "prompt must be a string"},
		{name: "empty rule", source: "arguments {\nargument \"v\" {\ntype = string\nvalidation {}\n}\n}", want: "at least one rule"},
		{name: "numeric pattern", source: "arguments {\nargument \"v\" {\ntype = string\nvalidation { pattern = 1 }\n}\n}", want: "pattern requires"},
		{name: "null choices", source: "arguments {\nargument \"v\" {\ntype = integer\nvalidation { choices = null }\n}\n}", want: "non-empty list"},
		{name: "fractional choice", source: "arguments {\nargument \"v\" {\ntype = integer\nvalidation { choices = [1, 1.5] }\n}\n}", want: "64-bit integer"},
		{name: "bool choices", source: "arguments {\nargument \"v\" {\ntype = bool\nvalidation { choices = [true, false] }\n}\n}", want: "string or integer"},
		{name: "fractional bound", source: "arguments {\nargument \"v\" {\ntype = integer\nvalidation { min = 1.5 }\n}\n}", want: "integer in range"},
		{name: "overflow bound", source: "arguments {\nargument \"v\" {\ntype = integer\nvalidation { max = 9223372036854775808 }\n}\n}", want: "integer in range"},
		{name: "negative length", source: "arguments {\nargument \"v\" {\ntype = string\nvalidation { min_length = -1 }\n}\n}", want: "integer in range"},
		{name: "reversed lengths", source: "arguments {\nargument \"v\" {\ntype = string\nvalidation {\nmin_length = 3\nmax_length = 2\n}\n}\n}", want: "minimum"},
		{name: "numeric message", source: "arguments {\nargument \"v\" {\ntype = string\nvalidation {\npattern = \"a\"\nmessage = 1\n}\n}\n}", want: "message must be a string"},
		{name: "mixed include forms", source: "include = []\ninclude \"base\" {}", want: "not both"},
		{name: "unlabeled include", source: "include {}", want: "one profile name"},
		{name: "include unknown field", source: "include \"base\" { typo = true }", want: "Unsupported argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Prepare([]byte(tc.source+"\n"+oneTask("Check")), "edge.hcl", t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestArgumentValueBoundaries(t *testing.T) {
	integer := Argument{Name: "n", Type: "integer"}
	for _, value := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
		text := fmt.Sprint(value)
		got, err := integer.ParseValue(text)
		if err != nil || !got.RawEquals(cty.NumberIntVal(value)) {
			t.Fatalf("integer boundary %s: %s, %v", text, got.GoString(), err)
		}
	}
	for _, text := range []string{"-9223372036854775809", "9223372036854775808", "1.0", "1e3", " 1", "1 ", ""} {
		if _, err := integer.ParseValue(text); err == nil {
			t.Errorf("accepted integer text %q", text)
		}
	}
	for _, value := range []cty.Value{
		cty.NullVal(cty.Number), cty.StringVal("1"), cty.BoolVal(true),
		cty.NumberFloatVal(1.5), cty.NumberFloatVal(math.Inf(1)),
	} {
		if err := integer.Validate(value); err == nil {
			t.Errorf("accepted non-integer %s", value.GoString())
		}
	}
	textArgument := Argument{Name: "text", Type: "string"}
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "exact byte limit", value: strings.Repeat("a", maxArgumentSize), valid: true},
		{name: "over byte limit", value: strings.Repeat("a", maxArgumentSize+1)},
		{name: "multibyte byte limit", value: strings.Repeat("界", maxArgumentSize/3+1)},
		{name: "spaces", value: " \t\r\n"},
		{name: "Unicode space", value: "\u2003\u00a0"},
		{name: "embedded NUL", value: "a\x00b"},
		{name: "invalid UTF-8", value: string([]byte{0xff, 'a'})},
		{name: "multiline", value: "one\ntwo", valid: true},
		{name: "shell syntax is data", value: "a'b; $(id) `id` \"x\"", valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := textArgument.ParseValue(tc.value)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestArgumentRuleIntersection(t *testing.T) {
	source := argumentProfile(`argument "n" {
  type = integer
  validation { min = -2 }
  validation { max = 2 }
  validation { choices = [-2, 0, 2] }
}`, `["echo ${args.n}"]`)
	p, err := Prepare([]byte(source), "edge.hcl", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for n := int64(-3); n <= 3; n++ {
		_, err := p.Bind(map[string]cty.Value{"n": cty.NumberIntVal(n)})
		valid := n == -2 || n == 0 || n == 2
		if (err == nil) != valid {
			t.Fatalf("n=%d, error=%v", n, err)
		}
	}
}

func TestArgumentIncludeContractMatrix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		include string
		want    string
	}{
		{name: "literal value", include: `include "base" { arguments = {n = 2} }`},
		{name: "default value", include: `include "base" {}`},
		{name: "empty map", include: `include "base" { arguments = {} }`},
		{name: "legacy default", include: `include = ["base"]`},
		{name: "null map", include: `include "base" { arguments = null }`, want: "must be an object"},
		{name: "list map", include: `include "base" { arguments = [1] }`, want: "must be an object"},
		{name: "unknown input", include: `include "base" { arguments = {typo = 1} }`, want: "unknown argument"},
		{name: "wrong input type", include: `include "base" { arguments = {n = "2"} }`, want: "must be integer"},
		{name: "invalid value", include: `include "base" { arguments = {n = -1} }`, want: "range"},
		{name: "undeclared reference", include: `include "base" { arguments = {n = args.typo} }`, want: "typo"},
		{name: "duplicate blocks", include: "include \"base\" {}\ninclude \"base\" {}", want: "more than once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeFixture(t, base, "base.hcl", argumentProfile("argument \"n\" {\ntype = integer\ndefault = 1\nvalidation { min = 0 }\n}", `["echo ${args.n}"]`))
			p, err := Prepare([]byte(tc.include), "root.hcl", base)
			if err == nil {
				_, err = p.Bind(nil)
			}
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error=%v, want=%q", err, tc.want)
			}
		})
	}
}

func TestParameterizedSnippetDepthAndCycles(t *testing.T) {
	for _, depth := range []int{maxDepth, maxDepth + 1} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			base := t.TempDir()
			var source strings.Builder
			for i := range depth {
				command := `"echo ${args.value}"`
				if i+1 < depth {
					command = fmt.Sprintf(`run("s%d", {value = args.value})`, i+1)
				}
				fmt.Fprintf(&source, "snippet \"s%d\" {\narguments {\nargument \"value\" { type = integer }\n}\ncommands = [%s]\n}\n", i, command)
			}
			writeFixture(t, base, "snippets/chain.hcl", source.String())
			p, err := Prepare([]byte(argumentProfile(`argument "value" { type = integer }`, `[run("s0", {value = args.value})]`)), "edge.hcl", base)
			if depth > maxDepth {
				if err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			plan, err := p.Bind(map[string]cty.Value{"value": cty.NumberIntVal(42)})
			if err != nil || plan.Tasks[0].Steps[0].Text != "echo 42" {
				t.Fatalf("plan=%+v, error=%v", plan, err)
			}
		})
	}
	base := t.TempDir()
	writeFixture(t, base, "snippets/cycle.hcl", `snippet "s" {
  arguments {
    argument "value" { type = integer }
  }
  commands = [run("s", {value = args.value + 1})]
}`)
	_, err := Prepare([]byte(taskSource("Cycle", `order = 0
script = [run("s", {value = 0})]`)), "edge.hcl", base)
	if err == nil || !strings.Contains(err.Error(), "snippet cycle") {
		t.Fatal(err)
	}
}

func TestProgramConcurrentBindingsAreIndependent(t *testing.T) {
	p, err := Prepare([]byte(argumentProfile(`argument "value" { type = integer }`, `["echo ${args.value}"]`)), "edge.hcl", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for n := range 32 {
		workers.Go(func() {
			plan, err := p.Bind(map[string]cty.Value{"value": cty.NumberIntVal(int64(n))})
			if err != nil {
				t.Error(err)
				return
			}
			if got := plan.Tasks[0].Steps[0].Text; got != fmt.Sprintf("echo %d", n) {
				t.Errorf("binding %d produced %q", n, got)
			}
		})
	}
	workers.Wait()
	if _, err := p.Bind(map[string]cty.Value{"value": cty.StringVal("bad")}); err == nil {
		t.Fatal("invalid binding accepted")
	}
	plan, err := p.Bind(map[string]cty.Value{"value": cty.NumberIntVal(99)})
	if err != nil || plan.Tasks[0].Steps[0].Text != "echo 99" {
		t.Fatalf("failed bind poisoned program: %+v, %v", plan, err)
	}
}

func TestParameterizedSnippetExpansionLimit(t *testing.T) {
	base := t.TempDir()
	commands := strings.Repeat(`"echo ${args.n}",`, maxSteps/2)
	writeFixture(t, base, "snippets/large.hcl", "snippet \"large\" {\narguments {\nargument \"n\" { type = integer }\n}\ncommands = ["+commands+"]\n}")
	for _, extra := range []bool{false, true} {
		script := `[run("large", {n = 1}), run("large", {n = 2})`
		if extra {
			script += `, "echo too-many"`
		}
		script += "]"
		p, err := Prepare([]byte(argumentProfile("", script)), "edge.hcl", base)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := p.Bind(nil)
		if extra {
			if err == nil || !strings.Contains(err.Error(), "10000") {
				t.Fatalf("expansion limit was not enforced: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		steps := plan.Tasks[0].Steps
		if len(steps) != maxSteps || steps[0].Text != "echo 1" || steps[maxSteps-1].Text != "echo 2" {
			t.Fatalf("boundary expansion lost its call scopes: %d steps", len(steps))
		}
	}
}

func TestExplicitArgumentsMatchDefaults(t *testing.T) {
	base := t.TempDir()
	property := func(n int64, enabled bool) bool {
		decl := fmt.Sprintf("argument \"n\" {\ntype = integer\ndefault = %d\n}\nargument \"enabled\" {\ntype = bool\ndefault = %t\n}", n, enabled)
		p, err := Prepare([]byte(argumentProfile(decl, `["echo ${args.n} ${args.enabled}"]`)), "edge.hcl", base)
		if err != nil {
			t.Log(err)
			return false
		}
		defaults, err := p.Bind(nil)
		if err != nil {
			t.Log(err)
			return false
		}
		explicit, err := p.Bind(map[string]cty.Value{"n": cty.NumberIntVal(n), "enabled": cty.BoolVal(enabled)})
		return err == nil && reflect.DeepEqual(defaults, explicit)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestUnitArgumentVariablesRequireStrings(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "files/edge.service.tmpl", "[Service]\nExecStart=${exec_start}\n")
	for _, tc := range []struct {
		name     string
		variable string
		valid    bool
	}{
		{name: "bare number", variable: "args.port"},
		{name: "single interpolation keeps number type", variable: `"${args.port}"`},
		{name: "command template is a string", variable: `"/usr/bin/server --port ${args.port}"`, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := argumentProfile("argument \"port\" {\ntype = integer\ndefault = 9001\n}", `["echo ready"]`) +
				"\nunit \"edge.service\" {\nsource = \"edge.service.tmpl\"\nvars = {exec_start = " + tc.variable + "}\n}"
			p, err := Prepare([]byte(source), "edge.hcl", base)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := p.Bind(nil)
			if !tc.valid {
				if err == nil || !strings.Contains(err.Error(), "string value") {
					t.Fatalf("numeric unit var accepted: %v", err)
				}
				return
			}
			if err != nil || !strings.Contains(plan.Units["edge.service"].Content, "ExecStart=/usr/bin/server --port 9001") {
				t.Fatalf("string interpolation failed: %+v, %v", plan, err)
			}
		})
	}
}

func FuzzArgumentForwarding(f *testing.F) {
	base := f.TempDir()
	write := func(name, source string) {
		f.Helper()
		path := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			f.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			f.Fatal(err)
		}
	}
	write("snippets/forward.hcl", `snippet "forward" {
  arguments {
    argument "text" { type = string }
    argument "number" { type = integer }
    argument "enabled" { type = bool }
  }
  commands = [log(args.text), "echo ${args.number} ${args.enabled}"]
}`)
	source := argumentProfile("argument \"text\" { type = string }\nargument \"number\" { type = integer }\nargument \"enabled\" { type = bool }", `[run("forward", {text = args.text, number = args.number, enabled = args.enabled})]`)
	p, err := Prepare([]byte(source), "fuzz.hcl", base)
	if err != nil {
		f.Fatal(err)
	}
	for _, text := range []string{"normal", "a'b; $(id)", "${args.text}", "\x00", "", "界\n二"} {
		f.Add(text, int64(42), true)
	}
	f.Add("minimum", int64(math.MinInt64), false)
	f.Add("maximum", int64(math.MaxInt64), true)
	f.Fuzz(func(t *testing.T, text string, number int64, enabled bool) {
		values := map[string]cty.Value{"text": cty.StringVal(text), "number": cty.NumberIntVal(number), "enabled": cty.BoolVal(enabled)}
		plan, err := p.Bind(values)
		valid := p.Arguments()[0].Validate(values["text"]) == nil
		if !valid {
			if err == nil || plan != nil {
				t.Fatal("invalid text produced an executable plan")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		steps := plan.Tasks[0].Steps
		if len(steps) != 2 || steps[0].Text != values["text"].AsString() ||
			steps[1].Text != fmt.Sprintf("echo %d %t", number, enabled) {
			t.Fatalf("incorrect forwarding: %+v", steps)
		}
		checkPlanInvariants(t, plan)
	})
}
