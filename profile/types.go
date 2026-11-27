// Package profile loads, validates, and compiles HCL v2 Plonkr profiles into execution plans.
package profile

import (
	"github.com/vanvanni/plonkr/plugin"
	"github.com/vanvanni/plonkr/secret"
	"maps"
	"slices"
	"time"
)

// Phase identifies whether a step belongs to managed setup or shell script work.
type Phase string

const (
	OpPlugin    Op    = "plugin"
	PhaseSetup  Phase = "setup"
	PhaseScript Phase = "script"
)

// Op identifies the kind of operation a step performs.
// The zero value represents an arbitrary remote shell command.
type Op string

const (
	OpShell     Op = ""
	OpGroup     Op = "group"
	OpUser      Op = "user"
	OpTimezone  Op = "timezone"
	OpRepo      Op = "repo"
	OpUnit      Op = "unit"
	OpLog       Op = "log"
	OpWait      Op = "wait"
	OpWaitSSH   Op = "wait_ssh"
	OpWaitUntil Op = "wait_until"
)

// String returns a human-readable operation name.
func (o Op) String() string {
	if o == "" {
		return "shell"
	}
	return string(o)
}

// Step is one compiled action inside a task.
type Step struct {
	Plugin   string
	Phase    Phase
	Op       Op
	Text     string
	Resource string
	Flags    []string
	Color    string // Local presentation color for OpLog steps only.
	Duration time.Duration
	Interval time.Duration
	Command  string // Remote readiness command for OpWaitUntil.
}

// MissingFlags returns any required flags not enabled in enabled.
func (s Step) MissingFlags(enabled map[string]bool) []string {
	var missing []string
	for _, name := range s.Flags {
		if !enabled[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// Task is an ordered group of steps, or the synthesized configuration baseline.
type Task struct {
	Order    int
	Name     string
	Baseline bool
	Bypass   bool
	Lock     Lock
	Steps    []Step
}

// Group describes a managed Linux group declared in configuration.
type Group struct {
	Name   string
	GID    *int
	Create bool
}

// User describes a managed, non-root Linux account declared in configuration.
type User struct {
	Name       string
	Home       string
	Shell      string
	Service    bool
	Groups     []string
	PublicKeys []string
	Create     bool
}

// Repository describes a Git repository checkout on the target machine.
// Name retains the profile's unnamespaced label for target SSH key naming.
type Repository struct {
	Name       string
	URL        string
	Path       string
	User       string
	Key        string
	Ref        string
	KnownHosts []string
}

// Unit describes a rendered systemd unit file to be installed on the target.
type Unit struct {
	Name    string
	Content string
}

// Plan is the validated, fully expanded execution plan for a profile.
type Plan struct {
	Definitions    map[string]map[string]Definition
	PluginSessions map[string]*plugin.Session
	Secrets        *secret.Redactor
	Name           string
	Timezone       string
	Groups         map[string]Group
	Users          map[string]User
	Repositories   map[string]Repository
	Units          map[string]Unit
	Tasks          []Task
}

// FlagNames returns the sorted set of unique profile flags referenced by steps in the plan.
func (p *Plan) FlagNames() []string {
	if p == nil {
		return nil
	}
	seen := make(map[string]struct{})
	for _, task := range p.Tasks {
		for _, step := range task.Steps {
			for _, flag := range step.Flags {
				seen[flag] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// IsCommitHash reports whether ref is a full 40- or 64-character hexadecimal Git object ID.
func IsCommitHash(ref string) bool {
	if len(ref) != 40 && len(ref) != 64 {
		return false
	}
	for i := range len(ref) {
		c := ref[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
