// Package provision executes validated managed actions and shell steps over SSH.
// Private keys and unit contents travel only over SSH stdin, never in argv or events.
package provision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"plonk/profile"
	"plonk/runner"
)

// Executor runs compiled plan steps over SSH using resources from Plan and prepared Keys.
// The caller retains ownership of Keys and clearing any sensitive buffers after the run.
type Executor struct {
	SSH  runner.SSH
	Plan *profile.Plan
	Keys map[string][]byte
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
		if repo.Key != "" {
			key = e.Keys[repo.Key]
			if len(key) == 0 {
				return fmt.Errorf("provision: missing prepared deploy key %q", repo.Key)
			}
			input = bytes.NewReader(key)
		}
		return e.SSH.ExecuteInput(ctx, asUser(repo.User, repoScript(repo, key)), input, stdout, stderr)
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

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func asRoot(script string) string {
	qScript := quote(script)
	return `if [ "$(id -u)" = 0 ]; then sh -ec ` + qScript + `; else sudo -n -- sh -ec ` + qScript + `; fi`
}

func asUser(user, script string) string {
	qUser := quote(user)
	qScript := quote(script)
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
