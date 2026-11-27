package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedProfileNames(t *testing.T) {
	for _, name := range []string{"web", "proxmox/lab", "_a/B-2/3", strings.Repeat("a", 255), "a/b/c/d/e/f/g/h"} {
		if !validProfileReference(name) {
			t.Errorf("rejected %q", name)
		}
	}
	for _, name := range []string{"", "/a", "a/", "a//b", ".", "..", "a/../b", "a/./b", `a\b`, "a/.hidden", "snippets/x", "files", "files/x", "a b/c", "a/-b", "a/b.hcl", strings.Repeat("a", 256), "a/b/c/d/e/f/g/h/i", "a/é"} {
		if validProfileReference(name) {
			t.Errorf("accepted %q", name)
		}
		if _, err := LoadProfile(name, t.TempDir()); err == nil {
			t.Errorf("loaded invalid name %q", name)
		}
		if _, err := ParseWithBase([]byte("include = ["+`"`+strings.ReplaceAll(name, `\`, `\\`)+`"`+"]"), "top.hcl", t.TempDir()); err == nil {
			t.Errorf("included invalid name %q", name)
		}
	}
}

func TestNestedProfileGraph(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "proxmox/lab.hcl", `include = ["shared/base", "proxmox/other"]`)
	writeFixture(t, base, "proxmox/other.hcl", `include "shared/base" {}`)
	writeFixture(t, base, "shared/base.hcl", taskSource("shared", "order = 0\nscript = [\"true\"]\nlock { exists = [\"/tmp/done\"] }"))
	plan, err := LoadProfile("proxmox/lab", base)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Name != "proxmox/lab" || len(plan.Tasks) != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	writeFixture(t, base, "proxmox/other.hcl", `include = ["proxmox/lab"]`)
	_, err = LoadProfile("proxmox/lab", base)
	checkError(t, err, "proxmox/lab -> proxmox/other -> proxmox/lab")
	writeFixture(t, base, "proxmox/lab.hcl", `include = ["shared/base", "shared/base"]`)
	_, err = LoadProfile("proxmox/lab", base)
	checkError(t, err, "more than once")
}

func TestNestedProfileSymlinkEscape(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	writeFixture(t, outside, "leaf.hcl", oneTask("outside"))
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := LoadProfile("escape/leaf", base); err == nil {
		t.Fatal("loaded escaping named profile")
	}
	_, err := ParseWithBase([]byte(`include = ["escape/leaf"]`), filepath.Join(base, "top.hcl"), base)
	checkError(t, err, `read profile "escape/leaf"`)
}

func FuzzProfileReference(f *testing.F) {
	for _, name := range []string{"a", "proxmox/lab", "prn", "a//b", "a/../b", `a\b`, "snippets/a", strings.Repeat("a", 255)} {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if !validProfileReference(name) {
			return
		}
		if len(name) > 255 || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
			t.Fatalf("unsafe reference %q", name)
		}
		parts := strings.Split(name, "/")
		if len(parts) > 8 {
			t.Fatalf("too deep: %q", name)
		}
		for _, part := range parts {
			if !profileName.MatchString(part) {
				t.Fatalf("unsafe segment %q", part)
			}
		}
	})
}

func TestNestedProfileAssetsAndArguments(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "nested/lab.hcl", `
arguments {
 argument "message" {
  type = string
  default = "root assets"
 }
}
include "shared/leaf" {
 arguments = { message = args.message }
}
`)
	writeFixture(t, base, "shared/leaf.hcl", `
arguments {
 argument "message" { type = string }
}
unit "nested.service" {
 source = "nested.service.tmpl"
 vars = { description = args.message }
}
tasks {
 task "nested assets" {
  order = 0
  setup = [unit("nested.service")]
  script = [run("root-snippet", {message = args.message})]
 }
}
`)
	writeFixture(t, base, "files/nested.service.tmpl", "[Unit]\nDescription=${description}\n")
	writeFixture(t, base, "snippets/root.hcl", `
snippet "root-snippet" {
 arguments {
  argument "message" { type = string }
 }
 commands = ["echo ${args.message}"]
}
`)
	plan, err := LoadProfile("nested/lab", base)
	if err != nil {
		t.Fatal(err)
	}
	unit := plan.Units["profile:shared/leaf:unit:nested.service"]
	if !strings.Contains(string(unit.Content), "root assets") {
		t.Fatalf("unit did not use root assets: %+v", unit)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].Steps[1].Text != "echo root assets" {
		t.Fatalf("unexpected tasks: %+v", plan.Tasks)
	}
}
