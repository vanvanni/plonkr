package provision

func timezoneScript(tz string) string {
	script := "tz=" + quote(tz) + "\n" + `[ -f "/usr/share/zoneinfo/$tz" ] || { printf 'Unknown timezone %s\n' "$tz" >&2; exit 1; }
if command -v timedatectl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  timedatectl set-timezone "$tz"
else
  [ -L /etc/localtime ] || [ ! -d /etc/localtime ] || { echo '/etc/localtime must not be a real directory' >&2; exit 1; }
  rm -f -- /etc/localtime
  ln -s -- "/usr/share/zoneinfo/$tz" /etc/localtime
  if [ -e /etc/timezone ] && [ ! -L /etc/timezone ]; then
    printf '%s\n' "$tz" > /etc/timezone
  fi
fi
printf 'Timezone set to %s\n' "$tz"
`
	return asRoot(script)
}
