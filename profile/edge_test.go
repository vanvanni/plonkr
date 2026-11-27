package profile

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// taskNames lists the task names of plan in execution order.
func taskNames(plan *Plan) []string {
	var names []string
	for _, task := range plan.Tasks {
		names = append(names, task.Name)
	}
	return names
}

func oneTask(name string) string {
	return taskSource(name, "order = 1\nscript = [\"echo "+name+"\"]")
}

// A profile reached through more than one include path is merged once. These
// tests pin the "include once" decision for shared base profiles.
func TestIncludeMergesEachProfileOnce(t *testing.T) {
	t.Run("diamond", func(t *testing.T) {
		base := t.TempDir()
		writeFixture(t, base, "top.hcl", `include = ["left", "right"]`+"\n"+oneTask("Top"))
		writeFixture(t, base, "left.hcl", `include = ["shared"]`+"\n"+oneTask("Left"))
		writeFixture(t, base, "right.hcl", `include = ["shared"]`+"\n"+oneTask("Right"))
		writeFixture(t, base, "shared.hcl", oneTask("Shared"))
		plan, err := LoadProfile("top", base)
		if err != nil {
			t.Fatal(err)
		}
		// Depth-first in listed order: Shared is reached first through left.
		if got, want := taskNames(plan), []string{"Shared", "Left", "Right", "Top"}; !slices.Equal(got, want) {
			t.Fatalf("tasks = %v, want %v", got, want)
		}
	})

	t.Run("diamond with locked tasks", func(t *testing.T) {
		base := t.TempDir()
		locked := taskSource("Install", "order = 1\nscript = [\"true\"]\nlock { exists = [\"/usr/bin/true\"] }")
		writeFixture(t, base, "top.hcl", `include = ["left", "right"]`)
		writeFixture(t, base, "left.hcl", `include = ["shared"]`)
		writeFixture(t, base, "right.hcl", `include = ["shared"]`)
		writeFixture(t, base, "shared.hcl", locked)
		plan, err := LoadProfile("top", base)
		if err != nil {
			t.Fatalf("a locked task in a shared profile must not collide with itself: %v", err)
		}
		if len(plan.Tasks) != 1 || plan.Tasks[0].Lock.IsZero() {
			t.Fatalf("tasks = %+v", plan.Tasks)
		}
	})

	t.Run("direct and transitive", func(t *testing.T) {
		for _, include := range []string{`["shared", "mid"]`, `["mid", "shared"]`} {
			base := t.TempDir()
			writeFixture(t, base, "top.hcl", "include = "+include)
			writeFixture(t, base, "mid.hcl", `include = ["shared"]`+"\n"+oneTask("Mid"))
			writeFixture(t, base, "shared.hcl", oneTask("Shared"))
			plan, err := LoadProfile("top", base)
			if err != nil {
				t.Fatal(err)
			}
			if got := taskNames(plan); len(got) != 2 || strings.Count(strings.Join(got, ","), "Shared") != 1 {
				t.Fatalf("include = %s: tasks = %v", include, got)
			}
		}
	})

	t.Run("shared baseline runs once", func(t *testing.T) {
		base := t.TempDir()
		config := "configuration {\n  groups = [{name = \"ops\"}]\n}\n"
		writeFixture(t, base, "top.hcl", `include = ["left", "right"]`)
		writeFixture(t, base, "left.hcl", `include = ["shared"]`)
		writeFixture(t, base, "right.hcl", `include = ["shared"]`)
		writeFixture(t, base, "shared.hcl", config)
		plan, err := LoadProfile("top", base)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Tasks) != 1 || len(plan.Tasks[0].Steps) != 1 {
			t.Fatalf("baseline tasks = %+v", plan.Tasks)
		}
	})

	t.Run("steps are counted once", func(t *testing.T) {
		base := t.TempDir()
		many := taskSource("many", "order = 0\nscript = ["+strings.Repeat(`"true",`, maxSteps/2+1)+"]")
		writeFixture(t, base, "top.hcl", `include = ["left", "right"]`)
		writeFixture(t, base, "left.hcl", `include = ["shared"]`)
		writeFixture(t, base, "right.hcl", `include = ["shared"]`)
		writeFixture(t, base, "shared.hcl", many)
		if _, err := LoadProfile("top", base); err != nil {
			t.Fatalf("a profile merged once must count once against the step limit: %v", err)
		}
	})
}

func TestIncludeListedTwiceIsAnError(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "top.hcl", `include = ["shared", "other", "shared"]`)
	writeFixture(t, base, "shared.hcl", oneTask("Shared"))
	writeFixture(t, base, "other.hcl", oneTask("Other"))
	_, err := LoadProfile("top", base)
	checkError(t, err, `include lists profile "shared" more than once`)
}

