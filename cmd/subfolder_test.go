package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileReferenceClassification(t *testing.T) {
	for _, ref := range []string{"web", "proxmox/lab", "a/b/c"} {
		if isExplicitPath(ref) {
			t.Errorf("name classified as path: %q", ref)
		}
	}
	for _, ref := range []string{"./proxmox/lab.hcl", "proxmox/lab.hcl", "web.hcl", "./proxmox/lab", "../lab", `C:\plonkr\web.hcl`, `proxmox\lab`, "/etc/plonkr/web.hcl"} {
		if !isExplicitPath(ref) {
			t.Errorf("path classified as name: %q", ref)
		}
	}
}

func TestNestedProfileResolution(t *testing.T) {
	base, cwd, flagDir := t.TempDir(), t.TempDir(), t.TempDir()
	writeProfile(t, filepath.Join(base, "proxmox/lab.hcl"), "root-profile")
	writeProfile(t, filepath.Join(cwd, "proxmox/lab.hcl"), "cwd-profile")
	writeProfile(t, filepath.Join(cwd, "proxmox/lab"), "extensionless-path")
	writeProfile(t, filepath.Join(flagDir, "proxmox/lab.hcl"), "flag-profile")
	t.Setenv("PLONK_PROFILES_DIR", base)
	t.Chdir(cwd)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"proxmox/lab"}, "root-profile"},
		{[]string{"proxmox/lab", "--profiles-dir", flagDir}, "flag-profile"},
		{[]string{"proxmox/lab.hcl"}, "cwd-profile"},
		{[]string{"./proxmox/lab"}, "extensionless-path"},
	} {
		got, err := resolvedName(t, tc.args...)
		if err != nil || got != tc.want {
			t.Fatalf("%v: got %q, %v; want %q", tc.args, got, err, tc.want)
		}
	}
	out, _, err := runCLI(t, "run", "host", "proxmox/lab", "--dry-run", "--no-tui")
	if err != nil || !strings.Contains(out, "root-profile") {
		t.Fatalf("nested dry run: %v\n%s", err, out)
	}
	_, err = resolvedName(t, "missing/lab")
	if err == nil || !strings.Contains(err.Error(), "./") {
		t.Fatalf("missing path hint: %v", err)
	}
}
