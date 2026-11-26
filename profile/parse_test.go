package profile

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestDocumentedProfile(t *testing.T) {
	plan := parseOK(t, `
name = "Prepare application machine"

configuration {
  groups = [
    { name = "app" },
    { name = "reports", create = false },
  ]
  users = [
    { name = "app", service = true, groups = ["app"] },
    { name = "reporter", service = false, groups = ["reports"], create = false },
  ]
}

repository "your-repo" {
  url  = "git@example.com:team/your-repo.git"
  path = "/srv/your-repo"
  user = "app"
  key  = "/home/operator/.ssh/your-repo"
  ref  = "main"
}

tasks {
  task "Clone application" {
    order = 20
    script = ["echo application-checkout-ready"]
    setup = [repo("your-repo")]
    bypass = true
  }
  task "Create reports account" {
    order = 10
    setup = [group("reports"), user("reporter")]
  }
}
`)
	assertSteps(t, plan.Tasks[0].Steps, []Step{setupStep(OpGroup, "app"), setupStep(OpUser, "app")})
	assertSteps(t, plan.Tasks[1].Steps, []Step{setupStep(OpGroup, "reports"), setupStep(OpUser, "reporter")})
	assertSteps(t, plan.Tasks[2].Steps, []Step{setupStep(OpRepo, "your-repo"), {Phase: PhaseScript, Text: "echo application-checkout-ready"}})
	if plan.Name != "Prepare application machine" || len(plan.Tasks) != 3 ||
		plan.Tasks[0].Name != "Configuration" || plan.Tasks[0].Order != -1 || !plan.Tasks[0].Baseline ||
		plan.Tasks[1].Order != 10 || plan.Tasks[1].Bypass ||
		plan.Tasks[2].Order != 20 || !plan.Tasks[2].Bypass {
		t.Fatalf("unexpected task metadata: %+v", plan)
	}
	app, reporter := plan.Users["app"], plan.Users["reporter"]
	if app.Home != "/var/lib/app" || app.Shell != "/usr/sbin/nologin" || !app.Create ||
		reporter.Home != "/home/reporter" || reporter.Shell != "/bin/bash" || reporter.Create {
		t.Fatalf("unexpected user defaults: %+v", plan.Users)
	}
}

func TestBaselineDeclarationOrder(t *testing.T) {
	plan := parseOK(t, `
# { comment }
{
  configuration {
    groups = [{name = "second", gid = 12}, {name = "manual", create = false}, {name = "first"}]
    users = [{name = "worker_z", groups = ["sudo"]}, {name = "manual", create = false}, {name = "worker_a"}]
    timezone = "America/Argentina/Buenos_Aires"
  }
}
`)
	if len(plan.Tasks) != 1 || plan.Groups["second"].GID == nil || *plan.Groups["second"].GID != 12 {
		t.Fatalf("unexpected baseline: %+v", plan)
	}
	assertSteps(t, plan.Tasks[0].Steps, []Step{
		setupStep(OpGroup, "second"), setupStep(OpGroup, "first"),
		setupStep(OpUser, "worker_z"), setupStep(OpUser, "worker_a"),
		setupStep(OpTimezone, "America/Argentina/Buenos_Aires"),
	})
}

