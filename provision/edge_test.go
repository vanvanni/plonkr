package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

// shellRun executes script in a real POSIX shell with stub tools first on PATH.
type shellRun struct {
	t     *testing.T
	sh    string
	tools string
}

func newShellRun(t *testing.T, stubs map[string]string) *shellRun {
	t.Helper()
	sh := findPOSIXShell()
	if sh == "" {
		t.Skip("no POSIX sh available")
	}
	tools := t.TempDir()
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &shellRun{t: t, sh: sh, tools: tools}
}

func (s *shellRun) run(script, stdin string, env ...string) (string, error) {
	s.t.Helper()
	// A script file avoids command-line quoting between Go and Git for Windows'
	// shell, which corrupts large multiply-quoted arguments. Real targets run
	// the same text through OpenSSH and a Linux shell.
	file := filepath.Join(s.t.TempDir(), "script.sh")
	if err := os.WriteFile(file, []byte(withPOSIXTools(s.tools, script)), 0o600); err != nil {
		s.t.Fatal(err)
	}
	cmd := exec.CommandContext(s.t.Context(), s.sh, "-e", filepath.ToSlash(file))
	cmd.Env = posixEnv(s.sh, env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// symlinksWork reports whether the shell creates real symbolic links here.
func symlinksWork(t *testing.T, sh string) bool {
	t.Helper()
	dir := filepath.ToSlash(t.TempDir())
	cmd := exec.CommandContext(t.Context(), sh, "-c", `ln -s target "`+dir+`/l" 2>/dev/null && [ -L "`+dir+`/l" ]`)
	cmd.Env = posixEnv(sh)
	return cmd.Run() == nil
}

func TestQuoteRoundTripsThroughARealShell(t *testing.T) {
	s := newShellRun(t, nil)
	for _, value := range []string{
		"", "plain", "with space", "it's", "''", "'", `"double"`, "$HOME", "$(touch /tmp/pwned)", "`id`", `back\slash`, `\`,
		"-n", "-e", "--", "*", "?", "[a]", "~", "~root", "a;b", "a&b", "a|b", "a>b", "a<b", "!x", "#x", "%s", "%",
		"line1\nline2", "\n", "trailing newline\n", "tab\there", "unicode ✓ é 日本語", strings.Repeat("a'b", 200),
	} {
		out, err := s.run("printf '%s' "+quote(value), "")
		if err != nil || out != value {
			t.Errorf("quote(%q): output %q, error %v", value, out, err)
		}
	}
}

func FuzzQuote(f *testing.F) {
	for _, seed := range []string{"", "a", "'", "''", "a'b", "\n", `\`, "$x", "é"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		got, ok := unquoteShell(quote(value))
		if !ok || got != value {
			t.Fatalf("quote(%q) = %q does not unquote to the input (got %q, ok = %t)", value, quote(value), got, ok)
		}
	})
}

// unquoteShell interprets text as a sequence of POSIX single-quoted and
// double-quoted segments, the only forms quote produces.
func unquoteShell(text string) (string, bool) {
	var out strings.Builder
	for len(text) > 0 {
		switch text[0] {
		case '\'':
			end := strings.IndexByte(text[1:], '\'')
			if end < 0 {
				return "", false
			}
			out.WriteString(text[1 : 1+end])
			text = text[end+2:]
		case '"':
			end := strings.IndexByte(text[1:], '"')
			if end < 0 || strings.ContainsAny(text[1:1+end], "$`\\") {
				return "", false
			}
			out.WriteString(text[1 : 1+end])
			text = text[end+2:]
		default:
			return "", false // an unquoted character would be subject to expansion
		}
	}
	return out.String(), true
}

