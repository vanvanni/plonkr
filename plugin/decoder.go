package plugin

import (
	"fmt"
	"github.com/vanvanni/plonkr/secret"
	"github.com/zclconf/go-cty/cty"
	"math/big"
)

// Decoder reads validated attributes without implicit conversions or secret disclosure.
type Decoder struct {
	label    string
	values   map[string]cty.Value
	schema   map[string]Attribute
	redactor *secret.Redactor
	err      error
	children []*Decoder
}

func NewDecoder(label string, values map[string]cty.Value, attrs []Attribute, redactor *secret.Redactor) *Decoder {
	d := &Decoder{label: label, values: values, schema: map[string]Attribute{}, redactor: redactor}
	for _, a := range attrs {
		d.schema[a.Name] = a
	}
	return d
}
func (d *Decoder) Label() string { return d.label }
func (d *Decoder) Err() error {
	if d.err != nil {
		return d.err
	}
	for _, child := range d.children {
		if err := child.Err(); err != nil {
			return err
		}
	}
	return nil
}
func (d *Decoder) fail(name string) {
	if d.err == nil {
		d.err = fmt.Errorf("attribute %s has an invalid type or access mode", name)
	}
}
func (d *Decoder) value(name string, sensitive bool) (cty.Value, bool) {
	a, declared := d.schema[name]
	if !declared || a.Sensitive != sensitive {
		d.fail(name)
		return cty.NilVal, false
	}
	v, ok := d.values[name]
	if !ok {
		return cty.NilVal, false
	}
	if !v.IsWhollyKnown() || v.IsNull() || v.ContainsMarked() {
		d.fail(name)
		return cty.NilVal, false
	}
	return v, true
}
func (d *Decoder) String(name string) string {
	v, ok := d.value(name, false)
	if !ok {
		d.fail(name)
		return ""
	}
	if !v.Type().Equals(cty.String) {
		d.fail(name)
		return ""
	}
	return v.AsString()
}
func (d *Decoder) OptionalString(name, fallback string) string {
	if _, ok := d.values[name]; !ok {
		return fallback
	}
	return d.String(name)
}
func (d *Decoder) Int(name string) int {
	v, ok := d.value(name, false)
	if ok && v.Type().Equals(cty.Number) {
		n, acc := v.AsBigFloat().Int64()
		if acc == big.Exact && int64(int(n)) == n {
			return int(n)
		}
	}
	d.fail(name)
	return 0
}
func (d *Decoder) Bool(name string, fallback bool) bool {
	v, ok := d.value(name, false)
	if !ok {
		return fallback
	}
	if v.Type().Equals(cty.Bool) {
		return v.True()
	}
	d.fail(name)
	return false
}
func (d *Decoder) Strings(name string) []string {
	v, ok := d.value(name, false)
	if !ok {
		return nil
	}
	if !v.Type().IsTupleType() && !v.Type().IsListType() {
		d.fail(name)
		return nil
	}
	out := []string{}
	for _, x := range v.AsValueSlice() {
		if x.IsNull() || !x.Type().Equals(cty.String) {
			d.fail(name)
			return nil
		}
		out = append(out, x.AsString())
	}
	return out
}
func (d *Decoder) Object(name string) *Decoder {
	v, ok := d.value(name, false)
	if !ok || !v.Type().IsObjectType() {
		d.fail(name)
		return d
	}
	values := v.AsValueMap()
	attrs := []Attribute{}
	for n := range values {
		attrs = append(attrs, Attribute{Name: n})
	}
	child := NewDecoder(d.label, values, attrs, d.redactor)
	d.children = append(d.children, child)
	return child
}
func (d *Decoder) Secret(name string) secret.Value {
	v, ok := d.value(name, true)
	if !ok || !v.Type().Equals(cty.String) {
		d.fail(name)
		return secret.Value{}
	}
	s := secret.New(v.AsString())
	if d.redactor != nil {
		d.redactor.Add(s)
	}
	return s
}
