package profile

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

const maxUnitSize = 1 << 20

var unitName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]*\.(service|timer|socket|target|path|mount|automount|slice|swap|device|scope)$`)

func (l *profileLoader) loadUnit(block *hcl.Block, units map[string]Unit) error {
	name := block.Labels[0]
	if len(name) > 255 || !unitName.MatchString(name) {
		return fmt.Errorf("unit name must be a filename with a supported systemd suffix")
	}
	if _, exists := units[name]; exists {
		return fmt.Errorf("duplicate unit %q", name)
	}
	fields, err := literalFields(block.Body, "source", "vars")
	if err != nil {
		return err
	}
	source := fields.requiredString("source")
	if fields.err != nil {
		return fields.err
	}
	local, err := filepath.Localize(source)
	if err != nil || source == "." || path.Clean(source) != source ||
		strings.ContainsAny(source, "\\:") || strings.ContainsFunc(source, unicode.IsControl) {
		return fmt.Errorf("unit source must be a relative path inside files/ without traversal")
	}

	variables := map[string]cty.Value{}
	if vars, set := fields.values["vars"]; set {
		if vars.IsNull() || !(vars.Type().IsObjectType() || vars.Type().IsMapType()) {
			return fmt.Errorf("unit vars must be an object of strings")
		}
		for variable, value := range vars.AsValueMap() {
			if !hclsyntax.ValidIdentifier(variable) || !isType(value, cty.String) {
				return fmt.Errorf("unit variable %q must be an HCL identifier with a string value", variable)
			}
			variables[variable] = value
		}
	}

	root, err := l.directory()
	if err != nil {
		return err
	}
	files, err := root.OpenRoot("files")
	if err != nil {
		return fmt.Errorf("open unit templates directory: %w", err)
	}
	defer files.Close()
	data, err := readRegularFile(files, local, maxUnitSize)
	if err != nil {
		return fmt.Errorf("read unit source %q: %w", source, err)
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("unit source %q must be UTF-8", source)
	}
	expression, diagnostics := hclsyntax.ParseTemplate(normalizeNewlines(data), filepath.Join(l.base, "files", local), hcl.InitialPos)
	if diagnostics.HasErrors() {
		return diagnostics
	}
	value, diagnostics := expression.Value(&hcl.EvalContext{Variables: variables})
	if diagnostics.HasErrors() {
		return diagnostics
	}
	if !isType(value, cty.String) {
		return fmt.Errorf("unit template must render to a string")
	}
	content := value.AsString()
	if strings.TrimSpace(content) == "" || len(content) > maxUnitSize || strings.ContainsRune(content, 0) {
		return fmt.Errorf("rendered unit must be non-empty, at most 1 MiB, and contain no NUL bytes")
	}
	units[name] = Unit{Name: name, Content: content}
	return nil
}