func TestPrivilegeWrappersPreserveScriptsAndNeverPromptForPasswords(t *testing.T) {
	script := "printf '%s|' \"it's\" 'a b' \"$(printf x)\"\necho done"
	want := "it's|a b|x|done\n"
	asRootStubs := map[string]string{"id": "echo 0\n", "sudo": "echo sudo-must-not-run >&2; exit 9\n"}
	s := newShellRun(t, asRootStubs)
	if out, err := s.run(asRoot(script), ""); err != nil || out != want {
		t.Errorf("as root: %q, %v", out, err)
	}

	// Not root: the wrapper must use `sudo -n` so a password prompt can never hang a run.
	s = newShellRun(t, map[string]string{
		"id":   "echo 1000\n",
		"sudo": `[ "$1" = -n ] || { echo "sudo without -n: $*" >&2; exit 9; }; shift; [ "$1" = -- ] || exit 8; shift; exec "$@"` + "\n",
	})
	if out, err := s.run(asRoot(script), ""); err != nil || out != want {
		t.Errorf("via sudo: %q, %v", out, err)
	}
	if out, err := s.run(asRoot("exit 3"), ""); err == nil {
		t.Errorf("a failing script must fail the wrapper: %q", out)
	}

	// asUser: same user, root via runuser, other login via sudo -n -H -u.
	for name, stubs := range map[string]map[string]string{
		"already that user": {"id": `[ "$1" = -un ] && echo deploy || echo 1000` + "\n"},
		"root uses runuser": {
			"id":      `[ "$1" = -un ] && echo root || echo 0` + "\n",
			"getent":  "echo 'deploy:x:1000:1000::/home/deploy:/bin/sh'\n",
			"runuser": `[ "$1" = -u ] && [ "$2" = deploy ] && shift 3 && exec "$@"` + "\n",
		},
		"other login uses sudo": {
			"id":   `[ "$1" = -un ] && echo ops || echo 1000` + "\n",
			"sudo": `[ "$1" = -n ] && [ "$2" = -H ] && [ "$3" = -u ] && [ "$4" = deploy ] && [ "$5" = -- ] && shift 5 && exec "$@"` + "\n",
		},
	} {
		s := newShellRun(t, stubs)
		home := filepath.ToSlash(t.TempDir())
		out, err := s.run(asUser("deploy", "pwd; "+script), "", "HOME="+home)
		if err != nil || !strings.HasPrefix(out, "/") || !strings.HasSuffix(out, want) {
			t.Errorf("%s: %q, %v", name, out, err)
		}
		if first, _, _ := strings.Cut(out, "\n"); first != "/" && !strings.HasSuffix(first, ":/") {
			t.Errorf("%s: the script must start in /, not the caller's directory: %q", name, first)
		}
	}
}

func accountStubs(extra map[string]string) map[string]string {
	stubs := map[string]string{
		"id": `case "$1" in
  -un) echo "${FIXTURE_USER:-app}" ;;
  -u) if [ -n "$2" ]; then [ "$2" = "$FIXTURE_EXISTS" ] || exit 1; echo "${FIXTURE_UID:-1000}"; else echo 0; fi ;;
  *) echo 0 ;;
esac
`,
		"getent": `case "$1 $2" in
  "passwd $FIXTURE_EXISTS") echo "$FIXTURE_EXISTS:x:1000:1000::$FIXTURE_HOME:$FIXTURE_SHELL" ;;
  "group $FIXTURE_GROUP"|"group $FIXTURE_GROUP2") echo "$2:x:2000:" ;;
  *) exit 2 ;;
esac
`,
		"useradd": "printf 'useradd %s\\n' \"$*\"\n",
		"usermod": "printf 'usermod %s\\n' \"$*\"\n",
	}
	for name, body := range extra {
		stubs[name] = body
	}
	return stubs
}

