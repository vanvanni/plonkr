package profile

import (
	"fmt"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"
)

func TestWaitParsing(t *testing.T) {
	for _, phase := range []string{"setup", "script"} {
		plan := parseOK(t, taskSource("waits", fmt.Sprintf("order = 0\n%s = [wait(\"1m30s\"), flag(\"ready\", wait_ssh(\"2m\")), wait_until(\"true\", \"5s\", \"1s\")]", phase)))
		steps := plan.Tasks[0].Steps
		if len(steps) != 3 || steps[0].Duration != 90*time.Second || steps[1].Interval != 5*time.Second || steps[2].Command != "true" || len(steps[1].Flags) != 1 {
			t.Fatalf("steps: %+v", steps)
		}
	}
	for _, expr := range []string{
		`wait()`, `wait("1s", "2s")`, `wait(1)`, `wait(null)`, `wait("0s")`, `wait("1.5s")`, `wait("1ms")`, `wait("3601s")`, `wait("-1s")`, `wait_ssh("1s")`, `wait_ssh("5s", "6s")`, `wait_until("", "1m")`, `wait_until("true")`, `wait_until("a\u0000b", "1m")`,
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := Parse([]byte(taskSource("bad", "order = 0\nscript = ["+expr+"]")), "wait.hcl"); err == nil {
				t.Fatal("accepted invalid wait")
			}
		})
	}
}

func TestWaitGoDurationSyntax(t *testing.T) {
	for _, value := range []string{".5m", "0.5m", "+30s", "30.s", "1m30s", "1h"} {
		if _, err := waitDuration(value); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
}

func TestWaitArgumentsAndSnippets(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "root.hcl", argumentProfile(`argument "delay" {
type = string
default = "1s"
}`, `[run("ready", {delay = args.delay})]`))
	writeFixture(t, dir, "snippets/ready.hcl", `snippet "ready" {
arguments {
argument "delay" { type = string }
}
commands = [flag("ready", wait(args.delay)), wait_ssh("5s"), wait_until("true", "5s")]
}
`)
	p, err := PrepareProfile("root", dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Tasks[0].Steps[0].Duration != time.Second {
		t.Fatal("argument not bound")
	}
	if _, err := p.Bind(map[string]cty.Value{"delay": cty.StringVal("bad")}); err == nil {
		t.Fatal("invalid duration bound")
	}
}
