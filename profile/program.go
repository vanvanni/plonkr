package profile

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Program is a parsed, structurally checked profile graph. Bind resolves its inputs
// into a fully validated Plan. Sources are read once per preparation.
type Program struct {
	base         string
	entry        string
	documents    map[string]*profileDocument
	snippets     map[string]snippet
	flags        map[string]bool
	snippetDepth map[string]int
}

type profileDocument struct {
	body      *hclsyntax.Body
	arguments []Argument
	includes  []profileInclude
}

type profileInclude struct {
	name      string
	arguments hcl.Expression
}

// Arguments returns the selected profile's inputs in declaration order.
func (p *Program) Arguments() []Argument { return slices.Clone(p.documents[p.entry].arguments) }

// FlagNames returns the static flags discovered across the referenced graph.
func (p *Program) FlagNames() []string { return slices.Sorted(maps.Keys(p.flags)) }

// Prepare parses a profile and checks all referenced argument contracts without
// requiring answers. File references remain confined beneath base.
func Prepare(source []byte, filename, base string) (*Program, error) {
	filename = filepath.Clean(filename)
	if protectedMetadata(filepath.Base(filename)) {
		return nil, fmt.Errorf("refusing Syncthing metadata path")
	}
	p := &Program{base: base, entry: filename, documents: map[string]*profileDocument{}, flags: map[string]bool{}, snippetDepth: map[string]int{}}
	l := profileLoader{base: base}
	defer l.close()
	if err := p.readDocument(&l, source, filename, nil); err != nil {
		return nil, err
	}
	return p, nil
}

func protectedMetadata(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, ".stfolder") || strings.HasPrefix(name, ".stignore") ||
		strings.HasPrefix(name, ".stversions") || strings.HasPrefix(name, ".syncthing") ||
		strings.HasPrefix(name, "~syncthing~") || strings.Contains(name, ".sync-conflict-")
}

// PrepareProfile reads a named profile under base, then prepares its graph.
func PrepareProfile(name, base string) (*Program, error) {
	name = strings.TrimSuffix(name, ".hcl")
	if !profileName.MatchString(name) {
		return nil, fmt.Errorf("invalid profile reference %q: use a profile name", name)
	}
	l := profileLoader{base: base}
	defer l.close()
	root, err := l.directory()
	if err != nil {
		return nil, err
	}
	source, err := readRegularFile(root, name+".hcl", maxSourceSize)
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", name, err)
	}
	return Prepare(source, filepath.Join(base, name+".hcl"), base)
}