func TestUserScriptEdges(t *testing.T) {
	user := profile.User{Name: "app", Home: "/home/app", Shell: "/bin/bash", Create: true}
	for _, tc := range []struct {
		name   string
		user   profile.User
		env    []string
		want   []string // all must appear
		forbid []string // none may appear
		fails  string
	}{
		{
			name: "new user gets a private group", user: user,
			want:   []string{"useradd --create-home --user-group --home-dir /home/app --shell /bin/bash -- app", "User app ready"},
			forbid: []string{"--system", "usermod"},
		},
		{
			name: "service user is a system account", user: profile.User{Name: "svc", Home: "/var/lib/svc", Shell: "/usr/sbin/nologin", Service: true},
			want: []string{"--system", "--home-dir /var/lib/svc", "--shell /usr/sbin/nologin"},
		},
		{
			name: "existing user with matching home and shell is untouched", user: user,
			env:    []string{"FIXTURE_EXISTS=app", "FIXTURE_HOME=/home/app", "FIXTURE_SHELL=/bin/bash"},
			want:   []string{"User app ready"},
			forbid: []string{"useradd"},
		},
		{
			name: "existing user with another home fails", user: user,
			env:   []string{"FIXTURE_EXISTS=app", "FIXTURE_HOME=/srv/app", "FIXTURE_SHELL=/bin/bash"},
			fails: "home/shell differs from declared values",
		},
		{
			name: "existing user with another shell fails", user: user,
			env:   []string{"FIXTURE_EXISTS=app", "FIXTURE_HOME=/home/app", "FIXTURE_SHELL=/bin/zsh"},
			fails: "home/shell differs from declared values",
		},
		{
			name: "a UID 0 account is never modified", user: user,
			env:   []string{"FIXTURE_EXISTS=app", "FIXTURE_UID=0", "FIXTURE_HOME=/home/app", "FIXTURE_SHELL=/bin/bash"},
			fails: "Refusing to modify a UID 0 account",
		},
		{
			name: "supplementary groups are added in one call", user: profile.User{Name: "app", Home: "/home/app", Shell: "/bin/bash", Groups: []string{"docker", "adm"}},
			env:  []string{"FIXTURE_GROUP=docker", "FIXTURE_GROUP2=adm"},
			want: []string{"usermod -a -G docker,adm -- app"},
		},
		{
			name: "a missing supplementary group fails before usermod", user: profile.User{Name: "app", Home: "/home/app", Shell: "/bin/bash", Groups: []string{"docker", "ghost"}},
			env:    []string{"FIXTURE_GROUP=docker"},
			fails:  "Required supplementary group ghost does not exist",
			forbid: []string{"usermod"},
		},
		{
			name: "an existing same-named group is reused", user: user,
			env:    []string{"FIXTURE_GROUP=app"},
			want:   []string{"useradd --create-home -g app "},
			forbid: []string{"--user-group"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newShellRun(t, accountStubs(nil))
			home := filepath.ToSlash(t.TempDir())
			out, err := s.run(userScript(tc.user), "", append([]string{"HOME=" + home}, tc.env...)...)
			if tc.fails != "" {
				if err == nil || !strings.Contains(out, tc.fails) {
					t.Fatalf("expected failure %q, got %v\n%s", tc.fails, err, out)
				}
			} else if err != nil {
				t.Fatalf("failed: %v\n%s", err, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			for _, forbid := range tc.forbid {
				if strings.Contains(out, forbid) {
					t.Errorf("output must not contain %q:\n%s", forbid, out)
				}
			}
		})
	}
}

func TestUserScriptPublicKeys(t *testing.T) {
	const keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA a@host"
	const keyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB b@host"
	user := profile.User{Name: "app", Home: "/home/app", Shell: "/bin/bash", PublicKeys: []string{keyA, keyB}}
	run := func(t *testing.T, s *shellRun, home string, u profile.User) (string, error) {
		return s.run(userScript(u), "", "HOME="+home, "FIXTURE_EXISTS=app", "FIXTURE_HOME=/home/app", "FIXTURE_SHELL=/bin/bash")
	}

	t.Run("keys are appended once and reruns change nothing", func(t *testing.T) {
		s := newShellRun(t, accountStubs(nil))
		home := filepath.ToSlash(t.TempDir())
		for range 3 {
			if out, err := run(t, s, home, user); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
		}
		got, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
		if err != nil || string(got) != keyA+"\n"+keyB+"\n" {
			t.Fatalf("authorized_keys = %q, %v", got, err)
		}
		if runtime.GOOS != "windows" {
			for path, want := range map[string]os.FileMode{filepath.Join(home, ".ssh"): 0o700, filepath.Join(home, ".ssh", "authorized_keys"): 0o600} {
				if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
					t.Errorf("%s mode = %v, %v; want %v", path, info.Mode().Perm(), err, want)
				}
			}
		}
	})
	t.Run("existing keys and comments are kept", func(t *testing.T) {
		s := newShellRun(t, accountStubs(nil))
		home := filepath.ToSlash(t.TempDir())
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		existing := "# managed by hand\nssh-rsa AAAAexisting old@host\n" + keyA // no final newline
		if err := os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys"), []byte(existing), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := run(t, s, home, user); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		got, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
		if string(got) != existing+"\n"+keyB+"\n" {
			t.Fatalf("authorized_keys = %q", got)
		}
	})
	t.Run("a key that is a prefix of a longer line is still added", func(t *testing.T) {
		s := newShellRun(t, accountStubs(nil))
		home := filepath.ToSlash(t.TempDir())
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		longer := keyA + " extra comment"
		if err := os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys"), []byte(longer+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		single := profile.User{Name: "app", Home: "/home/app", Shell: "/bin/bash", PublicKeys: []string{keyA}}
		if out, err := run(t, s, home, single); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		got, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
		if string(got) != longer+"\n"+keyA+"\n" {
			t.Fatalf("whole-line matching must not treat a longer line as present: %q", got)
		}
	})
	t.Run("symlinked authorized_keys is refused", func(t *testing.T) {
		s := newShellRun(t, accountStubs(nil))
		if !symlinksWork(t, s.sh) {
			t.Skip("symbolic links unavailable")
		}
		home := filepath.ToSlash(t.TempDir())
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(home, "elsewhere")
		if err := os.Symlink(target, filepath.Join(home, ".ssh", "authorized_keys")); err != nil {
			t.Skip(err)
		}
		out, err := run(t, s, home, user)
		if err == nil || !strings.Contains(out, "Refusing symlink authorized_keys") {
			t.Fatalf("%v\n%s", err, out)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("the symlink target must not be written")
		}
	})
	t.Run("symlinked .ssh directory is refused", func(t *testing.T) {
		s := newShellRun(t, accountStubs(nil))
		if !symlinksWork(t, s.sh) {
			t.Skip("symbolic links unavailable")
		}
		home := filepath.ToSlash(t.TempDir())
		elsewhere := filepath.Join(home, "elsewhere")
		if err := os.Mkdir(elsewhere, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(home, ".ssh")); err != nil {
			t.Skip(err)
		}
		out, err := run(t, s, home, user)
		if err == nil || !strings.Contains(out, "Refusing symlink .ssh directory") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
	t.Run("an unusable HOME is refused", func(t *testing.T) {
		s := newShellRun(t, accountStubs(nil))
		// Assign inside the script: Git for Windows' shell refills an empty HOME
		// from the environment, which no Linux target does.
		for _, assign := range []string{"HOME=''", "HOME=/", "HOME='/tmp/a\nb'"} {
			out, err := s.run(assign+"\n"+userScript(user), "", "HOME=/tmp", "FIXTURE_EXISTS=app", "FIXTURE_HOME=/home/app", "FIXTURE_SHELL=/bin/bash")
			if err == nil || !strings.Contains(out, "Invalid target home") {
				t.Errorf("%s: %v\n%s", assign, err, out)
			}
		}
	})
}

func TestTimezoneScriptEdges(t *testing.T) {
	setup := func(t *testing.T, systemd bool) (s *shellRun, tmp func(string) string, script func(string) string) {
		t.Helper()
		stubs := map[string]string{"id": "echo 0\n"}
		root := filepath.ToSlash(t.TempDir())
		stubs["timedatectl"] = `printf 'timedatectl %s\n' "$*"` + "\n"
		s = newShellRun(t, stubs)
		for _, dir := range []string{"zoneinfo/Europe", "etc", "run"} {
			if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, zone := range []string{"UTC", "Europe/Paris"} {
			if err := os.WriteFile(filepath.Join(root, "zoneinfo", zone), []byte("tz"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if systemd {
			if err := os.MkdirAll(filepath.Join(root, "run", "systemd", "system"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		tmp = func(name string) string { return root + "/" + name }
		script = func(tz string) string {
			text := timezoneScript(tz)
			for old, replacement := range map[string]string{
				"/usr/share/zoneinfo": root + "/zoneinfo", "/run/systemd/system": root + "/run/systemd/system",
				"/etc/localtime": root + "/etc/localtime", "/etc/timezone": root + "/etc/timezone",
			} {
				text = strings.ReplaceAll(text, old, replacement)
			}
			return text
		}
		return s, tmp, script
	}

	t.Run("unknown zone fails before changing anything", func(t *testing.T) {
		s, tmp, script := setup(t, true)
		out, err := s.run(script("Mars/Olympus"), "")
		if err == nil || !strings.Contains(out, "Unknown timezone Mars/Olympus") || strings.Contains(out, "timedatectl") {
			t.Fatalf("%v\n%s", err, out)
		}
		if _, err := os.Lstat(tmp("etc/localtime")); !os.IsNotExist(err) {
			t.Fatal("localtime must not be created for an unknown zone")
		}
	})
	t.Run("a directory is not a zone", func(t *testing.T) {
		s, _, script := setup(t, true)
		if out, err := s.run(script("Europe"), ""); err == nil || !strings.Contains(out, "Unknown timezone") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
	t.Run("systemd hosts use timedatectl", func(t *testing.T) {
		s, _, script := setup(t, true)
		out, err := s.run(script("Europe/Paris"), "")
		if err != nil || !strings.Contains(out, "timedatectl set-timezone Europe/Paris") || !strings.Contains(out, "Timezone set to Europe/Paris") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
	t.Run("hosts without systemd link localtime and update /etc/timezone", func(t *testing.T) {
		s, tmp, script := setup(t, false)
		if !symlinksWork(t, s.sh) {
			t.Skip("symbolic links unavailable")
		}
		if err := os.WriteFile(filepath.FromSlash(tmp("etc/timezone")), []byte("Etc/UTC\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.FromSlash(tmp("etc/localtime")), []byte("old regular file"), 0o644); err != nil {
			t.Fatal(err)
		}
		for range 2 { // idempotent
			if out, err := s.run(script("Europe/Paris"), ""); err != nil || strings.Contains(out, "timedatectl") {
				t.Fatalf("%v\n%s", err, out)
			}
		}
		target, err := os.Readlink(filepath.FromSlash(tmp("etc/localtime")))
		if err != nil || !strings.HasSuffix(filepath.ToSlash(target), "zoneinfo/Europe/Paris") {
			t.Fatalf("localtime -> %q, %v", target, err)
		}
		if got, _ := os.ReadFile(filepath.FromSlash(tmp("etc/timezone"))); string(got) != "Europe/Paris\n" {
			t.Fatalf("/etc/timezone = %q", got)
		}
	})
	t.Run("a missing /etc/timezone is not created", func(t *testing.T) {
		s, tmp, script := setup(t, false)
		if !symlinksWork(t, s.sh) {
			t.Skip("symbolic links unavailable")
		}
		if out, err := s.run(script("UTC"), ""); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if _, err := os.Lstat(filepath.FromSlash(tmp("etc/timezone"))); !os.IsNotExist(err) {
			t.Fatal("/etc/timezone must only be rewritten when it already exists")
		}
	})
	t.Run("a real directory at /etc/localtime is refused", func(t *testing.T) {
		s, tmp, script := setup(t, false)
		if err := os.Mkdir(filepath.FromSlash(tmp("etc/localtime")), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := s.run(script("UTC"), ""); err == nil || !strings.Contains(out, "must not be a real directory") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
}

func TestRepositoryConfigScriptEdges(t *testing.T) {
	repo := profile.Repository{Name: "app", URL: "git@github.com:team/app.git"}
	// The EXIT trap that releases the lock and removes temporaries belongs to the
	// surrounding repository script; reuse its exact text so the cleanup it
	// promises is what these tests exercise.
	var trap string
	for line := range strings.SplitSeq(repoScript(profile.Repository{Name: "app", URL: "https://example.com/a.git", Path: "/srv/app", User: "u"}, nil), "\n") {
		if strings.HasPrefix(line, "trap '") {
			trap = line
		}
	}
	if trap == "" {
		t.Fatal("repository script has no EXIT trap")
	}
	run := func(t *testing.T, home string) (string, error) {
		s := newShellRun(t, nil)
		return s.run("tmp=''\nclone_tmp=''\nin_place=''\nconfig_lock=''\n"+trap+"\nmkdir -p \"$HOME/.ssh\"\n"+repositoryConfigScript(repo), "", "HOME="+home)
	}

	t.Run("creates the file with the managed block", func(t *testing.T) {
		home := filepath.ToSlash(t.TempDir())
		if out, err := run(t, home); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		got, _ := os.ReadFile(filepath.Join(home, ".ssh", "config"))
		want := "# BEGIN plonkr app.github.com\nHost app.github.com\n  HostName github.com\n  IdentityFile ~/.ssh/plonkr-app.key\n  IdentitiesOnly yes\n\nHost *\n# END plonkr app.github.com\n"
		if string(got) != want {
			t.Fatalf("config = %q\nwant %q", got, want)
		}
		if _, err := os.Stat(filepath.Join(home, ".ssh", ".plonkr-config.lock")); !os.IsNotExist(err) {
			t.Fatal("the lock directory must be released")
		}
	})
	t.Run("a half-written managed block is rejected and the file is untouched", func(t *testing.T) {
		for name, content := range map[string]string{
			"begin without end": "Host x\n# BEGIN plonkr app.github.com\nHost app.github.com\n",
			"end without begin": "Host x\n# END plonkr app.github.com\n",
			"two begins":        "# BEGIN plonkr app.github.com\n# BEGIN plonkr app.github.com\n# END plonkr app.github.com\n",
		} {
			t.Run(name, func(t *testing.T) {
				home := filepath.ToSlash(t.TempDir())
				if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(home, ".ssh", "config")
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				if out, err := run(t, home); err == nil {
					t.Fatalf("expected failure\n%s", out)
				}
				if got, _ := os.ReadFile(path); string(got) != content {
					t.Fatalf("config was modified: %q", got)
				}
				if _, err := os.Stat(filepath.Join(home, ".ssh", ".plonkr-config.lock")); !os.IsNotExist(err) {
					t.Fatal("the lock directory must be released after a failure")
				}
				if entries, _ := filepath.Glob(filepath.Join(home, ".ssh", ".plonkr-config.*")); len(entries) != 0 {
					t.Fatalf("temporary files left behind: %v", entries)
				}
			})
		}
	})
	t.Run("a stale lock explains how to recover and is left alone", func(t *testing.T) {
		home := filepath.ToSlash(t.TempDir())
		lock := filepath.Join(home, ".ssh", ".plonkr-config.lock")
		if err := os.MkdirAll(lock, 0o700); err != nil {
			t.Fatal(err)
		}
		out, err := run(t, home)
		if err == nil || !strings.Contains(out, "Another SSH config update is active") || !strings.Contains(out, "/.ssh/.plonkr-config.lock and retry.") {
			t.Fatalf("%v\n%s", err, out)
		}
		if _, err := os.Stat(lock); err != nil {
			t.Fatal("a lock owned by someone else must not be removed")
		}
	})
	t.Run("a symlinked config is refused", func(t *testing.T) {
		s := newShellRun(t, nil)
		if !symlinksWork(t, s.sh) {
			t.Skip("symbolic links unavailable")
		}
		home := filepath.ToSlash(t.TempDir())
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(home, "real-config"), filepath.Join(home, ".ssh", "config")); err != nil {
			t.Skip(err)
		}
		if out, err := run(t, home); err == nil || !strings.Contains(out, "must be a regular file, not a symlink") {
			t.Fatalf("%v\n%s", err, out)
		}
	})
}

func TestRepositoryAliasEdges(t *testing.T) {
	for _, tc := range []struct{ name, url, alias, host, port, clone string }{
		{"app", "https://github.com/team/app.git", "app.github.com", "github.com", "", "https://github.com/team/app.git"},
		{"app", "ssh://github.com/team/app.git", "app.github.com", "github.com", "", "ssh://app.github.com/team/app.git"},
		{"app", "ssh://git@Host.Example:22/x.git", "app.Host.Example", "Host.Example", "22", "ssh://git@app.Host.Example:22/x.git"},
		{"a.b", "git@example.com:x/y.git", "a.b.example.com", "example.com", "", "git@a.b.example.com:x/y.git"},
		{"app", "git@example.com:~me/app.git", "app.example.com", "example.com", "", "git@app.example.com:~me/app.git"},
		{"app", "git@[2001:db8::1]:x/y.git", "app.ssh", "2001:db8::1", "", "git@app.ssh:x/y.git"},
		{"app", "ssh://git@[2001:db8::1]/x.git", "app.ssh", "2001:db8::1", "", "ssh://git@app.ssh/x.git"},
	} {
		alias, host, port, clone := repositoryAlias(profile.Repository{Name: tc.name, URL: tc.url})
		if alias != tc.alias || host != tc.host || port != tc.port || clone != tc.clone {
			t.Errorf("%s %s: got alias=%q host=%q port=%q clone=%q", tc.name, tc.url, alias, host, port, clone)
		}
	}
	// Unparseable input yields no alias, which disables the SSH config block.
	for _, url := range []string{"", "nonsense", "git@", "git@x"} {
		if alias, _, _, _ := repositoryAlias(profile.Repository{Name: "app", URL: url}); alias != "" {
			t.Errorf("%q produced alias %q", url, alias)
		}
		if script := repositoryConfigScript(profile.Repository{Name: "app", URL: url}); script != "" {
			t.Errorf("%q produced a config script", url)
		}
	}
}

func TestExecutorTimezoneFallsBackToThePlan(t *testing.T) {
	capture := installFakeSSH(t)
	plan := &profile.Plan{Timezone: "Europe/Paris"}
	executor := Executor{SSH: runner.SSH{Host: "fixture"}, Plan: plan}
	if err := executor.Execute(t.Context(), profile.Step{Op: profile.OpTimezone}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if call := readCapture(t, capture); !strings.Contains(call.Args[len(call.Args)-1], "Europe/Paris") {
		t.Fatalf("remote command = %q", call.Args[len(call.Args)-1])
	}
	if err := (Executor{SSH: runner.SSH{Host: "fixture"}, Plan: &profile.Plan{}}).Execute(t.Context(), profile.Step{Op: profile.OpTimezone}, nil, nil); err == nil || !strings.Contains(err.Error(), "timezone is empty") {
		t.Fatalf("an empty timezone must fail before connecting: %v", err)
	}
}
