package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// OpenSSH refuses these layouts with "error in libcrypto", while Go's parser
// accepts them. The preflight must reject them with a clear message and must
// never rewrite the file.
func TestReadKeySourceOpenSSHLayout(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "layout")
	if err != nil {
		t.Fatal(err)
	}
	valid := pem.EncodeToMemory(block)

	tests := []struct {
		name    string
		content []byte
		want    string // empty means accepted
	}{
		{"LF with final newline", valid, ""},
		{"CRLF", bytes.ReplaceAll(valid, []byte("\n"), []byte("\r\n")), "CRLF"},
		{"missing final newline", bytes.TrimRight(valid, "\n"), "final newline"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, tc.content, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := ReadKeySource(path)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, tc.content) {
					t.Fatal("key bytes were altered")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want mention of %q", err, tc.want)
			}
			if after, _ := os.ReadFile(path); !bytes.Equal(after, tc.content) {
				t.Fatal("source key file was modified")
			}
		})
	}
}

func TestReadKeySourceRequiresAbsolutePath(t *testing.T) {
	for _, path := range []string{"relative/key", `C:\keys\id`, "/keys/id"} {
		if filepath.IsAbs(path) {
			continue // absolute on this OS; covered by the other tests
		}
		_, err := ReadKeySource(path)
		if err == nil || !strings.Contains(err.Error(), "not an absolute path") {
			t.Errorf("ReadKeySource(%q) error = %v", path, err)
		}
	}
}
