#!/bin/sh
# SuperCli installer for Termux (Android). One line:
#   curl -fsSL https://raw.githubusercontent.com/snakex21/SuperCli/main/scripts/install-termux.sh | sh
# Optional: SUPERCLI_VERSION=1.0.14 (default: latest release), SUPERCLI_DIR (default: ~/supercli).
# Running it again updates the binaries; supercli-data (settings, sessions) is kept.
set -eu

repo="snakex21/SuperCli"
dir="${SUPERCLI_DIR:-$HOME/supercli}"
prefix="${PREFIX:-/data/data/com.termux/files/usr}"

fail() {
	printf 'supercli install: %s\n' "$*" >&2
	exit 1
}

# The android build survives Android < 12, where pidfd syscalls are blocked.
# Go supports android/amd64 only with cgo, so x86_64 emulators use linux.
case "$(uname -m)" in
aarch64 | arm64) os=android arch=arm64 ;;
x86_64 | amd64) os=linux arch=amd64 ;;
*) fail "unsupported CPU $(uname -m); releases exist for arm64 and x86_64" ;;
esac

for tool in curl unzip sha256sum; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		command -v pkg >/dev/null 2>&1 || fail "missing $tool"
		pkg install -y curl unzip coreutils ca-certificates
		break
	fi
done

version="${SUPERCLI_VERSION:-}"
if [ -z "$version" ]; then
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
		fail "cannot reach GitHub"
	version=${latest##*/}
fi
version=${version#v}
case "$version" in
'' | *[!0-9.]*) fail "could not determine the release version ($version)" ;;
esac

name="supercli_${version}_${os}_${arch}.zip"
base="https://github.com/$repo/releases/download/v$version"
tmp=$(mktemp -d "${TMPDIR:-$prefix/tmp}/supercli.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'Downloading SuperCli %s (%s)...\n' "$version" "$arch"
curl -fL --progress-bar -o "$tmp/$name" "$base/$name" || fail "download failed: $base/$name"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "checksum list download failed"
(cd "$tmp" && grep "  $name\$" SHA256SUMS | sha256sum -c -) >/dev/null 2>&1 || fail "checksum mismatch for $name"

mkdir -p "$dir"
unzip -oq "$tmp/$name" -d "$dir"
chmod 755 "$dir/supercli" "$dir/supercli-web"

# Small launchers keep data beside the real binaries (portable mode).
for cmd in supercli supercli-web; do
	cat >"$prefix/bin/$cmd" <<EOF
#!$prefix/bin/sh
[ -n "\${TZ:-}" ] || TZ=\$(getprop persist.sys.timezone 2>/dev/null) && export TZ
exec "$dir/$cmd" "\$@"
EOF
	chmod 755 "$prefix/bin/$cmd"
done

printf 'SuperCli %s installed in %s\n' "$version" "$dir"
printf 'Start: supercli\n'
printf 'Web GUI: supercli-web --no-window, then open the shown address in the browser\n'
