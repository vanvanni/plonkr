package provision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vanvanni/plonkr/profile"
)

// DefaultStateDir is the target directory holding lock state, one file per
// profile. It is root-owned and private; reading or writing it uses root or
// passwordless sudo, as every other managed change does. Locks with only exists
// never touch it.
const DefaultStateDir = "/etc/plonkr/.states"

const stateVersion = 1

var errNoPlan = errors.New("provision: execution plan is nil")

// lockState is the content of one profile's state file.
type lockState struct {
	Version int                  `json:"version"`
	Profile string               `json:"profile"`
	Tasks   map[string]lockEntry `json:"tasks"`
}

// lockEntry records what a task's eval commands produced after it last completed.
type lockEntry struct {
	// Lock is the digest of the lock definition the state belongs to.
	Lock string `json:"lock"`
	// Eval holds one SHA-256 per eval command over its exit code and stdout.
	Eval    []string  `json:"eval"`
	Updated time.Time `json:"updated"`
}

// lockObservation is what one probe saw on the target.
type lockObservation struct {
	// ExistsAll is true when every exists path was present.
	ExistsAll bool
	// Eval is empty when no eval ran, either because none is configured or
	// because a missing path made the result irrelevant.
	Eval []string
}

// CheckLock implements runner.Locker. A task is held (ignored) only when every
// directive holds: all exists paths are present and the eval results equal the
// ones recorded after the previous completed run. A missing record never holds.
func (e Executor) CheckLock(ctx context.Context, task profile.Task) (bool, string, error) {
	lock := task.Lock
	obs, err := e.probeLock(ctx, lock.Exists, lock.Eval)
	if err != nil {
		return false, "", err
	}
	if !obs.ExistsAll {
		return false, "", nil
	}
	var parts []string
	if len(lock.Exists) > 0 {
		parts = append(parts, "exists")
	}
	if len(lock.Eval) > 0 {
		state, err := e.readLockState(ctx)
		if err != nil {
			return false, "", err
		}
		if !state.holds(task.Name, lock, obs.Eval) {
			return false, "", nil
		}
		parts = append(parts, "eval unchanged")
	}
	return true, strings.Join(parts, " and "), nil
}

// RecordLock implements runner.Locker. The eval commands run again after the
// task succeeded, so the stored state is the one the task produced, not the
// one that made it run. exists directives keep no state.
func (e Executor) RecordLock(ctx context.Context, task profile.Task) error {
	lock := task.Lock
	if len(lock.Eval) == 0 {
		return nil
	}
	if e.Plan == nil {
		return errNoPlan
	}
	obs, err := e.probeLock(ctx, nil, lock.Eval)
	if err != nil {
		return err
	}
	state, err := e.readLockState(ctx)
	if err != nil {
		return err
	}
	raw, err := state.with(e.Plan.Name, task.Name, lockEntry{Lock: lock.Digest(), Eval: obs.Eval, Updated: time.Now().UTC()})
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	if err := e.SSH.ExecuteInput(ctx, asRoot(stateWriteScript(e.stateDir(), stateFileName(e.Plan.Name))), bytes.NewReader(raw), nil, &stderr); err != nil {
		return fmt.Errorf("write lock state: %w%s", err, remoteDetail(&stderr))
	}
	return nil
}

func (e Executor) probeLock(ctx context.Context, exists, eval []string) (lockObservation, error) {
	var stdout, stderr bytes.Buffer
	if err := e.SSH.ExecuteInput(ctx, lockProbeScript(exists, eval), nil, &stdout, &stderr); err != nil {
		return lockObservation{}, fmt.Errorf("probe lock: %w%s", err, remoteDetail(&stderr))
	}
	return parseLockProbe(stdout.String(), len(exists), len(eval))
}

func (e Executor) readLockState(ctx context.Context) (lockState, error) {
	if e.Plan == nil {
		return lockState{}, errNoPlan
	}
	var stdout, stderr bytes.Buffer
	if err := e.SSH.ExecuteInput(ctx, asRoot(stateReadScript(e.stateDir(), stateFileName(e.Plan.Name))), nil, &stdout, &stderr); err != nil {
		return lockState{}, fmt.Errorf("read lock state: %w%s", err, remoteDetail(&stderr))
	}
	return parseLockState(stdout.Bytes(), e.Plan.Name)
}

func (e Executor) stateDir() string {
	if e.StateDir != "" {
		return e.StateDir
	}
	return DefaultStateDir
}

func remoteDetail(stderr *bytes.Buffer) string {
	text := strings.Join(strings.Fields(stderr.String()), " ")
	if text == "" {
		return ""
	}
	if len(text) > 200 {
		text = text[:200]
	}
	return ": " + text
}

// stateFileName maps a profile name, which may hold spaces or other characters
// unfit for a file name, to a fixed-width name. The file records the name itself.
func stateFileName(profileName string) string {
	sum := sha256.Sum256([]byte(profileName))
	return hex.EncodeToString(sum[:8]) + ".json"
}

