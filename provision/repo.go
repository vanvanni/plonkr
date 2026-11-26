package provision

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"plonk/profile"
)

func repoScript(r profile.Repository, key []byte) string {
	var b strings.Builder
	b.WriteString("destination=" + quote(r.Path) + "\n")
	b.WriteString("repo_url=" + quote(r.URL) + "\n")
	b.WriteString("repo_name=" + quote(r.Name) + "\n")
	if r.Ref != "" {
		b.WriteString("repo_ref=" + quote(r.Ref) + "\n")
	}
	b.WriteString("command -v git >/dev/null\n")
	b.WriteString(validateHome)

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
		for _, host := range r.KnownHosts {
			b.WriteString("host=" + quote(host) + "\ngrep -qxF -- \"$host\" \"$known\" || printf '%s\\n' \"$host\" >> \"$known\"\n")
		}
	}

	b.WriteString(`tmp=''
clone_tmp=''
trap 'if [ -n "$tmp" ]; then rm -f -- "$tmp"; fi; if [ -n "$clone_tmp" ]; then rm -rf -- "$clone_tmp"; fi' EXIT
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
  cmp -s "$tmp" "$keyfile" || { printf 'Different key file already exists at %s; refusing to replace it\n' "$keyfile" >&2; exit 1; }
  chmod 600 "$keyfile"
  rm -f -- "$tmp"
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
ssh_command="ssh -i '$q_key' -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile='$q_known'"
`)
	} else {
		b.WriteString("ssh_command='ssh -o BatchMode=yes -o StrictHostKeyChecking=yes'\n")
	}

	b.WriteString(`if [ -e "$destination" ] || [ -L "$destination" ]; then
  [ -d "$destination" ] && [ ! -L "$destination" ] || { printf 'Checkout destination %s is not a real directory\n' "$destination" >&2; exit 1; }
  [ -d "$destination/.git" ] || [ -f "$destination/.git" ] || { printf 'Checkout destination %s is not a git repository root\n' "$destination" >&2; exit 1; }
  inside=$(git -C "$destination" rev-parse --is-inside-work-tree 2>/dev/null || true)
  [ "$inside" = true ] || { printf 'Checkout destination %s is not a valid git work tree\n' "$destination" >&2; exit 1; }
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
		b.WriteString(`  clone_tmp=$(mktemp -d "$(dirname -- "$destination")/.plonkr-clone.XXXXXX")
  GIT_SSH_COMMAND="$ssh_command" GIT_SSH_VARIANT=ssh git -c core.sshCommand="$ssh_command" clone --no-checkout -- "$repo_url" "$clone_tmp"
  git -C "$clone_tmp" checkout --detach "$repo_ref"
`)
		if r.Key != "" {
			b.WriteString("  git -C \"$clone_tmp\" config core.sshCommand \"$ssh_command\"\n")
		}
		b.WriteString(`  [ ! -e "$destination" ] && [ ! -L "$destination" ] || { printf 'Checkout destination %s appeared during clone\n' "$destination" >&2; exit 1; }
  mv -T -- "$clone_tmp" "$destination"
  clone_tmp=''
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
