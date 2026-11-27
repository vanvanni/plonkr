package profile

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func argumentProfile(declarations, script string) string {
	return "arguments {\n" + declarations + "\n}\n" + taskSource("Arguments", "order = 1\nscript = "+script)
}

func TestArgumentBindingAndCallIsolation(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "snippets/app.hcl", `snippet "outer" {
  arguments {
    argument "name" { type = string }
    argument "port" { type = integer }
  }
  commands = [run("inner", { name = args.name, port = args.port })]
}
snippet "inner" {
  arguments {
    argument "name" { type = string }
    argument "port" {
      type = integer
      validation {
        min = 1024
        max = 65535
      }
    }
  }
  commands = ["printf '%s:%s' ${shellquote(args.name)} ${args.port}"]
}`)
	source := argumentProfile(`argument "app" { type = string }
argument "port" {
  type = integer
  default = 8080
}`, `[run("outer", {name = args.app, port = args.port}), run("outer", {name = "second", port = 9000})]`)
	p, err := Prepare([]byte(source), "app.hcl", base)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first's app; $(id)", "third"} {
		plan, err := p.Bind(map[string]cty.Value{"app": cty.StringVal(name)})
		if err != nil {
			t.Fatal(err)
		}
		steps := plan.Tasks[0].Steps
		if len(steps) != 2 || !strings.Contains(steps[0].Text, "8080") || !strings.Contains(steps[1].Text, "'second' 9000") {
			t.Fatalf("incorrect invocation binding: %+v", steps)
		}
		if name == "first's app; $(id)" && !strings.Contains(steps[0].Text, `'first'"'"'s app; $(id)'`) {
			t.Fatal(steps[0].Text)
		}
	}
	if _, err := p.Bind(map[string]cty.Value{"app": cty.StringVal("ok"), "port": cty.NumberIntVal(80)}); err == nil {
		t.Fatal("nested snippet validation must reject an invalid forwarded value")
	}
}