func (p *Program) readDocument(l *profileLoader, source []byte, filename string, stack []string) error {
	if slices.Contains(stack, filename) {
		names := []string{}
		for _, file := range append(stack, filename) {
			names = append(names, strings.TrimSuffix(filepath.Base(file), ".hcl"))
		}
		return fmt.Errorf("profile include cycle: %s", strings.Join(names, " -> "))
	}
	if len(stack) >= maxDepth {
		return fmt.Errorf("profile include nesting exceeds %d levels", maxDepth)
	}
	if _, ok := p.documents[filename]; ok {
		return nil
	}
	if len(p.documents) >= maxSteps {
		return fmt.Errorf("profile graph exceeds %d files", maxSteps)
	}
	body, err := parseBody(source, filename)
	if err != nil {
		return err
	}
	arguments, err := decodeArguments(body, false)
	if err != nil {
		return err
	}
	doc := &profileDocument{body: body, arguments: arguments, includes: []profileInclude{}}
	if attr := body.Attributes["include"]; attr != nil {
		fields, err := evaluateLiterals(hcl.Attributes{"include": attr.AsHCLAttribute()}, "include")
		if err != nil {
			return err
		}
		for _, name := range fields.strings("include") {
			doc.includes = append(doc.includes, profileInclude{name: name})
		}
		if fields.err != nil {
			return fields.err
		}
	}
	for _, block := range body.Blocks {
		if block.Type != "include" {
			continue
		}
		if body.Attributes["include"] != nil {
			return fmt.Errorf("%s: use include blocks or the include list, not both", block.DefRange())
		}
		if len(block.Labels) != 1 {
			return fmt.Errorf("%s: include requires one profile name", block.DefRange())
		}
		attrs, err := blockAttributes(block.Body, "arguments")
		if err != nil {
			return err
		}
		call := profileInclude{name: block.Labels[0]}
		if attr := attrs["arguments"]; attr != nil {
			call.arguments = attr.Expr
		}
		doc.includes = append(doc.includes, call)
	}
	p.documents[filename] = doc
	seen := map[string]bool{}
	for _, include := range doc.includes {
		if !profileName.MatchString(include.name) {
			return fmt.Errorf("invalid include profile reference %q", include.name)
		}
		if seen[include.name] {
			return fmt.Errorf("include lists profile %q more than once", include.name)
		}
		seen[include.name] = true
		root, err := l.directory()
		if err != nil {
			return err
		}
		childFile := filepath.Join(p.base, include.name+".hcl")
		if _, exists := p.documents[childFile]; !exists || slices.Contains(append(stack, filename), childFile) {
			child, err := readRegularFile(root, include.name+".hcl", maxSourceSize)
			if err != nil {
				return fmt.Errorf("read profile %q: %w", include.name, err)
			}
			if err := p.readDocument(l, child, childFile, append(stack, filename)); err != nil {
				return err
			}
		}
		ctx := unknownArguments(arguments)
		values, err := evaluateArgumentMap(include.arguments, ctx)
		if err != nil {
			return err
		}
		if _, err := bindArguments(p.documents[childFile].arguments, values, true); err != nil {
			return fmt.Errorf("%s: include %q: %w", filename, include.name, err)
		}
	}
	stripped := doc.stripped()
	for _, block := range stripped.Blocks {
		if slices.Contains([]string{"script", "scripts", "account"}, block.Type) {
			return fmt.Errorf("%s: legacy %q block is not supported in Plonkr v0.1.0; use configuration and tasks", block.DefRange(), block.Type)
		}
	}
	if _, diags := stripped.Content(&profileSchema); diags.HasErrors() {
		return diags
	}
	return p.checkBody(l, stripped, unknownArguments(arguments), nil)
}

func unknownArguments(arguments []Argument) *hcl.EvalContext {
	values := map[string]cty.Value{}
	for _, a := range arguments {
		values[a.Name] = cty.UnknownVal(a.valueType())
	}
	return argumentContext(values)
}

func evaluateArgumentMap(expr hcl.Expression, ctx *hcl.EvalContext) (map[string]cty.Value, error) {
	if expr == nil {
		return map[string]cty.Value{}, nil
	}
	value, diags := expr.Value(ctx)
	if diags.HasErrors() {
		return nil, diags
	}
	if !value.IsKnown() || value.IsNull() || !value.Type().IsObjectType() {
		return nil, fmt.Errorf("%s: arguments must be an object with statically known keys", expr.Range())
	}
	return value.AsValueMap(), nil
}

func (p *Program) checkBody(l *profileLoader, body *hclsyntax.Body, ctx *hcl.EvalContext, stack []string) error {
	for _, attr := range body.Attributes {
		if attr.Name == "setup" {
			var wrong bool
			hclsyntax.VisitAll(attr.Expr, func(node hclsyntax.Node) hcl.Diagnostics {
				if call, ok := node.(*hclsyntax.FunctionCallExpr); ok && call.Name == "run" {
					wrong = true
				}
				return nil
			})
			if wrong {
				return fmt.Errorf("run() is only allowed in script, not setup")
			}
		}
		if err := p.checkExpression(l, attr.Expr, ctx, stack); err != nil {
			return err
		}
		eval := ctx.NewChild()
		switch attr.Name {
		case "setup":
			eval.Functions = setupContext.Functions
		case "script", "commands":
			eval.Functions = scriptContext.Functions
		}
		if _, diags := attr.Expr.Value(eval); diags.HasErrors() {
			return diags
		}
	}
	for _, block := range body.Blocks {
		if block.Type == "arguments" {
			continue
		}
		if err := checkBlockStructure(block); err != nil {
			return err
		}
		if block.Type == "unit" {
			if attr := block.Body.Attributes["source"]; attr != nil {
				if _, diags := attr.Expr.Value(nil); diags.HasErrors() {
					return fmt.Errorf("%s: unit source must be literal", attr.Range())
				}
			}
		}
		if err := p.checkBody(l, block.Body, ctx, stack); err != nil {
			return err
		}
	}
	return nil
}

