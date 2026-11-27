package provision

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

func TestMain(m *testing.M) {
	switch os.Getenv("PLONK_PROV_FAKE_SSH") {
	case "1":
		os.Exit(runFakeProvisionSSH())
	case "exec":
		os.Exit(runLocalExecSSH())
	}
	os.Exit(m.Run())
}

type sshInvocation struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
}

func runFakeProvisionSSH() int {
	input, _ := io.ReadAll(os.Stdin)
	data, err := json.Marshal(sshInvocation{
		Args:  os.Args[1:],
		Stdin: string(input),
	})
	if err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		return 2
	}
	if path := os.Getenv("PLONK_PROV_CAPTURE"); path != "" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			return 2
		}
	}
	return 0
}

func installFakeSSH(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	name := "ssh"
	if runtime.GOOS == "windows" {
		name = "ssh.exe"
	}
	dst := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		data, readErr := os.ReadFile(exe)
		if readErr != nil {
			t.Fatalf("read test executable: %v", readErr)
		}
		if writeErr := os.WriteFile(dst, data, 0700); writeErr != nil {
			t.Fatalf("write fake ssh: %v", writeErr)
		}
	} else if err := os.Link(exe, dst); err != nil {
		data, readErr := os.ReadFile(exe)
		if readErr != nil {
			t.Fatalf("read test executable: %v", readErr)
		}
		if writeErr := os.WriteFile(dst, data, 0700); writeErr != nil {
			t.Fatalf("write fake ssh: %v", writeErr)
		}
	}
	capture := filepath.Join(dir, "capture.json")
	t.Setenv("PLONK_PROV_FAKE_SSH", "1")
	t.Setenv("PLONK_PROV_CAPTURE", capture)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return capture
}

func readCapture(t *testing.T, path string) sshInvocation {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var inv sshInvocation
	if err := json.Unmarshal(raw, &inv); err != nil {
		t.Fatalf("unmarshal capture: %v", err)
	}
	return inv
}

