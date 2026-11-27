package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
)

func TestRepositoryAlias(t *testing.T) {
	alias, _, _, checkoutURL := repositoryAlias(profile.Repository{Name: "your-repo", URL: "git@example.com:team/your-repo.git"})
	if alias != "your-repo.example.com" || checkoutURL != "git@your-repo.example.com:team/your-repo.git" {
		t.Fatalf("checkout URL = %q, alias = %q", checkoutURL, alias)
	}
	for _, tc := range []struct{ url, alias, host, port, clone string }{
		{"git@github.com:team/app.git", "app.github.com", "github.com", "", "git@app.github.com:team/app.git"},
		{"ssh://git@github.com:2222/team/app.git", "app.github.com", "github.com", "2222", "ssh://git@app.github.com:2222/team/app.git"},
		{"ssh://git@[::1]:2222/app.git", "app.ssh", "::1", "2222", "ssh://git@app.ssh:2222/app.git"},
		{"git@[::1]:team/app.git", "app.ssh", "::1", "", "git@app.ssh:team/app.git"},
	} {
		a, h, p, c := repositoryAlias(profile.Repository{Name: "app", URL: tc.url})
		if a != tc.alias || h != tc.host || p != tc.port || c != tc.clone {
			t.Fatalf("%s: got %q %q %q %q", tc.url, a, h, p, c)
		}
	}
}

func TestRepositoryConfigPreservesOtherHosts(t *testing.T) {
	sh := findPOSIXShell()
	if sh == "" {
		t.Skip("no POSIX shell")
	}
	home := filepath.ToSlash(t.TempDir())
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, ".ssh", "config")
	original := "Host personal\n  HostName personal.example\n  IdentityFile ~/.ssh/personal\n"
	if err := os.WriteFile(file, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app", "other", "app"} {
		script := "tmp=''\n" + repositoryConfigScript(profile.Repository{Name: name, URL: "git@github.com:team/app.git"})
		cmd := exec.CommandContext(t.Context(), sh, "-ec", script)
		cmd.Env = posixEnv(sh, "HOME="+home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{original, "Host app.github.com\n", "Host other.github.com\n"} {
		if strings.Count(string(data), want) != 1 {
			t.Fatalf("missing/duplicated %q in %s", want, data)
		}
	}
	if ssh, err := exec.LookPath("ssh"); err == nil {
		// -G parses configuration without opening a network connection.
		cmd := exec.CommandContext(t.Context(), ssh, "-G", "-F", file, "app.github.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("OpenSSH rejected generated config: %v: %s", err, out)
		}
		if !strings.Contains(string(out), "hostname github.com") || !strings.Contains(string(out), "identityfile ~/.ssh/plonkr-app.key") {
			t.Fatalf("OpenSSH did not resolve repository identity: %s", out)
		}
	}
}
