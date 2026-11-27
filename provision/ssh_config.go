package provision

import (
	"net"
	"net/url"
	"strings"

	"github.com/vanvanni/plonkr/profile"
)

// Give each repository its own SSH identity without changing the public profile.
func repositoryAlias(r profile.Repository) (alias, host, port, cloneURL string) {
	if strings.Contains(r.URL, "://") {
		u, err := url.Parse(r.URL)
		if err != nil {
			return
		}
		host, port = u.Hostname(), u.Port()
		alias = r.Name + "." + host
		if strings.Contains(host, ":") {
			alias = r.Name + ".ssh"
		}
		if u.Scheme != "ssh" {
			return alias, host, port, r.URL
		}
		u.Host = alias
		if port != "" {
			u.Host = net.JoinHostPort(alias, port)
		}
		return alias, host, port, u.String()
	}
	colon := strings.Index(r.URL, ":")
	if strings.HasPrefix(r.URL, "git@[") {
		colon = strings.Index(r.URL, "]:") + 1
	}
	if colon < 4 {
		return
	}
	host = strings.Trim(r.URL[4:colon], "[]")
	alias = r.Name + "." + host
	if strings.Contains(host, ":") {
		alias = r.Name + ".ssh"
	}
	return alias, host, "", "git@" + alias + r.URL[colon:]
}

func repositoryConfigScript(r profile.Repository) string {
	alias, host, port, cloneURL := repositoryAlias(r)
	if alias == "" {
		return ""
	}
	block := "Host " + alias + "\n  HostName " + host + "\n  IdentityFile ~/.ssh/plonkr-" + r.Name + ".key\n  IdentitiesOnly yes\n"
	if port != "" {
		block += "  Port " + port + "\n"
	}
	begin, end := "# BEGIN plonkr "+alias, "# END plonkr "+alias
	return "# Repository-specific SSH configuration\nrepo_url=" + quote(cloneURL) + "\n" +
		"config_begin=" + quote(begin) + "\nconfig_end=" + quote(end) + "\nconfig_block=" + quote(block) + "\n" + `
config="$HOME/.ssh/config"
[ ! -L "$config" ] && { [ ! -e "$config" ] || [ -f "$config" ]; } || { echo 'SSH config must be a regular file, not a symlink' >&2; exit 1; }
config_lock="$HOME/.ssh/.plonkr-config.lock"
mkdir "$config_lock" || { echo 'Another SSH config update is active' >&2; config_lock=''; exit 1; }
tmp=$(mktemp "$HOME/.ssh/.plonkr-config.XXXXXX")
chmod 600 "$tmp"
printf '%s\n%s\nHost *\n%s\n' "$config_begin" "$config_block" "$config_end" > "$tmp"
if [ -e "$config" ]; then
  awk -v begin="$config_begin" -v end="$config_end" '
    $0 == begin { if (skip) exit 1; skip=1; next }
    $0 == end { if (!skip) exit 1; skip=0; next }
    !skip { print }
    END { if (skip) exit 1 }
  ' "$config" >> "$tmp"
fi
mv -fT -- "$tmp" "$config"
tmp=''
chmod 600 "$config"
rmdir "$config_lock"
config_lock=''
`
}
