// Package plugin describes compiled-in integrations independent of the host CLI.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
)

type Attribute struct {
	Name                                 string
	Required, Static, Sensitive, Replace bool
}
type Change struct{ Attribute, From, To string }
type Approval struct{ Plugin, Definition, Action string }
type Target interface {
	Run(context.Context, string, io.Reader) error
	Output(context.Context, string, int) ([]byte, error)
}
type Run[C any] struct {
	Client  C
	Out     io.Writer
	Target  Target
	Approve func(context.Context, Approval) error
	reason  func(string)
}
type Spec[C any] struct {
	Name, Summary string
	Settings      []Attribute
	Configure     func(*Decoder) (C, error)
	Connect       func(context.Context, C) error
	Close         func(C) error
	Objects       []Object[C]
	Actions       []Action[C]
}
type ObjectSpec[C, T any] struct {
	Name       string
	Attributes []Attribute
	Decode     func(*Decoder) (T, error)
	Read       func(context.Context, Run[C], T) (T, bool, error)
	Create     func(context.Context, Run[C], T) error
	Update     func(context.Context, Run[C], T, T) error
	Delete     func(context.Context, Run[C], T) error
	Diff       func(T, T) []Change
}
type ActionSpec[C, P any] struct {
	Name        string
	Attributes  []Attribute
	Decode      func(*Decoder) (P, error)
	Do          func(context.Context, Run[C], P) error
	Destructive bool
}
type Object[C any] struct{ action action[C] }
type Action[C any] struct{ action action[C] }
type action[C any] struct {
	name          string
	attrs         []Attribute
	object, valid bool
	decode        func(*Decoder) (any, error)
	execute       func(context.Context, Run[C], any, bool, bool, Approval) error
}
type Plugin struct {
	Name, Summary string
	Settings      []Attribute
	actions       map[string]ActionInfo
	configure     func(*Decoder) (*Session, error)
	valid         bool
}
type ActionInfo struct {
	Name       string
	Attributes []Attribute
	Object     bool
	decode     func(*Decoder) (any, error)
}
type Set struct{ plugins map[string]Plugin }

func (s Set) Names() []string                          { return slices.Sorted(maps.Keys(s.plugins)) }
func (s Set) Get(name string) (Plugin, bool)           { p, ok := s.plugins[name]; return p, ok }
func (p Plugin) Action(name string) (ActionInfo, bool) { a, ok := p.actions[name]; return a, ok }
func (p Plugin) ActionNames() []string                 { return slices.Sorted(maps.Keys(p.actions)) }
func (p Plugin) Configure(d *Decoder) (*Session, error) {
	s, err := p.configure(d)
	return s, errors.Join(err, d.Err())
}
func (a ActionInfo) Decode(d *Decoder) (any, error) {
	v, err := a.decode(d)
	return v, errors.Join(err, d.Err())
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var pluginName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
var Label = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
var reserved = map[string]bool{}

func init() {
	for _, n := range []string{"plugin", "name", "include", "arguments", "environment", "configuration", "repository", "unit", "tasks", "task", "lock", "group", "user", "repo", "log", "flag", "run", "wait", "wait_ssh", "wait_until", "shellquote", "args", "env"} {
		reserved[n] = true
	}
}
func checkAttrs(attrs []Attribute, settings bool) error {
	seen := map[string]bool{}
	for _, a := range attrs {
		if !identifier.MatchString(a.Name) || seen[a.Name] || a.Name == "action" || a.Name == "ensure" || (settings && a.Replace) {
			return fmt.Errorf("invalid or duplicate attribute %q", a.Name)
		}
		seen[a.Name] = true
	}
	return nil
}
func NewSet(list ...Plugin) (Set, error) {
	s := Set{plugins: map[string]Plugin{}}
	for _, p := range list {
		if !p.valid || !pluginName.MatchString(p.Name) || reserved[p.Name] {
			return Set{}, fmt.Errorf("invalid or reserved plugin %q", p.Name)
		}
		if _, ok := s.plugins[p.Name]; ok {
			return Set{}, fmt.Errorf("duplicate plugin %q", p.Name)
		}
		if err := checkAttrs(p.Settings, true); err != nil {
			return Set{}, err
		}
		for _, a := range p.actions {
			if !identifier.MatchString(a.Name) {
				return Set{}, fmt.Errorf("invalid action %q", a.Name)
			}
			if err := checkAttrs(a.Attributes, false); err != nil {
				return Set{}, err
			}
		}
		s.plugins[p.Name] = p
	}
	return s, nil
}

// Session owns one run's configured client. It connects lazily and closes once.
type Session struct {
	reason            string
	connect           func(context.Context) error
	close             func() error
	execute           func(context.Context, string, any, bool, bool, io.Writer, Target, func(context.Context, Approval) error, Approval) error
	connected, closed bool
}

func (s *Session) Execute(ctx context.Context, action string, value any, absent, dry bool, out io.Writer, target Target, approve func(context.Context, Approval) error, a Approval) error {
	if s == nil || s.closed {
		return errors.New("plugin session is closed")
	}
	s.reason = ""
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.connected {
		if s.connect != nil {
			if err := s.connect(ctx); err != nil {
				return err
			}
		}
		s.connected = true
	}
	return s.execute(ctx, action, value, absent, dry, out, target, approve, a)
}
func (s *Session) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	defer func() { s.execute = nil; s.connect = nil; s.close = nil }()
	if s.connected && s.close != nil {
		return s.close()
	}
	return nil
}
func New[C any](spec Spec[C]) Plugin {
	all := map[string]action[C]{}
	valid := spec.Configure != nil
	for _, o := range spec.Objects {
		a := o.action
		if _, ok := all[a.name]; ok {
			valid = false
		}
		all[a.name] = a
		valid = valid && a.valid
	}
	for _, o := range spec.Actions {
		a := o.action
		if _, ok := all[a.name]; ok {
			valid = false
		}
		all[a.name] = a
		valid = valid && a.valid
	}
	p := Plugin{Name: spec.Name, Summary: spec.Summary, Settings: slices.Clone(spec.Settings), valid: valid, actions: map[string]ActionInfo{}}
	for n, a := range all {
		p.actions[n] = ActionInfo{Name: n, Attributes: slices.Clone(a.attrs), Object: a.object, decode: a.decode}
	}
	p.configure = func(d *Decoder) (*Session, error) {
		client, err := spec.Configure(d)
		if err != nil {
			return nil, err
		}
		s := &Session{}
		if spec.Connect != nil {
			s.connect = func(ctx context.Context) error { return spec.Connect(ctx, client) }
		}
		if spec.Close != nil {
			s.close = func() error { return spec.Close(client) }
		}
		s.execute = func(ctx context.Context, name string, v any, absent, dry bool, out io.Writer, t Target, approve func(context.Context, Approval) error, approval Approval) error {
			a, ok := all[name]
			if !ok {
				return fmt.Errorf("unknown action %q", name)
			}
			return a.execute(ctx, Run[C]{Client: client, Out: out, Target: t, Approve: approve, reason: func(reason string) { s.reason = reason }}, v, absent, dry, approval)
		}
		return s, nil
	}
	return p
}

