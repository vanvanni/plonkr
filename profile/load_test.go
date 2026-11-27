package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProfileWithIncludesUnitsAndSnippets(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "parent.hcl", `
include = ["child"]
configuration {
  groups = [{name = "app", gid = 55}]
}
unit "app.service" {
  source = "system/app.service.tmpl"
  vars = { description = "Example" }
}
tasks {
  task "install" {
    order = 0
    script = [flag("details", run("inspect-machine", "announce")), "echo done"]
    setup = [flag("install", unit("app.service"), log("Preparing", "yellow"))]
  }
}
`)
	writeFixture(t, base, "child.hcl", `
include = ["leaf"]
configuration {
  groups = [{name = "app", gid = 66}]
  users = [{name = "app", groups = ["app"]}]
  timezone = "UTC"
}
repository "app" {
  url = "https://example.com/app.git"
  path = "/srv/app"
  user = "app"
}
unit "app.service" {
  source = "system/app.service.tmpl"
  vars = { description = "Child" }
}
tasks {
  task "checkout" {
    order = 20
    setup = [repo("app"), unit("app.service")]
  }
}
`)
	writeFixture(t, base, "leaf.hcl", `
configuration {
  groups = [{name = "app", gid = 77}]
}
`)
	writeFixture(t, base, "snippets/library.hcl", `
snippet "inspect-machine" {
  commands = ["uname -a", run("id")]
}
snippet "id" {
  commands = [flag("identity", "id")]
}
snippet "announce" {
  commands = [log("Done", "green")]
}
`)
	writeFixture(t, base, "files/system/app.service.tmpl",
		"[Unit]\nDescription=${description}\n[Service]\nExecStart=/bin/true\n")

	plan, err := LoadProfile("parent", base)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Name != "parent" || len(plan.Tasks) != 5 || plan.Timezone != "" {
		t.Fatalf("unexpected plan metadata: %+v", plan)
	}
	for id, gid := range map[string]int{"app": 55, "profile:child:group:app": 66, "profile:leaf:group:app": 77} {
		group := plan.Groups[id]
		if group.Name != "app" || group.GID == nil || *group.GID != gid {
			t.Fatalf("group %s = %+v, want gid %d", id, group, gid)
		}
	}
	if plan.Users["profile:child:user:app"].Name != "app" || plan.Repositories["profile:child:repo:app"].Name != "app" {
		t.Fatalf("included resource names: %v, %v", plan.Users, plan.Repositories)
	}
	for id, description := range map[string]string{"app.service": "Example", "profile:child:unit:app.service": "Child"} {
		unit := plan.Units[id]
		if unit.Name != "app.service" || !strings.Contains(unit.Content, "Description="+description+"\n") {
			t.Fatalf("unit %s = %+v, want description %s", id, unit, description)
		}
	}
	assertSteps(t, plan.Tasks[0].Steps, []Step{
		{Phase: PhaseSetup, Op: OpGroup, Text: "group app", Resource: "profile:leaf:group:app"},
	})
	assertSteps(t, plan.Tasks[1].Steps, []Step{
		{Phase: PhaseSetup, Op: OpGroup, Text: "group app", Resource: "profile:child:group:app"},
		{Phase: PhaseSetup, Op: OpUser, Text: "user app", Resource: "profile:child:user:app"},
		setupStep(OpTimezone, "UTC"),
	})
	assertSteps(t, plan.Tasks[2].Steps, []Step{setupStep(OpGroup, "app")})
	assertSteps(t, plan.Tasks[3].Steps, []Step{
		{Phase: PhaseSetup, Op: OpRepo, Text: "repo app", Resource: "profile:child:repo:app"},
		{Phase: PhaseSetup, Op: OpUnit, Text: "unit app.service", Resource: "profile:child:unit:app.service"},
	})
	assertSteps(t, plan.Tasks[4].Steps, []Step{
		{Phase: PhaseSetup, Op: OpUnit, Text: "unit app.service", Resource: "app.service", Flags: []string{"install"}},
		{Phase: PhaseSetup, Op: OpLog, Text: "Preparing", Color: "yellow", Flags: []string{"install"}},
		{Phase: PhaseScript, Text: "uname -a", Flags: []string{"details"}},
		{Phase: PhaseScript, Text: "id", Flags: []string{"details", "identity"}},
		{Phase: PhaseScript, Op: OpLog, Text: "Done", Color: "green", Flags: []string{"details"}},
		{Phase: PhaseScript, Text: "echo done"},
	})
}

func TestIncludeValidation(t *testing.T) {
	for _, tc := range []struct{ name, parent, child, errorText string }{
		{"invalid path", `include = ["../child"]`, "", "invalid include profile reference"},
		{"invalid extension", `include = ["child.hcl"]`, "", "invalid include profile reference"},
		{"invalid type", `include = [123]`, "", "must contain only strings"},
		{"missing", `include = ["missing"]`, "", `read profile "missing"`},
		{"self cycle", `include = ["parent"]`, "", "profile include cycle: parent -> parent"},
		{"indirect cycle", `include = ["child"]`, `include = ["parent"]`, "profile include cycle: parent -> child -> parent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeFixture(t, base, "parent.hcl", tc.parent)
			writeFixture(t, base, "child.hcl", tc.child)
			_, err := LoadProfile("parent", base)
			checkError(t, err, tc.errorText)
		})
	}
}

