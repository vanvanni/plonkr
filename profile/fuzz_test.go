package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

// The fuzz targets double as regression tests: `go test` runs every seed, and
// `go test -fuzz '^FuzzParse$' ./profile` explores beyond them.

func seedProfiles(f *testing.F) {
	f.Helper()
	for _, source := range []string{
		"",
		"{}",
		"tasks {}",
		"# comment only\n",
		oneTask("A"),
		"configuration {\n groups = [{name = \"app\", gid = 100}]\n users = [{name = \"app\", groups = [\"app\"]}]\n timezone = \"UTC\"\n}\n",
		"repository \"r\" {\n url = \"git@example.com:a/b.git\"\n path = \"/srv/r\"\n user = \"app\"\n key = \"/k/id\"\n ref = \"main\"\n}\n" +
			taskSource("Clone", "order = 0\nsetup = [repo(\"r\")]"),
		taskSource("Locked", "order = 2\nscript = [flag(\"x\", log(\"hi\", \"red\"), \"echo $HOME\")]\nlock {\n exists = [\"/a\"]\n eval = [\"true\"]\n}"),
		"{\n" + oneTask("Envelope") + "}\n",
		"tasks {\r\n task \"crlf\" {\r\n order = 0\r\n script = [<<-EOT\r\n echo a\r\n EOT\r\n ]\r\n }\r\n}\r\n",
		"include = [\"a\", \"a\"]\n",
		argumentProfile("argument \"text\" {\ntype = string\ndefault = \"a'b\"\nvalidation { pattern = \".+\" }\n}", `["echo ${shellquote(args.text)}"]`),
		argumentProfile("argument \"port\" {\ntype = integer\ndefault = 8080\nvalidation {\nmin = 1024\nmax = 65535\n}\n}", `["echo ${args.port}"]`),
		argumentProfile("argument \"enabled\" {\ntype = bool\ndefault = true\n}", `["echo ${args.enabled}"]`),
		"unit \"a.service\" {\n source = \"a.tmpl\"\n}\n",
		"tasks { task \"x\" { order = 1\n script = [run(\"s\")] } }",
		"\xef\xbb\xbftasks {}",
		"tasks { task \"\\u0000\" { order = 1\n script = [\"\\u0000\"] } }",
	} {
		f.Add(source)
	}
}

func FuzzParse(f *testing.F) {
	seedProfiles(f)
	base := f.TempDir()
	f.Fuzz(func(t *testing.T, source string) {
		plan, err := ParseWithBase([]byte(source), filepath.Join(base, "fuzz.hcl"), base)
		if err != nil {
			if plan != nil {
				t.Fatalf("error %v returned a plan", err)
			}
			return
		}
		checkPlanInvariants(t, plan)
	})
}

// checkPlanInvariants asserts what every accepted profile guarantees to the
// provisioning code that trusts it.
func checkPlanInvariants(t *testing.T, plan *Plan) {
	t.Helper()
	total := 0
	lockedNames := map[string]bool{}
	for i, task := range plan.Tasks {
		if len(task.Steps) == 0 {
			t.Fatalf("task %d %q has no steps", i, task.Name)
		}
		if strings.TrimSpace(task.Name) == "" {
			t.Fatalf("task %d has a blank name", i)
		}
		if task.Order < -1 || (task.Order == -1) != task.Baseline {
			t.Fatalf("task %q: order %d, baseline %t", task.Name, task.Order, task.Baseline)
		}
		if !task.Lock.IsZero() {
			if lockedNames[task.Name] {
				t.Fatalf("locked task %q appears twice", task.Name)
			}
			lockedNames[task.Name] = true
		}
		for _, step := range task.Steps {
			total++
			checkStepInvariants(t, plan, step)
		}
	}
	if total < 1 || total > maxSteps {
		t.Fatalf("plan has %d steps", total)
	}
	for _, name := range plan.FlagNames() {
		if !flagName.MatchString(name) {
			t.Fatalf("flag %q is not a valid flag name", name)
		}
	}
	for id, g := range plan.Groups {
		if !ValidLinuxName(g.Name) || (g.GID != nil && (*g.GID < 1 || int64(*g.GID) > maxGID)) {
			t.Fatalf("group %q = %+v", id, g)
		}
	}
	for id, u := range plan.Users {
		if !ValidLinuxName(u.Name) || !safePOSIXPath(u.Home) || !safePOSIXPath(u.Shell) {
			t.Fatalf("user %q = %+v", id, u)
		}
		for _, key := range u.PublicKeys {
			if !validPublicKeyLine(key) {
				t.Fatalf("user %q public key %q", id, key)
			}
		}
	}
	for id, r := range plan.Repositories {
		if !validRepositoryURL(r.URL) || !safePOSIXPath(r.Path) || !ValidLinuxName(r.User) || !repositoryLabel.MatchString(r.Name) {
			t.Fatalf("repository %q = %+v", id, r)
		}
		if r.Key != "" && (!validKeyPath(r.Key) || strings.HasPrefix(r.URL, "https://")) {
			t.Fatalf("repository %q key %q for %q", id, r.Key, r.URL)
		}
		if r.Key == "" && !strings.HasPrefix(r.URL, "https://") {
			t.Fatalf("repository %q has an SSH URL and no key", id)
		}
		if r.Ref != "" && !validRepositoryRef(r.Ref) {
			t.Fatalf("repository %q ref %q", id, r.Ref)
		}
	}
	for id, u := range plan.Units {
		if !unitName.MatchString(u.Name) || strings.TrimSpace(u.Content) == "" || strings.ContainsRune(u.Content, 0) || strings.ContainsRune(u.Content, '\r') {
			t.Fatalf("unit %q = %q", id, u.Name)
		}
	}
}

