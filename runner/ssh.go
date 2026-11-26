package runner

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// SSH delegates configuration, authentication, and host-key verification to OpenSSH.
// It never disables host-key checking or invokes a local shell.
type SSH struct {
	Host  string
	TTY   bool
	Stdin io.Reader
}

var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9_\[][a-zA-Z0-9_.@:%+\[\]-]*$`)

// ValidateHost rejects empty hosts, leading dashes, whitespace, and shell metacharacters.
func ValidateHost(host string) error {
	if !hostPattern.MatchString(host) {
		return fmt.Errorf("invalid SSH destination %q: use an SSH alias, hostname or user@host", host)
	}
	return nil
}

// Execute runs command on the remote host using sh -ec.
func (s SSH) Execute(ctx context.Context, command string, stdout, stderr io.Writer) error {
	return s.execute(ctx, command, s.Stdin, stdout, stderr)
}

// ExecuteInput runs command noninteractively with input attached to remote stdin.
// PTY allocation is always disabled so sensitive input is never echoed.
func (s SSH) ExecuteInput(ctx context.Context, command string, input io.Reader, stdout, stderr io.Writer) error {
	s.TTY = false
	return s.execute(ctx, command, input, stdout, stderr)
}

func (s SSH) execute(ctx context.Context, command string, input io.Reader, stdout, stderr io.Writer) error {
	if err := ValidateHost(s.Host); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Override RequestTTY from user SSH configuration in noninteractive mode.
	args := []string{"-T", "-o", "BatchMode=yes"}
	if s.TTY {
		args = []string{"-t"}
	}
	// SSH concatenates remote argv into a single shell command; quote the entire
	// script explicitly for remote sh, preserving embedded quotes and newlines.
	remote := "sh -ec '" + strings.ReplaceAll(command, "'", `'"'"'`) + "'"
	args = append(args, "--", s.Host, remote)

	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin = input
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
