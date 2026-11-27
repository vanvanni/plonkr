package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// capture runs run() with the given arguments and returns its exit code and
// everything written to the real standard output and error streams.
func capture(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	oldArgs, oldOut, oldErr := os.Args, os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Args, os.Stdout, os.Stderr = append([]string{"plonk"}, args...), outW, errW
	code := run()
	os.Args, os.Stdout, os.Stderr = oldArgs, oldOut, oldErr
	_ = outW.Close()
	_ = errW.Close()
	out, _ := io.ReadAll(outR)
	errOut, _ := io.ReadAll(errR)
	return code, string(out), string(errOut)
}

func TestExitCodesAndStreams(t *testing.T) {
	t.Setenv("PLONK_LOG_DIR", t.TempDir())
	dir := t.TempDir()
	bad := dir + string(os.PathSeparator) + "bad.hcl"
	if err := os.WriteFile(bad, []byte("tasks {"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		args        []string
		code        int
		out, errOut string
	}{
		{"version", []string{"version"}, 0, "plonk ", ""},
		{"help", []string{"--help"}, 0, "Plonkr configures", ""},
		{"no arguments prints help", nil, 0, "Usage:", ""},
		{"unknown command", []string{"frobnicate"}, 1, "", "plonk: unknown command"},
		{"invalid profile", []string{"validate", bad}, 1, "", "plonk: "},
		{"invalid host", []string{"run", "-bad", bad}, 1, "", "invalid SSH destination"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := capture(t, tc.args...)
			if code != tc.code {
				t.Errorf("exit code = %d, want %d (stderr %q)", code, tc.code, errOut)
			}
			if !strings.Contains(out, tc.out) || !strings.Contains(errOut, tc.errOut) {
				t.Errorf("stdout = %q, stderr = %q", out, errOut)
			}
			if tc.code != 0 && out != "" {
				t.Errorf("failures must not write to stdout: %q", out)
			}
			if strings.ContainsAny(errOut, "\x1b\x00") {
				t.Errorf("error text leaked control characters: %q", errOut)
			}
		})
	}
}