func (p *Program) checkExpression(l *profileLoader, expr hclsyntax.Expression, ctx *hcl.EvalContext, stack []string) error {
	for _, traversal := range expr.Variables() {
		if traversal.RootName() != "args" {
			return fmt.Errorf("%s: Variables may not be used except declared args inputs", traversal.SourceRange())
		}
		if _, diags := traversal.TraverseAbs(ctx); diags.HasErrors() {
			return diags
		}
	}
	var checkErr error
	hclsyntax.VisitAll(expr, func(node hclsyntax.Node) hcl.Diagnostics {
		if checkErr != nil {
			return nil
		}
		call, ok := node.(*hclsyntax.FunctionCallExpr)
		if !ok {
			return nil
		}
		switch call.Name {
		case "flag":
			if len(call.Args) == 0 {
				checkErr = fmt.Errorf("%s: flag requires a name", call.Range())
				break
			}
			value, diags := call.Args[0].Value(nil)
			if diags.HasErrors() || !isType(value, cty.String) {
				checkErr = fmt.Errorf("%s: flag name must be a string literal", call.Range())
				break
			}
			_, checkErr = flagAction([]cty.Value{value})
			p.flags[value.AsString()] = true
		case "run":
			checkErr = p.checkRun(l, call, ctx, stack)
		}
		return nil
	})
	return checkErr
}

func (p *Program) checkRun(l *profileLoader, call *hclsyntax.FunctionCallExpr, ctx *hcl.EvalContext, stack []string) error {
	if len(call.Args) == 0 {
		return fmt.Errorf("%s: Not enough function arguments: run requires snippet names", call.Range())
	}
	if call.ExpandFinal {
		return fmt.Errorf("%s: run requires literal snippet names", call.Range())
	}
	// A final object is the argument map; the legacy variadic string form remains valid.
	count := len(call.Args)
	values := map[string]cty.Value{}
	if count == 2 {
		value, diags := call.Args[1].Value(ctx)
		if !diags.HasErrors() && value.Type().IsObjectType() {
			var err error
			values, err = evaluateArgumentMap(call.Args[1], ctx)
			if err != nil {
				return err
			}
			count = 1
		}
	}
	for _, expr := range call.Args[:count] {
		value, diags := expr.Value(nil)
		if diags.HasErrors() || !isType(value, cty.String) {
			return fmt.Errorf("%s: snippet name must be a literal string", expr.Range())
		}
		name := value.AsString()
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%s: run() requires non-empty string snippet names", call.Range())
		}
		if slices.Contains(stack, name) {
			return fmt.Errorf("snippet cycle: %s", strings.Join(append(stack, name), " -> "))
		}
		if len(stack) >= maxDepth {
			return fmt.Errorf("snippet nesting exceeds %d levels", maxDepth)
		}
		if l.snippets == nil {
			if err := l.loadSnippets(); err != nil {
				return err
			}
			p.snippets = l.snippets
		}
		definition, ok := l.snippets[name]
		if !ok {
			return fmt.Errorf("%s: unknown snippet %q", call.Range(), name)
		}
		if _, err := bindArguments(definition.arguments, values, false); err != nil {
			return fmt.Errorf("%s: snippet %q (call path %s): %w", call.Range(), name, strings.Join(append(stack, name), " -> "), err)
		}
		if depth, ok := p.snippetDepth[name]; ok {
			if len(stack)+depth > maxDepth {
				return fmt.Errorf("snippet nesting exceeds %d levels", maxDepth)
			}
			continue
		}
		if err := p.checkExpression(l, definition.expression.(hclsyntax.Expression), unknownArguments(definition.arguments), append(stack, name)); err != nil {
			return err
		}
		eval := unknownArguments(definition.arguments).NewChild()
		eval.Functions = scriptContext.Functions
		if _, diags := definition.expression.Value(eval); diags.HasErrors() {
			return diags
		}
		depth := 1
		hclsyntax.VisitAll(definition.expression.(hclsyntax.Expression), func(node hclsyntax.Node) hcl.Diagnostics {
			if nested, ok := node.(*hclsyntax.FunctionCallExpr); ok && nested.Name == "run" {
				for _, arg := range nested.Args {
					v, _ := arg.Value(nil)
					if isType(v, cty.String) {
						depth = max(depth, 1+p.snippetDepth[v.AsString()])
					}
				}
			}
			return nil
		})
		p.snippetDepth[name] = depth
	}
	return nil
}