func findPOSIXShell() string {
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	if runtime.GOOS == "windows" {
		for _, candidate := range []string{
			`C:\Program Files\Git\usr\bin\sh.exe`,
			`C:\Program Files\Git\bin\sh.exe`,
		} {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func TestExecutorErrors(t *testing.T) {
	basePlan := &profile.Plan{
		Groups:       map[string]profile.Group{},
		Users:        map[string]profile.User{},
		Repositories: map[string]profile.Repository{"app": {Name: "app", User: "deploy", URL: "git@example.test:org/app.git", Path: "/srv/app", Key: "/local/id_ed25519"}},
		Units:        map[string]profile.Unit{},
	}

	tests := []struct {
		name    string
		exec    Executor
		step    profile.Step
		wantErr string
	}{
		{
			name:    "nil plan",
			exec:    Executor{SSH: runner.SSH{Host: "fixture"}},
			step:    profile.Step{Op: profile.OpGroup, Resource: "app"},
			wantErr: "nil",
		},
		{
			name:    "unknown group",
			exec:    Executor{SSH: runner.SSH{Host: "fixture"}, Plan: basePlan},
			step:    profile.Step{Op: profile.OpGroup, Resource: "missing"},
			wantErr: "unknown group",
		},
		{
			name:    "unknown user",
			exec:    Executor{SSH: runner.SSH{Host: "fixture"}, Plan: basePlan},
			step:    profile.Step{Op: profile.OpUser, Resource: "missing"},
			wantErr: "unknown user",
		},
		{
			name:    "empty timezone",
			exec:    Executor{SSH: runner.SSH{Host: "fixture"}, Plan: basePlan},
			step:    profile.Step{Op: profile.OpTimezone},
			wantErr: "timezone",
		},
		{
			name:    "unknown repo",
			exec:    Executor{SSH: runner.SSH{Host: "fixture"}, Plan: basePlan},
			step:    profile.Step{Op: profile.OpRepo, Resource: "missing"},
			wantErr: "unknown repository",
		},
		{
			name:    "missing deploy key bytes",
			exec:    Executor{SSH: runner.SSH{Host: "fixture"}, Plan: basePlan},
			step:    profile.Step{Op: profile.OpRepo, Resource: "app"},
			wantErr: "missing prepared deploy key",
		},
		{
			name:    "empty deploy key bytes",
			exec:    Executor{SSH: runner.SSH{Host: "fixture"}, Plan: basePlan, Keys: map[string][]byte{"/local/id_ed25519": nil}},
			step:    profile.Step{Op: profile.OpRepo, Resource: "app"},
			wantErr: "missing prepared deploy key",
		},
		{
			name:    "unknown unit",
			exec:    Executor{SSH: runner.SSH{Host: "fixture"}, Plan: basePlan},
			step:    profile.Step{Op: profile.OpUnit, Resource: "missing"},
			wantErr: "unknown unit",
		},
		{
			name:    "unsupported op",
			exec:    Executor{SSH: runner.SSH{Host: "fixture"}, Plan: basePlan},
			step:    profile.Step{Op: profile.Op("bogus")},
			wantErr: "unsupported operation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			err := tc.exec.Execute(t.Context(), tc.step, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestExecutorDispatchAndSecretIsolation(t *testing.T) {
	secret := []byte("PRIVATE-KEY-SENTINEL-BYTES\r\n")
	unitContent := "[Unit]\nDescription=UNIT_SENTINEL\n[Service]\nExecStart=/bin/true\n"
	plan := &profile.Plan{
		Timezone: "UTC",
		Groups: map[string]profile.Group{
			"app": {Name: "app", GID: new(2000), Create: true},
		},
		Users: map[string]profile.User{
			"deploy": {Name: "deploy", Home: "/var/lib/deploy", Shell: "/usr/sbin/nologin", Service: true, Groups: []string{"app"}},
		},
		Repositories: map[string]profile.Repository{
			"api": {
				Name: "api", User: "deploy", URL: "git@example.test:org/api.git",
				Path: "/srv/api", Key: "/local/keys/api", KnownHosts: []string{"example.test ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI"},
			},
			"pub": {
				Name: "pub", User: "deploy", URL: "https://example.test/org/pub.git",
				Path: "/srv/pub",
			},
		},
		Units: map[string]profile.Unit{
			"api": {Name: "api.service", Content: unitContent},
		},
	}

	tests := []struct {
		name        string
		step        profile.Step
		wantTTY     bool
		wantStdin   string
		forbidInArg string
	}{
		{
			name:    "shell respects tty",
			step:    profile.Step{Op: profile.OpShell, Text: "echo ok"},
			wantTTY: true,
		},
		{
			name: "group uses noninteractive input",
			step: profile.Step{Op: profile.OpGroup, Resource: "app"},
		},
		{
			name: "user uses noninteractive input",
			step: profile.Step{Op: profile.OpUser, Resource: "deploy"},
		},
		{
			name: "timezone uses noninteractive input",
			step: profile.Step{Op: profile.OpTimezone, Resource: "Europe/Berlin"},
		},
		{
			name:        "repo key travels only on stdin without pty",
			step:        profile.Step{Op: profile.OpRepo, Resource: "api"},
			wantStdin:   string(secret),
			forbidInArg: "PRIVATE-KEY-SENTINEL-BYTES",
		},
		{
			name: "https repo without key sends empty stdin",
			step: profile.Step{Op: profile.OpRepo, Resource: "pub"},
		},
		{
			name:        "unit content travels only on stdin without pty",
			step:        profile.Step{Op: profile.OpUnit, Resource: "api"},
			wantStdin:   unitContent,
			forbidInArg: "UNIT_SENTINEL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capturePath := installFakeSSH(t)
			exec := Executor{
				SSH:  runner.SSH{Host: "fixture", TTY: true},
				Plan: plan,
				Keys: map[string][]byte{"/local/keys/api": secret},
			}
			var out, errOut bytes.Buffer
			if err := exec.Execute(t.Context(), tc.step, &out, &errOut); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			inv := readCapture(t, capturePath)
			hasTTY := false
			hasBatch := false
			for _, arg := range inv.Args {
				if arg == "-t" {
					hasTTY = true
				}
				if arg == "BatchMode=yes" {
					hasBatch = true
				}
			}
			if hasTTY != tc.wantTTY {
				t.Errorf("hasTTY = %v, want %v (args=%q)", hasTTY, tc.wantTTY, inv.Args)
			}
			if !tc.wantTTY && !hasBatch {
				t.Errorf("missing BatchMode=yes in args=%q", inv.Args)
			}
			if inv.Stdin != tc.wantStdin {
				t.Errorf("stdin = %q, want %q", inv.Stdin, tc.wantStdin)
			}
			if tc.forbidInArg != "" {
				joined := strings.Join(inv.Args, " ")
				if strings.Contains(joined, tc.forbidInArg) {
					t.Errorf("sensitive value %q leaked into argv", tc.forbidInArg)
				}
				if strings.Contains(out.String(), tc.forbidInArg) || strings.Contains(errOut.String(), tc.forbidInArg) {
					t.Errorf("sensitive value %q leaked into stdio", tc.forbidInArg)
				}
			}
		})
	}
}

func TestGeneratedScriptsSyntaxAndContracts(t *testing.T) {
	key := []byte("opaque-key-bytes\n")
	keySum := fmt.Sprintf("%x", sha256.Sum256(key))
	commitSHA1 := "0123456789abcdef0123456789abcdef01234567"
	commitSHA256 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		name   string
		script string
		want   []string
		forbid []string
	}{
		{
			name:   "group without gid",
			script: groupScript(profile.Group{Name: "devs"}),
			want:   []string{"sudo -n -- sh -ec", "getent group", "groupadd --", "Group %s ready (existing)", "Group %s created"},
			forbid: []string{"groupadd -g"},
		},
		{
			name:   "group with gid",
			script: groupScript(profile.Group{Name: "devs", GID: new(1500)}),
			want:   []string{"1500", "actual_gid", "groupadd -g", "already in use", "Group %s ready (existing)", "Group %s created"},
		},
		{
			name: "user service with groups and keys",
			script: userScript(profile.User{
				Name: "deploy", Home: "/var/lib/deploy", Shell: "/usr/sbin/nologin",
				Service: true, Groups: []string{"www-data", "app"},
				PublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test@host"},
			}),
			want: []string{
				"Refusing to modify a UID 0 account",
				"getent passwd",
				"if getent group \"$name\" >/dev/null 2>&1; then",
				"useradd --create-home -g \"$name\"",
				"useradd --create-home --user-group",
				"--system",
				"getent group",
				"www-data",
				"usermod -a -G",
				"www-data,app",
				"Invalid target home",
				"Refusing symlink .ssh directory",
				"Refusing symlink authorized_keys",
				"chmod 700",
				"chmod 600",
				"grep -qxF",
			},
		},
		{
			name:   "timezone with systemd and posix fallback",
			script: timezoneScript("Europe/Berlin"),
			want: []string{
				"/usr/share/zoneinfo/$tz",
				"timedatectl",
				"/run/systemd/system",
				"timedatectl set-timezone",
				"[ -L /etc/localtime ]",
				"rm -f -- /etc/localtime",
				"ln -s --",
				"/etc/localtime",
				"/etc/timezone",
			},
			forbid: []string{"ln -snf"},
		},
		{
			name: "repo with deploy key and branch",
			script: repoScript(profile.Repository{
				Name: "api", URL: "git@example.test:org/api.git", Path: "/srv/api",
				User: "deploy", Key: "/local/api.key", Ref: "main",
				KnownHosts: []string{"example.test ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI"},
			}, key),
			want: []string{
				"command -v git",
				"Invalid target home",
				"known_hosts",
				"grep -qxF",
				keySum,
				`keyfile="$HOME/.ssh/plonkr-$repo_name.key"`,
				`mktemp "$HOME/.ssh/.plonkr-key.XXXXXX"`,
				"sha256sum",
				"cmp -s",
				`ln -T -- "$tmp" "$keyfile"`,
				"chmod 600",
				"IdentitiesOnly=yes",
				"BatchMode=yes",
				"StrictHostKeyChecking=no",
				"UserKnownHostsFile=",
				"rev-parse --is-inside-work-tree",
				"remote get-url origin",
				"rev-parse --verify HEAD",
				"symbolic-ref --quiet --short HEAD",
				"Repository %s already present and verified",
				`clone --branch "$repo_ref"`,
				"config core.sshCommand",
			},
			forbid: []string{
				".ssh/viac",
				".ssh/plonkr/",
				"-F /dev/null",
				"IdentityAgent",
				"ssh-agent",
				"VIAC_SSH_WRAPPER",
				"git fetch",
				"git pull",
			},
		},
		{
			name: "repo with sha1 commit hash",
			script: repoScript(profile.Repository{
				Name: "api", URL: "https://example.test/org/api.git", Path: "/srv/api",
				User: "deploy", Ref: commitSHA1,
			}, nil),
			want: []string{
				"ssh_command='ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o GlobalKnownHostsFile=/dev/null'",
				"clone --no-checkout",
				"checkout --detach",
				`mv -T -- "$clone_tmp" "$destination"`,
				commitSHA1,
			},
			forbid: []string{"clone --branch", "config core.sshCommand", "mkdir -p -- \"$HOME/.ssh\""},
		},
		{
			name: "repo with sha256 commit hash",
			script: repoScript(profile.Repository{
				Name: "api", URL: "https://example.test/org/api.git", Path: "/srv/api",
				User: "deploy", Ref: commitSHA256,
			}, nil),
			want:   []string{"clone --no-checkout", "checkout --detach", `mv -T -- "$clone_tmp" "$destination"`, commitSHA256},
			forbid: []string{"clone --branch"},
		},
		{
			name:   "unit script uses atomic mv -fT",
			script: unitScript(profile.Unit{Name: "app.service", Content: "[Service]\nExecStart=/bin/true\n"}),
			want: []string{
				"sudo -n -- sh -ec",
				"/etc/systemd/system",
				".plonkr-unit.XXXXXX",
				"expected=30",
				"wc -c",
				"chown root:root",
				"chmod 0644",
				`mv -fT -- "$tmp" "$destination"`,
			},
			forbid: []string{"ExecStart=/bin/true"},
		},
	}

	sh := findPOSIXShell()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, w := range tc.want {
				if !strings.Contains(tc.script, w) {
					t.Errorf("script missing %q\nscript:\n%s", w, tc.script)
				}
			}
			for _, f := range tc.forbid {
				if strings.Contains(tc.script, f) {
					t.Errorf("script must not contain %q\nscript:\n%s", f, tc.script)
				}
			}
			if sh != "" {
				cmd := exec.CommandContext(t.Context(), sh, "-n")
				cmd.Stdin = strings.NewReader(tc.script)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("sh -n failed: %v\n%s\nscript:\n%s", err, out, tc.script)
				}
			}
		})
	}
}

