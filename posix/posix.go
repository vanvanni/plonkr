// Package posix provides shared Linux shell and path primitives.
package posix

import (
	"path"
	"regexp"
	"strings"
	"unicode"
)

var linuxName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func ValidLinuxName(name string) bool { return name != "root" && linuxName.MatchString(name) }
func SafePath(value string) bool {
	return path.IsAbs(value) && path.Clean(value) == value && value != "/" && !strings.ContainsRune(value, '\\') && !strings.ContainsFunc(value, unicode.IsControl)
}
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func AsRoot(script string) string {
	qScript := Quote(script)
	return `if [ "$(id -u)" = 0 ]; then sh -ec ` + qScript + `; else sudo -n -- sh -ec ` + qScript + `; fi`
}

func AsUser(user, script string) string {
	qUser := Quote(user)
	// runuser and sudo keep the caller's directory, which the target user may not be
	// allowed to enter (for example /root).
	qScript := Quote("cd /\n" + script)
	return `if [ "$(id -un)" = ` + qUser + ` ]; then sh -ec ` + qScript +
		`; elif [ "$(id -u)" = 0 ]; then target_home=$(getent passwd ` + qUser + ` | cut -d: -f6); runuser -u ` + qUser + ` -- env HOME="$target_home" sh -ec ` + qScript +
		`; else sudo -n -H -u ` + qUser + ` -- sh -ec ` + qScript + `; fi`
}