func TestConfigurationValidation(t *testing.T) {
	for _, tc := range []struct{ name, config, errorText string }{
		{"conflicting create", "groups = [{name = \"manual\", create = false}]\nusers = [{name = \"app\", groups = [\"manual\"]}]", "with create = false"},
		{"duplicate group name", `groups = [{name = "app"}, {name = "app"}]`, "duplicate group name"},
		{"duplicate user name", `users = [{name = "app"}, {name = "app"}]`, "duplicate user name"},
		{"duplicate GID", `groups = [{name = "app", gid = 5}, {name = "logs", gid = 5}]`, "duplicate GID"},
		{"fractional GID", `groups = [{name = "app", gid = 1.5}]`, "gid must be an integer"},
		{"zero GID", `groups = [{name = "app", gid = 0}]`, "gid must be an integer"},
		{"overflow GID", `groups = [{name = "app", gid = 1000000000000000000000}]`, "gid must be an integer"},
		{"unknown group field", `groups = [{name = "app", id = 5}]`, "id is not an allowed field"},
		{"unknown user field", `users = [{name = "app", enabled = true}]`, "enabled is not an allowed field"},
		{"root group", `groups = [{name = "root"}]`, "invalid Linux group name"},
		{"invalid user name", `users = [{name = "Bad"}]`, "invalid Linux user name"},
		{"invalid secondary group", `users = [{name = "app", groups = ["bad/name"]}]`, "invalid Linux group name"},
		{"strict group string", `groups = [{name = 123}]`, "name must be a string"},
		{"strict user create", `users = [{name = "app", create = "true"}]`, "create must be a bool"},
		{"strict list elements", `users = [{name = "app", groups = [true]}]`, "only strings"},
		{"null group", `groups = [null]`, "expected an object"},
		{"invalid home path", `users = [{name = "app", home = "/var/../tmp"}]`, "safe absolute POSIX paths"},
		{"invalid shell path", `users = [{name = "app", shell = "/"}]`, "safe absolute POSIX paths"},
		{"invalid timezone", `timezone = "Europe/../UTC"`, "invalid timezone"},
		{"empty timezone", `timezone = ""`, "invalid timezone"},
		{"long timezone", "timezone = " + strconv.Quote(strings.Repeat("A", 129)), "invalid timezone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte("configuration {\n"+tc.config+"\n}"), "bad.hcl")
			checkError(t, err, tc.errorText)
		})
	}
}

func TestPublicKeys(t *testing.T) {
	line := publicKeyLine(t)
	for _, tc := range []struct{ name, line, errorText string }{
		{"valid", line + " test@example.com", ""},
		{"wrong textual type", strings.Replace(line, "ssh-ed25519", "ssh-rsa", 1), "invalid public_keys"},
		{"options", "no-port-forwarding " + line, "invalid public_keys"},
		{"multiple keys", line + "\n" + line, "invalid public_keys"},
		{"bad key", "ssh-ed25519 x", "invalid public_keys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf("configuration {\n users = [{name = \"app\", public_keys = [%q]}]\n}", tc.line)
			plan, err := Parse([]byte(source), "key.hcl")
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
				return
			}
			if err != nil || !slices.Equal(plan.Users["app"].PublicKeys, []string{tc.line}) {
				t.Fatalf("Parse returned %+v, %v", plan, err)
			}
		})
	}
}

func TestTaskValidation(t *testing.T) {
	for _, tc := range []struct{ name, attributes, errorText string }{
		{"missing order", `script = ["true"]`, "order is required"},
		{"negative order", "order = -1\nscript = [\"true\"]", "order must be an integer"},
		{"fractional order", "order = 1.5\nscript = [\"true\"]", "order must be an integer"},
		{"string order", "order = \"1\"\nscript = [\"true\"]", "order must be an integer"},
		{"string bypass", "order = 0\nbypass = \"true\"\nscript = [\"true\"]", "bypass must be a bool"},
		{"missing steps", "order = 0", "must produce at least one step"},
		{"empty steps", "order = 0\nsetup = []\nscript = []", "must produce at least one step"},
		{"raw setup", "order = 0\nsetup = [\"true\"]", "raw shell strings are only allowed in script"},
		{"flagged raw setup", "order = 0\nsetup = [flag(\"opt\", \"true\")]", "raw shell strings are only allowed in script"},
		{"run in setup", "order = 0\nsetup = [run(\"foo\")]", "run() is only allowed in script, not setup"},
		{"group in script", "order = 0\nscript = [group(\"app\")]", "group() is only allowed in setup, not script"},
		{"user in script", "order = 0\nscript = [user(\"app\")]", "user() is only allowed in setup, not script"},
		{"repo in script", "order = 0\nscript = [repo(\"app\")]", "repo() is only allowed in setup, not script"},
		{"unit in script", "order = 0\nscript = [flag(\"opt\", unit(\"app.service\"))]", "unit() is only allowed in setup, not script"},
		{"unknown group", "order = 0\nsetup = [group(\"app\")]", "unknown group"},
		{"unknown user", "order = 0\nsetup = [user(\"app\")]", "unknown user"},
		{"unknown repo", "order = 0\nsetup = [repo(\"app\")]", "unknown repo"},
		{"unknown unit", "order = 0\nsetup = [unit(\"app.service\")]", "unknown unit"},
		{"forged action", "order = 0\nsetup = [{op = \"group\", resource = \"app\"}]", "invalid setup step"},
		{"non-string script", "order = 0\nscript = [true]", "invalid script step"},
		{"invalid flag name", "order = 0\nscript = [flag(\"Bad\", \"true\")]", "invalid flag name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(taskSource("Check", tc.attributes)), "bad.hcl")
			checkError(t, err, tc.errorText)
		})
	}

	for _, name := range []string{"help", "tty", "dry-run", "no-tui", "profiles-dir", "bypass", "version", "auto-approve"} {
		t.Run(name, func(t *testing.T) {
			source := taskSource("Check", fmt.Sprintf("order = 0\nscript = [flag(%q, \"true\")]", name))
			_, err := Parse([]byte(source), "bad.hcl")
			checkError(t, err, "reserved flag name")
		})
	}
}

