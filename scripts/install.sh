#!/bin/sh
# Install the telegraph CLI from a GitHub release. No Go toolchain needed.
#
#   curl -fsSL https://raw.githubusercontent.com/ao3575911/telegraph/main/scripts/install.sh | sh
#
# Environment:
#   TELEGRAPH_VERSION  release tag to install, e.g. v0.2.1 (default: latest)
#   BINDIR             install directory (default: ~/.local/bin)
#   TELEGRAPH_DOWNLOAD_BASE  release download base URL (mirrors, tests)
set -eu

REPO=ao3575911/telegraph
BINDIR=${BINDIR:-$HOME/.local/bin}
BASE=${TELEGRAPH_DOWNLOAD_BASE:-https://github.com/$REPO/releases/download}

say()  { printf '%s\n' "$*"; }
die()  { printf 'telegraph install: %s\n' "$*" >&2; exit 1; }

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
  latest() { curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
  latest() { wget -q -S --spider "https://github.com/$REPO/releases/latest" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -1; }
else
  die "needs curl or wget"
fi

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW* | MSYS* | CYGWIN*) die "on Windows, download the .zip from https://github.com/$REPO/releases/latest" ;;
  *) die "unsupported OS: $(uname -s)" ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported CPU: $(uname -m)" ;;
esac

tag=${TELEGRAPH_VERSION:-}
if [ -z "$tag" ]; then
  tag=$(latest | tr -d '\r' | sed -n 's#.*/tag/##p')
  [ -n "$tag" ] || die "could not find the latest release; set TELEGRAPH_VERSION=vX.Y.Z"
fi
case $tag in v*) ;; *) tag=v$tag ;; esac
ver=${tag#v}
name="telegraph_${ver}_${os}_${arch}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
say "Downloading telegraph $tag for $os/$arch…"
fetch "$BASE/$tag/$name.tar.gz" "$tmp/$name.tar.gz" || die "download failed: $BASE/$tag/$name.tar.gz"
fetch "$BASE/$tag/SHA256SUMS" "$tmp/SHA256SUMS" || die "download failed: $BASE/$tag/SHA256SUMS"

want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "$name.tar.gz is not listed in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$name.tar.gz" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{print $1}')
else
  die "needs sha256sum or shasum to verify the download"
fi
[ "$got" = "$want" ] || die "checksum mismatch for $name.tar.gz (got $got, want $want)"
say "Checksum verified."

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$BINDIR"
cp "$tmp/$name/telegraph" "$BINDIR/telegraph.tmp"
chmod 0755 "$BINDIR/telegraph.tmp"
mv -f "$BINDIR/telegraph.tmp" "$BINDIR/telegraph"
say "Installed $("$BINDIR/telegraph" version) to $BINDIR/telegraph"

case ":$PATH:" in
  *":$BINDIR:"*) say "Next: telegraph init" ;;
  *)
    rc="$HOME/.profile"
    case ${SHELL:-} in */zsh) rc="$HOME/.zshrc" ;; */bash) rc="$HOME/.bashrc" ;; esac
    say ""
    say "$BINDIR is not on your PATH. Add it with:"
    say "  echo 'export PATH=\"$BINDIR:\$PATH\"' >> $rc && . $rc"
    say "Then: telegraph init"
    ;;
esac
