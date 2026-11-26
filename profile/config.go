package profile

import (
	"fmt"
	"maps"
	"math/big"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"golang.org/x/crypto/ssh"
)

var (
	linuxName    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	timezoneName = regexp.MustCompile(`^[A-Za-z0-9_+-]+(?:/[A-Za-z0-9_+-]+)*$`)
)

// ValidLinuxName reports whether name is a supported non-root user or group name.
func ValidLinuxName(name string) bool {
	return name != "root" && linuxName.MatchString(name)
}

func safePOSIXPath(value string) bool {
	return path.IsAbs(value) && path.Clean(value) == value && value != "/" &&
		!strings.ContainsRune(value, '\\') && !strings.ContainsFunc(value, unicode.IsControl)
}

func decodeConfiguration(block *hcl.Block, plan *Plan) (Task, error) {
	baseline := Task{Name: "Configuration", Order: -1, Baseline: true}
	fields, err := literalFields(block.Body, "groups", "users", "timezone")
	if err != nil {
		return Task{}, err
	}
	groups := fields.list("groups")
	users := fields.list("users")
	plan.Timezone = fields.optionalString("timezone", "")
	if fields.err != nil {
		return Task{}, fields.err
	}
	if _, set := fields.values["timezone"]; set && (len(plan.Timezone) > 128 || !timezoneName.MatchString(plan.Timezone)) {
		return Task{}, fmt.Errorf("invalid timezone")
	}

	gids := make(map[int]bool)
	for i, value := range groups {
		group, err := decodeGroup(value)
		if err != nil {
			return Task{}, fmt.Errorf("groups[%d]: %w", i, err)
		}
		if _, exists := plan.Groups[group.Name]; exists {
			return Task{}, fmt.Errorf("duplicate group name %q", group.Name)
		}
		if group.GID != nil {
			if gids[*group.GID] {
				return Task{}, fmt.Errorf("duplicate GID %d", *group.GID)
			}
			gids[*group.GID] = true
		}
		plan.Groups[group.Name] = group
		if group.Create {
			baseline.Steps = append(baseline.Steps, setupStep(OpGroup, group.Name))
		}
	}

	for i, value := range users {
		user, err := decodeUser(value)
		if err != nil {
			return Task{}, fmt.Errorf("users[%d]: %w", i, err)
		}
		if _, exists := plan.Users[user.Name]; exists {
			return Task{}, fmt.Errorf("duplicate user name %q", user.Name)
		}
		plan.Users[user.Name] = user
		if user.Create {
			for _, name := range user.Groups {
				if group, exists := plan.Groups[name]; exists && !group.Create {
					return Task{}, fmt.Errorf("auto-created user %q references group %q with create = false", user.Name, name)
				}
			}
			baseline.Steps = append(baseline.Steps, setupStep(OpUser, user.Name))
		}
	}

	if plan.Timezone != "" {
		baseline.Steps = append(baseline.Steps, setupStep(OpTimezone, plan.Timezone))
	}
	return baseline, nil
}

func decodeGroup(value cty.Value) (Group, error) {
	fields := objectFields(value, "name", "gid", "create")
	group := Group{Name: fields.requiredString("name"), Create: fields.boolean("create", true)}
	if _, set := fields.values["gid"]; set {
		gid := fields.integer("gid", 1)
		group.GID = new(gid)
	}
	if fields.err != nil {
		return Group{}, fields.err
	}
	if !ValidLinuxName(group.Name) {
		return Group{}, fmt.Errorf("invalid Linux group name %q", group.Name)
	}
	return group, nil
}

func decodeUser(value cty.Value) (User, error) {
	fields := objectFields(value, "name", "service", "home", "shell", "groups", "public_keys", "create")
	user := User{
		Name: fields.requiredString("name"), Service: fields.boolean("service", false),
		Create: fields.boolean("create", true), Groups: fields.strings("groups"),
		PublicKeys: fields.strings("public_keys"),
	}
	home, shell := "/home/"+user.Name, "/bin/bash"
	if user.Service {
		home, shell = "/var/lib/"+user.Name, "/usr/sbin/nologin"
	}
	user.Home = fields.optionalString("home", home)
	user.Shell = fields.optionalString("shell", shell)
	if fields.err != nil {
		return User{}, fields.err
	}
	if !ValidLinuxName(user.Name) {
		return User{}, fmt.Errorf("invalid Linux user name %q", user.Name)
	}
	if !safePOSIXPath(user.Home) || !safePOSIXPath(user.Shell) {
		return User{}, fmt.Errorf("user %q home and shell must be safe absolute POSIX paths", user.Name)
	}
	for _, group := range user.Groups {
		if !ValidLinuxName(group) {
			return User{}, fmt.Errorf("invalid Linux group name %q for user %q", group, user.Name)
		}
	}
	for i, line := range user.PublicKeys {
		if !validPublicKeyLine(line) {
			return User{}, fmt.Errorf("invalid public_keys[%d] for user %q", i, user.Name)
		}
	}
	return user, nil
}

