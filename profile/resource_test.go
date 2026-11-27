package profile

import (
	"fmt"
	"strconv"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

func TestRepositoryValidation(t *testing.T) {
	const sshURL = "git@example.com:team/app.git"
	const httpsURL = "https://example.com/team/app.git"
	for _, tc := range []struct{ name, url, key, errorText string }{
		{"POSIX", sshURL, "/home/op/.ssh/k", ""},
		{"Windows forward slashes", sshURL, "C:/Users/op/.ssh/k", ""},
		{"Windows backslashes", sshURL, `C:\Users\op\.ssh\k`, ""},
		{"SSH scheme", "ssh://acme@example.com:2222/team/app.git", "/home/op/.ssh/k", ""},
		{"HTTPS without key", httpsURL, "", ""},
		{"key-store ID", sshURL, "my-deploy-key", "private-key store was removed in v0.1.0"},
		{"Windows traversal", sshURL, "C:/a/../b", "absolute local file path"},
		{"Windows traversal with backslashes", sshURL, `C:\a\..\b`, "absolute local file path"},
		{"Windows ambiguous traversal", sshURL, `C:\a\.. \b`, "absolute local file path"},
		{"Windows relative drive", sshURL, `C:a\b`, "absolute local file path"},
		{"POSIX traversal", sshURL, "/a/../b", "absolute local file path"},
		{"POSIX root", sshURL, "/", "absolute local file path"},
		{"Windows root", sshURL, `C:\`, "absolute local file path"},
		{"control character", sshURL, "/a/\nb", "absolute local file path"},
		{"missing key", sshURL, "", "key is required for SSH"},
		{"HTTPS credentials", "https://user:pass@example.com/app.git", "", "credential-free"},
		{"SSH credentials", "ssh://git:token@example.com/app.git", "/key", "credential-free"},
		{"query credential", "https://example.com/app.git?token=x", "", "credential-free"},
		{"invalid hostname", "git@-option:app.git", "/key", "credential-free"},
		{"invalid SCP username", "operator@example.com:app.git", "/key", "credential-free"},
		{"unsupported protocol", "file:///app.git", "", "credential-free"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attrs string
			if tc.key != "" {
				attrs = "key = " + strconv.Quote(tc.key)
			}
			source := repositorySource(tc.url, "/srv/app", "external", attrs)
			plan, err := Parse([]byte(source), "repo.hcl")
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			repo := plan.Repositories["app"]
			if repo.Key != tc.key || repo.URL != tc.url || repo.User != "external" {
				t.Fatalf("repo = %+v", repo)
			}
		})
	}
}

func TestRepositoryFields(t *testing.T) {
	key := publicKeyLine(t)
	for _, tc := range []struct{ name, path, user, attrs, errorText string }{
		{"tag and known hosts", "/srv/app", "external", fmt.Sprintf("ref = \"v1.2.3\"\nknown_hosts = [%q]", "@cert-authority example.com "+key), ""},
		{"ref full namespace", "/srv/app", "external", `ref = "refs/tags/v1.2.3"`, "invalid repository ref"},
		{"host patterns and a port", "/srv/app", "external", fmt.Sprintf("known_hosts = [%q]", "!old.example.com,*.example.com,[127.0.0.1]:2222 "+key+" comment with multiple words"), ""},
		{"hashed host", "/srv/app", "external", fmt.Sprintf("known_hosts = [%q]", knownhosts.HashHostname("example.com")+" "+key), ""},
		{"commit", "/srv/app", "external", `ref = "0123456789abcdef0123456789abcdef01234567"`, ""},
		{"ref option", "/srv/app", "external", `ref = "--option"`, "invalid repository ref"},
		{"ref revision", "/srv/app", "external", `ref = "origin/main~2"`, "invalid repository ref"},
		{"known hosts bad key", "/srv/app", "external", `known_hosts = ["example.com ssh-ed25519 x"]`, "invalid known_hosts"},
		{"known hosts invalid type", "/srv/app", "external", "known_hosts = [true]", "only strings"},
		{"known hosts bad marker", "/srv/app", "external", fmt.Sprintf("known_hosts = [%q]", "@other example.com "+key), "invalid known_hosts"},
		{"known hosts bad port", "/srv/app", "external", fmt.Sprintf("known_hosts = [%q]", "[example.com]:99999 "+key), "invalid known_hosts"},
		{"known hosts bad hash", "/srv/app", "external", fmt.Sprintf("known_hosts = [%q]", "|1|bad|bad "+key), "invalid known_hosts"},
		{"root user", "/srv/app", "root", "", "invalid Linux repository user"},
		{"root path", "/", "external", "", "safe absolute POSIX path"},
		{"relative path", "app", "external", "", "safe absolute POSIX path"},
		{"non-string key", "/srv/app", "external", "key = true", "key must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(repositorySource("https://example.com/app.git", tc.path, tc.user, tc.attrs)), "repo.hcl")
			if tc.errorText != "" {
				checkError(t, err, tc.errorText)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func repositorySource(url, path, user, attrs string) string {
	return fmt.Sprintf("repository \"app\" {\nurl = %q\npath = %q\nuser = %q\n%s\n}\n", url, path, user, attrs) +
		taskSource("Clone", "order = 0\nsetup = [repo(\"app\")]")
}
