package profile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zclconf/go-cty/cty"
)

const (
	maxDepth = 64
	// maxSourceSize bounds every profile and snippet file, matching the limit
	// plonk applies to a profile named by path.
	maxSourceSize = 1 << 20
)

type profileLoader struct {
	program    *Program
	bindings   map[string]cty.Value
	base       string
	root       *os.Root
	profiles   map[string]compiledProfile
	snippets   map[string]snippet
	expansions map[string]compiledSnippet
	// merged records the profiles already merged into the plan being compiled.
	// A profile reached again through another include path adds nothing, so a
	// shared base profile included by two parents still runs once.
	merged map[string]bool
}

type compiledProfile struct {
	plan  *Plan
	steps int
	depth int
}

// LoadProfile reads a named profile beneath base and compiles its dependencies.
func LoadProfile(name, base string) (*Plan, error) {
	program, err := PrepareProfile(name, base)
	if err != nil {
		return nil, err
	}
	return program.Bind(nil)
}

func (l *profileLoader) directory() (*os.Root, error) {
	if l.root == nil {
		root, err := os.OpenRoot(l.base)
		if err != nil {
			return nil, fmt.Errorf("open profiles directory: %w", err)
		}
		l.root = root
	}
	return l.root, nil
}

func (l *profileLoader) close() {
	if l.root != nil {
		l.root.Close()
	}
}

func (l *profileLoader) load(name string, stack []string, supplied map[string]cty.Value) (compiledProfile, error) {
	if slices.Contains(stack, name) {
		return compiledProfile{}, fmt.Errorf("profile include cycle: %s", strings.Join(append(stack, name), " -> "))
	}
	filename := filepath.Join(l.base, name+".hcl")
	values, err := bindArguments(l.program.documents[filename].arguments, supplied, true)
	if err != nil {
		return compiledProfile{}, fmt.Errorf("include %q: %w", name, err)
	}
	bound := cty.ObjectVal(values)
	if previous, ok := l.bindings[filename]; ok && !previous.RawEquals(bound) {
		return compiledProfile{}, fmt.Errorf("included profile %q reached with different arguments", name)
	}
	l.bindings[filename] = bound
	if cached, ok := l.profiles[name]; ok {
		if len(stack)+cached.depth > maxDepth {
			return compiledProfile{}, fmt.Errorf("profile include nesting exceeds %d levels", maxDepth)
		}
		return cached, nil
	}
	if len(stack) >= maxDepth {
		return compiledProfile{}, fmt.Errorf("profile include nesting exceeds %d levels", maxDepth)
	}
	result, err := l.compile(filename, append(stack, name))
	if err != nil {
		return compiledProfile{}, err
	}
	if l.profiles == nil {
		l.profiles = make(map[string]compiledProfile)
	}
	l.profiles[name] = result
	return result, nil
}

func (p *compiledProfile) appendTask(task Task) error {
	if len(task.Steps) > maxSteps-p.steps {
		return fmt.Errorf("profile exceeds %d steps", maxSteps)
	}
	p.steps += len(task.Steps)
	p.plan.Tasks = append(p.plan.Tasks, task)
	return nil
}

func (p *compiledProfile) include(name string, child compiledProfile) ([]Task, error) {
	if child.steps > maxSteps-p.steps {
		return nil, fmt.Errorf("profile exceeds %d steps", maxSteps)
	}
	p.depth = max(p.depth, child.depth+1)
	includeResources(p.plan.Groups, child.plan.Groups, name, OpGroup)
	includeResources(p.plan.Users, child.plan.Users, name, OpUser)
	includeResources(p.plan.Repositories, child.plan.Repositories, name, OpRepo)
	includeResources(p.plan.Units, child.plan.Units, name, OpUnit)
	var ordered []Task
	for _, task := range child.plan.Tasks {
		task.Steps = slices.Clone(task.Steps)
		for i := range task.Steps {
			step := &task.Steps[i]
			switch step.Op {
			case OpGroup, OpUser, OpRepo, OpUnit:
				step.Resource = resourceID(name, step.Op, step.Resource)
			}
			step.Flags = slices.Clone(step.Flags)
		}
		if task.Baseline {
			if err := p.appendTask(task); err != nil {
				return nil, err
			}
		} else {
			ordered = append(ordered, task)
		}
	}
	return ordered, nil
}

func includeResources[T any](dst, src map[string]T, profile string, op Op) {
	for id, resource := range src {
		dst[resourceID(profile, op, id)] = resource
	}
}

// A transitive include keeps its original namespace when merged again.
func resourceID(profile string, op Op, id string) string {
	if strings.HasPrefix(id, "profile:") {
		return id
	}
	return "profile:" + profile + ":" + string(op) + ":" + id
}

func readRegularFile(root *os.Root, name string, limit int64) ([]byte, error) {
	before, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%q must be a regular file", name)
	}
	if limit > 0 && before.Size() > limit {
		return nil, fmt.Errorf("%q exceeds %d bytes", name, limit)
	}
	file, err := openRootRegular(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("%q changed between stat and open", name)
	}
	if limit == 0 {
		return io.ReadAll(file)
	}
	if after.Size() > limit {
		return nil, fmt.Errorf("%q exceeds %d bytes", name, limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%q exceeds %d bytes", name, limit)
	}
	return data, nil
}