func checkStepInvariants(t *testing.T, plan *Plan, step Step) {
	t.Helper()
	if step.Phase != PhaseSetup && step.Phase != PhaseScript {
		t.Fatalf("step %+v has no phase", step)
	}
	for _, flag := range step.Flags {
		if !flagName.MatchString(flag) {
			t.Fatalf("step %+v: bad flag %q", step, flag)
		}
	}
	switch step.Op {
	case OpWait, OpWaitSSH, OpWaitUntil:
		if step.Duration < time.Second || step.Duration > time.Hour || step.Duration%time.Second != 0 {
			t.Fatalf("invalid wait duration %s", step.Duration)
		}
	case OpShell:
		if step.Phase != PhaseScript || strings.TrimSpace(step.Text) == "" || strings.ContainsRune(step.Text, 0) {
			t.Fatalf("shell step %+v", step)
		}
	case OpLog:
		switch step.Color {
		case "", "default", "black", "red", "green", "yellow", "blue", "magenta", "cyan", "white", "gray":
		default:
			t.Fatalf("log colour %q", step.Color)
		}
	case OpGroup:
		if _, ok := plan.Groups[step.Resource]; !ok || step.Phase != PhaseSetup {
			t.Fatalf("dangling group step %+v", step)
		}
	case OpUser:
		if _, ok := plan.Users[step.Resource]; !ok || step.Phase != PhaseSetup {
			t.Fatalf("dangling user step %+v", step)
		}
	case OpRepo:
		if _, ok := plan.Repositories[step.Resource]; !ok || step.Phase != PhaseSetup {
			t.Fatalf("dangling repo step %+v", step)
		}
	case OpUnit:
		if _, ok := plan.Units[step.Resource]; !ok || step.Phase != PhaseSetup {
			t.Fatalf("dangling unit step %+v", step)
		}
	case OpTimezone:
		if step.Phase != PhaseSetup || plan.Timezone == "" && !strings.HasPrefix(step.Text, "timezone ") {
			t.Fatalf("timezone step %+v", step)
		}
	default:
		t.Fatalf("unknown op %q", step.Op)
	}
}

func FuzzParseWithIncludes(f *testing.F) {
	seedProfiles(f)
	f.Add(`include = ["leaf"]` + "\n" + oneTask("Top"))
	f.Add(`include = ["leaf", "other"]`)
	f.Add(`include = ["top"]`)
	f.Add(`include = ["nested/leaf", "nested/other"]`)
	// Dependencies are immutable. Recreating files for every mutation makes this
	// a filesystem benchmark and starves the parser of useful fuzz iterations.
	base := f.TempDir()
	for name, source := range map[string]string{
		"leaf.hcl":         "configuration {\n groups = [{name = \"leaf\"}]\n}\n" + oneTask("Leaf"),
		"other.hcl":        "include = [\"leaf\"]\n" + oneTask("Other"),
		"nested/leaf.hcl":  oneTask("Nested"),
		"nested/other.hcl": "include = [\"nested/leaf\"]\n",
		"top.hcl":          "include = [\"top\"]\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(base, name)), 0700); err != nil {
			f.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, name), []byte(source), 0600); err != nil {
			f.Fatal(err)
		}
	}
	f.Fuzz(func(t *testing.T, source string) {
		plan, err := ParseWithBase([]byte(source), filepath.Join(base, "top.hcl"), base)
		if err != nil {
			return
		}
		checkPlanInvariants(t, plan)
	})
}