func posixEnv(sh string, extra ...string) []string {
	shDir := filepath.Dir(sh)
	var env []string
	for _, kv := range os.Environ() {
		upper := strings.ToUpper(kv)
		if strings.HasPrefix(upper, "HOME=") {
			continue
		}
		if strings.HasPrefix(upper, "PATH=") {
			idx := strings.IndexByte(kv, '=')
			env = append(env, kv[:idx+1]+shDir+string(os.PathListSeparator)+kv[idx+1:])
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// Prepend fixture commands after the shell has imported the Windows environment.
// Git Bash needs a colon-delimited POSIX PATH, not a Windows drive/path list.
// Export it so nested sh invocations in the provisioning scripts use the mocks too.
func withPOSIXTools(tools, script string) string {
	return "fixture_tools=" + quote(filepath.ToSlash(tools)) + `
if command -v cygpath >/dev/null 2>&1; then
  fixture_tools=$(cygpath -u "$fixture_tools")
fi
PATH="$fixture_tools:$PATH"
export PATH
` + script
}

func TestPOSIXToolsReachNestedShell(t *testing.T) {
	sh := findPOSIXShell()
	if sh == "" {
		t.Skip("no POSIX shell")
	}
	tools := filepath.Join(t.TempDir(), "mock tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "id"), []byte("#!/bin/sh\nprintf 'fixture-root\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), sh, "-ec", withPOSIXTools(tools, `test "$(id -u)" = fixture-root; sh -ec 'test "$(id -u)" = fixture-root'`))
	cmd.Env = posixEnv(sh)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nested shell bypassed mock commands: %v: %s", err, out)
	}
}

func TestRepoScriptPOSIXExecution(t *testing.T) {
	sh := findPOSIXShell()
	if sh == "" {
		t.Skip("no POSIX sh available")
	}

	t.Run("rejects invalid HOME values", func(t *testing.T) {
		script := repoScript(profile.Repository{
			Name: "app", URL: "https://example.test/org/app.git", Path: "/srv/app", User: "deploy",
		}, nil)
		cases := []struct {
			name   string
			assign string
		}{
			{name: "empty", assign: `HOME=''`},
			{name: "root", assign: `HOME='/'`},
			{name: "newline", assign: "HOME='/tmp/bad\nhome'"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), sh, "-ec", tc.assign+"\n"+script)
				cmd.Env = posixEnv(sh)
				out, err := cmd.CombinedOutput()
				if err == nil {
					t.Fatalf("succeeded, want error (out=%s)", out)
				}
				if !strings.Contains(string(out), "Invalid target home") {
					t.Errorf("output=%q, want 'Invalid target home'", out)
				}
			})
		}
	})

	t.Run("escapes single quotes in ssh_command", func(t *testing.T) {
		script := repoScript(profile.Repository{
			Name: "app", URL: "git@example.test:org/app.git", Path: "/srv/app",
			User: "deploy", Key: "/local/app.key",
		}, []byte("test-key"))
		start := strings.Index(script, "escape_sq()")
		end := strings.Index(script, "# Repository-specific SSH configuration")
		if start < 0 || end <= start {
			t.Fatalf("could not locate escape_sq block in script:\n%s", script)
		}
		snippet := "HOME=\"/tmp/o'brien\"\nrepo_name='app'\nkeyfile=\"$HOME/.ssh/plonkr-$repo_name.key\"\n" +
			script[start:end] +
			"\neval \"set -- $ssh_command\"\nprintf '%s\\n' \"$3\" \"${11}\"\n"

		cmd := exec.CommandContext(t.Context(), sh, "-ec", snippet)
		cmd.Env = posixEnv(sh)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("sh failed: %v: %s", err, out)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) != 2 {
			t.Fatalf("got lines %q", lines)
		}
		if lines[0] != "/tmp/o'brien/.ssh/plonkr-app.key" {
			t.Errorf("key arg = %q", lines[0])
		}
		if lines[1] != "UserKnownHostsFile=/dev/null" {
			t.Errorf("known_hosts arg = %q", lines[1])
		}
	})

	t.Run("key installation checksum conflict and existing repo verification", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not installed")
		}
		base := t.TempDir()
		home := filepath.ToSlash(filepath.Join(base, "home"))
		if err := os.MkdirAll(home, 0700); err != nil {
			t.Fatal(err)
		}
		dest := filepath.ToSlash(filepath.Join(base, "checkout"))
		repoURL := "git@example.test:org/app.git"

		// Pre-create a valid git work tree at dest with matching origin URL and checked-out commit.
		for _, args := range [][]string{
			{"init", dest},
			{"-C", dest, "remote", "add", "origin", repoURL},
			{"-C", dest, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "init"},
		} {
			cmd := exec.CommandContext(t.Context(), "git", args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		branchOut, err := exec.CommandContext(t.Context(), "git", "-C", dest, "symbolic-ref", "--quiet", "--short", "HEAD").CombinedOutput()
		if err != nil {
			t.Fatalf("git symbolic-ref: %v: %s", err, branchOut)
		}
		headBranch := strings.TrimSpace(string(branchOut))

		keyBytes := []byte("opaque-private-key-bytes\r\nline-two\r\n")
		repo := profile.Repository{
			Name:       "your-repo",
			URL:        repoURL,
			Path:       dest,
			User:       "deploy",
			Key:        "/local/your-repo",
			KnownHosts: []string{"example.test ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI"},
		}
		script := repoScript(repo, keyBytes)

		runScript := func(stdin []byte) (string, error) {
			cmd := exec.CommandContext(t.Context(), sh, "-ec", script)
			cmd.Env = posixEnv(sh, "HOME="+home)
			cmd.Stdin = bytes.NewReader(stdin)
			out, err := cmd.CombinedOutput()
			return string(out), err
		}

		// 1. Corrupt transfer fails checksum and installs nothing.
		out, err := runScript(append(bytes.Clone(keyBytes), 'X'))
		if err == nil || !strings.Contains(out, "checksum mismatch") {
			t.Fatalf("corrupt transfer: err=%v out=%q", err, out)
		}
		keyFile := filepath.Join(base, "home", ".ssh", "plonkr-your-repo.key")
		if _, statErr := os.Stat(keyFile); !os.IsNotExist(statErr) {
			t.Fatalf("corrupt transfer left key file: %v", statErr)
		}

		// 2. Clean transfer installs key directly in .ssh/plonkr-<name>.key and verifies existing repo.
		out, err = runScript(keyBytes)
		if err != nil {
			t.Fatalf("clean run failed: %v: %s", err, out)
		}
		if !strings.Contains(out, "Repository your-repo already present and verified") {
			t.Errorf("unexpected output: %q", out)
		}
		installed, err := os.ReadFile(keyFile)
		if err != nil || !bytes.Equal(installed, keyBytes) {
			t.Fatalf("installed key mismatch: err=%v got=%q", err, installed)
		}
		configFile := filepath.Join(base, "home", ".ssh", "config")
		// This checkout started with the original host URL. Setup must migrate
		// its saved origin automatically so users keep the original profile URL.
		origin, err := exec.CommandContext(t.Context(), "git", "-C", dest, "remote", "get-url", "origin").CombinedOutput()
		if err != nil || strings.TrimSpace(string(origin)) != "git@your-repo.example.test:org/app.git" {
			t.Fatalf("existing origin was not rewritten to the SSH alias: %v: %s", err, origin)
		}
		config, err := os.ReadFile(configFile)
		if err != nil || !strings.Contains(string(config), "Host your-repo.example.test\n  HostName example.test\n  IdentityFile ~/.ssh/plonkr-your-repo.key") {
			t.Fatalf("SSH config mismatch: %v: %s", err, config)
		}

		// Verify core.sshCommand was set directly without wrapper or -F /dev/null.
		cfgOut, err := exec.CommandContext(t.Context(), "git", "-C", dest, "config", "--get", "core.sshCommand").CombinedOutput()
		if err != nil {
			t.Fatalf("git config core.sshCommand: %v: %s", err, cfgOut)
		}
		sshCmd := strings.TrimSpace(string(cfgOut))
		if !strings.Contains(sshCmd, "plonkr-your-repo.key") || !strings.Contains(sshCmd, "IdentitiesOnly=yes") || strings.Contains(sshCmd, "-F /dev/null") {
			t.Errorf("unexpected core.sshCommand = %q", sshCmd)
		}

		// 3. Repeat run with identical key succeeds and deduplicates known_hosts.
		if out, err = runScript(keyBytes); err != nil {
			t.Fatalf("repeat run failed: %v: %s", err, out)
		}
		repeatedConfig, err := os.ReadFile(configFile)
		if err != nil || !bytes.Equal(config, repeatedConfig) {
			t.Fatal("repeat run changed or duplicated SSH config", err)
		}
		knownData, err := os.ReadFile(filepath.Join(base, "home", ".ssh", "known_hosts"))
		if err != nil || strings.Count(string(knownData), "example.test") != 1 {
			t.Fatalf("known_hosts not deduplicated: err=%v data=%q", err, knownData)
		}

		// 4. Different key bytes are refused without overwriting existing key.
		otherKey := []byte("different-key-bytes\n")
		otherScript := repoScript(repo, otherKey)
		cmd := exec.CommandContext(t.Context(), sh, "-ec", otherScript)
		cmd.Env = posixEnv(sh, "HOME="+home)
		cmd.Stdin = bytes.NewReader(otherKey)
		outBytes, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(outBytes), "Different key file already exists") {
			t.Fatalf("conflict run: err=%v out=%q", err, outBytes)
		}
		stillOriginal, _ := os.ReadFile(keyFile)
		if !bytes.Equal(stillOriginal, keyBytes) {
			t.Fatal("conflicting run modified existing key file")
		}
		// Approval is bound to the exact old bytes, and replacement is atomic.
		for _, approval := range []struct {
			hash    string
			allowed bool
		}{
			{strings.Repeat("0", 64), false},
			{fmt.Sprintf("%x", sha256.Sum256(keyBytes)), true},
		} {
			replaceCmd := exec.CommandContext(t.Context(), sh, "-ec", repoScriptReplacement(repo, otherKey, approval.hash))
			replaceCmd.Env = posixEnv(sh, "HOME="+home)
			replaceCmd.Stdin = bytes.NewReader(otherKey)
			out, replaceErr := replaceCmd.CombinedOutput()
			if (replaceErr == nil) != approval.allowed {
				t.Fatalf("replacement allowed=%v: %v: %s", approval.allowed, replaceErr, out)
			}
			got, err := os.ReadFile(keyFile)
			want := keyBytes
			if approval.allowed {
				want = otherKey
			}
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("replacement bytes differ", err)
			}
		}
		if err := os.WriteFile(keyFile, keyBytes, 0600); err != nil {
			t.Fatal(err)
		}

		// 5. Mismatched origin URL fails cleanly.
		mismatchRepo := repo
		mismatchRepo.URL = "git@example.test:org/other.git"
		mismatchCmd := exec.CommandContext(t.Context(), sh, "-ec", repoScript(mismatchRepo, keyBytes))
		mismatchCmd.Env = posixEnv(sh, "HOME="+home)
		mismatchCmd.Stdin = bytes.NewReader(keyBytes)
		outBytes, err = mismatchCmd.CombinedOutput()
		if err == nil || !strings.Contains(string(outBytes), "has origin") {
			t.Fatalf("origin mismatch: err=%v out=%q", err, outBytes)
		}

		// 6. Existing repo verifies matching ref and rejects mismatched ref.
		matchRefRepo := repo
		matchRefRepo.Ref = headBranch
		matchCmd := exec.CommandContext(t.Context(), sh, "-ec", repoScript(matchRefRepo, keyBytes))
		matchCmd.Env = posixEnv(sh, "HOME="+home)
		matchCmd.Stdin = bytes.NewReader(keyBytes)
		if outBytes, err = matchCmd.CombinedOutput(); err != nil {
			t.Fatalf("matching branch ref failed: %v: %s", err, outBytes)
		}
		wrongRefRepo := repo
		wrongRefRepo.Ref = "nonexistent-branch"
		wrongCmd := exec.CommandContext(t.Context(), sh, "-ec", repoScript(wrongRefRepo, keyBytes))
		wrongCmd.Env = posixEnv(sh, "HOME="+home)
		wrongCmd.Stdin = bytes.NewReader(keyBytes)
		outBytes, err = wrongCmd.CombinedOutput()
		if err == nil || !strings.Contains(string(outBytes), "not on requested ref") {
			t.Fatalf("expected ref mismatch error, got err=%v out=%q", err, outBytes)
		}

		// 7. No temporary .plonkr-key.* files left behind.
		temps, err := filepath.Glob(filepath.Join(base, "home", ".ssh", ".plonkr-key.*"))
		if err != nil || len(temps) != 0 {
			t.Fatalf("leftover temp files: %v %v", temps, err)
		}
	})

	t.Run("failed commit checkout cleans staging and leaves no destination", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not installed")
		}
		base := t.TempDir()
		home := filepath.ToSlash(filepath.Join(base, "home"))
		if err := os.MkdirAll(home, 0700); err != nil {
			t.Fatal(err)
		}
		upstream := filepath.ToSlash(filepath.Join(base, "upstream"))
		for _, args := range [][]string{
			{"init", upstream},
			{"-C", upstream, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "init"},
		} {
			if out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}

		dest := filepath.ToSlash(filepath.Join(base, "failed-commit"))
		badCommit := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
		script := repoScript(profile.Repository{
			Name: "badcommit",
			URL:  "https://example.invalid/org/repo.git",
			Path: dest,
			User: "deploy",
			Ref:  badCommit,
		}, nil)
		// Redirect clone URL locally via -c url.<upstream>.insteadOf so git clones without network.
		wrapped := "git() { command git -c url." + quote(upstream) + ".insteadOf=" + quote("https://example.invalid/org/repo.git") + " \"$@\"; }\n" + script
		cmd := exec.CommandContext(t.Context(), sh, "-ec", wrapped)
		cmd.Env = posixEnv(sh, "HOME="+home)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("expected failed commit checkout, got success:\n%s", out)
		}
		if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
			t.Fatalf("failed commit checkout left destination %s behind: %v", dest, statErr)
		}
		leftovers, _ := filepath.Glob(filepath.Join(base, ".plonkr-clone.*"))
		if len(leftovers) != 0 {
			t.Fatalf("leftover staging dirs: %v", leftovers)
		}
	})

	t.Run("user script reuses existing same-name group", func(t *testing.T) {
		tools := t.TempDir()
		stubs := map[string]string{
			"id": `#!/bin/sh
if [ "$1" = "-u" ] && [ "$2" = "app" ]; then exit 1; fi
if [ "$1" = "-u" ]; then echo 0; exit 0; fi
echo root
`,
			"getent": `#!/bin/sh
if [ "$1" = "group" ] && [ "$2" = "app" ]; then echo 'app:x:1001:'; exit 0; fi
exit 2
`,
			"useradd": "#!/bin/sh\nprintf 'useradd %s\\n' \"$*\"\n",
			"usermod": "#!/bin/sh\nprintf 'usermod %s\\n' \"$*\"\n",
		}
		for name, body := range stubs {
			if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
		}

		script := userScript(profile.User{Name: "app", Home: "/var/lib/app", Shell: "/usr/sbin/nologin", Service: true, Groups: []string{"app"}})
		cmd := exec.CommandContext(t.Context(), sh, "-ec", script)
		cmd.Env = posixEnv(sh)
		cmd.Args[len(cmd.Args)-1] = withPOSIXTools(tools, cmd.Args[len(cmd.Args)-1])
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("user script failed: %v: %s", err, out)
		}
		if !strings.Contains(string(out), "useradd --create-home -g app ") {
			t.Fatalf("expected useradd -g app, got:\n%s", out)
		}
		if strings.Contains(string(out), "--user-group") {
			t.Fatalf("must not pass --user-group when group exists, got:\n%s", out)
		}
	})
}