func TestProfileSchema(t *testing.T) {
	for _, tc := range []struct{ name, source, errorText string }{
		{"duplicate task order", "tasks {\n" + taskBlock("A", "order = 1\nscript = [\"true\"]") + taskBlock("B", "order = 1\nscript = [\"true\"]") + "}", "duplicate task order"},
		{"empty task name", taskSource(" ", "order = 0\nscript = [\"true\"]"), "task name must not be empty"},
		{"duplicate configuration", "configuration {}\nconfiguration {}", "duplicate configuration"},
		{"duplicate tasks", "tasks {}\ntasks {}", "duplicate tasks"},
		{"legacy script", `script "1" "name" {}`, `legacy "script" block is not supported in Plonkr v0.1.0`},
		{"legacy scripts", `scripts {}`, `legacy "scripts" block is not supported in Plonkr v0.1.0`},
		{"legacy account", `account "test" {}`, `legacy "account" block is not supported in Plonkr v0.1.0`},
		{"empty name", `name = ""`, "profile name must not be empty"},
		{"non-string name", `name = 123`, "name must be a string"},
		{"unknown attribute", `config = true`, "Unsupported argument"},
		{"empty plan", `configuration { groups = [{name = "manual", create = false}] }`, "between 1 and 10000 steps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.source), "bad.hcl")
			checkError(t, err, tc.errorText)
		})
	}
}

func TestDocumentEnvelopeKeepsDiagnosticLocation(t *testing.T) {
	_, err := Parse([]byte("# { ignored }\n{\n  invalid = true\n}\n"), "envelope.hcl")
	checkError(t, err, "envelope.hcl:3,3")
}

func TestTotalSteps(t *testing.T) {
	for _, count := range []int{maxSteps, maxSteps + 1} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			source := taskSource("many", "order = 0\nscript = ["+strings.Repeat(`"true",`, count)+"]")
			plan, err := Parse([]byte(source), "limit.hcl")
			if count > maxSteps {
				checkError(t, err, "exceeds 10000 steps")
			} else if err != nil || len(plan.Tasks[0].Steps) != count {
				t.Fatalf("expected %d steps, error: %v", count, err)
			}
		})
	}
}

func parseOK(t *testing.T, source string) *Plan {
	t.Helper()
	plan, err := Parse([]byte(source), "local.hcl")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func assertSteps(t *testing.T, got, want []Step) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("steps = %+v, want %+v", got, want)
	}
}

func taskSource(name, attributes string) string {
	return "tasks {\n" + taskBlock(name, attributes) + "}\n"
}

func taskBlock(name, attributes string) string {
	return fmt.Sprintf("task %q {\n%s\n}\n", name, attributes)
}

func checkError(t *testing.T, err error, expected string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), expected) {
		t.Fatalf("error = %v, want it to contain %q", err, expected)
	}
}

func writeFixture(t *testing.T, base, name, source string) {
	t.Helper()
	if err := t.Context().Err(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func publicKeyLine(t *testing.T) string {
	t.Helper()
	key, err := ssh.NewPublicKey(make(ed25519.PublicKey, ed25519.PublicKeySize))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

func TestGIDUpperBound(t *testing.T) {
	_, err := Parse([]byte(taskSource("x", "order = 0\nscript = [\"true\"]")+"\nconfiguration {\n  groups = [{ name = \"app\", gid = 4294967295 }]\n}\n"), "gid.hcl")
	checkError(t, err, "gid must be an integer <= 4294967294")
	if _, err := Parse([]byte(taskSource("x", "order = 0\nscript = [\"true\"]")+"\nconfiguration {\n  groups = [{ name = \"app\", gid = 4294967294 }]\n}\n"), "gid.hcl"); err != nil {
		t.Fatal(err)
	}
}
