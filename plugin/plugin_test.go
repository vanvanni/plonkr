package plugin_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/plugin"
	"github.com/vanvanni/plonkr/plugin/plugintest"
	"github.com/vanvanni/plonkr/plugins"
	"github.com/vanvanni/plonkr/secret"
	"github.com/zclconf/go-cty/cty"
)

func TestObjectLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name                                                               string
		found, changed, replace, absent, dry, refuse, failCreate, mismatch bool
		want                                                               []string
		bad                                                                bool
	}{
		{name: "create", want: []string{"GET", "POST"}},
		{name: "matching", found: true, want: []string{"GET"}},
		{name: "update", found: true, changed: true, want: []string{"GET", "PATCH"}},
		{name: "replace", found: true, changed: true, replace: true, want: []string{"GET", "DELETE", "POST"}},
		{name: "delete", found: true, absent: true, want: []string{"GET", "DELETE"}},
		{name: "absent", absent: true, want: []string{"GET"}},
		{name: "refuse", found: true, absent: true, refuse: true, want: []string{"GET"}, bad: true},
		{name: "failed replacement", found: true, changed: true, replace: true, failCreate: true, want: []string{"GET", "DELETE", "POST"}, bad: true},
		{name: "identity mismatch", found: true, mismatch: true, want: []string{"GET"}, bad: true},
		{name: "dry create", dry: true, want: []string{"GET"}},
		{name: "dry replace", found: true, changed: true, replace: true, dry: true, want: []string{"GET"}},
		{name: "dry delete", found: true, absent: true, dry: true, want: []string{"GET"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			methods := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				if r.Method == "POST" && tc.failCreate {
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			call := func(ctx context.Context, method string) error {
				req, _ := http.NewRequestWithContext(ctx, method, server.URL, nil)
				resp, err := server.Client().Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					return errors.New("API failure")
				}
				return nil
			}
			object := plugin.NewObject(plugin.ObjectSpec[string, int]{Name: "machine", Attributes: []plugin.Attribute{{Name: "image", Replace: tc.replace, Sensitive: true}}, Decode: func(*plugin.Decoder) (int, error) { return 2, nil }, Read: func(ctx context.Context, _ plugin.Run[string], _ int) (int, bool, error) {
				err := call(ctx, "GET")
				if tc.mismatch {
					err = errors.New("identity mismatch")
				}
				return 1, tc.found, err
			}, Create: func(ctx context.Context, _ plugin.Run[string], _ int) error { return call(ctx, "POST") }, Update: func(ctx context.Context, _ plugin.Run[string], _, _ int) error { return call(ctx, "PATCH") }, Delete: func(ctx context.Context, _ plugin.Run[string], _ int) error { return call(ctx, "DELETE") }, Diff: func(_, _ int) []plugin.Change {
				if tc.changed {
					return []plugin.Change{{Attribute: "image", From: "oldsecret", To: "newsecret"}}
				}
				return nil
			}})
			connected, closed := 0, 0
			p := plugin.New(plugin.Spec[string]{Name: "fake", Configure: func(*plugin.Decoder) (string, error) { return "", nil }, Connect: func(context.Context, string) error { connected++; return nil }, Close: func(string) error { closed++; return nil }, Objects: []plugin.Object[string]{object}})
			set, err := plugin.NewSet(p)
			if err != nil {
				t.Fatal(err)
			}
			p, _ = set.Get("fake")
			s, err := p.Configure(plugin.NewDecoder("", nil, nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			a := &plugintest.Approver{}
			if tc.refuse {
				a.Err = errors.New("refused")
			}
			var out bytes.Buffer
			err = s.Execute(t.Context(), "machine", 2, tc.absent, tc.dry, &out, nil, a.Approve, plugin.Approval{})
			if (err != nil) != tc.bad {
				t.Fatalf("error %v", err)
			}
			if !reflect.DeepEqual(methods, tc.want) {
				t.Fatalf("calls %v, want %v", methods, tc.want)
			}
			if strings.Contains(out.String(), "secret") {
				t.Fatal("secret in diff", out.String())
			}
			if tc.dry && len(a.Calls) > 0 {
				t.Fatal("dry run prompted")
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if connected != 1 || closed != 1 {
				t.Fatal(connected, closed)
			}
		})
	}
}
func TestSessionFailures(t *testing.T) {
	for _, connectErr := range []error{nil, errors.New("offline")} {
		closes, connects := 0, 0
		a := plugin.NewAction(plugin.ActionSpec[int, int]{Name: "reboot", Decode: func(*plugin.Decoder) (int, error) { return 0, nil }, Do: func(context.Context, plugin.Run[int], int) error { return context.Canceled }, Destructive: true})
		p := plugin.New(plugin.Spec[int]{Name: "fake", Configure: func(*plugin.Decoder) (int, error) { return 0, nil }, Connect: func(context.Context, int) error { connects++; return connectErr }, Close: func(int) error { closes++; return nil }, Actions: []plugin.Action[int]{a}})
		s, _ := p.Configure(plugin.NewDecoder("", nil, nil, nil))
		var out bytes.Buffer
		if err := s.Execute(t.Context(), "reboot", 0, false, false, &out, nil, nil, plugin.Approval{}); err == nil {
			t.Fatal("missing approval allowed")
		}
		s.Close()
		if connects != 1 || (connectErr == nil && closes != 1) || (connectErr != nil && closes != 0) {
			t.Fatal(connects, closes)
		}
	}
}

func TestOperationAndCancellation(t *testing.T) {
	calls := 0
	p := plugin.New(plugin.Spec[int]{Name: "fake", Configure: func(*plugin.Decoder) (int, error) { return 0, nil }, Actions: []plugin.Action[int]{plugin.NewAction(plugin.ActionSpec[int, int]{Name: "reboot", Destructive: true, Decode: func(*plugin.Decoder) (int, error) { return 0, nil }, Do: func(context.Context, plugin.Run[int], int) error { calls++; return context.Canceled }})}})
	s, _ := p.Configure(plugin.NewDecoder("", nil, nil, nil))
	defer s.Close()
	a := &plugintest.Approver{}
	var out bytes.Buffer
	if err := s.Execute(t.Context(), "reboot", 0, false, true, &out, nil, a.Approve, plugin.Approval{}); err != nil || calls != 0 || len(a.Calls) != 0 {
		t.Fatal(err, calls, a.Calls)
	}
	if err := s.Execute(t.Context(), "reboot", 0, false, false, &out, nil, a.Approve, plugin.Approval{}); !errors.Is(err, context.Canceled) || calls != 1 || len(a.Calls) != 1 {
		t.Fatal(err, calls, a.Calls)
	}
}
func TestSetInvariants(t *testing.T) {
	_ = plugins.All()
	for _, name := range []string{"run", "configuration", "plugin", "bad-name", "Fake", ""} {
		p := plugin.New(plugin.Spec[int]{Name: name, Configure: func(*plugin.Decoder) (int, error) { return 0, nil }})
		if _, err := plugin.NewSet(p); err == nil {
			t.Fatal(name)
		}
	}
	p := plugin.New(plugin.Spec[int]{Name: "fake", Configure: func(*plugin.Decoder) (int, error) { return 0, nil }})
	if _, err := plugin.NewSet(p, p); err == nil {
		t.Fatal("duplicates")
	}
}
func TestDecoder(t *testing.T) {
	r := &secret.Redactor{}
	defer r.Clear()
	d, err := plugintest.Decode("token = \"hunter2\"\nnumber = 3\nnetwork = {ip = 4}\n", "web", []plugin.Attribute{{Name: "token", Sensitive: true}, {Name: "number"}, {Name: "network"}}, nil, r)
	if err != nil {
		t.Fatal(err)
	}
	if d.Label() != "web" || d.Int("number") != 3 {
		t.Fatal(d.Err())
	}
	v := d.Secret("token")
	if fmt.Sprintf("%#v", v) != "(sensitive)" || r.Text("hunter2") != "[sensitive]" {
		t.Fatal("secret formatting")
	}
	d.Object("network").String("ip")
	if d.Err() == nil {
		t.Fatal("nested error lost")
	}
	for _, v := range []cty.Value{cty.NullVal(cty.String), cty.UnknownVal(cty.String), cty.StringVal("x").Mark("sensitive"), cty.NumberFloatVal(1.5)} {
		d := plugin.NewDecoder("", map[string]cty.Value{"v": v}, []plugin.Attribute{{Name: "v"}}, nil)
		d.Int("v")
		if d.Err() == nil {
			t.Fatal("invalid value")
		}
	}
	d = plugin.NewDecoder("", map[string]cty.Value{"s": cty.StringVal("hidden")}, []plugin.Attribute{{Name: "s", Sensitive: true}}, nil)
	d.String("s")
	if d.Err() == nil {
		t.Fatal("secret access")
	}
}
func TestDependencyRules(t *testing.T) {
	for _, root := range []string{".", "../plugins"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, i := range f.Imports {
				p, _ := strconv.Unquote(i.Path.Value)
				if strings.HasPrefix(p, "github.com/vanvanni/plonkr/") {
					suffix := strings.TrimPrefix(p, "github.com/vanvanni/plonkr/")
					if suffix != "plugin" && suffix != "secret" && suffix != "posix" {
						t.Errorf("forbidden import %s in %s", p, path)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