func TestAccountsAndUnitPOSIXExecution(t *testing.T) {
	sh := findPOSIXShell()
	if sh == "" {
		t.Skip("no POSIX sh available")
	}

	t.Run("group script handles existing mismatch gid conflict and create", func(t *testing.T) {
		tools := t.TempDir()
		stubs := map[string]string{
			"id": "#!/bin/sh\necho 0\n",
			"getent": `#!/bin/sh
case "$2" in
  existing) echo 'existing:x:1200:' ;;
  1300) echo 'other:x:1300:' ;;
  *) exit 2 ;;
esac
`,
			"groupadd": "#!/bin/sh\nprintf 'groupadd %s\\n' \"$*\"\n",
		}
		for name, body := range stubs {
			if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
		}

		tests := []struct {
			name    string
			group   profile.Group
			wantOut string
			wantErr bool
		}{
			{
				name:    "existing group matching gid",
				group:   profile.Group{Name: "existing", GID: new(1200)},
				wantOut: "Group existing ready (existing)",
			},
			{
				name:    "existing group mismatched gid fails",
				group:   profile.Group{Name: "existing", GID: new(9999)},
				wantOut: "has GID 1200, expected 9999",
				wantErr: true,
			},
			{
				name:    "new group with conflicting gid fails",
				group:   profile.Group{Name: "fresh", GID: new(1300)},
				wantOut: "already in use",
				wantErr: true,
			},
			{
				name:    "new group with free gid created",
				group:   profile.Group{Name: "fresh", GID: new(1400)},
				wantOut: "Group fresh created",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), sh, "-ec", groupScript(tc.group))
				cmd.Env = posixEnv(sh)
				cmd.Args[len(cmd.Args)-1] = withPOSIXTools(tools, cmd.Args[len(cmd.Args)-1])
				out, err := cmd.CombinedOutput()
				if (err != nil) != tc.wantErr {
					t.Fatalf("err=%v wantErr=%v out=%s", err, tc.wantErr, out)
				}
				if !strings.Contains(string(out), tc.wantOut) {
					t.Errorf("out=%q, want substring %q", out, tc.wantOut)
				}
			})
		}
	})

	t.Run("unit script installs intact and rejects truncated input", func(t *testing.T) {
		base := t.TempDir()
		unitDir := filepath.ToSlash(filepath.Join(base, "units"))
		tools := t.TempDir()
		if err := os.MkdirAll(unitDir, 0700); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{
			"id":    "#!/bin/sh\necho 0\n",
			"chown": "#!/bin/sh\nexit 0\n",
		} {
			if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
		}

		unit := profile.Unit{Name: "demo.service", Content: "[Service]\nExecStart=/bin/true\n"}
		script := strings.ReplaceAll(unitScript(unit), "/etc/systemd/system", unitDir)
		runUnit := func(input string) (string, error) {
			cmd := exec.CommandContext(t.Context(), sh, "-ec", script)
			cmd.Env = posixEnv(sh)
			cmd.Args[len(cmd.Args)-1] = withPOSIXTools(tools, cmd.Args[len(cmd.Args)-1])
			cmd.Stdin = strings.NewReader(input)
			out, err := cmd.CombinedOutput()
			return string(out), err
		}

		if out, err := runUnit(unit.Content); err != nil {
			t.Fatalf("valid install failed: %v: %s", err, out)
		}
		got, err := os.ReadFile(filepath.Join(base, "units", "demo.service"))
		if err != nil || string(got) != unit.Content {
			t.Fatalf("installed unit mismatch: err=%v got=%q", err, got)
		}

		if out, err := runUnit(unit.Content[:len(unit.Content)-1]); err == nil || !strings.Contains(out, "Incomplete unit transfer") {
			t.Fatalf("truncated install: err=%v out=%q", err, out)
		}
		// Existing unit must remain intact after failed transfer.
		still, err := os.ReadFile(filepath.Join(base, "units", "demo.service"))
		if err != nil || string(still) != unit.Content {
			t.Fatalf("existing unit damaged after failed transfer: err=%v got=%q", err, still)
		}
	})
}

