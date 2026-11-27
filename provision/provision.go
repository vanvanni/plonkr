// Package provision executes validated managed actions and shell steps over SSH.
// Private keys and unit contents travel only over SSH stdin, never in argv or events.
package provision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/runner"
)

// Executor runs compiled plan steps over SSH using resources from Plan and prepared Keys.
// The caller retains ownership of Keys and clearing any sensitive buffers after the run.
type Executor struct {
	SSH  runner.SSH
	Plan *profile.Plan
	Keys map[string][]byte
	// StateDir overrides DefaultStateDir, where task lock state is recorded.
	StateDir string
	// ApproveKeyReplacement is called only for a differing existing key.
	// A nil callback refuses replacement.
	ApproveKeyReplacement func(context.Context, profile.Repository) error
}

// Execute dispatches one non-log step to SSH transport.
func (e Executor) Execute(ctx context.Context, step profile.Step, stdout, stderr io.Writer) error {
	if e.Plan == nil {
		return errors.New("provision: execution plan is nil")
	}

	switch step.Op {
	case profile.OpShell:
		return e.SSH.Execute(ctx, step.Text, stdout, stderr)
	case profile.OpGroup:
		group, ok := e.Plan.Groups[step.Resource]
		if !ok {
			return fmt.Errorf("provision: unknown group %q", step.Resource)
		}
		return e.SSH.ExecuteInput(ctx, groupScript(group), nil, stdout, stderr)
	case profile.OpUser:
		user, ok := e.Plan.Users[step.Resource]
		if !ok {
			return fmt.Errorf("provision: unknown user %q", step.Resource)
		}
		return e.SSH.ExecuteInput(ctx, userScript(user), nil, stdout, stderr)
	case profile.OpTimezone:
		tz := step.Resource
		if tz == "" {
			tz = e.Plan.Timezone
		}
		if tz == "" {
			return errors.New("provision: timezone is empty")
		}
		return e.SSH.ExecuteInput(ctx, timezoneScript(tz), nil, stdout, stderr)
	case profile.OpRepo:
		repo, ok := e.Plan.Repositories[step.Resource]
		if !ok {
			return fmt.Errorf("provision: unknown repository %q", step.Resource)
		}
		var key []byte
		var input io.Reader
		var approvedHash string
		if repo.Key != "" {
			key = e.Keys[repo.Key]
			if len(key) == 0 {
				return fmt.Errorf("provision: missing prepared deploy key %q", repo.Key)
			}
			input = bytes.NewReader(key)
			var existing bytes.Buffer
			if err := e.SSH.ExecuteInput(ctx, asUser(repo.User, keyProbe(repo, key)), nil, &existing, stderr); err != nil {
				return fmt.Errorf("inspect target deploy key: %w", err)
			}
			approvedHash = strings.TrimSpace(existing.String())
			if approvedHash != "" {
				if len(approvedHash) != 64 || strings.Trim(approvedHash, "0123456789abcdef") != "" {
					return errors.New("invalid target deploy key checksum")
				}
				if e.ApproveKeyReplacement == nil {
					return errors.New("different target deploy key requires replacement approval")
				}
				if err := e.ApproveKeyReplacement(ctx, repo); err != nil {
					return err
				}
			}
		}
		if err := e.SSH.ExecuteInput(ctx, checkoutDirScript(repo), nil, stdout, stderr); err != nil {
			return fmt.Errorf("prepare checkout directory: %w", err)
		}
		return e.SSH.ExecuteInput(ctx, asUser(repo.User, repoScriptReplacement(repo, key, approvedHash)), input, stdout, stderr)
	case profile.OpUnit:
		unit, ok := e.Plan.Units[step.Resource]
		if !ok {
			return fmt.Errorf("provision: unknown unit %q", step.Resource)
		}
		return e.SSH.ExecuteInput(ctx, unitScript(unit), strings.NewReader(unit.Content), stdout, stderr)
	default:
		return fmt.Errorf("provision: unsupported operation %q", step.Op)
	}
}

// Probe only metadata/checksum, never key material. The install script rechecks
// the approved checksum so a concurrent change cannot inherit approval.
func keyProbe(r profile.Repository, key []byte) string {
	return validateHome + "expected=" + quote(fmt.Sprintf("%x", sha256.Sum256(key))) + "\n" +
		"keyfile=\"$HOME/.ssh/plonkr-" + r.Name + ".key\"\n" + `
[ ! -L "$HOME/.ssh" ] && [ ! -L "$keyfile" ] || { echo 'Refusing symlink deploy key path' >&2; exit 1; }
if [ -e "$keyfile" ]; then
  [ -f "$keyfile" ] || { echo 'Deploy key must be a regular file' >&2; exit 1; }
  actual=$(sha256sum < "$keyfile")
  actual=${actual%% *}
  if [ "$actual" != "$expected" ]; then printf '%s\n' "$actual"; fi
fi
`
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func asRoot(script string) string {
	qScript := quote(script)
	return `if [ "$(id -u)" = 0 ]; then sh -ec ` + qScript + `; else sudo -n -- sh -ec ` + qScript + `; fi`
}

func asUser(user, script string) string {
	qUser := quote(user)
	// runuser and sudo keep the caller's directory, which the target user may not be
	// allowed to enter (for example /root).
	qScript := quote("cd /\n" + script)
	return `if [ "$(id -un)" = ` + qUser + ` ]; then sh -ec ` + qScript +
		`; elif [ "$(id -u)" = 0 ]; then target_home=$(getent passwd ` + qUser + ` | cut -d: -f6); runuser -u ` + qUser + ` -- env HOME="$target_home" sh -ec ` + qScript +
		`; else sudo -n -H -u ` + qUser + ` -- sh -ec ` + qScript + `; fi`
}

const validateHome = `nl='
'
case "$HOME" in
  ''|/|*"$nl"*) echo 'Invalid target home' >&2; exit 1 ;;
esac
`

const ensureSSHDir = `[ ! -L "$HOME/.ssh" ] || { echo 'Refusing symlink .ssh directory' >&2; exit 1; }
umask 077
mkdir -p -- "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
`

// asRootIfAvailable runs script as root when the SSH login is root or has
// passwordless sudo, and otherwise does nothing.
func asRootIfAvailable(script string) string {
	qScript := quote(script)
	return `if [ "$(id -u)" = 0 ]; then sh -ec ` + qScript +
		`; elif command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then sudo -n -- sh -ec ` + qScript + `; fi`
}

// appendLineFunc defines a shell function that adds line $1 to file $2 unless
// it is already present. A final line without a trailing newline (common in
// hand-edited authorized_keys and known_hosts files) is terminated first so the
// new entry is not glued to it.
const appendLineFunc = `append_line() {
  grep -qxF -- "$1" "$2" && return 0
  [ ! -s "$2" ] || [ -z "$(tail -c 1 "$2")" ] || printf '\n' >> "$2"
  printf '%s\n' "$1" >> "$2"
}
`