func TestIncludeOrderingAndNamespaces(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "top.hcl", `include = ["a", "b"]
configuration { groups = [{name = "top"}] }
tasks {
  task "Top late" {
    order = 50
    script = ["true"]
  }
  task "Top early" {
    order = 5
    script = ["true"]
  }
}`)
	writeFixture(t, base, "a.hcl", "configuration { groups = [{name = \"a\"}] }\n"+oneTask("A"))
	writeFixture(t, base, "b.hcl", "configuration { groups = [{name = \"b\"}] }\n"+oneTask("B"))
	plan, err := LoadProfile("top", base)
	if err != nil {
		t.Fatal(err)
	}
	// Baselines first (children before the parent), then included tasks in
	// listed order, then the parent's own tasks sorted by order. An included
	// task's order never interleaves with the parent's.
	want := []string{"Configuration", "Configuration", "Configuration", "A", "B", "Top early", "Top late"}
	if got := taskNames(plan); !slices.Equal(got, want) {
		t.Fatalf("tasks = %v, want %v", got, want)
	}
	if plan.Tasks[0].Steps[0].Resource != "profile:a:group:a" || plan.Tasks[1].Steps[0].Resource != "profile:b:group:b" || plan.Tasks[2].Steps[0].Resource != "top" {
		t.Fatalf("baseline resources: %+v", plan.Tasks[:3])
	}
}

func TestIncludedTaskOrderMayRepeatParentOrder(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "top.hcl", "include = [\"child\"]\n"+oneTask("Parent"))
	writeFixture(t, base, "child.hcl", oneTask("Child"))
	if _, err := LoadProfile("top", base); err != nil {
		t.Fatalf("order is unique per profile, not across includes: %v", err)
	}
}

func TestRepositoryKeyAppliesOnlyToSSH(t *testing.T) {
	for _, tc := range []struct {
		name, url, attrs, errorText string
	}{
		{"https with key", "https://example.com/app.git", `key = "/home/op/.ssh/k"`, "key applies only to SSH repository URLs"},
		{"https with empty key", "https://example.com/app.git", `key = ""`, "key applies only to SSH repository URLs"},
		{"https with relative key", "https://example.com/app.git", `key = "k"`, "key applies only to SSH repository URLs"},
		{"https without key", "https://example.com/app.git", "", ""},
		{"scp with key", "git@example.com:app.git", `key = "/home/op/.ssh/k"`, ""},
		{"ssh with key", "ssh://git@example.com/app.git", `key = "/home/op/.ssh/k"`, ""},
		{"ssh with empty key", "ssh://git@example.com/app.git", `key = ""`, "absolute local file path"},
		{"ssh with relative key", "git@example.com:app.git", `key = "k"`, "private-key store was removed"},
		{"ssh without key", "ssh://git@example.com/app.git", "", "key is required for SSH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(repositorySource(tc.url, "/srv/app", "deploy", tc.attrs)), "repo.hcl")
			if tc.errorText == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			checkError(t, err, tc.errorText)
		})
	}
}

func TestRepositoryURLEdges(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"https://example.com/app.git", true},
		{"https://example.com:8443/team/app.git", true},
		{"https://EXAMPLE.com/Team/App.git", true},
		{"https://[2001:db8::1]/app.git", true},
		{"https://127.0.0.1/app.git", true},
		{"https://example.com./app.git", true},
		{"ssh://git@example.com:2222/team/app.git", true},
		{"ssh://example.com/team/app.git", true},
		{"git@example.com:team/app.git", true},
		{"git@[2001:db8::1]:team/app.git", true},
		{"git@example.com:~user/app.git", true},
		{"", false},
		{"http://example.com/app.git", false},
		{"git://example.com/app.git", false},
		{"file:///srv/app.git", false},
		{"ftp://example.com/app.git", false},
		{"/srv/app.git", false},
		{"./app.git", false},
		{"example.com:app.git", false},
		{"git@example.com:", false},
		{"git@example.com:/", false},
		{"git@example.com:-app.git", false},
		{"git@example.com:../app.git", false},
		{"git@example.com:a/../app.git", false},
		{"git@:app.git", false},
		{"git@example.com", false},
		{"https://example.com", false},
		{"https://example.com/", false},
		{"https://example.com/app.git#frag", false},
		{"https://example.com/app.git?x=1", false},
		{"https://example.com/app.git?", false},
		{"https://user@example.com/app.git", false},
		{"https://example.com:/app.git", false},
		{"https://example.com:0/app.git", false},
		{"https://example.com:65536/app.git", false},
		{"https://example.com:99999999999999999999/app.git", false},
		{"https://example.com:abc/app.git", false},
		{"https://-example.com/app.git", false},
		{"https://exa_mple.com/app.git", false},
		{"https://example..com/app.git", false},
		{"https://" + strings.Repeat("a", 64) + ".com/app.git", false},
		{"https://example.com/a b.git", false},
		{"https://example.com/a\tb.git", false},
		{"https://example.com/a\nb.git", false},
		{"https://example.com/a%20b.git", false},
		{"https://example.com/a\u00e9.git", false},
		{"https://example.com/app.git\x00", false},
		{"ssh://git@example.com/-oProxyCommand=x", false},
		{"ssh://git@example.com//app.git", true},
		{"ssh://git:pw@example.com/app.git", false},
		{"ssh://-git@example.com/app.git", false},
		{"ssh://git@exa mple.com/app.git", false},
		{"ssh:/git@example.com/app.git", false},
		{"ssh:git@example.com/app.git", false},
	} {
		t.Run(strconv.Quote(tc.url), func(t *testing.T) {
			if got := validRepositoryURL(tc.url); got != tc.valid {
				t.Fatalf("validRepositoryURL(%q) = %t, want %t", tc.url, got, tc.valid)
			}
		})
	}
}

