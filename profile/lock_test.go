package profile

import (
	"reflect"
	"testing"
)

func TestLockBlock(t *testing.T) {
	plan := parseOK(t, taskSource("Install Go", `order = 0
script = ["true"]
lock {
  exists = ["/usr/local/go/bin/go", "/etc/profile.d/go.sh"]
  eval   = ["go version", "uname -m"]
}`))
	want := Lock{
		Exists: []string{"/usr/local/go/bin/go", "/etc/profile.d/go.sh"},
		Eval:   []string{"go version", "uname -m"},
	}
	if got := plan.Tasks[0].Lock; !reflect.DeepEqual(got, want) {
		t.Fatalf("lock = %+v, want %+v", got, want)
	}
}

func TestLockDirectivesAreIndependent(t *testing.T) {
	for _, tc := range []struct {
		name, lock string
		want       Lock
	}{
		{"exists only", `exists = ["/usr/bin/something"]`, Lock{Exists: []string{"/usr/bin/something"}}},
		{"eval only", `eval = ["go version"]`, Lock{Eval: []string{"go version"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := parseOK(t, taskSource("Check", "order = 0\nscript = [\"true\"]\nlock {\n"+tc.lock+"\n}"))
			if got := plan.Tasks[0].Lock; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lock = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTaskWithoutLock(t *testing.T) {
	plan := parseOK(t, taskSource("Check", "order = 0\nscript = [\"true\"]"))
	if !plan.Tasks[0].Lock.IsZero() {
		t.Fatalf("task without a lock block has lock %+v", plan.Tasks[0].Lock)
	}
}

func TestLockValidation(t *testing.T) {
	const task = "order = 0\nscript = [\"true\"]\n"
	for _, tc := range []struct{ name, source, errorText string }{
		{"empty block", task + "lock {}", "lock requires exists, eval or both"},
		{"unknown argument", task + "lock {\nexists = [\"/a\"]\nunless = [\"/b\"]\n}", "Unsupported argument"},
		{"nested block", task + "lock {\nexists = [\"/a\"]\nlock {}\n}", "Unsupported block type"},
		{"exists string", task + "lock { exists = \"/a\" }", "exists must be a list or tuple"},
		{"eval number", task + "lock { eval = [1] }", "eval must contain only strings"},
		{"empty exists", task + "lock {\nexists = []\neval = [\"true\"]\n}", "exists must not be empty"},
		{"empty eval", task + "lock {\neval = []\nexists = [\"/a\"]\n}", "eval must not be empty"},
		{"relative path", task + "lock { exists = [\"usr/bin/go\"] }", "exists[0] must be a safe absolute POSIX path"},
		{"root path", task + "lock { exists = [\"/\"] }", "exists[0] must be a safe absolute POSIX path"},
		{"unclean path", task + "lock { exists = [\"/a\", \"/a/../b\"] }", "exists[1] must be a safe absolute POSIX path"},
		{"control character path", task + "lock { exists = [\"/a\\nb\"] }", "exists[0] must be a safe absolute POSIX path"},
		{"blank command", task + "lock { eval = [\"true\", \"  \"] }", "eval[1] must be a non-empty command"},
		{"duplicate block", task + "lock { exists = [\"/a\"] }\nlock { exists = [\"/b\"] }", "duplicate lock block"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(taskSource("Check", tc.source)), "bad.hcl")
			checkError(t, err, tc.errorText)
		})
	}
}

func TestLockEntryLimit(t *testing.T) {
	list := "["
	for range maxLockEntries + 1 {
		list += `"true",`
	}
	list += "]"
	_, err := Parse([]byte(taskSource("Check", "order = 0\nscript = [\"true\"]\nlock { eval = "+list+" }")), "bad.hcl")
	checkError(t, err, "at most 64 entries")
}

func TestLockedTaskNamesMustBeUnique(t *testing.T) {
	locked := "script = [\"true\"]\nlock { exists = [\"/a\"] }\n"
	source := "tasks {\n" +
		taskBlock("Same", "order = 1\n"+locked) +
		taskBlock("Same", "order = 2\n"+locked) + "}\n"
	_, err := Parse([]byte(source), "bad.hcl")
	checkError(t, err, "locked tasks must have unique names")

	// Names only need to be unique among locked tasks.
	source = "tasks {\n" +
		taskBlock("Same", "order = 1\n"+locked) +
		taskBlock("Same", "order = 2\nscript = [\"true\"]") + "}\n"
	if _, err := Parse([]byte(source), "ok.hcl"); err != nil {
		t.Fatalf("an unlocked task may share a name with a locked one: %v", err)
	}
}

func TestLockDigest(t *testing.T) {
	base := Lock{Exists: []string{"/a"}, Eval: []string{"go version"}}
	if base.Digest() != (Lock{Exists: []string{"/a"}, Eval: []string{"go version"}}).Digest() {
		t.Fatal("equal locks must share a digest")
	}
	for name, other := range map[string]Lock{
		"changed path":    {Exists: []string{"/b"}, Eval: []string{"go version"}},
		"changed command": {Exists: []string{"/a"}, Eval: []string{"go env"}},
		"dropped eval":    {Exists: []string{"/a"}},
		"dropped exists":  {Eval: []string{"go version"}},
		"moved entry":     {Exists: []string{"go version"}, Eval: []string{"/a"}},
	} {
		if other.Digest() == base.Digest() {
			t.Errorf("%s keeps the digest", name)
		}
	}
}

func TestIncludedTaskKeepsLock(t *testing.T) {
	base := t.TempDir()
	writeFixture(t, base, "child.hcl", taskSource("Child task", "order = 0\nscript = [\"true\"]\nlock { exists = [\"/opt/child\"] }"))
	writeFixture(t, base, "parent.hcl", "include = [\"child\"]\n")
	plan, err := LoadProfile("parent", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) != 1 || !reflect.DeepEqual(plan.Tasks[0].Lock.Exists, []string{"/opt/child"}) {
		t.Fatalf("included task lost its lock: %+v", plan.Tasks)
	}
}
