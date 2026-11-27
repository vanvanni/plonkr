package cmd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/profile"
	"golang.org/x/crypto/ssh"
)

func writeKey(t *testing.T, name string, content []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadKeySourceAcceptsEveryCommonKeyFormatByteForByte(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(edKey)
	if err != nil {
		t.Fatal(err)
	}
	openSSHRSA, err := ssh.MarshalPrivateKey(rsaKey, "")
	if err != nil {
		t.Fatal(err)
	}
	openSSHEC, err := ssh.MarshalPrivateKey(ecKey, "")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"RSA PKCS#1 PEM":      pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}),
		"EC PEM":              pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER}),
		"PKCS#8 ed25519":      pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}),
		"OpenSSH RSA":         pem.EncodeToMemory(openSSHRSA),
		"OpenSSH ECDSA":       pem.EncodeToMemory(openSSHEC),
		"leading blank lines": append([]byte("\n\n"), pem.EncodeToMemory(openSSHEC)...),
		"trailing blank line": append(pem.EncodeToMemory(openSSHEC), '\n'),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeKey(t, "key", content, 0o600)
			got, err := ReadKeySource(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, content) {
				t.Fatal("key bytes were altered")
			}
		})
	}
}

func TestReadKeySourceRejections(t *testing.T) {
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(edKey)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := ssh.MarshalPrivateKey(edKey, "")
	if err != nil {
		t.Fatal(err)
	}
	pemText := pem.EncodeToMemory(valid)
	for _, tc := range []struct {
		name    string
		content []byte
		want    string
	}{
		{"empty file", nil, "between 1 byte and 1 MiB"},
		{"oversized", bytes.Repeat([]byte("x"), maxKeySize+1), "between 1 byte and 1 MiB"},
		{"public key instead of private", ssh.MarshalAuthorizedKey(signer.PublicKey()), "parse deploy key"},
		{"plain text", []byte("hunter2\n"), "parse deploy key"},
		{"truncated key", pemText[:len(pemText)/2], "parse deploy key"},
		{"binary garbage", []byte{0, 1, 2, 3, 0xff, 0xfe}, "parse deploy key"},
		{"whitespace only", []byte(" \n\t\n"), "parse deploy key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeKey(t, "key", tc.content, 0o600)
			_, err := ReadKeySource(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
			if after, _ := os.ReadFile(path); !bytes.Equal(after, tc.content) {
				t.Fatal("a rejected key file must never be modified")
			}
		})
	}
	if _, err := ReadKeySource(""); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty path: %v", err)
	}
	if _, err := ReadKeySource(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing key file must fail")
	}
}

func TestReadKeySourcePermissionsAndFileTypes(t *testing.T) {
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(edKey, "")
	if err != nil {
		t.Fatal(err)
	}
	content := pem.EncodeToMemory(block)

	t.Run("symlink is refused", func(t *testing.T) {
		target := writeKey(t, "target", content, 0o600)
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := ReadKeySource(link); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("directory is refused", func(t *testing.T) {
		if _, err := ReadKeySource(t.TempDir()); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("error = %v", err)
		}
	})
	if runtime.GOOS == "windows" {
		return // POSIX permission bits do not exist; ACL checks are not implemented.
	}
	for mode, ok := range map[os.FileMode]bool{0o600: true, 0o400: true, 0o640: false, 0o604: false, 0o644: false, 0o660: false, 0o777: false} {
		t.Run(mode.String(), func(t *testing.T) {
			path := writeKey(t, "key", content, mode)
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			_, err := ReadKeySource(path)
			if ok != (err == nil) {
				t.Fatalf("mode %v: error = %v, want accepted = %t", mode, err, ok)
			}
			if err != nil && !strings.Contains(err.Error(), "too permissive") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestKeyReplacementApprovalEdges(t *testing.T) {
	repo := profile.Repository{Name: "app", User: "deploy"}
	for _, tc := range []struct {
		name, input string
		want        string // empty means approved
	}{
		{"uppercase yes", "YES\n", ""},
		{"mixed case y", "Y\n", ""},
		{"surrounding spaces", "  y  \n", ""},
		{"tab padding", "\tyes\t\n", ""},
		{"backspace edits the answer", "n\x7fy\n", ""},
		{"ctrl-h edits the answer", "n\x08y\n", ""},
		{"backspace on empty input", "\x7f\x7fyes\n", ""},
		{"yes then carriage return", "yes\r", ""},
		{"no", "no\n", "declined"},
		{"uppercase no", "NO\n", "declined"},
		{"empty answer defaults to no", "\n", "declined"},
		{"carriage return only defaults to no", "\r", "declined"},
		{"yes with a suffix", "yes please\n", "declined"},
		{"yep", "yep\n", "declined"},
		{"ye", "ye\n", "declined"},
		{"truthy lookalike", "1\n", "declined"},
		{"overlong answer is cut off and declined", strings.Repeat("y", 40) + "\n", "declined"},
		{"ctrl-c cancels", "y\x03", "context canceled"},
		{"ctrl-d is end of input", "\x04", "not approved"},
		{"end of input without an answer", "", "not approved"},
		{"end of input after a partial answer", "y", "not approved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := approveKeyReplacement(t.Context(), repo, strings.NewReader(tc.input), &out, false)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
			if !strings.Contains(out.String(), "Warning:") || !strings.Contains(out.String(), "Replace this key? [y/N]: ") {
				t.Fatalf("prompt = %q", out.String())
			}
		})
	}

	t.Run("automatic approval never reads input", func(t *testing.T) {
		var out bytes.Buffer
		reader := strings.NewReader("no\n")
		if err := approveKeyReplacement(t.Context(), repo, reader, &out, true); err != nil || reader.Len() != 3 {
			t.Fatalf("err = %v, unread = %d", err, reader.Len())
		}
		if !strings.Contains(out.String(), "approved by --auto-approve") || strings.Contains(out.String(), "[y/N]") {
			t.Fatalf("output = %q", out.String())
		}
	})
	t.Run("the warning names the repository, user and key file", func(t *testing.T) {
		var out bytes.Buffer
		_ = approveKeyReplacement(t.Context(), repo, strings.NewReader("n\n"), &out, false)
		for _, want := range []string{`"app"`, `"deploy"`, "~/.ssh/plonkr-app.key"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("warning missing %q: %q", want, out.String())
			}
		}
	})
	t.Run("cancelled before prompting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var out bytes.Buffer
		if err := approveKeyReplacement(ctx, repo, strings.NewReader("y\n"), &out, true); err != context.Canceled || out.Len() != 0 {
			t.Fatalf("err = %v, output = %q", err, out.String())
		}
	})
	t.Run("answers are read one line at a time", func(t *testing.T) {
		in := strings.NewReader("y\nn\ny\n")
		for i, want := range []bool{true, false, true} {
			err := approveKeyReplacement(t.Context(), repo, in, &bytes.Buffer{}, false)
			if (err == nil) != want {
				t.Fatalf("approval %d: error = %v, want approved = %t", i, err, want)
			}
		}
	})
}
