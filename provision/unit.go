package provision

import (
	"fmt"

	"github.com/vanvanni/plonkr/profile"
)

func unitScript(u profile.Unit) string {
	script := "destination=" + quote("/etc/systemd/system/"+u.Name) +
		fmt.Sprintf("\nexpected=%d\n", len(u.Content)) +
		`[ -d /etc/systemd/system ] && [ ! -L /etc/systemd/system ] || { echo 'System unit directory must be a real directory' >&2; exit 1; }
[ ! -L "$destination" ] && { [ ! -e "$destination" ] || [ -f "$destination" ]; } || { echo 'Unit destination must be a regular file, not a symlink' >&2; exit 1; }
umask 077
tmp=$(mktemp /etc/systemd/system/.plonkr-unit.XXXXXX)
trap 'rm -f -- "$tmp"' EXIT
cat > "$tmp"
[ "$(wc -c < "$tmp")" -eq "$expected" ] || { echo 'Incomplete unit transfer' >&2; exit 1; }
chown root:root "$tmp"
chmod 0644 "$tmp"
mv -fT -- "$tmp" "$destination"
printf 'Installed unit %s\n' "$destination"
`
	return asRoot(script)
}