func TestArgumentContractsBeforeAnswers(t *testing.T) {
	for _, tc := range []struct{ name, declarations, script, want string }{
		{"missing", `argument "app" { type = string }`, `[run("s")]`, `requires argument "value"`},
		{"unknown", `argument "app" { type = string }`, `[run("s", {value = args.app, typo = "x"})]`, `unknown argument "typo"`},
		{"wrong type", `argument "app" { type = integer }`, `[run("s", {value = args.app})]`, `must be string`},
		{"undeclared", `argument "app" { type = string }`, `[run("s", {value = args.typo})]`, `typo`},
		{"not implicit", `argument "value" { type = string }`, `[run("s")]`, `requires argument "value"`},
		{"disabled", `argument "app" { type = string }`, `[flag("optional", run("s"))]`, `requires argument "value"`},
		{"dynamic name", `argument "app" { type = string }`, `[run(args.app)]`, `literal`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeFixture(t, base, "snippets/a.hcl", `snippet "s" {
  arguments {
    argument "value" { type = string }
  }
  commands = ["echo ${shellquote(args.value)}"]
}`)
			_, err := Prepare([]byte(argumentProfile(tc.declarations, tc.script)), "test.hcl", base)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestArgumentDeclarationsAndRules(t *testing.T) {
	for _, tc := range []struct{ name, decl, want string }{
		{"duplicate", "argument \"a\" { type = string }\nargument \"a\" { type = string }", "duplicate"},
		{"name", `argument "Bad-name" { type = string }`, "invalid"},
		{"type", `argument "a" { type = number }`, "type must be"},
		{"default type", "argument \"a\" {\ntype = integer\ndefault = \"3\"\n}", "must be integer"},
		{"default rule", "argument \"a\" {\ntype = integer\ndefault = 3\nvalidation { min = 4 }\n}", "range"},
		{"pattern", "argument \"a\" {\ntype = string\nvalidation { pattern = \"[\" }\n}", "invalid pattern"},
		{"irrelevant rule", "argument \"a\" {\ntype = bool\nvalidation { min = 3 }\n}", "invalid for bool"},
		{"reversed range", "argument \"a\" {\ntype = integer\nvalidation {\nmin = 4\nmax = 3\n}\n}", "minimum"},
		{"empty choices", "argument \"a\" {\ntype = string\nvalidation { choices = [] }\n}", "non-empty"},
		{"mixed choices", "argument \"a\" {\ntype = string\nvalidation { choices = [\"x\", 2] }\n}", "must be string"},
		{"unknown rule", "argument \"a\" {\ntype = string\nvalidation { typo = 1 }\n}", "Unsupported argument"},
		{"message alone", "argument \"a\" {\ntype = string\nvalidation { message = \"no\" }\n}", "at least one rule"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Prepare([]byte(argumentProfile(tc.decl, `["true"]`)), "x.hcl", t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	source := argumentProfile(`argument "text" {
  type = string
  validation {
    min_length = 2
    max_length = 3
    message = "Use two or three characters."
  }
  validation { choices = ["éé", "abc"] }
}
argument "enabled" { type = bool }
argument "port" {
  type = integer
  validation { choices = [8080, 9000] }
}`, `["echo ${shellquote(args.text)} ${args.enabled} ${args.port}"]`)
	p, err := Prepare([]byte(source), "x.hcl", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	args := p.Arguments()
	for _, tc := range []struct {
		index int
		value string
		valid bool
	}{
		{0, "éé", true}, {0, "abc", true}, {0, "x", false}, {0, "abcd", false}, {0, "ab", false},
		{0, "", false}, {0, "a\x00", false}, {1, "true", true}, {1, "false", true}, {1, "yes", false},
		{2, "8080", true}, {2, "80", false}, {2, "8.5", false}, {2, "9223372036854775808", false},
	} {
		_, err := args[tc.index].ParseValue(tc.value)
		if (err == nil) != tc.valid {
			t.Errorf("argument %d value %q: %v", tc.index, tc.value, err)
		}
	}
}

func TestArgumentIncludesAndResourceValues(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "base.hcl", argumentProfile(`argument "zone" {
  type = string
  default = "UTC"
}`, `["echo ${shellquote(args.zone)}"]`))
	writeFixture(t, base, "left.hcl", "include \"base\" { arguments = {zone = \"UTC\"} }\n")
	writeFixture(t, base, "right.hcl", "include \"base\" { arguments = {zone = \"UTC\"} }\n")
	plan, err := LoadProfileSourceForTest(t, base, "include = [\"left\", \"right\"]\n")
	if err != nil || len(plan.Tasks) != 1 {
		t.Fatalf("merge once: %v, %+v", err, plan)
	}
	writeFixture(t, base, "right.hcl", "include \"base\" { arguments = {zone = \"Europe/Amsterdam\"} }\n")
	_, err = LoadProfileSourceForTest(t, base, "include = [\"left\", \"right\"]\n")
	if err == nil || !strings.Contains(err.Error(), "different arguments") {
		t.Fatal(err)
	}
	writeFixture(t, base, "files/app.service.tmpl", "[Service]\nUser=${user}\n")
	source := `arguments {
  argument "account" { type = string }
  argument "zone" { type = string }
}
include "base" { arguments = {zone = args.zone} }
configuration {
  users = [{name = args.account}]
  timezone = args.zone
}
repository "app" {
  url = "https://example.com/app.git"
  user = args.account
  path = "/srv/${args.account}"
}
unit "app.service" {
  source = "app.service.tmpl"
  vars = {user = args.account}
}
tasks {
  task "Deploy" {
    order = 1
    setup = [repo("app"), unit("app.service")]
    script = ["echo done"]
    lock { exists = ["/srv/${args.account}"] }
  }
}`
	p, err := Prepare([]byte(source), "resources.hcl", base)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = p.Bind(map[string]cty.Value{"account": cty.StringVal("app"), "zone": cty.StringVal("UTC")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Timezone != "UTC" || plan.Repositories["app"].Path != "/srv/app" ||
		!strings.Contains(plan.Units["app.service"].Content, "User=app") || plan.Tasks[len(plan.Tasks)-1].Lock.Exists[0] != "/srv/app" {
		t.Fatalf("resource inputs not bound: %+v", plan)
	}
}

func LoadProfileSourceForTest(t *testing.T, base, source string) (*Plan, error) {
	t.Helper()
	writeFixture(t, base, "root.hcl", source)
	return LoadProfile("root", base)
}

func TestSnippetArgumentRestrictions(t *testing.T) {
	for _, extra := range []string{`default = "x"`, `prompt = "Value"`} {
		base := t.TempDir()
		writeFixture(t, base, "snippets/s.hcl", "snippet \"s\" {\narguments {\nargument \"v\" {\ntype = string\n"+extra+"\n}\n}\ncommands = [\"true\"]\n}")
		_, err := Prepare([]byte(taskSource("S", "order = 1\nscript = [run(\"s\", {v = \"x\"})]")), "root.hcl", base)
		if err == nil || !strings.Contains(err.Error(), "cannot have prompt or default") {
			t.Fatal(err)
		}
	}
}

func BenchmarkArgumentPreflight(b *testing.B) {
	declarations := strings.Builder{}
	for i := range 100 {
		fmt.Fprintf(&declarations, "argument \"input_%d\" {\ntype = integer\ndefault = %d\nvalidation { min = 0 }\n}\n", i, i)
	}
	source := []byte(argumentProfile(declarations.String(), `["echo ${args.input_0}"]`))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		program, err := Prepare(source, "bench.hcl", ".")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := program.Bind(nil); err != nil {
			b.Fatal(err)
		}
	}
}