func FuzzValidRepositoryURL(f *testing.F) {
	for _, seed := range []string{
		"https://example.com/a.git", "git@example.com:a/b.git", "ssh://git@host:22/a", "", "git@[::1]:a",
		"https://u:p@h/a", "file:///x", "https://h/a#b", "ssh://-oProxyCommand=x/a", "https://0/@",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if !validRepositoryURL(value) {
			return
		}
		if value == "" || strings.HasPrefix(value, "-") || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
			t.Fatalf("accepted %q", value)
		}
		if !strings.HasPrefix(value, "https://") && !strings.HasPrefix(value, "ssh://") && !strings.HasPrefix(value, "git@") {
			t.Fatalf("accepted unsupported scheme in %q", value)
		}
		if rest, ok := strings.CutPrefix(value, "https://"); ok {
			authority, _, _ := strings.Cut(rest, "/")
			if strings.Contains(authority, "@") {
				t.Fatalf("accepted https credentials in %q", value)
			}
		}
		if strings.ContainsAny(value, "'\"`$;&|<>\\*?") {
			t.Fatalf("accepted shell-significant characters in %q", value)
		}
	})
}

func FuzzValidRepositoryRef(f *testing.F) {
	for _, seed := range []string{"main", "v1.0", "", "-x", "a..b", "refs/heads/x", "a b", "@{", "x.lock", "a/.b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, ref string) {
		if !validRepositoryRef(ref) {
			return
		}
		if ref == "" || ref[0] == '-' || strings.ContainsAny(ref, " ~^:?*[\\") || strings.Contains(ref, "..") ||
			strings.Contains(ref, "@{") || strings.ContainsFunc(ref, unicode.IsControl) || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, "/") {
			t.Fatalf("accepted %q", ref)
		}
	})
}

func FuzzValidKeyPath(f *testing.F) {
	for _, seed := range []string{"/a/b", "C:/a/b", `C:\a\b`, "/", "/a/../b", "a", "", "C:a", "//x/y", "/a/ ", "C:/a/b."} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if !validKeyPath(value) {
			return
		}
		if strings.ContainsFunc(value, unicode.IsControl) || strings.Contains(value, "/../") || strings.HasSuffix(value, "/..") ||
			strings.Contains(value, "//") || strings.HasSuffix(value, "/") {
			t.Fatalf("accepted %q", value)
		}
		if !strings.HasPrefix(value, "/") && !windowsDrive.MatchString(value) {
			t.Fatalf("accepted a path that is neither POSIX nor drive absolute: %q", value)
		}
	})
}

func FuzzValidKnownHostLine(f *testing.F) {
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, seed := range []string{
		"example.com " + key, "@cert-authority *.example.com " + key, "|1|AAAA|AAAA " + key,
		"[host]:22 " + key, "", "host", "! " + key, "host\t" + key,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		if validKnownHostLine(line) && strings.ContainsFunc(line, func(r rune) bool { return unicode.IsControl(r) && r != '\t' }) {
			t.Fatalf("accepted control characters in %q", line)
		}
	})
}

func FuzzValidPublicKeyLine(f *testing.F) {
	f.Add("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA user@host")
	f.Add("command=\"x\" ssh-rsa AAAA")
	f.Add("")
	f.Fuzz(func(t *testing.T, line string) {
		if !validPublicKeyLine(line) {
			return
		}
		if strings.ContainsFunc(line, func(r rune) bool { return unicode.IsControl(r) && r != '\t' }) || strings.ContainsRune(line, '\n') {
			t.Fatalf("accepted %q", line)
		}
		if f := strings.Fields(line); len(f) < 2 || strings.HasPrefix(f[0], "command") {
			t.Fatalf("accepted options or a keyless line: %q", line)
		}
	})
}