func TestIncludeDepth(t *testing.T) {
	base := t.TempDir()
	for i := range maxDepth + 1 {
		source := fmt.Sprintf("include = [\"level%d\"]", i+1)
		if i == maxDepth {
			source = taskSource("bottom", "order = 0\nscript = [\"true\"]")
		}
		writeFixture(t, base, fmt.Sprintf("level%d.hcl", i), source)
	}
	if _, err := LoadProfile("level1", base); err != nil {
		t.Fatalf("64 profiles: %v", err)
	}
	_, err := LoadProfile("level0", base)
	checkError(t, err, "profile include nesting exceeds 64 levels")

	// Preload level1 at a shallow depth, then reference the same cached
	// expansion through another profile where the nesting would be too deep.
	writeFixture(t, base, "cached.hcl", `include = ["level2", "level0"]`)
	_, err = LoadProfile("cached", base)
	checkError(t, err, "profile include nesting exceeds 64 levels")
}

func TestIncludedStepLimit(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "parent.hcl", `include = ["child", "sibling"]`)
	many := taskSource("many", "order = 0\nscript = ["+strings.Repeat(`"true",`, maxSteps/2+1)+"]")
	writeFixture(t, base, "child.hcl", many)
	writeFixture(t, base, "sibling.hcl", many)
	_, err := LoadProfile("parent", base)
	checkError(t, err, "exceeds 10000 steps")
}

func TestSnippetValidation(t *testing.T) {
	for _, tc := range []struct{ name, source, errorText string }{
		{"unknown", `snippet "a" { commands = [run("missing")] }`, `unknown snippet "missing"`},
		{"cycle", "snippet \"a\" { commands = [run(\"b\")] }\nsnippet \"b\" { commands = [run(\"a\")] }", "snippet cycle: a -> b -> a"},
		{"empty expansion", `snippet "a" { commands = [] }`, "must produce at least one step"},
		{"groups", `snippet "a" { commands = [group("app")] }`, "group() is only allowed in setup, not script"},
		{"users", `snippet "a" { commands = [user("app")] }`, "user() is only allowed in setup, not script"},
		{"repositories", `snippet "a" { commands = [repo("app")] }`, "repo() is only allowed in setup, not script"},
		{"units", `snippet "a" { commands = [unit("app.service")] }`, "unit() is only allowed in setup, not script"},
		{"invalid commands", `snippet "a" { commands = "true" }`, "expected a list or tuple"},
		{"duplicate", "snippet \"a\" { commands = [] }\nsnippet \"a\" { commands = [] }", "duplicate snippet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			profile := taskSource("check", "order = 0\nscript = [run(\"a\")]")
			writeFixture(t, base, "snippets/not-a.hcl", tc.source)
			_, err := Parse([]byte(profile), filepath.Join(base, "parent.hcl"))
			checkError(t, err, tc.errorText)
		})
	}
}

func TestSnippetDepth(t *testing.T) {
	base := t.TempDir()
	var library strings.Builder
	for i := range maxDepth + 1 {
		if i == maxDepth {
			fmt.Fprintf(&library, "snippet \"level%d\" { commands = [\"true\"] }\n", i)
		} else {
			fmt.Fprintf(&library, "snippet \"level%d\" { commands = [run(\"level%d\")] }\n", i, i+1)
		}
	}
	writeFixture(t, base, "snippets/library.hcl", library.String())
	for _, tc := range []struct{ names, errorText string }{
		{`"level1"`, ""},
		{`"level0"`, "snippet nesting exceeds 64 levels"},
		{`"level2", "level0"`, "snippet nesting exceeds 64 levels"},
	} {
		t.Run(tc.names, func(t *testing.T) {
			profile := taskSource("check", "order = 0\nscript = [run("+tc.names+")]")
			_, err := Parse([]byte(profile), filepath.Join(base, "parent.hcl"))
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnitSourceValidation(t *testing.T) {
	for _, tc := range []struct{ name, source, errorText string }{
		{"traversal", "../outside", "inside files/ without traversal"},
		{"internal traversal", "sub/../outside", "inside files/ without traversal"},
		{"absolute", "/etc/example", "inside files/ without traversal"},
		{"Windows absolute", `C:\outside`, "inside files/ without traversal"},
		{"directory", "sub", "must be a regular file"},
		{"large file", "large", "exceeds 1048576 bytes"},
		{"missing var", "template", "Unknown variable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeFixture(t, base, "files/sub/placeholder", "")
			writeFixture(t, base, "files/large", strings.Repeat("x", maxUnitSize+1))
			writeFixture(t, base, "files/template", "${missing}")
			source := fmt.Sprintf("unit \"app.service\" { source = %q }", tc.source)
			_, err := Parse([]byte(source), filepath.Join(base, "parent.hcl"))
			checkError(t, err, tc.errorText)
		})
	}
}

func TestRootRejectsEscapingSymlinks(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	writeFixture(t, base, "files/placeholder", "")
	writeFixture(t, outside, "outside.hcl", taskSource("outside", "order = 0\nscript = [\"true\"]"))
	writeFixture(t, outside, "outside.service", "[Unit]\nDescription=Outside\n")
	for _, name := range []string{"outside.hcl", "files/outside.service"} {
		err := os.Symlink(filepath.Join(outside, filepath.Base(name)), filepath.Join(base, name))
		if err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	_, err := Parse([]byte(`include = ["outside"]`), filepath.Join(base, "parent.hcl"))
	checkError(t, err, `read profile "outside"`)
	_, err = Parse([]byte(`unit "app.service" { source = "outside.service" }`), filepath.Join(base, "parent.hcl"))
	checkError(t, err, `read unit source "outside.service"`)
}
