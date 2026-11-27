package profile_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vanvanni/plonkr/plugin"
	"github.com/vanvanni/plonkr/profile"
	"github.com/vanvanni/plonkr/provision"
	"github.com/vanvanni/plonkr/runlog"
	"github.com/vanvanni/plonkr/runner"
	"github.com/vanvanni/plonkr/secret"
	"github.com/zclconf/go-cty/cty"
)

type fakeClient struct{ token secret.Value }
type fakeState struct {
	connect, close, configure int
	calls                     []string
	fail                      bool
}

func fakeSet(t *testing.T, state *fakeState) plugin.Set {
	t.Helper()
	p := plugin.New(plugin.Spec[fakeClient]{Name: "fake", Settings: []plugin.Attribute{{Name: "endpoint", Required: true}, {Name: "token", Required: true, Sensitive: true}}, Configure: func(d *plugin.Decoder) (fakeClient, error) {
		state.configure++
		_ = d.String("endpoint")
		return fakeClient{d.Secret("token")}, d.Err()
	}, Connect: func(context.Context, fakeClient) error { state.connect++; return nil }, Close: func(fakeClient) error { state.close++; return nil }, Actions: []plugin.Action[fakeClient]{plugin.NewAction(plugin.ActionSpec[fakeClient, string]{Name: "ping", Attributes: []plugin.Attribute{{Name: "value", Required: true}, {Name: "identity", Static: true}, {Name: "password", Sensitive: true}}, Decode: func(d *plugin.Decoder) (string, error) {
		value := d.String("value")
		_ = d.OptionalString("identity", "")
		return value, d.Err()
	}, Do: func(ctx context.Context, r plugin.Run[fakeClient], value string) error {
		state.calls = append(state.calls, value)
		token := r.Client.token.Reveal()
		io.WriteString(r.Out, token[:3])
		io.WriteString(r.Out, token[3:])
		if state.fail {
			return errors.New(token)
		}
		return nil
	}})}})
	set, err := plugin.NewSet(p)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

const pluginSettings = `plugin "fake" {
 endpoint = "https://example.test"
 token = "credential"
}
`
const pluginDefinitions = `fake "web" {
 action = "ping"
 value = "web"
}
fake "db" {
 action = "ping"
 value = "db"
}
`
const pluginTasks = `tasks {
 task "Create" {
  order = 1
  setup = [fake("web", "db")]
 }
}
`

func TestPluginGraph(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".hcl"), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("settings", pluginSettings)
	write("catalog", "include = [\"settings\"]\n"+pluginDefinitions)
	write("left", "include = [\"catalog\"]\n")
	write("right", "include = [\"catalog\"]\n")
	state := &fakeState{}
	set := fakeSet(t, state)
	p, err := profile.Prepare([]byte("include = [\"left\",\"right\"]\n"+pluginTasks), filepath.Join(dir, "root.hcl"), dir, set)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.ClosePlugins()
	if state.connect != 0 || state.configure != 1 {
		t.Fatal(*state)
	}
	if len(plan.Definitions["fake"]) != 2 || plan.NeedsTarget() {
		t.Fatal("invalid plan")
	}
	for _, dry := range []bool{true, false} {
		e := provision.Executor{Plan: plan, DryRun: dry}
		summary, err := runner.Run(t.Context(), plan, runner.Options{DryRun: dry, PluginPreview: e.Preview}, e.Execute, nil)
		if err != nil {
			t.Fatal(err)
		}
		if dry && summary.Counts.Planned != 2 {
			t.Fatal(summary)
		}
	}
	if !reflect.DeepEqual(state.calls, []string{"web", "db"}) || state.connect != 1 {
		t.Fatal(*state)
	}
	plan.ClosePlugins()
	if state.close != 1 {
		t.Fatal(*state)
	}
}
func TestPluginValidationEdges(t *testing.T) {
	cases := []struct {
		name, source, want string
		bind               bool
	}{
		{"not loaded", pluginDefinitions + pluginTasks, "need plugin", false},
		{"call not loaded", pluginTasks, "needs plugin", false},
		{"unknown plugin", "plugin \"other\" {}\n" + pluginTasks, "unknown plugin", false},
		{"unknown action", pluginSettings + strings.ReplaceAll(pluginDefinitions, "ping", "missing") + pluginTasks, "unknown", false},
		{"duplicate definition", pluginSettings + pluginDefinitions + pluginDefinitions + pluginTasks, "duplicate", false},
		{"unknown attribute", pluginSettings + strings.Replace(pluginDefinitions, `value = "web"`, `unknown = "web"`, 1) + pluginTasks, "schema", false},
		{"missing setting", strings.Replace(pluginSettings, ` endpoint = "https://example.test"`, "", 1) + pluginDefinitions + pluginTasks, "required", true},
		{"conflict", pluginSettings + "plugin \"fake\" { token = \"other\" }\n" + pluginDefinitions + pluginTasks, "conflicting", true},
		{"unknown call", pluginSettings + pluginDefinitions + strings.Replace(pluginTasks, `"db"`, `"missing"`, 1), "unknown fake definition", true},
		{"wrong phase", pluginSettings + pluginDefinitions + strings.Replace(pluginTasks, "setup =", "script =", 1), "only allowed in setup", false},
		{"empty call", pluginSettings + pluginDefinitions + strings.Replace(pluginTasks, `"web", "db"`, "", 1), "arguments", false},
		{"ensure on operation", pluginSettings + strings.Replace(pluginDefinitions, `action = "ping"`, `action = "ping"`+"\n ensure = \"absent\"", 1) + pluginTasks, "ensure", false},
		{"static argument", "arguments {\n argument \"id\" { type = string }\n}\n" + pluginSettings + strings.Replace(pluginDefinitions, `value = "web"`, `value = "web"`+"\n identity = args.id", 1) + pluginTasks, "static", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := &fakeState{}
			p, err := profile.Prepare([]byte(tc.source), "root.hcl", t.TempDir(), fakeSet(t, state))
			if tc.bind && err == nil {
				_, err = p.Bind(nil)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v, want %s", err, tc.want)
			}
			if strings.Contains(err.Error(), "credential") {
				t.Fatal("secret leaked")
			}
			if state.connect != 0 {
				t.Fatal("network during validation")
			}
		})
	}
}
func TestPluginSecretsAndBypass(t *testing.T) {
	for _, mode := range []string{"normal", "bypass", "flag", "failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			state := &fakeState{fail: mode == "failure"}
			source := "name = \"credential\"\n" + pluginSettings + pluginDefinitions + pluginTasks
			if mode == "flag" {
				source = strings.Replace(source, `setup = [fake("web", "db")]`, `setup = [flag("create", fake("web", "db"))]`, 1)
			}
			p, err := profile.Prepare([]byte(source), "root.hcl", t.TempDir(), fakeSet(t, state))
			if err != nil {
				t.Fatal(err)
			}
			plan, err := p.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.ClosePlugins()
			journal, err := runlog.Open(t.TempDir(), "", "fake", plan)
			if err != nil {
				t.Fatal(err)
			}
			e := provision.Executor{Plan: plan}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			var output bytes.Buffer
			_, runErr := runner.Run(ctx, plan, runner.Options{BypassTasks: map[string]bool{"Create": mode == "bypass"}}, e.Execute, func(event runner.Event) {
				event, err = journal.Event(event)
				if err != nil {
					t.Error(err)
				}
				fmt.Fprint(&output, event.Text)
				if event.Err != nil {
					fmt.Fprint(&output, event.Err)
				}
			})
			journal.Finish(runErr, false)
			if strings.Contains(journal.Summary(runErr, false), "credential") {
				t.Fatal("summary disclosed secret")
			}
			data, err := os.ReadFile(journal.Path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), "credential") || bytes.Contains(data, []byte("credential")) {
				t.Fatal("secret disclosed")
			}
			if (mode == "failure" || mode == "cancel") != (runErr != nil) {
				t.Fatal(runErr)
			}
			if mode == "bypass" || mode == "flag" || mode == "cancel" {
				if state.connect != 0 {
					t.Fatal("bypassed plugin connected")
				}
			}
			plan.ClosePlugins()
			if state.close != state.connect {
				t.Fatal(*state)
			}
		})
	}
}
func TestPluginArgumentBindingAndUnusedCatalog(t *testing.T) {
	source := `arguments {
 argument "machine" {
  type = string
  default = "web"
 }
}

` + pluginSettings + `fake "unused" {
 action = "ping"
 value = args.machine
 password = 42
}
` + strings.Replace(pluginDefinitions, `value = "web"`, `value = args.machine`, 1) + pluginTasks
	p, err := profile.Prepare([]byte(source), "root.hcl", t.TempDir(), fakeSet(t, &fakeState{}))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Bind(map[string]cty.Value{"machine": cty.StringVal("bound")})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.ClosePlugins()
	if plan.Definitions["fake"]["web"].Value != "bound" || len(plan.Definitions["fake"]) != 2 {
		t.Fatal("binding")
	}
}

func FuzzPluginGraph(f *testing.F) {
	for _, s := range []string{pluginSettings + pluginDefinitions + pluginTasks, `plugin "other" {}`, pluginSettings + `fake "x" { action = "missing" }`, pluginSettings + strings.Replace(pluginDefinitions, `value = "web"`, `value = run("oops")`, 1) + pluginTasks} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 1<<16 {
			t.Skip()
		}
		state := &fakeState{}
		p, err := profile.Prepare([]byte(source), "fuzz.hcl", t.TempDir(), fakeSet(t, state))
		if err == nil && len(p.Arguments()) == 0 {
			plan, err := p.Bind(nil)
			if err == nil {
				plan.ClosePlugins()
			}
		}
		if state.connect != 0 {
			t.Fatal("preflight connected")
		}
	})
}
