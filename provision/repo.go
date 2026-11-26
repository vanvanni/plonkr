package provision

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/vanvanni/plonkr/profile"
)

func repoScript(r profile.Repository, key []byte) string {
	return repoScriptReplacement(r, key, "")
}

func repoScriptReplacement(r profile.Repository, key []byte, approvedHash string) string {
	var b strings.Builder
	b.WriteString("destination=" + quote(r.Path) + "\n")
	b.WriteString("repo_url=" + quote(r.URL) + "\n")
	b.WriteString("repo_name=" + quote(r.Name) + "\n")
	b.WriteString("approved_hash=" + quote(approvedHash) + "\n")
	if r.Ref != "" {
		b.WriteString("repo_ref=" + quote(r.Ref) + "\n")
	}
	b.WriteString("command -v git >/dev/null\n")
	b.WriteString(validateHome)
	b.WriteString("old_umask=$(umask)\n")

	if len(r.KnownHosts) > 0 || r.Key != "" {
		b.WriteString(ensureSSHDir)
	}

	if len(r.KnownHosts) > 0 {
		b.WriteString(`known="$HOME/.ssh/known_hosts"
[ ! -L "$known" ] || { echo 'Refusing symlink known_hosts' >&2; exit 1; }
umask 077
touch "$known"
chmod 600 "$known"
`)
		b.WriteString(appendLineFunc)
		for _, host := range r.KnownHosts {
			b.WriteString("append_line " + quote(host) + " \"$known\"\n")
		}
	}

	b.WriteString(`tmp=''
clone_tmp=''
in_place=''
trap 'if [ -n "$tmp" ]; then rm -f -- "$tmp"; fi; if [ -n "$clone_tmp" ]; then rm -rf -- "$clone_tmp"; fi; if [ -n "$in_place" ]; then find "$destination" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +; fi' EXIT
`)

	if r.Key != "" {
		sum := fmt.Sprintf("%x", sha256.Sum256(key))
		b.WriteString("expected=" + quote(sum) + "\n")
		b.WriteString(`command -v sha256sum >/dev/null
command -v cmp >/dev/null
keyfile="$HOME/.ssh/plonkr-$repo_name.key"
umask 077
tmp=$(mktemp "$HOME/.ssh/.plonkr-key.XXXXXX")
chmod 600 "$tmp"
cat > "$tmp"
actual=$(sha256sum < "$tmp")
[ "${actual%% *}" = "$expected" ] || { echo 'Key transfer checksum mismatch; refusing installation' >&2; exit 1; }
[ ! -L "$keyfile" ] && { [ ! -e "$keyfile" ] || [ -f "$keyfile" ]; } || { echo 'Deploy key must be a regular file, not a symlink' >&2; exit 1; }
if [ -e "$keyfile" ]; then
  [ ! -L "$keyfile" ] && [ -f "$keyfile" ] || { echo 'Deploy key must be a regular file' >&2; exit 1; }
  if ! cmp -s "$tmp" "$keyfile"; then
    current=$(sha256sum < "$keyfile")
    [ -n "$approved_hash" ] && [ "${current%% *}" = "$approved_hash" ] || { printf 'Different key file already exists at %s; replacement requires approval\n' "$keyfile" >&2; exit 1; }
    mv -fT -- "$tmp" "$keyfile"
    tmp=''
    printf 'Replaced deploy key: %s\n' "$keyfile"
  fi
  chmod 600 "$keyfile"
  if [ -n "$tmp" ]; then rm -f -- "$tmp"; fi
  tmp=''
else
  ln -T -- "$tmp" "$keyfile"
  chmod 600 "$keyfile"
  rm -f -- "$tmp"
  tmp=''
fi
escape_sq() {
  rest=$1
  out=''
  while :; do
    case "$rest" in
      *\'*)
        out="${out}${rest%%\'*}'\\''"
        rest=${rest#*\'}
        ;;
      *)
        printf '%s%s' "$out" "$rest"
        return
        ;;
    esac
  done
}
q_key=$(escape_sq "$keyfile")
q_known=$(escape_sq "$HOME/.ssh/known_hosts")
ssh_command="ssh -i '$q_key' -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o GlobalKnownHostsFile=/dev/null"
`)
	} else {
		b.WriteString("ssh_command='ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o GlobalKnownHostsFile=/dev/null'\n")
	}

	// An empty real directory is a valid clone target: Plonkr prepares one for
	// the repository user when the parent is not writable by that user.
	b.WriteString(`fresh=''
# Key and known_hosts files are private, but the checkout follows the caller's umask.
umask "$old_umask"
if [ -d "$destination" ] && [ ! -L "$destination" ] && [ -z "$(ls -A -- "$destination")" ]; then
  fresh=1
fi
if [ -z "$fresh" ] && { [ -e "$destination" ] || [ -L "$destination" ]; }; then
  [ -d "$destination" ] && [ ! -L "$destination" ] || { printf 'Checkout destination %s is not a real directory\n' "$destination" >&2; exit 1; }
  [ -d "$destination/.git" ] || [ -f "$destination/.git" ] || { printf 'Checkout destination %s is not a git repository root\n' "$destination" >&2; exit 1; }
  inside=$(git -C "$destination" rev-parse --is-inside-work-tree 2>&1 || true)
  [ "$inside" = true ] || { printf 'Checkout destination %s is not a valid git work tree: %s\n' "$destination" "$inside" >&2; exit 1; }
  actual_url=$(git -C "$destination" remote get-url origin 2>/dev/null || true)
  [ "$actual_url" = "$repo_url" ] || { printf 'Existing repository at %s has origin %s, expected %s\n' "$destination" "$actual_url" "$repo_url" >&2; exit 1; }
  actual_head=$(git -C "$destination" rev-parse --verify HEAD 2>/dev/null || true)
  [ -n "$actual_head" ] || { printf 'Existing repository at %s has no checked-out HEAD\n' "$destination" >&2; exit 1; }
`)
	if r.Ref != "" {
		if profile.IsCommitHash(r.Ref) {
			b.WriteString("  expected_commit=" + quote(strings.ToLower(r.Ref)) + "\n")
			b.WriteString(`  [ "$actual_head" = "$expected_commit" ] || { printf 'Existing repository at %s is at %s, expected commit %s\n' "$destination" "$actual_head" "$expected_commit" >&2; exit 1; }
`)
		} else {
			b.WriteString(`  actual_branch=$(git -C "$destination" symbolic-ref --quiet --short HEAD 2>/dev/null || true)
  if [ "$actual_branch" != "$repo_ref" ]; then
    tag_oid=$(git -C "$destination" rev-parse --verify "refs/tags/$repo_ref^{commit}" 2>/dev/null || true)
    [ -n "$tag_oid" ] && [ "$tag_oid" = "$actual_head" ] || { printf 'Existing repository at %s is not on requested ref %s\n' "$destination" "$repo_ref" >&2; exit 1; }
  fi
`)
		}
	}
	if r.Key != "" {
		b.WriteString("  git -C \"$destination\" config core.sshCommand \"$ssh_command\"\n")
	}
	b.WriteString(`  printf 'Repository %s already present and verified\n' "$repo_name"
else
  mkdir -p -- "$(dirname -- "$destination")"
`)
	switch {
	case r.Ref != "" && profile.IsCommitHash(r.Ref):
		// A prepared empty directory cannot be replaced from a sibling temporary
		// clone because the parent may not be writable, so clone in place and
		// empty it again if anything fails.
		b.WriteString(`  if [ -n "$fresh" ]; then
    in_place=1
    GIT_SSH_COMMAND="$ssh_command" GIT_SSH_VARIANT=ssh git -c core.sshCommand="$ssh_command" clone --no-checkout -- "$repo_url" "$destination"
    git -C "$destination" checkout --detach "$repo_ref"
`)
		if r.Key != "" {
			b.WriteString("    git -C \"$destination\" config core.sshCommand \"$ssh_command\"\n")
		}
		b.WriteString(`    in_place=''
  else
    clone_tmp=$(mktemp -d "$(dirname -- "$destination")/.plonkr-clone.XXXXXX")
    GIT_SSH_COMMAND="$ssh_command" GIT_SSH_VARIANT=ssh git -c core.sshCommand="$ssh_command" clone --no-checkout -- "$repo_url" "$clone_tmp"
    git -C "$clone_tmp" checkout --detach "$repo_ref"
`)
		if r.Key != "" {
			b.WriteString("    git -C \"$clone_tmp\" config core.sshCommand \"$ssh_command\"\n")
		}
		b.WriteString(`    [ ! -e "$destination" ] && [ ! -L "$destination" ] || { printf 'Checkout destination %s appeared during clone\n' "$destination" >&2; exit 1; }
    chmod "$(printf '%o' $((0777 & ~$(umask))))" "$clone_tmp"
    mv -T -- "$clone_tmp" "$destination"
    clone_tmp=''
  fi
`)
	case r.Ref != "":
		b.WriteString("  GIT_SSH_COMMAND=\"$ssh_command\" GIT_SSH_VARIANT=ssh git -c core.sshCommand=\"$ssh_command\" clone --branch \"$repo_ref\" -- \"$repo_url\" \"$destination\"\n")
		if r.Key != "" {
			b.WriteString("  git -C \"$destination\" config core.sshCommand \"$ssh_command\"\n")
		}
	default:
		b.WriteString("  GIT_SSH_COMMAND=\"$ssh_command\" GIT_SSH_VARIANT=ssh git -c core.sshCommand=\"$ssh_command\" clone -- \"$repo_url\" \"$destination\"\n")
		if r.Key != "" {
			b.WriteString("  git -C \"$destination\" config core.sshCommand \"$ssh_command\"\n")
		}
	}
	b.WriteString(`  printf 'Repository %s ready\n' "$repo_name"
fi
`)
	return b.String()
}

// checkoutDirScript creates the checkout directory for the repository user when
// that user cannot create it, for example /srv/app under a root-owned /srv.
// It changes nothing when the destination already exists or the user can create
// it themselves; the checkout step then validates or clones as before.
func checkoutDirScript(r profile.Repository) string {
	script := "destination=" + quote(r.Path) + "\nowner=" + quote(r.User) + `
if [ -e "$destination" ] || [ -L "$destination" ]; then exit 0; fi
id -u "$owner" >/dev/null 2>&1 || exit 0
parent=$(dirname -- "$destination")
if [ -d "$parent" ] && command -v runuser >/dev/null 2>&1 &&
  runuser -u "$owner" -- sh -c 'test -w "$1" && test -x "$1"' sh "$parent"; then
  exit 0
fi
umask 022
mkdir -p -- "$parent"
group=$(id -gn "$owner")
install -d -m 0755 -o "$owner" -g "$group" -- "$destination"
printf 'Prepared checkout directory %s for %s\n' "$destination" "$owner"
`
	return asRootIfAvailable(script)
}