func checkBlockStructure(block *hclsyntax.Block) error {
	var schema hcl.BodySchema
	switch block.Type {
	case "configuration":
		for _, name := range []string{"groups", "users", "timezone"} {
			schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: name})
		}
	case "repository":
		for _, name := range []string{"url", "path", "user", "key", "ref", "known_hosts"} {
			schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: name})
		}
	case "unit":
		for _, name := range []string{"source", "vars"} {
			schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: name})
		}
	case "tasks":
		schema.Blocks = []hcl.BlockHeaderSchema{{Type: "task", LabelNames: []string{"name"}}}
	case "task":
		schema = taskSchema
	case "lock":
		schema = lockSchema
	default:
		return fmt.Errorf("%s: unsupported block %q", block.DefRange(), block.Type)
	}
	_, diags := block.Body.Content(&schema)
	if diags.HasErrors() {
		return diags
	}
	return nil
}

func (d *profileDocument) stripped() *hclsyntax.Body {
	body := *d.body
	body.Attributes = maps.Clone(d.body.Attributes)
	body.Blocks = hclsyntax.Blocks{}
	for _, block := range d.body.Blocks {
		if block.Type != "arguments" && block.Type != "include" {
			body.Blocks = append(body.Blocks, block)
		}
	}
	names := []cty.Value{}
	for _, call := range d.includes {
		names = append(names, cty.StringVal(call.name))
	}
	if len(names) > 0 {
		body.Attributes["include"] = &hclsyntax.Attribute{Name: "include", Expr: &hclsyntax.LiteralValueExpr{Val: cty.TupleVal(names), SrcRange: d.body.Range()}}
	}
	return &body
}

// Bind fills root defaults, checks all effective values and compiles every call.
func (p *Program) Bind(supplied map[string]cty.Value) (*Plan, error) {
	values, err := bindArguments(p.Arguments(), supplied, true)
	if err != nil {
		return nil, err
	}
	l := profileLoader{base: p.base, program: p, snippets: p.snippets, bindings: map[string]cty.Value{}}
	defer l.close()
	l.bindings[p.entry] = cty.ObjectVal(values)
	name := strings.TrimSuffix(filepath.Base(p.entry), ".hcl")
	loaded, err := l.compile(p.entry, []string{name})
	return validatePlan(loaded, err)
}

// scopedBody supplies args to existing decoders without changing their strict
// type checks. The parsed syntax tree stays immutable and reusable across binds.
type scopedBody struct {
	hcl.Body
	context *hcl.EvalContext
}

func (b scopedBody) Content(schema *hcl.BodySchema) (*hcl.BodyContent, hcl.Diagnostics) {
	content, diags := b.Body.Content(schema)
	return b.scope(content), diags
}

func (b scopedBody) PartialContent(schema *hcl.BodySchema) (*hcl.BodyContent, hcl.Body, hcl.Diagnostics) {
	content, rest, diags := b.Body.PartialContent(schema)
	return b.scope(content), scopedBody{Body: rest, context: b.context}, diags
}

func (b scopedBody) scope(content *hcl.BodyContent) *hcl.BodyContent {
	if content == nil {
		return nil
	}
	for name, attr := range content.Attributes {
		copy := *attr
		copy.Expr = scopedExpression{Expression: attr.Expr, context: b.context}
		content.Attributes[name] = &copy
	}
	for i, block := range content.Blocks {
		copy := *block
		copy.Body = scopedBody{Body: block.Body, context: b.context}
		content.Blocks[i] = &copy
	}
	return content
}

type scopedExpression struct {
	hcl.Expression
	context *hcl.EvalContext
}

func (e scopedExpression) Value(ctx *hcl.EvalContext) (cty.Value, hcl.Diagnostics) {
	child := e.context.NewChild()
	if ctx != nil {
		child.Functions = ctx.Functions
	}
	return e.Expression.Value(child)
}
