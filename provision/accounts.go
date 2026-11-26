package provision

import (
	"strconv"
	"strings"

	"github.com/vanvanni/plonkr/profile"
)

func groupScript(g profile.Group) string {
	var b strings.Builder
	b.WriteString("name=" + quote(g.Name) + "\n")
	if g.GID != nil {
		b.WriteString("gid=" + quote(strconv.Itoa(*g.GID)) + "\n")
	}
	b.WriteString(`if entry=$(getent group "$name" 2>/dev/null) && [ -n "$entry" ]; then
`)
	if g.GID != nil {
		b.WriteString(`  actual_gid=$(printf '%s\n' "$entry" | cut -d: -f3)
  [ "$actual_gid" = "$gid" ] || { printf 'Existing group %s has GID %s, expected %s\n' "$name" "$actual_gid" "$gid" >&2; exit 1; }
`)
	}
	b.WriteString(`  printf 'Group %s ready (existing)\n' "$name"
else
`)
	if g.GID != nil {
		b.WriteString(`  if getent group "$gid" >/dev/null 2>&1; then
    printf 'GID %s is already in use by another group\n' "$gid" >&2
    exit 1
  fi
  groupadd -g "$gid" -- "$name"
`)
	} else {
		b.WriteString(`  groupadd -- "$name"
`)
	}
	b.WriteString(`  printf 'Group %s created\n' "$name"
fi
`)
	return asRoot(b.String())
}

func userScript(u profile.User) string {
	var b strings.Builder
	b.WriteString("name=" + quote(u.Name) + "\nhome=" + quote(u.Home) + "\nshell=" + quote(u.Shell) + "\n")
	b.WriteString(`if id -u "$name" >/dev/null 2>&1; then
  [ "$(id -u "$name")" != 0 ] || { echo 'Refusing to modify a UID 0 account' >&2; exit 1; }
  entry=$(getent passwd "$name")
  actual_home=$(printf '%s\n' "$entry" | cut -d: -f6)
  actual_shell=$(printf '%s\n' "$entry" | cut -d: -f7)
  [ "$actual_home" = "$home" ] && [ "$actual_shell" = "$shell" ] || { printf 'Existing user %s home/shell differs from declared values\n' "$name" >&2; exit 1; }
else
`)
	useraddFlags := ` --home-dir "$home" --shell "$shell"`
	if u.Service {
		useraddFlags += " --system"
	}
	b.WriteString("  if getent group \"$name\" >/dev/null 2>&1; then\n")
	b.WriteString("    useradd --create-home -g \"$name\"" + useraddFlags + " -- \"$name\"\n")
	b.WriteString("  else\n")
	b.WriteString("    useradd --create-home --user-group" + useraddFlags + " -- \"$name\"\n")
	b.WriteString("  fi\n")
	b.WriteString("fi\n")

	if len(u.Groups) > 0 {
		for _, g := range u.Groups {
			qg := quote(g)
			b.WriteString("getent group " + qg + " >/dev/null 2>&1 || { printf 'Required supplementary group %s does not exist\\n' " + qg + " >&2; exit 1; }\n")
		}
		b.WriteString("usermod -a -G " + quote(strings.Join(u.Groups, ",")) + " -- \"$name\"\n")
	}

	if len(u.PublicKeys) > 0 {
		var keys strings.Builder
		keys.WriteString(validateHome)
		keys.WriteString(ensureSSHDir)
		keys.WriteString(`authorized="$HOME/.ssh/authorized_keys"
[ ! -L "$authorized" ] || { echo 'Refusing symlink authorized_keys' >&2; exit 1; }
umask 077
touch "$authorized"
chmod 600 "$authorized"
`)
		keys.WriteString(appendLineFunc)
		for _, key := range u.PublicKeys {
			keys.WriteString("append_line " + quote(key) + " \"$authorized\"\n")
		}
		b.WriteString(asUser(u.Name, keys.String()) + "\n")
	}

	b.WriteString("printf 'User %s ready\\n' \"$name\"\n")
	return asRoot(b.String())
}
