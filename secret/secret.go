// Package secret owns sensitive buffers and output redaction.
package secret

import (
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Value deliberately hides its bytes from formatting and serialization.
type Value struct{ data []byte }

func New(value string) Value                 { return Value{[]byte(value)} }
func (v Value) Reveal() string               { return string(v.data) }
func (v Value) String() string               { return "(sensitive)" }
func (v Value) GoString() string             { return v.String() }
func (v Value) Format(s fmt.State, _ rune)   { fmt.Fprint(s, v.String()) }
func (v Value) MarshalJSON() ([]byte, error) { return []byte(`"(sensitive)"`), nil }
func (v Value) Clear()                       { clear(v.data) }

// Redactor belongs to one bound plan. Clear after all output is consumed.
type Redactor struct {
	mu     sync.Mutex
	values []Value
}

func (r *Redactor) Add(v Value) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(v.data) > 0 {
		r.values = append(r.values, v)
		slices.SortStableFunc(r.values, func(a, b Value) int { return len(b.data) - len(a.data) })
	}
}
func (r *Redactor) Text(s string) string {
	if r == nil {
		return s
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Longest first avoids a short credential exposing the suffix of a longer one.
	pairs := make([]string, 0, len(r.values)*2)
	for _, v := range r.values {
		if len(v.data) > 0 {
			pairs = append(pairs, v.Reveal(), "[sensitive]")
		}
	}
	if len(pairs) == 0 {
		return s
	}
	return strings.NewReplacer(pairs...).Replace(s)
}
func (r *Redactor) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.values {
		v.Clear()
	}
	r.values = nil
}