func TestRepositoryRefEdges(t *testing.T) {
	for _, tc := range []struct {
		ref   string
		valid bool
	}{
		{"main", true}, {"v1.2.3", true}, {"release/2026.11", true}, {"feature/a-b_c", true},
		{"HEAD", true}, {"a@b", true}, {"UPPER", true}, {"v1.0-rc.1", true},
		{"0123456789abcdef0123456789abcdef01234567", true},
		{"", false}, {"@", false}, {"-main", false}, {"--upload-pack=x", false},
		{"refs/heads/main", false}, {"main.", false}, {"a..b", false}, {"a@{1}", false},
		{"a b", false}, {"a~1", false}, {"a^", false}, {"a:b", false}, {"a?", false},
		{"a*", false}, {"a[0]", false}, {`a\b`, false}, {"a//b", false}, {"/a", false},
		{"a/", false}, {".hidden", false}, {"a/.hidden", false}, {"branch.lock", false},
		{"a/b.lock/c", false}, {"a\tb", false}, {"a\nb", false}, {"a\x00b", false}, {"a\x7fb", false},
	} {
		t.Run(strconv.Quote(tc.ref), func(t *testing.T) {
			if got := validRepositoryRef(tc.ref); got != tc.valid {
				t.Fatalf("validRepositoryRef(%q) = %t, want %t", tc.ref, got, tc.valid)
			}
		})
	}
}

func TestRepositoryNameAndPathEdges(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		valid      bool
	}{
		{"app", "/srv/app", true}, {"my.repo-1_x", "/srv/app", true}, {"A", "/srv/app", true},
		{"-app", "/srv/app", false}, {".app", "/srv/app", false}, {"_app", "/srv/app", false},
		{"app/x", "/srv/app", false}, {"app x", "/srv/app", false}, {"", "/srv/app", false},
		{"app", "/srv/app/", false}, {"app", "/srv//app", false}, {"app", "/srv/./app", false},
		{"app", "/srv/../app", false}, {"app", `/srv\app`, false}, {"app", "/srv/a\nb", false},
		{"app", "/srv/with space", true}, {"app", "/srv/$HOME", true}, {"app", "/srv/it's", true},
		{"app", "relative/path", false}, {"app", "", false}, {"app", "/", false},
	} {
		t.Run(fmt.Sprintf("%q %q", tc.name, tc.path), func(t *testing.T) {
			source := fmt.Sprintf("repository %q {\nurl = \"https://example.com/app.git\"\npath = %q\nuser = \"deploy\"\n}\n", tc.name, tc.path) +
				taskSource("Clone", "order = 0\nscript = [\"true\"]")
			_, err := Parse([]byte(source), "repo.hcl")
			if (err == nil) != tc.valid {
				t.Fatalf("valid = %t, error = %v", tc.valid, err)
			}
		})
	}
}

func TestDuplicateRepositoryAndUnit(t *testing.T) {
	repo := "repository \"app\" {\nurl = \"https://example.com/app.git\"\npath = \"/srv/app\"\nuser = \"deploy\"\n}\n"
	_, err := Parse([]byte(repo+repo+oneTask("T")), "dup.hcl")
	checkError(t, err, `duplicate repository "app"`)
}

func TestLinuxNameEdges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"app", true}, {"_app", true}, {"app-1", true}, {"a_b-c", true}, {"a", true},
		{strings.Repeat("a", 32), true}, {strings.Repeat("a", 33), false},
		{"", false}, {"root", false}, {"App", false}, {"1app", false}, {"-app", false},
		{"app.x", false}, {"app$", false}, {"app name", false}, {"app\n", false},
		{"app:x", false}, {"app/x", false}, {"é", false}, {"app\x00", false},
	} {
		if got := ValidLinuxName(tc.name); got != tc.valid {
			t.Errorf("ValidLinuxName(%q) = %t, want %t", tc.name, got, tc.valid)
		}
	}
}