func parseLockState(raw []byte, profileName string) (lockState, error) {
	state := lockState{Version: stateVersion, Profile: profileName, Tasks: map[string]lockEntry{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return lockState{}, fmt.Errorf("lock state is not valid JSON: %w", err)
	}
	if state.Version != stateVersion {
		return lockState{}, fmt.Errorf("lock state version %d is not supported", state.Version)
	}
	if state.Tasks == nil {
		state.Tasks = map[string]lockEntry{}
	}
	return state, nil
}

// holds reports whether task was recorded under this lock definition with the
// same eval results.
func (s lockState) holds(task string, lock profile.Lock, eval []string) bool {
	entry, ok := s.Tasks[task]
	return ok && entry.Lock == lock.Digest() && slices.Equal(entry.Eval, eval)
}

// with returns the serialized state after setting one task's entry.
func (s lockState) with(profileName, task string, entry lockEntry) ([]byte, error) {
	tasks := make(map[string]lockEntry, len(s.Tasks)+1)
	for name, existing := range s.Tasks {
		tasks[name] = existing
	}
	tasks[task] = entry
	return json.MarshalIndent(lockState{Version: stateVersion, Profile: profileName, Tasks: tasks}, "", "  ")
}

// lockProbeScript prints one line per directive: "exists N 0|1" and "eval N HASH".
// eval commands run only while every exists path is present, because a missing
// path already decides the outcome. Each command runs in its own shell with no
// stdin; its stderr is discarded and does not count toward the state. A failing
// command is a state like any other: its exit code is hashed with its stdout.
func lockProbeScript(exists, eval []string) string {
	var b strings.Builder
	b.WriteString("ok=1\n")
	for i, path := range exists {
		n := strconv.Itoa(i)
		b.WriteString("if [ -e " + quote(path) + " ]; then echo 'exists " + n + " 1'; else ok=0; echo 'exists " + n + " 0'; fi\n")
	}
	if len(eval) > 0 {
		b.WriteString("if [ \"$ok\" = 1 ]; then\n")
		for i, command := range eval {
			b.WriteString("rc=0\n")
			b.WriteString("out=$(sh -ec " + quote(command) + " 2>/dev/null </dev/null) || rc=$?\n")
			b.WriteString("h=$(printf '%s\\n%s' \"$rc\" \"$out\" | sha256sum)\n")
			b.WriteString("echo \"eval " + strconv.Itoa(i) + " ${h%% *}\"\n")
		}
		b.WriteString("fi\n")
	}
	return b.String()
}

func parseLockProbe(out string, existsCount, evalCount int) (lockObservation, error) {
	present := make([]bool, existsCount)
	seen := make([]bool, existsCount)
	hashes := make([]string, evalCount)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return lockObservation{}, fmt.Errorf("unexpected probe output %q", line)
		}
		index, err := strconv.Atoi(fields[1])
		if err != nil || index < 0 {
			return lockObservation{}, fmt.Errorf("unexpected probe output %q", line)
		}
		switch fields[0] {
		case "exists":
			if index >= existsCount || (fields[2] != "0" && fields[2] != "1") {
				return lockObservation{}, fmt.Errorf("unexpected probe output %q", line)
			}
			seen[index], present[index] = true, fields[2] == "1"
		case "eval":
			if index >= evalCount || !validHash(fields[2]) {
				return lockObservation{}, fmt.Errorf("unexpected probe output %q", line)
			}
			hashes[index] = fields[2]
		default:
			return lockObservation{}, errors.New("unexpected probe output")
		}
	}
	obs := lockObservation{ExistsAll: true}
	for i := range present {
		if !seen[i] {
			return lockObservation{}, errors.New("incomplete probe output")
		}
		obs.ExistsAll = obs.ExistsAll && present[i]
	}
	if !obs.ExistsAll || evalCount == 0 {
		return obs, nil
	}
	if slices.Contains(hashes, "") {
		return lockObservation{}, errors.New("incomplete probe output")
	}
	obs.Eval = hashes
	return obs, nil
}

func validHash(s string) bool {
	return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == ""
}

// stateReadScript prints the state file, or nothing when there is none.
func stateReadScript(dir, file string) string {
	return "dir=" + quote(dir) + "\nfile=\"$dir/\"" + quote(file) + "\n" + `if [ -e "$file" ] || [ -L "$file" ]; then
  [ ! -L "$dir" ] && [ ! -L "$file" ] && [ -f "$file" ] || { echo 'Refusing unsafe lock state path' >&2; exit 1; }
  cat -- "$file"
fi
`
}

// stateWriteScript replaces the state file with stdin. The write goes to a
// private temporary file in the same directory and is renamed into place, so a
// reader never sees a partial file.
func stateWriteScript(dir, file string) string {
	return "dir=" + quote(dir) + "\nfile=\"$dir/\"" + quote(file) + "\n" + `umask 077
parent=$(dirname -- "$dir")
[ ! -L "$parent" ] && [ ! -L "$dir" ] || { echo 'Refusing symlink lock state directory' >&2; exit 1; }
[ -d "$parent" ] || (umask 022; mkdir -p -- "$parent")
[ -d "$dir" ] || mkdir -- "$dir"
[ ! -L "$file" ] || { echo 'Refusing symlink lock state file' >&2; exit 1; }
tmp=$(mktemp "$dir/.tmp.XXXXXX")
trap 'rm -f -- "$tmp"' EXIT
cat > "$tmp"
mv -f -- "$tmp" "$file"
`
}