// Reason is the most recent successful no-op outcome, for host summaries.
func (s *Session) Reason() string {
	if s == nil {
		return ""
	}
	return s.reason
}

func approval[C any](ctx context.Context, r Run[C], a Approval) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.Approve == nil {
		return errors.New("destructive plugin action requires approval")
	}
	return r.Approve(ctx, a)
}
func NewAction[C, P any](s ActionSpec[C, P]) Action[C] {
	return Action[C]{action[C]{name: s.Name, attrs: s.Attributes, valid: s.Decode != nil && s.Do != nil,
		decode: func(d *Decoder) (any, error) { return s.Decode(d) },
		execute: func(ctx context.Context, r Run[C], v any, _, dry bool, a Approval) error {
			if dry {
				_, err := fmt.Fprintln(r.Out, "planned operation")
				return err
			}
			if s.Destructive {
				a.Action = "execute"
				if err := approval(ctx, r, a); err != nil {
					return err
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return s.Do(ctx, r, v.(P))
		}}}
}
func NewObject[C, T any](s ObjectSpec[C, T]) Object[C] {
	return Object[C]{action[C]{name: s.Name, attrs: s.Attributes, object: true, valid: s.Decode != nil && s.Read != nil && s.Create != nil && s.Delete != nil && s.Diff != nil,
		decode: func(d *Decoder) (any, error) { return s.Decode(d) },
		execute: func(ctx context.Context, r Run[C], v any, absent, dry bool, a Approval) error {
			want := v.(T)
			have, found, err := s.Read(ctx, r, want)
			if err != nil {
				return err
			}
			mode := "no changes"
			changes := []Change{}
			replace := s.Update == nil
			if absent {
				if found {
					mode = "- delete"
				} else {
					mode = "already absent"
				}
			} else if !found {
				mode = "+ create"
			} else {
				changes = s.Diff(have, want)
				for _, c := range changes {
					known := false
					for _, attr := range s.Attributes {
						if attr.Name == c.Attribute {
							known = true
							replace = replace || attr.Replace
						}
					}
					if !known {
						return fmt.Errorf("diff returned unknown attribute %q", c.Attribute)
					}
				}
				if len(changes) > 0 {
					mode = "~ update"
					if replace {
						mode = "-/+ replace"
					}
				}
			}
			if _, err = fmt.Fprintln(r.Out, mode); err != nil {
				return err
			}
			for _, c := range changes {
				from, to := c.From, c.To
				suffix := ""
				for _, attr := range s.Attributes {
					if attr.Name == c.Attribute {
						if attr.Sensitive {
							from, to = "(sensitive)", "(sensitive)"
						}
						if attr.Replace {
							suffix = " (forces replacement)"
						}
					}
				}
				if _, err = fmt.Fprintf(r.Out, "  %s: %s → %s%s\n", c.Attribute, from, to, suffix); err != nil {
					return err
				}
			}
			if dry {
				return nil
			}
			if mode == "no changes" || mode == "already absent" {
				r.reason(mode)
			}
			if mode == "- delete" || mode == "-/+ replace" {
				a.Action = "delete"
				if !absent {
					a.Action = "replace"
				}
				if err = approval(ctx, r, a); err != nil {
					return err
				}
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			switch mode {
			case "+ create":
				return s.Create(ctx, r, want)
			case "~ update":
				return s.Update(ctx, r, have, want)
			case "- delete":
				return s.Delete(ctx, r, have)
			case "-/+ replace":
				if err = s.Delete(ctx, r, have); err != nil {
					return err
				}
				if err = ctx.Err(); err != nil {
					return err
				}
				return s.Create(ctx, r, want)
			}
			return nil
		}}}
}