func TestConfigurationEdges(t *testing.T) {
	for _, tc := range []struct{ name, config, errorText string }{
		{"GID at maximum", `groups = [{name = "a", gid = 4294967294}]`, ""},
		{"GID one", `groups = [{name = "a", gid = 1}]`, ""},
		{"integral float GID", `groups = [{name = "a", gid = 5.0}]`, ""},
		{"negative GID", `groups = [{name = "a", gid = -1}]`, "gid must be an integer"},
		{"string GID", `groups = [{name = "a", gid = "5"}]`, "gid must be an integer"},
		{"null GID", `groups = [{name = "a", gid = null}]`, "gid must be an integer"},
		{"unknown creates", `groups = [{name = "a", create = null}]`, "create must be a bool"},
		{"timezone with plus", `timezone = "Etc/GMT+2"`, ""},
		{"timezone with underscore", `timezone = "America/New_York"`, ""},
		{"timezone leading slash", `timezone = "/UTC"`, "invalid timezone"},
		{"timezone trailing slash", `timezone = "UTC/"`, "invalid timezone"},
		{"timezone double slash", `timezone = "Europe//Paris"`, "invalid timezone"},
		{"timezone dot", `timezone = "Europe/."`, "invalid timezone"},
		{"timezone space", `timezone = "Europe/ Paris"`, "invalid timezone"},
		{"timezone newline", `timezone = "UTC\n"`, "invalid timezone"},
		{"timezone number", `timezone = 5`, "timezone must be a string"},
		{"parenthesized object", `groups = ({name = "a"})`, "groups must be a list or tuple"},
		{"groups not list", `groups = {name = "a"}`, "groups must be a list"},
		{"users not list", `users = "app"`, "users must be a list"},
		{"user home root", `users = [{name = "a", home = "/"}]`, "safe absolute POSIX paths"},
		{"user home relative", `users = [{name = "a", home = "home/a"}]`, "safe absolute POSIX paths"},
		{"user home trailing slash", `users = [{name = "a", home = "/home/a/"}]`, "safe absolute POSIX paths"},
		{"user home backslash", `users = [{name = "a", home = "/home\\a"}]`, "safe absolute POSIX paths"},
		{"user home newline", `users = [{name = "a", home = "/home/a\nb"}]`, "safe absolute POSIX paths"},
		{"user home with space", `users = [{name = "a", home = "/home/a b"}]`, ""},
		{"user shell relative", `users = [{name = "a", shell = "bash"}]`, "safe absolute POSIX paths"},
		{"user group case", `users = [{name = "a", groups = ["Wheel"]}]`, "invalid Linux group name"},
		{"duplicate supplementary groups", `users = [{name = "a", groups = ["sudo", "sudo"]}]`, ""},
		{"user in own group", `users = [{name = "a", groups = ["a"]}]`, ""},
		{"user with undeclared system group", `users = [{name = "a", groups = ["docker"]}]`, ""},
		{"deferred user with deferred group", `groups = [{name = "g", create = false}]` + "\n" + `users = [{name = "a", create = false, groups = ["g"]}]`, "between 1 and 10000 steps"},
		{"group and user may share a name", `groups = [{name = "app"}]` + "\n" + `users = [{name = "app"}]`, ""},
		{"non-object user", `users = ["app"]`, "expected an object"},
		{"missing user name", `users = [{}]`, "name is required"},
		{"missing group name", `groups = [{}]`, "name is required"},
		{"service user defaults", `users = [{name = "svc", service = true}]`, ""},
		{"service must be bool", `users = [{name = "svc", service = 1}]`, "service must be a bool"},
		{"public_keys must be list", `users = [{name = "a", public_keys = "x"}]`, "public_keys must be a list"},
		{"unknown configuration key", `hostname = "x"`, "Unsupported argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "configuration {\n" + tc.config + "\n}\n"
			plan, err := Parse([]byte(source), "cfg.hcl")
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_ = plan
		})
	}
}

func TestExistingGroupMayBeReferencedByDeferredUser(t *testing.T) {
	// A deferred (create = false) user may reference any group, including a
	// deferred one, because nothing runs until the task calls both explicitly.
	plan := parseOK(t, `configuration {
  groups = [{name = "g", create = false}]
  users  = [{name = "a", create = false, groups = ["g"]}]
}
`+taskSource("Explicit", "order = 0\nsetup = [group(\"g\"), user(\"a\")]"))
	if len(plan.Tasks) != 1 || len(plan.Tasks[0].Steps) != 2 {
		t.Fatalf("tasks = %+v", plan.Tasks)
	}
}

