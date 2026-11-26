//go:build multipass

package acceptance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var image = flag.String("image", "24.04", "Ubuntu Multipass image")
var keep = flag.Bool("keep", false, "retain this suite's disposable VM for debugging")
var artifacts = flag.String("artifacts", "", "new directory for transcript and guest results (default: OS temporary directory)")

// TestMultipass is deliberately excluded from normal go test ./... runs.
// Only a uniquely named VM created by this invocation may be deleted.
func TestMultipass(t *testing.T) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 40*time.Minute)
	defer cancel()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go", "multipass", "node"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("required tool %s: %v", name, err)
		}
	}
	var outDir string
	if *artifacts == "" {
		outDir, err = os.MkdirTemp("", "plonkr-acceptance-results-")
	} else {
		outDir, err = filepath.Abs(*artifacts)
		if err == nil {
			err = os.Mkdir(outDir, 0700)
		}
	}
	if err != nil {
		t.Fatalf("create new artifact directory: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(outDir, "suite.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	t.Logf("Artifacts: %s", outDir)
	run := func(c context.Context, dir string, name string, args ...string) ([]byte, error) {
		fmt.Fprintf(f, "\n$ %s %s\n", name, strings.Join(args, " "))
		cmd := exec.CommandContext(c, name, args...)
		cmd.Dir = dir
		cmd.WaitDelay = 10 * time.Second
		// Do not inherit developer profiles, logs or test-helper modes.
		for _, e := range os.Environ() {
			key, _, _ := strings.Cut(e, "=")
			if strings.HasPrefix(strings.ToUpper(key), "PLONK_") || key == "GOOS" || key == "GOARCH" || key == "GOFLAGS" {
				continue
			}
			cmd.Env = append(cmd.Env, e)
		}
		var output bytes.Buffer
		cmd.Stdout = io.MultiWriter(f, &output)
		cmd.Stderr = cmd.Stdout
		err := cmd.Run()
		return output.Bytes(), err
	}
	must := func(dir, name string, args ...string) []byte {
		t.Helper()
		output, err := run(ctx, dir, name, args...)
		if err != nil {
			if len(output) > 8000 {
				output = output[len(output)-8000:]
			}
			t.Fatalf("%s %v: %v\n%s\nFull transcript: %s", name, args, err, output, outDir)
		}
		return output
	}
	t.Log("Host: vet, unit tests and native CLI example validation")
	must(root, "node", "scripts/build.mjs", "--check")
	must(root, "go", "vet", "./...")
	must(root, "go", "test", "-count=1", "./...")
	work := t.TempDir()
	binary := filepath.Join(work, "plonk")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	must(root, "go", "build", "-o", binary, ".")
	must(root, binary, "version")
	examples, err := filepath.Glob(filepath.Join(root, "examples", "*.hcl"))
	if err != nil || len(examples) == 0 {
		t.Fatalf("examples: %v", err)
	}
	for _, example := range examples {
		must(root, binary, "validate", example, "--profiles-dir", filepath.Join(root, "examples"))
	}
	// No host SSH or real credentials are used by validation.
	must(root, "multipass", "version")
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	vm := "plonkr-suite-" + hex.EncodeToString(token[:])
	t.Logf("Disposable VM: %s (%s, 2 CPUs, 3G RAM, 12G disk)", vm, *image)
	// Install cleanup before launch: a launch timeout may still leave an instance.
	defer func() {
		for _, file := range []string{"acceptance-results.json", "guest-checks.log"} {
			// Stream through stdout: SFTP chmod of a Windows destination can
			// fail even after a successful transfer. Go owns the local file.
			collect, done := context.WithTimeout(context.Background(), 30*time.Second)
			err := collectArtifact(collect, vm, file, filepath.Join(outDir, file), f)
			done()
			if err != nil {
				t.Logf("Artifact %s unavailable (see suite.log): %v", file, err)
			}
		}
		if *keep {
			t.Logf("Retained VM: %s; remove with: multipass delete --purge %s", vm, vm)
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), 2*time.Minute)
		defer done()
		if _, err := run(cleanup, root, "multipass", "delete", "--purge", vm); err != nil {
			t.Errorf("cleanup failed for %s: %v; inspect suite.log and delete only that VM", vm, err)
		}
	}()
	must(root, "multipass", "launch", *image, "--name", vm, "--cpus", "2", "--memory", "3G", "--disk", "12G", "--timeout", "600")
	archive := filepath.Join(work, "source.tar.gz")
	if err := sourceArchive(root, archive); err != nil {
		t.Fatal(err)
	}
	must(root, "multipass", "transfer", archive, vm+":/home/ubuntu/source.tar.gz")
	must(root, "multipass", "exec", vm, "--", "sh", "-ec", "mkdir -p /home/ubuntu/plonkr-suite/cli; tar -xzf /home/ubuntu/source.tar.gz -C /home/ubuntu/plonkr-suite/cli")
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	version := regexp.MustCompile(`(?m)^go (\d+\.\d+\.\d+)\s*$`).FindSubmatch(mod)
	if len(version) != 2 {
		t.Fatal("go.mod must declare a full Go version")
	}
	t.Log("Guest: installing tools and the module's declared Go version")
	must(root, "multipass", "exec", vm, "--", "sh", "-ec", "sudo apt-get update -qq && sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq git openssh-client openssh-server python3 gcc libc6-dev ca-certificates curl")
	must(root, "multipass", "exec", vm, "--", "python3", "/home/ubuntu/plonkr-suite/cli/acceptance/install_go.py", string(version[1]))
	t.Log("Guest: Linux vet, race suite, build and real SSH acceptance scenarios")
	must(root, "multipass", "exec", vm, "--", "bash", "-o", "pipefail", "-ec", `cd /home/ubuntu/plonkr-suite/cli
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=1
{
go version
go vet ./...
go test -race -count=1 ./...
go build -o plonk .
PLONKR_ACCEPTANCE_VM=1 python3 -u acceptance/guest.py
} 2>&1 | tee guest-checks.log`)
}

func collectArtifact(ctx context.Context, vm, name, destination string, transcript io.Writer) error {
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "multipass", "transfer", vm+":/home/ubuntu/plonkr-suite/cli/"+name, "-")
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdout, cmd.Stderr = f, transcript
	err = cmd.Run()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func sourceArchive(root, destination string) (err error) {
	f, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer func() {
		if e := gz.Close(); err == nil {
			err = e
		}
	}()
	tw := tar.NewWriter(gz)
	defer func() {
		if e := tw.Close(); err == nil {
			err = e
		}
	}()
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := strings.ToLower(d.Name())
		// Skip metadata before inspecting or opening it, including directories.
		blocked := strings.HasPrefix(name, ".st") || strings.HasPrefix(name, ".syncthing") || strings.HasPrefix(name, "~syncthing~") || strings.Contains(name, ".sync-conflict-") || name == ".git"
		if blocked {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// The module shares its root with the website. Do not copy public
			// assets, editable designs, generated output or workspace tooling.
			switch name {
			case "docs", "scripts", "dist", "node_modules", ".github", ".agents", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing source symlink %s", path)
		}
		ext := filepath.Ext(name)
		if ext != ".go" && ext != ".hcl" && ext != ".tmpl" && ext != ".py" && name != "go.mod" && name != "go.sum" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular source: %s", rel)
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0600, Size: info.Size()}); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, src)
		closeErr := src.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