// A checkout directory prepared for the repository user is empty and exists, so
// the script must clone into it rather than treating it as a foreign directory.
func TestRepoScriptClonesIntoPreparedEmptyDirectory(t *testing.T) {
	sh := findPOSIXShell()
	if sh == "" {
		t.Skip("no POSIX sh available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	gitRun := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	base := t.TempDir()
	home := filepath.ToSlash(filepath.Join(base, "home"))
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.ToSlash(filepath.Join(base, "source"))
	gitRun("init", "-b", "main", source)
	gitRun("-C", source, "-c", "user.name=T", "-c", "user.email=t@example.test", "commit", "--allow-empty", "-m", "one")
	gitRun("-C", source, "tag", "v1")
	commit := gitRun("-C", source, "rev-parse", "HEAD")

	run := func(repo profile.Repository) (string, error) {
		cmd := exec.CommandContext(t.Context(), sh, "-ec", repoScript(repo, nil))
		cmd.Env = posixEnv(sh, "HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	prepared := func(name string) string {
		dir := filepath.ToSlash(filepath.Join(base, name))
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	for _, tc := range []struct{ name, ref string }{{"default branch", ""}, {"branch", "main"}, {"tag", "v1"}, {"commit", commit}} {
		t.Run(tc.name, func(t *testing.T) {
			dest := prepared("dest-" + strings.ReplaceAll(tc.name, " ", "-"))
			repo := profile.Repository{Name: "app", URL: source, Path: dest, User: "deploy", Ref: tc.ref}
			if out, err := run(repo); err != nil || !strings.Contains(out, "Repository app ready") {
				t.Fatalf("first run: err=%v out=%s", err, out)
			}
			if got := gitRun("-C", dest, "rev-parse", "HEAD"); got != commit {
				t.Fatalf("HEAD = %s, want %s", got, commit)
			}
			if out, err := run(repo); err != nil || !strings.Contains(out, "already present and verified") {
				t.Fatalf("second run: err=%v out=%s", err, out)
			}
		})
	}

	t.Run("failed commit checkout empties the prepared directory", func(t *testing.T) {
		dest := prepared("dest-failure")
		missing := strings.Repeat("a", 40)
		out, err := run(profile.Repository{Name: "app", URL: source, Path: dest, User: "deploy", Ref: missing})
		if err == nil {
			t.Fatalf("checkout of a missing commit succeeded: %s", out)
		}
		entries, readErr := os.ReadDir(dest)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("prepared directory not emptied: err=%v entries=%v", readErr, entries)
		}
		// The directory is reusable once the ref is valid.
		if out, err := run(profile.Repository{Name: "app", URL: source, Path: dest, User: "deploy", Ref: commit}); err != nil {
			t.Fatalf("retry failed: %v: %s", err, out)
		}
	})

	t.Run("non-empty directory without a repository is still refused", func(t *testing.T) {
		dest := prepared("dest-occupied")
		if err := os.WriteFile(filepath.Join(dest, "file"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := run(profile.Repository{Name: "app", URL: source, Path: dest, User: "deploy"})
		if err == nil || !strings.Contains(out, "not a git repository root") {
			t.Fatalf("err=%v out=%s", err, out)
		}
	})
}

func TestCheckoutDirScriptContract(t *testing.T) {
	repo := profile.Repository{Name: "app", URL: "https://example.test/app.git", Path: "/srv/it's app", User: "deploy"}
	script := checkoutDirScript(repo)
	for _, want := range []string{
		`if [ "$(id -u)" = 0 ]`, "sudo -n true", "runuser -u", "install -d -m 0755 -o",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
	if sh := findPOSIXShell(); sh != "" {
		if out, err := exec.CommandContext(t.Context(), sh, "-n", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("syntax check: %v: %s", err, out)
		}
	}
}

// Hand-edited authorized_keys and known_hosts files often lack a final newline.
func TestAppendLineTerminatesUnfinishedLastLine(t *testing.T) {
	sh := findPOSIXShell()
	if sh == "" {
		t.Skip("no POSIX sh available")
	}
	file := filepath.ToSlash(filepath.Join(t.TempDir(), "authorized_keys"))
	if err := os.WriteFile(file, []byte("ssh-ed25519 AAAA first"), 0600); err != nil {
		t.Fatal(err)
	}
	script := appendLineFunc + "append_line 'ssh-ed25519 BBBB second' " + quote(file) + "\n" +
		"append_line 'ssh-ed25519 BBBB second' " + quote(file) + "\n" +
		"append_line 'ssh-ed25519 AAAA first' " + quote(file) + "\n"
	cmd := exec.CommandContext(t.Context(), sh, "-ec", script)
	cmd.Env = posixEnv(sh)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ssh-ed25519 AAAA first\nssh-ed25519 BBBB second\n"; string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

// Private key material is written under umask 077; the checkout must not inherit it.
func TestRepoScriptCheckoutKeepsCallerUmask(t *testing.T) {
	sh := findPOSIXShell()
	if sh == "" || runtime.GOOS == "windows" {
		t.Skip("needs POSIX file modes")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	source := filepath.Join(base, "source")
	for _, args := range [][]string{
		{"init", "-b", "main", source},
		{"-C", source, "-c", "user.name=T", "-c", "user.email=t@example.test", "commit", "--allow-empty", "-m", "one"},
	} {
		if out, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit, err := exec.CommandContext(t.Context(), "git", "-C", source, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"", strings.TrimSpace(string(commit))} {
		dest := filepath.Join(base, "dest-"+ref[:min(4, len(ref))])
		script := "umask 022\n" + repoScript(profile.Repository{
			Name: "app", URL: source, Path: dest, User: "deploy", Ref: ref,
			KnownHosts: []string{"example.test ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI"},
		}, nil) + "stat -c %a \"$destination\"\n"
		cmd := exec.CommandContext(t.Context(), sh, "-ec", script)
		cmd.Env = posixEnv(sh, "HOME="+filepath.Join(base, "home"))
		if err := os.MkdirAll(filepath.Join(base, "home"), 0700); err != nil {
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ref %q: %v: %s", ref, err, out)
		}
		if lines := strings.Fields(string(out)); lines[len(lines)-1] != "755" {
			t.Fatalf("ref %q: checkout mode = %s, want 755\n%s", ref, lines[len(lines)-1], out)
		}
	}
}