func TestStepExpansionEdges(t *testing.T) {
	for _, tc := range []struct{ name, attrs, errorText string }{
		{"empty command", "order = 0\nscript = [\"\"]", "non-empty"},
		{"whitespace command", "order = 0\nscript = [\"  \\t\"]", "non-empty"},
		{"NUL in command", "order = 0\nscript = [\"a\\u0000b\"]", "NUL"},
		{"multi-line command", "order = 0\nscript = [\"a\\nb\"]", ""},
		{"flag without action", "order = 0\nscript = [flag(\"x\")]", "Not enough function arguments"},
		{"nested flags", "order = 0\nscript = [flag(\"a\", flag(\"b\", \"true\"))]", ""},
		{"repeated nested flag", "order = 0\nscript = [flag(\"a\", flag(\"a\", \"true\"))]", ""},
		{"flag name uppercase", "order = 0\nscript = [flag(\"A\", \"true\")]", "invalid flag name"},
		{"flag name underscore", "order = 0\nscript = [flag(\"a_b\", \"true\")]", "invalid flag name"},
		{"flag name digit first", "order = 0\nscript = [flag(\"1a\", \"true\")]", "invalid flag name"},
		{"flag name empty", "order = 0\nscript = [flag(\"\", \"true\")]", "invalid flag name"},
		{"flag name number", "order = 0\nscript = [flag(1, \"true\")]", "flag name must be a string"},
		{"flag of number", "order = 0\nscript = [flag(\"a\", 1)]", "invalid script step"},
		{"flag of null", "order = 0\nscript = [flag(\"a\", null)]", "must not be null"},
		{"log without color", "order = 0\nscript = [log(\"hi\")]", ""},
		{"log with default color", "order = 0\nscript = [log(\"hi\", \"default\")]", ""},
		{"log empty message", "order = 0\nscript = [log(\"\")]", ""},
		{"log bad color", "order = 0\nscript = [log(\"hi\", \"purple\")]", "unsupported log color"},
		{"log number", "order = 0\nscript = [log(1)]", "log() requires a string message"},
		{"log three arguments", "order = 0\nscript = [log(\"a\", \"red\", \"x\")]", "log() requires a string message"},
		{"log in setup", "order = 0\nsetup = [log(\"hi\")]", ""},
		{"run without arguments", "order = 0\nscript = [run()]", "Not enough function arguments"},
		{"run empty name", "order = 0\nscript = [run(\"\")]", "non-empty string snippet names"},
		{"script not a list", "order = 0\nscript = \"true\"", "expected a list or tuple of steps"},
		{"setup not a list", "order = 0\nsetup = group(\"a\")", "expected a list or tuple of steps"},
		{"setup action arity", "order = 0\nsetup = [group(\"a\", \"b\")]", "Too many function arguments"},
		{"unknown function", "order = 0\nscript = [upper(\"x\")]", "Call to unknown function"},
		{"variable reference", "order = 0\nscript = [var.x]", "Variables may not be used"},
		{"duplicate task attribute", "order = 0\norder = 1\nscript = [\"true\"]", "Attribute redefined"},
		{"unknown task attribute", "order = 0\ndescription = \"x\"\nscript = [\"true\"]", "Unsupported argument"},
		{"order huge", "order = 9223372036854775807\nscript = [\"true\"]", ""},
		{"order overflow", "order = 9223372036854775808\nscript = [\"true\"]", "order must be an integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "configuration {\n groups = [{name = \"a\"}]\n}\n" + taskSource("Check", tc.attrs)
			_, err := Parse([]byte(source), "steps.hcl")
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFlagsPropagateThroughNestingAndSnippets(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "snippets/s.hcl", `snippet "inner" { commands = [flag("deep", "inner-cmd")] }
snippet "outer" { commands = [flag("mid", run("inner"))] }
`)
	writeFixture(t, base, "p.hcl", taskSource("T", `order = 0
script = [flag("top", flag("top", run("outer")), "plain")]`))
	plan, err := LoadProfile("p", base)
	if err != nil {
		t.Fatal(err)
	}
	steps := plan.Tasks[0].Steps
	if len(steps) != 2 {
		t.Fatalf("steps = %+v", steps)
	}
	if want := []string{"top", "mid", "deep"}; !slices.Equal(steps[0].Flags, want) {
		t.Fatalf("flags = %v, want %v (duplicates collapse, outermost first)", steps[0].Flags, want)
	}
	if want := []string{"top"}; !slices.Equal(steps[1].Flags, want) {
		t.Fatalf("flags = %v, want %v", steps[1].Flags, want)
	}
	if got := plan.FlagNames(); !slices.Equal(got, []string{"deep", "mid", "top"}) {
		t.Fatalf("FlagNames = %v", got)
	}
}

func TestMissingFlagsReportsInOrder(t *testing.T) {
	step := Step{Flags: []string{"a", "b", "c"}}
	if got := step.MissingFlags(map[string]bool{"b": true}); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("MissingFlags = %v", got)
	}
	if got := step.MissingFlags(map[string]bool{"a": true, "b": true, "c": true}); got != nil {
		t.Fatalf("MissingFlags = %v, want none", got)
	}
	if got := (Step{}).MissingFlags(nil); got != nil {
		t.Fatalf("unflagged step reports %v", got)
	}
	if got := step.MissingFlags(map[string]bool{"a": false}); len(got) != 3 {
		t.Fatalf("explicitly disabled flags are still missing: %v", got)
	}
	var nilPlan *Plan
	if nilPlan.FlagNames() != nil {
		t.Fatal("nil plan has no flags")
	}
}

func TestIsCommitHash(t *testing.T) {
	for ref, want := range map[string]bool{
		strings.Repeat("a", 40): true, strings.Repeat("A", 40): true, strings.Repeat("0", 64): true,
		strings.Repeat("a", 39): false, strings.Repeat("a", 41): false, strings.Repeat("a", 63): false,
		strings.Repeat("g", 40): false, "": false, "main": false,
		strings.Repeat("a", 39) + "\n": false,
	} {
		if got := IsCommitHash(ref); got != want {
			t.Errorf("IsCommitHash(%q) = %t, want %t", ref, got, want)
		}
	}
}

func TestOpString(t *testing.T) {
	if OpShell.String() != "shell" || OpRepo.String() != "repo" || Op("x").String() != "x" {
		t.Fatal("unexpected Op names")
	}
}

// Systemd units use ${VAR} and %h themselves. Only ${...} and %{...} are HCL
// template syntax; everything else passes through, and $${ / %%{ escape them.
func TestUnitTemplateEscapes(t *testing.T) {
	for _, tc := range []struct {
		name, template, want, errorText string
	}{
		{"plain dollar", "ExecStart=/bin/x $OPTS $MAINPID\n", "ExecStart=/bin/x $OPTS $MAINPID\n", ""},
		{"specifiers", "WorkingDirectory=%h/%n %%\n", "WorkingDirectory=%h/%n %%\n", ""},
		{"escaped interpolation", "ExecStart=/bin/x $${OPTS}\n", "ExecStart=/bin/x ${OPTS}\n", ""},
		{"escaped directive", "X=%%{literal}\n", "X=%{literal}\n", ""},
		{"variable", "Description=${description}\n", "Description=Example\n", ""},
		{"unescaped systemd variable", "ExecStart=/bin/x ${OPTS}\n", "", "Unknown variable"},
		{"conditional", "%{ if description == \"Example\" }yes%{ else }no%{ endif }\n", "yes\n", ""},
		{"function call", "X=${upper(description)}\n", "", "Function calls not allowed"},
		{"non-string", "X=${5}\n", "X=5\n", ""},
		{"empty", "  \n\n", "", "non-empty"},
		{"CRLF", "A=1\r\nB=2\r\n", "A=1\nB=2\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeFixture(t, base, "files/a.tmpl", tc.template)
			writeFixture(t, base, "p.hcl", "unit \"a.service\" {\n  source = \"a.tmpl\"\n  vars = { description = \"Example\" }\n}\n"+
				taskSource("T", "order = 0\nsetup = [unit(\"a.service\")]"))
			plan, err := LoadProfile("p", base)
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := plan.Units["a.service"].Content; got != tc.want {
				t.Fatalf("content = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnitDeclarationEdges(t *testing.T) {
	for _, tc := range []struct{ name, label, attrs, errorText string }{
		{"service", "a.service", `source = "a.tmpl"`, ""},
		{"template instance", "a@.service", `source = "a.tmpl"`, ""},
		{"timer", "a.timer", `source = "a.tmpl"`, ""},
		{"no suffix", "a", `source = "a.tmpl"`, "supported systemd suffix"},
		{"unsupported suffix", "a.conf", `source = "a.tmpl"`, "supported systemd suffix"},
		{"uppercase suffix", "a.SERVICE", `source = "a.tmpl"`, "supported systemd suffix"},
		{"path in name", "x/a.service", `source = "a.tmpl"`, "supported systemd suffix"},
		{"traversal in name", "../a.service", `source = "a.tmpl"`, "supported systemd suffix"},
		{"leading dash", "-a.service", `source = "a.tmpl"`, "supported systemd suffix"},
		{"long name", strings.Repeat("a", 250) + ".service", `source = "a.tmpl"`, "supported systemd suffix"},
		{"missing source", "a.service", "", "source is required"},
		{"source traversal", "a.service", `source = "../a.tmpl"`, "without traversal"},
		{"source absolute", "a.service", `source = "/etc/passwd"`, "without traversal"},
		{"source backslash", "a.service", `source = "x\\a.tmpl"`, "without traversal"},
		{"source drive", "a.service", `source = "C:a.tmpl"`, "without traversal"},
		{"source dot", "a.service", `source = "."`, "without traversal"},
		{"source unclean", "a.service", `source = "x/../a.tmpl"`, "without traversal"},
		{"source missing file", "a.service", `source = "missing.tmpl"`, "read unit source"},
		{"vars not object", "a.service", "source = \"a.tmpl\"\nvars = \"x\"", "unit vars must be an object"},
		{"vars null", "a.service", "source = \"a.tmpl\"\nvars = null", "unit vars must be an object"},
		{"vars non-string", "a.service", "source = \"a.tmpl\"\nvars = { n = 5 }", "must be an HCL identifier with a string value"},
		{"vars bad name", "a.service", "source = \"a.tmpl\"\nvars = { \"a-b c\" = \"x\" }", "must be an HCL identifier"},
		{"unknown attribute", "a.service", "source = \"a.tmpl\"\nmode = \"0600\"", "Unsupported argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeFixture(t, base, "files/a.tmpl", "[Service]\nExecStart=/bin/true\n")
			writeFixture(t, base, "p.hcl", fmt.Sprintf("unit %q {\n%s\n}\n", tc.label, tc.attrs)+taskSource("T", "order = 0\nscript = [\"true\"]"))
			_, err := LoadProfile("p", base)
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnitSourceSizeAndEncoding(t *testing.T) {
	for _, tc := range []struct {
		name, content, errorText string
	}{
		{"invalid UTF-8", "[Service]\nExecStart=/bin/\xff\n", "must be UTF-8"},
		{"NUL byte", "[Service]\nA=\x00\n", ""},
		{"too large", strings.Repeat("a", maxUnitSize+1), "exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeFixture(t, base, "files/a.tmpl", tc.content)
			writeFixture(t, base, "p.hcl", "unit \"a.service\" {\n source = \"a.tmpl\"\n}\n"+taskSource("T", "order = 0\nscript = [\"true\"]"))
			_, err := LoadProfile("p", base)
			if tc.errorText == "" {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			checkError(t, err, tc.errorText)
		})
	}
}

func TestSnippetEdges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		task  string
		error string
	}{
		{"no snippets directory", nil, `run("a")`, `unknown snippet "a"`},
		{"non-hcl files are ignored", map[string]string{"snippets/readme.txt": "snippet \"a\" {}", "snippets/a.hcl": `snippet "a" { commands = ["true"] }`}, `run("a")`, ""},
		{"duplicate across files", map[string]string{"snippets/a.hcl": `snippet "x" { commands = ["true"] }`, "snippets/b.hcl": `snippet "x" { commands = ["true"] }`}, `run("x")`, "duplicate snippet"},
		{"empty snippet name", map[string]string{"snippets/a.hcl": `snippet " " { commands = ["true"] }`}, `run("x")`, "snippet name must not be empty"},
		{"missing commands", map[string]string{"snippets/a.hcl": `snippet "x" {}`}, `run("x")`, "requires commands"},
		{"unknown attribute", map[string]string{"snippets/a.hcl": "snippet \"x\" {\n commands = [\"true\"]\n description = \"d\"\n}"}, `run("x")`, "Unsupported argument"},
		{"setup action inside snippet", map[string]string{"snippets/a.hcl": `snippet "x" { commands = [group("g")] }`}, `run("x")`, "only allowed in setup"},
		{"nested snippet files with invalid sibling", map[string]string{"snippets/a.hcl": `snippet "x" { commands = ["true"] }`, "snippets/b.hcl": `snippet "y" {`}, `run("x")`, "Unclosed"},
		{"several in one file", map[string]string{"snippets/a.hcl": "snippet \"x\" { commands = [\"1\"] }\nsnippet \"y\" { commands = [run(\"x\"), \"2\"] }"}, `run("y")`, ""},
		{"same snippet twice in a task", map[string]string{"snippets/a.hcl": `snippet "x" { commands = ["true"] }`}, `run("x", "x")`, ""},
		{"unrelated top-level block", map[string]string{"snippets/a.hcl": `tasks {}`}, `run("x")`, "Unsupported block type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			for name, source := range tc.files {
				writeFixture(t, base, name, source)
			}
			writeFixture(t, base, "p.hcl", taskSource("T", "order = 0\nscript = ["+tc.task+"]"))
			_, err := LoadProfile("p", base)
			if tc.error != "" {
				checkError(t, err, tc.error)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSnippetsAreOnlyReadWhenUsed(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "snippets/broken.hcl", `snippet "x" {`)
	writeFixture(t, base, "p.hcl", taskSource("T", "order = 0\nscript = [\"true\"]"))
	if _, err := LoadProfile("p", base); err != nil {
		t.Fatalf("an unused, malformed snippet file must not affect a profile that never calls run(): %v", err)
	}
}

func TestProfileSourceEdges(t *testing.T) {
	for _, tc := range []struct{ name, source, errorText string }{
		{"empty file", "", "between 1 and 10000 steps"},
		{"comments only", "# nothing\n// here\n/* at all */\n", "between 1 and 10000 steps"},
		{"envelope braces", "{\n" + oneTask("A") + "}\n", ""},
		{"envelope with trailing comment", "{\n" + oneTask("A") + "} # done\n", ""},
		{"CRLF line endings", strings.ReplaceAll(oneTask("A"), "\n", "\r\n"), ""},
		{"unterminated block", "tasks {", "Unclosed configuration"},
		{"unterminated string", "name = \"x", "Unterminated"},
		{"unknown block", "stuff {}", "Unsupported block type"},
		{"heredoc command", "tasks {\n task \"T\" {\n order = 1\n script = [<<-EOT\n   echo a\n   echo b\n EOT\n ]\n }\n}\n", ""},
		{"profile name with newline", "name = \"a\\nb\"\n" + oneTask("A"), ""},
		{"unicode task name", taskSource("Zażółć 日本語 ✓", "order = 1\nscript = [\"true\"]"), ""},
		{"task name with quotes", taskSource(`say "hi"`, "order = 1\nscript = [\"true\"]"), ""},
		{"blocks after tasks", oneTask("A") + "configuration {\n groups = [{name = \"g\"}]\n}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Parse([]byte(tc.source), "src.hcl")
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Tasks) == 0 {
				t.Fatal("valid profile produced no tasks")
			}
		})
	}
}

func TestParsedHeredocHasNoCarriageReturns(t *testing.T) {
	plan := parseOK(t, strings.ReplaceAll("tasks {\n task \"T\" {\n order = 1\n script = [<<-EOT\n   echo a\n   echo b\n EOT\n ]\n }\n}\n", "\n", "\r\n"))
	text := plan.Tasks[0].Steps[0].Text
	if strings.ContainsRune(text, '\r') || text != "echo a\necho b\n" {
		t.Fatalf("heredoc = %q", text)
	}
}

func TestProfileNameDefaultsFromFile(t *testing.T) {
	plan, err := Parse([]byte(oneTask("A")), "/profiles/web-server.hcl")
	if err != nil || plan.Name != "web-server" {
		t.Fatalf("name = %q, error = %v", plan.Name, err)
	}
	plan, err = Parse([]byte("name = \"Display Name\"\n"+oneTask("A")), "/profiles/web-server.hcl")
	if err != nil || plan.Name != "Display Name" {
		t.Fatalf("name = %q, error = %v", plan.Name, err)
	}
}

func TestLoadProfileReference(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "web.hcl", oneTask("A"))
	for _, tc := range []struct {
		ref   string
		valid bool
	}{
		{"web", true}, {"web.hcl", true}, {"", false}, {".hcl", false}, {"../web", false},
		{"sub/web", false}, {`sub\web`, false}, {"-web", false}, {"web name", false}, {"web.hcl.hcl", false},
		{"web\x00", false}, {"missing", false},
	} {
		_, err := LoadProfile(tc.ref, base)
		if (err == nil) != tc.valid {
			t.Errorf("LoadProfile(%q): error = %v, want valid = %t", tc.ref, err, tc.valid)
		}
	}
	if _, err := LoadProfile("web", base+"-missing"); err == nil || !strings.Contains(err.Error(), "open profiles directory") {
		t.Fatalf("missing directory: %v", err)
	}
}

func TestProfileSizeLimits(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "big.hcl", "# "+strings.Repeat("x", maxSourceSize)+"\n")
	_, err := LoadProfile("big", base)
	checkError(t, err, "exceeds")
	writeFixture(t, base, "snippets/big.hcl", "# "+strings.Repeat("x", maxSourceSize)+"\n")
	writeFixture(t, base, "uses.hcl", taskSource("T", "order = 0\nscript = [run(\"x\")]"))
	_, err = LoadProfile("uses", base)
	checkError(t, err, "exceeds")
}

func TestProfileMustBeARegularFile(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "dir.hcl/inner.txt", "x")
	_, err := LoadProfile("dir", base)
	checkError(t, err, "must be a regular file")
}

func TestLockedTaskNamesAcrossIncludes(t *testing.T) {
	lock := "order = 1\nscript = [\"true\"]\nlock { exists = [\"/x\"] }"
	t.Run("same locked name in two different profiles", func(t *testing.T) {
		base := t.TempDir()
		writeFixture(t, base, "top.hcl", "include = [\"child\"]\n"+taskSource("Install", lock))
		writeFixture(t, base, "child.hcl", taskSource("Install", lock))
		_, err := LoadProfile("top", base)
		checkError(t, err, "locked tasks must have unique names")
	})
	t.Run("unlocked duplicates may repeat", func(t *testing.T) {
		base := t.TempDir()
		writeFixture(t, base, "top.hcl", "include = [\"child\"]\n"+oneTask("Same"))
		writeFixture(t, base, "child.hcl", oneTask("Same"))
		plan, err := LoadProfile("top", base)
		if err != nil || len(plan.Tasks) != 2 {
			t.Fatalf("tasks = %v, error = %v", taskNames(plan), err)
		}
	})
	t.Run("locked and unlocked share a name", func(t *testing.T) {
		base := t.TempDir()
		writeFixture(t, base, "top.hcl", "include = [\"child\"]\n"+oneTask("Same"))
		writeFixture(t, base, "child.hcl", taskSource("Same", lock))
		if _, err := LoadProfile("top", base); err != nil {
			t.Fatalf("only locked tasks key state by name: %v", err)
		}
	})
}

func TestLockEdges(t *testing.T) {
	for _, tc := range []struct{ name, block, errorText string }{
		{"exists only", `exists = ["/a"]`, ""},
		{"eval only", `eval = ["true"]`, ""},
		{"both", `exists = ["/a"]` + "\n" + `eval = ["true"]`, ""},
		{"empty block", ``, "lock requires exists, eval or both"},
		{"empty exists list", `exists = []`, "exists must not be empty"},
		{"empty eval list", `eval = []`, "eval must not be empty"},
		{"empty exists beside eval", `exists = []` + "\n" + `eval = ["true"]`, "exists must not be empty"},
		{"relative path", `exists = ["a/b"]`, "safe absolute POSIX path"},
		{"root path", `exists = ["/"]`, "safe absolute POSIX path"},
		{"trailing slash", `exists = ["/a/"]`, "safe absolute POSIX path"},
		{"dot segment", `exists = ["/a/./b"]`, "safe absolute POSIX path"},
		{"parent segment", `exists = ["/a/../b"]`, "safe absolute POSIX path"},
		{"glob characters are literal", `exists = ["/a/*"]`, ""},
		{"path with newline", `exists = ["/a\nb"]`, "safe absolute POSIX path"},
		{"blank eval", `eval = [" "]`, "non-empty command"},
		{"NUL in eval", `eval = ["a\u0000"]`, "non-empty command"},
		{"multi-line eval", `eval = ["a\nb"]`, ""},
		{"eval not a list", `eval = "true"`, "eval must be a list"},
		{"eval number", `eval = [1]`, "only strings"},
		{"unknown attribute", `exists = ["/a"]` + "\n" + `timeout = 5`, "Unsupported argument"},
		{"duplicate paths are allowed", `exists = ["/a", "/a"]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := taskSource("T", "order = 0\nscript = [\"true\"]\nlock {\n"+tc.block+"\n}")
			_, err := Parse([]byte(source), "lock.hcl")
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("two lock blocks", func(t *testing.T) {
		source := taskSource("T", "order = 0\nscript = [\"true\"]\nlock {\nexists = [\"/a\"]\n}\nlock {\nexists = [\"/b\"]\n}")
		_, err := Parse([]byte(source), "lock.hcl")
		checkError(t, err, "duplicate lock block")
	})
	t.Run("lock labels are rejected", func(t *testing.T) {
		source := taskSource("T", "order = 0\nscript = [\"true\"]\nlock \"x\" {\nexists = [\"/a\"]\n}")
		if _, err := Parse([]byte(source), "lock.hcl"); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestLockDigestIsOrderAndDirectiveSensitive(t *testing.T) {
	base := Lock{Exists: []string{"/a", "/b"}, Eval: []string{"x"}}
	for name, other := range map[string]Lock{
		"exists order": {Exists: []string{"/b", "/a"}, Eval: []string{"x"}},
		"eval changed": {Exists: []string{"/a", "/b"}, Eval: []string{"y"}},
		"exists added": {Exists: []string{"/a", "/b", "/c"}, Eval: []string{"x"}},
		"moved across": {Exists: []string{"/a"}, Eval: []string{"/b", "x"}},
	} {
		if base.Digest() == other.Digest() {
			t.Errorf("%s: digests must differ", name)
		}
	}
	if base.Digest() != (Lock{Exists: []string{"/a", "/b"}, Eval: []string{"x"}}).Digest() {
		t.Error("equal locks must share a digest")
	}
	// A command containing the field separator must not collide with a split pair.
	if (Lock{Eval: []string{"a", "b"}}).Digest() == (Lock{Eval: []string{`a","b`}}).Digest() {
		t.Error("digest must be unambiguous for commands containing quotes and commas")
	}
}
