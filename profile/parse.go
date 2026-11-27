package profile

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const maxSteps = 10_000

var profileName = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_-]*$`)

func validProfileReference(name string) bool {
	if len(name) > 255 {
		return false
	}
	segments := strings.Split(name, "/")
	if len(segments) > 8 || segments[0] == "snippets" || segments[0] == "files" {
		return false
	}
	for _, segment := range segments {
		if !profileName.MatchString(segment) {
			return false
		}
	}
	return true
}

var profileSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "name"}, {Name: "include"}},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "configuration"}, {Type: "repository", LabelNames: []string{"name"}},
		{Type: "unit", LabelNames: []string{"name"}}, {Type: "tasks"},
	},
}

var taskSchema = hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "order"}, {Name: "bypass"}, {Name: "setup"}, {Name: "script"}},
	Blocks:     []hcl.BlockHeaderSchema{{Type: "lock"}},
}

// Parse compiles a profile from source. File references resolve relative to filename.
func Parse(source []byte, filename string) (*Plan, error) {
	return ParseWithBase(source, filename, filepath.Dir(filename))
}

// ParseWithBase compiles a profile from source while resolving includes,
// snippets, and unit templates beneath base.
func ParseWithBase(source []byte, filename, base string) (*Plan, error) {
	program, err := Prepare(source, filename, base)
	if err != nil {
		return nil, err
	}
	return program.Bind(nil)
}

func validatePlan(loaded compiledProfile, err error) (*Plan, error) {
	if err != nil {
		return nil, err
	}
	if loaded.steps == 0 || loaded.steps > maxSteps {
		return nil, fmt.Errorf("profile must expand to between 1 and %d steps", maxSteps)
	}
	if err := validateLockNames(loaded.plan.Tasks); err != nil {
		return nil, err
	}
	return loaded.plan, nil
}

// normalizeNewlines converts CRLF to LF. Profiles edited on a Windows controller
// would otherwise carry a trailing carriage return into heredoc shell commands
// and rendered unit files, which break on the Linux target. Line and column
// positions in diagnostics are unchanged.
func normalizeNewlines(source []byte) []byte {
	return bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
}

func parseBody(source []byte, filename string) (*hclsyntax.Body, error) {
	source = normalizeNewlines(source)
	file, diagnostics := hclsyntax.ParseConfig(documentBody(source, filename), filename, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, diagnostics
	}
	return file.Body.(*hclsyntax.Body), nil
}

// Replace an anonymous envelope's braces with spaces to keep diagnostics at
// the original offsets, including when comments or strings contain braces.
func documentBody(source []byte, filename string) []byte {
	tokens, diagnostics := hclsyntax.LexConfig(source, filename, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return source
	}
	first := -1
	last := -1
	for i, token := range tokens {
		switch token.Type {
		case hclsyntax.TokenComment, hclsyntax.TokenNewline, hclsyntax.TokenEOF:
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 || tokens[first].Type != hclsyntax.TokenOBrace || tokens[last].Type != hclsyntax.TokenCBrace {
		return source
	}
	body := slices.Clone(source)
	body[tokens[first].Range.Start.Byte] = ' '
	body[tokens[last].Range.Start.Byte] = ' '
	return body
}

func (l *profileLoader) compile(filename string, stack []string) (compiledProfile, error) {
	doc := l.program.documents[filename]
	syntax := doc.stripped()
	ctx := argumentContext(l.bindings[filename].AsValueMap())
	body := scopedBody{Body: syntax, context: ctx}
	var err error
	for _, block := range syntax.Blocks {
		switch block.Type {
		case "script", "scripts", "account":
			return compiledProfile{}, fmt.Errorf("%s: legacy %q block is not supported in Plonkr v0.1.0; use configuration and tasks", block.DefRange(), block.Type)
		}
	}
	content, diagnostics := body.Content(&profileSchema)
	if diagnostics.HasErrors() {
		return compiledProfile{}, diagnostics
	}
	fields, err := evaluateLiterals(content.Attributes, "name", "include")
	if err != nil {
		return compiledProfile{}, err
	}
	name := fields.optionalString("name", stack[len(stack)-1])
	includes := fields.strings("include")
	if fields.err != nil {
		return compiledProfile{}, fmt.Errorf("%s: %w", filename, fields.err)
	}
	if _, set := fields.values["name"]; set && strings.TrimSpace(name) == "" {
		return compiledProfile{}, fmt.Errorf("%s: profile name must not be empty", filename)
	}
	for i, name := range includes {
		if !validProfileReference(name) {
			return compiledProfile{}, fmt.Errorf("%s: invalid include profile reference %q", filename, name)
		}
		if slices.Contains(includes[:i], name) {
			return compiledProfile{}, fmt.Errorf("%s: include lists profile %q more than once", filename, name)
		}
	}

	plan := &Plan{
		Name: name, Groups: map[string]Group{}, Users: map[string]User{},
		Repositories: map[string]Repository{}, Units: map[string]Unit{},
	}
	var baseline Task
	var tasks *hcl.Block
	var hasConfiguration bool
	for _, block := range content.Blocks {
		switch block.Type {
		case "configuration":
			if hasConfiguration {
				return compiledProfile{}, fmt.Errorf("%s: duplicate configuration block", block.DefRange)
			}
			hasConfiguration = true
			baseline, err = decodeConfiguration(block, plan)
		case "repository":
			var repo Repository
			repo, err = decodeRepository(block)
			if err == nil {
				if _, exists := plan.Repositories[repo.Name]; exists {
					err = fmt.Errorf("duplicate repository %q", repo.Name)
				}
				plan.Repositories[repo.Name] = repo
			}
		case "unit":
			err = l.loadUnit(block, plan.Units)
		case "tasks":
			if tasks != nil {
				return compiledProfile{}, fmt.Errorf("%s: duplicate tasks block", block.DefRange)
			}
			tasks = block
		}
		if err != nil {
			return compiledProfile{}, fmt.Errorf("%s: %w", block.DefRange, err)
		}
	}

	result := compiledProfile{plan: plan, depth: 1}
	var includedOrdered []Task
	for i, name := range includes {
		values, err := evaluateArgumentMap(doc.includes[i].arguments, ctx)
		if err != nil {
			return compiledProfile{}, err
		}
		child, err := l.load(name, stack, values)
		if err != nil {
			return compiledProfile{}, fmt.Errorf("%s: include %q: %w", filename, name, err)
		}
		if l.merged[name] {
			continue
		}
		if l.merged == nil {
			l.merged = make(map[string]bool)
		}
		l.merged[name] = true
		childOrdered, err := result.include(name, child)
		if err != nil {
			return compiledProfile{}, fmt.Errorf("%s: %w", filename, err)
		}
		includedOrdered = append(includedOrdered, childOrdered...)
	}
	if len(baseline.Steps) != 0 {
		if err := result.appendTask(baseline); err != nil {
			return compiledProfile{}, fmt.Errorf("%s: %w", filename, err)
		}
	}
	for _, task := range includedOrdered {
		if err := result.appendTask(task); err != nil {
			return compiledProfile{}, fmt.Errorf("%s: %w", filename, err)
		}
	}
	if tasks != nil {
		err = l.compileTasks(tasks, &result)
	}
	return result, err
}

func (l *profileLoader) compileTasks(block *hcl.Block, result *compiledProfile) error {
	content, diagnostics := block.Body.Content(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "task", LabelNames: []string{"name"}}},
	})
	if diagnostics.HasErrors() {
		return diagnostics
	}

	orders := make(map[int]string)
	first := len(result.plan.Tasks)
	for _, block := range content.Blocks {
		task, err := l.compileTask(block, result.plan, maxSteps-result.steps)
		if err != nil {
			return fmt.Errorf("%s: task %q: %w", block.DefRange, block.Labels[0], err)
		}
		if previous, exists := orders[task.Order]; exists {
			return fmt.Errorf("%s: duplicate task order %d (used by %q and %q)", block.DefRange, task.Order, previous, task.Name)
		}
		orders[task.Order] = task.Name
		if err := result.appendTask(task); err != nil {
			return err
		}
	}
	slices.SortFunc(result.plan.Tasks[first:], func(a, b Task) int {
		// Both orders are non-negative, so their difference fits in int.
		return a.Order - b.Order
	})
	return nil
}

func (l *profileLoader) compileTask(block *hcl.Block, plan *Plan, limit int) (Task, error) {
	name := block.Labels[0]
	if strings.TrimSpace(name) == "" {
		return Task{}, fmt.Errorf("task name must not be empty")
	}
	content, diagnostics := block.Body.Content(&taskSchema)
	if diagnostics.HasErrors() {
		return Task{}, diagnostics
	}
	attrs := content.Attributes
	fields, err := evaluateLiterals(attrs, "order", "bypass")
	if err != nil {
		return Task{}, err
	}
	task := Task{Name: name, Order: fields.integer("order", 0), Bypass: fields.boolean("bypass", false)}
	if fields.err != nil {
		return Task{}, fields.err
	}
	if len(content.Blocks) > 1 {
		return Task{}, fmt.Errorf("%s: duplicate lock block", content.Blocks[1].DefRange)
	}
	if len(content.Blocks) == 1 {
		if task.Lock, err = decodeLock(content.Blocks[0]); err != nil {
			return Task{}, fmt.Errorf("%s: lock: %w", content.Blocks[0].DefRange, err)
		}
	}
	if attribute := attrs["setup"]; attribute != nil {
		value, diagnostics := attribute.Expr.Value(l.program.phase(PhaseSetup))
		if diagnostics.HasErrors() {
			return Task{}, diagnostics
		}
		task.Steps, err = decodeSetupSteps(value, plan, limit)
		if err != nil {
			return Task{}, fmt.Errorf("setup: %w", err)
		}
	}
	if attribute := attrs["script"]; attribute != nil {
		value, diagnostics := attribute.Expr.Value(l.program.phase(PhaseScript))
		if diagnostics.HasErrors() {
			return Task{}, diagnostics
		}
		steps, _, err := l.decodeScriptSteps(value, nil, limit-len(task.Steps))
		if err != nil {
			return Task{}, fmt.Errorf("script: %w", err)
		}
		task.Steps = append(task.Steps, steps...)
	}
	if len(task.Steps) == 0 {
		return Task{}, fmt.Errorf("setup or script must produce at least one step")
	}
	return task, nil
}

func blockAttributes(body hcl.Body, names ...string) (hcl.Attributes, error) {
	schema := hcl.BodySchema{}
	for _, name := range names {
		schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: name})
	}
	content, diagnostics := body.Content(&schema)
	if diagnostics.HasErrors() {
		return nil, diagnostics
	}
	return content.Attributes, nil
}
