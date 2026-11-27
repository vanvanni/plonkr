package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
)

// maxLockEntries bounds each lock list so one task cannot trigger unbounded probing.
const maxLockEntries = 64

// Lock holds a task's optional ignore conditions. The task is ignored only when
// every configured directive holds; a zero Lock never ignores anything.
type Lock struct {
	// Exists lists absolute target paths that must all exist.
	Exists []string
	// Eval lists commands run on the target over SSH. Their combined result must
	// equal the result recorded after the task last completed.
	Eval []string
}

// IsZero reports whether no directive is configured.
func (l Lock) IsZero() bool {
	return len(l.Exists) == 0 && len(l.Eval) == 0
}

// Digest identifies the lock definition. State recorded under a different digest
// is stale, so editing a lock block makes the task run again.
func (l Lock) Digest() string {
	raw, _ := json.Marshal(struct {
		Exists []string `json:"exists"`
		Eval   []string `json:"eval"`
	}{l.Exists, l.Eval})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

var lockSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "exists"}, {Name: "eval"}},
}

func decodeLock(block *hcl.Block) (Lock, error) {
	content, diagnostics := block.Body.Content(&lockSchema)
	if diagnostics.HasErrors() {
		return Lock{}, diagnostics
	}
	fields, err := evaluateLiterals(content.Attributes, "exists", "eval")
	if err != nil {
		return Lock{}, err
	}
	lock := Lock{Exists: fields.strings("exists"), Eval: fields.strings("eval")}
	if fields.err != nil {
		return Lock{}, fields.err
	}
	for _, name := range []string{"exists", "eval"} {
		if _, set := fields.values[name]; set && len(fields.list(name)) == 0 {
			return Lock{}, fmt.Errorf("%s must not be empty", name)
		}
	}
	if lock.IsZero() {
		return Lock{}, fmt.Errorf("lock requires exists, eval or both")
	}
	if len(lock.Exists) > maxLockEntries || len(lock.Eval) > maxLockEntries {
		return Lock{}, fmt.Errorf("lock allows at most %d entries per list", maxLockEntries)
	}
	for i, value := range lock.Exists {
		if !safePOSIXPath(value) {
			return Lock{}, fmt.Errorf("exists[%d] must be a safe absolute POSIX path", i)
		}
	}
	for i, value := range lock.Eval {
		if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
			return Lock{}, fmt.Errorf("eval[%d] must be a non-empty command with no NUL bytes", i)
		}
	}
	return lock, nil
}

// validateLockNames rejects locked tasks that share a name. Recorded lock state
// is keyed by task name, so duplicates would overwrite each other.
func validateLockNames(tasks []Task) error {
	seen := make(map[string]bool)
	for _, task := range tasks {
		if task.Lock.IsZero() {
			continue
		}
		if seen[task.Name] {
			return fmt.Errorf("task %q: locked tasks must have unique names", task.Name)
		}
		seen[task.Name] = true
	}
	return nil
}
