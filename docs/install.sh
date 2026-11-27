#!/bin/sh
# Plonkr installer for Linux (amd64, arm64).
#
#   curl -fsSL https://plonkr.dev/install.sh | sh
#
# Installs the plonk command from a GitHub release of vanvanni/plonkr. The
# release archive is checked against the release's SHA256SUMS before anything
# is installed. Nothing is changed outside the install directory.
#
# Options (arguments, or the environment variable in brackets):
#   --version VERSION  Install this release, such as 2026.11.0-beta.3
#                      [PLONK_VERSION]. Default: the newest published release,
#                      including prereleases.
#   --dir DIR          Install into DIR [PLONK_INSTALL_DIR]. Default:
#                      /usr/local/bin for root, otherwise ~/.local/bin.
#   --uninstall        Remove plonk from the install directory.
#   --help             Show this help.
#
# Pass arguments through a pipe with:  curl -fsSL URL | sh -s -- --version X
#
# PLONK_API_URL and PLONK_DOWNLOAD_URL replace the GitHub endpoints, for a
# mirror or for testing.

set -eu

REPO="vanvanni/plonkr"
API_URL="${PLONK_API_URL:-https://api.github.com/repos/$REPO/releases}"
DOWNLOAD_URL="${PLONK_DOWNLOAD_URL:-https://github.com/$REPO/releases/download}"

say() { printf '%s\n' "$*"; }
warn() { printf 'install: %s\n' "$*" >&2; }
die() { warn "$*"; exit 1; }

usage() {
  cat <<'EOF'
Plonkr installer for Linux (amd64, arm64).

Usage: install.sh [--version VERSION] [--dir DIR] [--uninstall]

  --version VERSION  Install this release, such as 2026.11.0-beta.3
                     (PLONK_VERSION). Default: the newest published release,
                     including prereleases.
  --dir DIR          Install into DIR (PLONK_INSTALL_DIR). Default:
                     /usr/local/bin for root, otherwise ~/.local/bin.
  --uninstall        Remove plonk from the install directory.
  --help             Show this help.

Through a pipe:  curl -fsSL https://plonkr.dev/install.sh | sh -s -- --version X
EOF
}

# fetch URL [OUTPUT]: write to OUTPUT, or to stdout when it is omitted.
fetch() {
  if command -v curl >/dev/null 2>&1; then
    if [ $# -ge 2 ]; then curl -fsSL --retry 3 -o "$2" "$1"; else curl -fsSL --retry 3 "$1"; fi
  elif command -v wget >/dev/null 2>&1; then
    if [ $# -ge 2 ]; then wget -q -O "$2" "$1"; else wget -q -O - "$1"; fi
  else
    die "curl or wget is required"
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d ' ' -f 1
  else
    die "sha256sum or shasum is required to verify the download"
  fi
}

default_dir() {
  if [ "$(id -u)" = 0 ]; then say /usr/local/bin; else say "${HOME:?HOME is not set}/.local/bin"; fi
}

valid_version() {
  case "$1" in
    *[!0-9A-Za-z.-]* | '' | -* | .*) return 1 ;;
  esac
  printf '%s' "$1" | grep -Eq '^[1-9][0-9]{3}\.(0[1-9]|1[0-2])\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$'
}

main() {
  version="${PLONK_VERSION:-}"
  dir="${PLONK_INSTALL_DIR:-}"
  uninstall=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift 2 ;;
      --version=*) version=${1#--version=}; shift ;;
      --dir) [ $# -ge 2 ] || die "--dir needs a value"; dir=$2; shift 2 ;;
      --dir=*) dir=${1#--dir=}; shift ;;
      --uninstall) uninstall=1; shift ;;
      -h | --help) usage; return 0 ;;
      *) die "unknown option: $1 (try --help)" ;;
    esac
  done
  [ -n "$dir" ] || dir=$(default_dir)

  if [ -n "$uninstall" ]; then
    if [ -e "$dir/plonk" ] || [ -L "$dir/plonk" ]; then
      rm -f -- "$dir/plonk"
      say "Removed $dir/plonk"
    else
      say "Nothing to remove: $dir/plonk does not exist"
    fi
    return 0
  fi

  [ "$(uname -s)" = Linux ] || die "this installer supports Linux only; on Windows use install.ps1"
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported CPU architecture: $(uname -m) (releases cover amd64 and arm64)" ;;
  esac

  if [ -z "$version" ]; then
    say "Looking up the newest release..."
    version=$(fetch "$API_URL" 2>/dev/null | grep -m 1 '"tag_name"' | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/') || version=""
    [ -n "$version" ] || die "no published release was found at $API_URL; pass --version to choose one"
  fi
  version=${version#v}
  valid_version "$version" || die "invalid version: $version"
  tag="v$version"

  archive="plonk_${tag}_linux_${arch}.tar.gz"
  tmp=$(mktemp -d "${TMPDIR:-/tmp}/plonk-install.XXXXXX") || die "could not create a temporary directory"
  trap 'rm -rf -- "$tmp"' EXIT HUP INT TERM

  say "Downloading plonk $version (linux/$arch)..."
  fetch "$DOWNLOAD_URL/$tag/$archive" "$tmp/$archive" || die "could not download $archive for $tag"
  fetch "$DOWNLOAD_URL/$tag/SHA256SUMS" "$tmp/SHA256SUMS" || die "could not download SHA256SUMS for $tag"

  expected=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/SHA256SUMS")
  [ -n "$expected" ] || die "$archive is not listed in SHA256SUMS"
  actual=$(sha256_of "$tmp/$archive")
  [ "$expected" = "$actual" ] || die "checksum mismatch for $archive (expected $expected, got $actual); nothing was installed"
  say "Checksum verified."

  mkdir "$tmp/unpack"
  tar -xzf "$tmp/$archive" -C "$tmp/unpack" ./plonk 2>/dev/null || tar -xzf "$tmp/$archive" -C "$tmp/unpack" plonk \
    || die "the archive does not contain plonk"
  [ -f "$tmp/unpack/plonk" ] || die "the archive does not contain plonk"
  chmod 755 "$tmp/unpack/plonk"

  mkdir -p -- "$dir" 2>/dev/null || die "cannot create $dir; choose another with --dir, or run as root"
  [ -w "$dir" ] || die "cannot write to $dir; choose another with --dir, or run as root"
  # Copy beside the destination first so the final rename is atomic and a running plonk is not overwritten in place.
  cp "$tmp/unpack/plonk" "$dir/.plonk.new.$$" && mv -f -- "$dir/.plonk.new.$$" "$dir/plonk" \
    || { rm -f -- "$dir/.plonk.new.$$"; die "could not install into $dir"; }

  say "Installed plonk $version to $dir/plonk"
  case ":${PATH:-}:" in
    *":$dir:"*) ;;
    *) say "Note: $dir is not on your PATH. Add it, for example:  export PATH=\"$dir:\$PATH\"" ;;
  esac
  command -v ssh >/dev/null 2>&1 || warn "OpenSSH client (ssh) was not found; plonk needs it to reach machines."
  say "Check it with:  plonk version"
}

main "$@"
