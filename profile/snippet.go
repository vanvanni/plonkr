package profile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
)

type snippet struct {
	expression hcl.Expression
	location   hcl.Range
}

type compiledSnippet struct {
	steps []Step
	depth int
}

func (l *profileLoader) loadSnippets() error {
	root, err := l.directory()
	if err != nil {
		return err
	}
	snippets, err := root.OpenRoot("snippets")
	if err != nil {
		if os.IsNotExist(err) {
			l.snippets = map[string]snippet{}
			return nil
		}
		return fmt.Errorf("open snippets directory: %w", err)
	}
	defer snippets.Close()
	entries, err := fs.ReadDir(snippets.FS(), ".")
	if err != nil {
		return fmt.Errorf("read snippets directory: %w", err)
	}
	definitions := make(map[string]snippet)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".hcl" {
			continue
		}
		source, err := readRegularFile(snippets, entry.Name(), maxSourceSize)
		if err != nil {
			return fmt.Errorf("read snippet file %q: %w", entry.Name(), err)
		}
		body, err := parseBody(source, filepath.Join(l.base, "snippets", entry.Name()))
		if err != nil {
			return err
		}
		content, diagnostics := body.Content(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{{Type: "snippet", LabelNames: []string{"name"}}},
		})
		if diagnostics.HasErrors() {
			return diagnostics
		}
		for _, block := range content.Blocks {
			name := block.Labels[0]
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("%s: snippet name must not be empty", block.DefRange)
			}
			if previous, exists := definitions[name]; exists {
				return fmt.Errorf("%s: duplicate snippet %q (first defined at %s)", block.DefRange, name, previous.location)
			}
			attributes, err := blockAttributes(block.Body, "commands")
			if err != nil {
				return err
			}
			commands := attributes["commands"]
			if commands == nil {
				return fmt.Errorf("%s: snippet %q requires commands", block.DefRange, name)
			}
			definitions[name] = snippet{expression: commands.Expr, location: block.DefRange}
		}
	}
	l.snippets = definitions
	return nil
}

func (l *profileLoader) expandSnippet(name string, stack []string) (compiledSnippet, error) {
	if slices.Contains(stack, name) {
		return compiledSnippet{}, fmt.Errorf("snippet cycle: %s", strings.Join(append(stack, name), " -> "))
	}
	if cached, ok := l.expansions[name]; ok {
		if len(stack)+cached.depth > maxDepth {
			return compiledSnippet{}, fmt.Errorf("snippet nesting exceeds %d levels", maxDepth)
		}
		return cached, nil
	}
	if len(stack) >= maxDepth {
		return compiledSnippet{}, fmt.Errorf("snippet nesting exceeds %d levels", maxDepth)
	}
	if l.snippets == nil {
		if err := l.loadSnippets(); err != nil {
			return compiledSnippet{}, err
		}
	}
	definition, ok := l.snippets[name]
	if !ok {
		return compiledSnippet{}, fmt.Errorf("unknown snippet %q", name)
	}
	value, diagnostics := definition.expression.Value(scriptContext)
	if diagnostics.HasErrors() {
		return compiledSnippet{}, diagnostics
	}
	steps, depth, err := l.decodeScriptSteps(value, append(stack, name), maxSteps)
	if err != nil {
		return compiledSnippet{}, fmt.Errorf("%s: snippet %q: %w", definition.location, name, err)
	}
	result := compiledSnippet{steps: steps, depth: depth + 1}
	if l.expansions == nil {
		l.expansions = make(map[string]compiledSnippet)
	}
	l.expansions[name] = result
	return result, nil
}