func validPublicKeyLine(line string) bool {
	if strings.ContainsFunc(line, func(r rune) bool { return unicode.IsControl(r) && r != '\t' }) {
		return false
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || len(options) != 0 || len(rest) != 0 {
		return false
	}
	return strings.Fields(line)[0] == key.Type()
}

// fieldValues checks literal types without HCL's implicit string/number/bool conversions.
type fieldValues struct {
	values map[string]cty.Value
	err    error
}

func literalFields(body hcl.Body, names ...string) (fieldValues, error) {
	attributes, err := blockAttributes(body, names...)
	if err != nil {
		return fieldValues{}, err
	}
	return evaluateLiterals(attributes, names...)
}

func evaluateLiterals(attributes hcl.Attributes, names ...string) (fieldValues, error) {
	values := make(map[string]cty.Value, len(attributes))
	for _, name := range names {
		if attribute := attributes[name]; attribute != nil {
			value, diagnostics := attribute.Expr.Value(nil)
			if diagnostics.HasErrors() {
				return fieldValues{}, diagnostics
			}
			values[name] = value
		}
	}
	return fieldValues{values: values}, nil
}

func objectFields(value cty.Value, names ...string) fieldValues {
	if value.IsNull() || !value.Type().IsObjectType() {
		return fieldValues{err: fmt.Errorf("expected an object")}
	}
	fields := fieldValues{values: value.AsValueMap()}
	for _, name := range slices.Sorted(maps.Keys(fields.values)) {
		if !slices.Contains(names, name) {
			fields.fail(name, "is not an allowed field")
		}
	}
	return fields
}

func (f *fieldValues) fail(name, message string) {
	if f.err == nil {
		f.err = fmt.Errorf("%s %s", name, message)
	}
}

func (f *fieldValues) requiredString(name string) string {
	if _, ok := f.values[name]; !ok {
		f.fail(name, "is required")
	}
	return f.optionalString(name, "")
}

func (f *fieldValues) optionalString(name, fallback string) string {
	value, ok := f.values[name]
	if !ok {
		return fallback
	}
	if !isType(value, cty.String) {
		f.fail(name, "must be a string")
		return fallback
	}
	return value.AsString()
}

func (f *fieldValues) boolean(name string, fallback bool) bool {
	value, ok := f.values[name]
	if !ok {
		return fallback
	}
	if !isType(value, cty.Bool) {
		f.fail(name, "must be a bool")
		return fallback
	}
	return value.True()
}

func (f *fieldValues) integer(name string, minimum int) int {
	value, ok := f.values[name]
	if !ok {
		f.fail(name, "is required")
	} else if isType(value, cty.Number) {
		number, accuracy := value.AsBigFloat().Int64()
		if accuracy == big.Exact && number == int64(int(number)) && number >= int64(minimum) {
			return int(number)
		}
	}
	f.fail(name, fmt.Sprintf("must be an integer >= %d", minimum))
	return 0
}

func (f *fieldValues) list(name string) []cty.Value {
	value, ok := f.values[name]
	if !ok {
		return nil
	}
	if !isSequence(value) {
		f.fail(name, "must be a list or tuple")
		return nil
	}
	return value.AsValueSlice()
}

func (f *fieldValues) strings(name string) []string {
	var result []string
	for _, value := range f.list(name) {
		if !isType(value, cty.String) {
			f.fail(name, "must contain only strings")
			return nil
		}
		result = append(result, value.AsString())
	}
	return result
}

func isType(value cty.Value, want cty.Type) bool {
	return value.IsKnown() && !value.IsNull() && value.Type().Equals(want)
}

func isSequence(value cty.Value) bool {
	return value.IsKnown() && !value.IsNull() && (value.Type().IsTupleType() || value.Type().IsListType())
}
