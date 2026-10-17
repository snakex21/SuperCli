#!/bin/sh
# Review this script before running it. Fresh installs only; use --update later.
# Options: SUPERCLI_VERSION, SUPERCLI_DIR, SUPERCLI_NO_PATH=1.
set -eu

fail() { printf 'supercli install: %s\n' "$*" >&2; exit 1; }
repo=snakex21/SuperCli
termux=0
case "$(uname -s)" in
Linux)
	os=linux
	case "${PREFIX:-}" in */com.termux/files/usr) termux=1 ;; esac
	[ -z "${TERMUX_VERSION:-}" ] || termux=1
	;;
Darwin) os=darwin ;;
*) fail 'unsupported system; on Windows use install.ps1 in PowerShell' ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail 'this CPU architecture has no SuperCli release (amd64/arm64 required)' ;;
esac
if [ "$termux" = 1 ]; then
	# Android ARM64 avoids blocked pidfd syscalls on older Android versions.
	# x86_64 Termux uses the Linux build, matching the published release matrix.
	[ "$arch" != arm64 ] || os=android
	default_dir="$HOME/supercli"
	bin="${PREFIX:-/data/data/com.termux/files/usr}/bin"
else
	default_dir="$HOME/.local/share/supercli"
	bin="$HOME/.local/bin"
fi
dir="${SUPERCLI_DIR:-$default_dir}"
case "$dir" in /*) ;; *) fail 'SUPERCLI_DIR must be an absolute path' ;; esac
[ ! -e "$dir" ] && [ ! -L "$dir" ] || fail "destination already exists: $dir; use supercli --update or choose a new SUPERCLI_DIR"
if [ "${SUPERCLI_NO_PATH:-0}" != 1 ]; then
	for cmd in supercli supercli-web; do
		[ ! -e "$bin/$cmd" ] && [ ! -L "$bin/$cmd" ] || fail "command already exists: $bin/$cmd; use --update or SUPERCLI_NO_PATH=1"
	done
fi
for tool in curl unzip awk; do
	command -v "$tool" >/dev/null 2>&1 || fail "missing $tool; install curl and unzip first (Termux: pkg install curl unzip coreutils)"
done
if command -v sha256sum >/dev/null 2>&1; then
	hash=sha256sum
elif command -v shasum >/dev/null 2>&1; then
	hash=shasum
else
	fail 'missing SHA256 utility (sha256sum or shasum)'
fi

version="${SUPERCLI_VERSION:-}"
if [ -z "$version" ]; then
	latest=$(curl --proto '=https' --tlsv1.2 -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") || fail 'cannot resolve the latest GitHub release'
	version=${latest##*/}
fi
version=${version#v}
printf '%s\n' "$version" | awk 'NR == 1 && /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/ {ok=1} END {exit !(ok && NR == 1)}' || fail 'expected a stable x.y.z release version'
name="supercli_${version}_${os}_${arch}.zip"
base="https://github.com/$repo/releases/download/v$version"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/supercli-install.XXXXXX") || fail 'cannot create staging directory'
publish_tmp=
trap 'rm -rf "$tmp"; if [ -n "$publish_tmp" ]; then rm -rf "$publish_tmp"; fi' 0
trap 'exit 1' 1 2 15
printf 'Downloading SuperCli %s (%s/%s)...\n' "$version" "$os" "$arch"
curl --proto '=https' --tlsv1.2 -fsSL "$base/$name" -o "$tmp/bundle.zip" || fail "cannot download $name; check that this platform/version is published"
curl --proto '=https' --tlsv1.2 -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS" || fail 'cannot download SHA256SUMS'
expected=$(awk -v name="$name" '$2 == name {if (NF != 2) exit 1; count++; digest=$1} END {if (count != 1) exit 1; print digest}' "$tmp/SHA256SUMS") || fail 'missing or duplicate checksum for the selected archive'
case "$expected" in '' | *[!0-9a-fA-F]*) fail 'invalid SHA256 checksum' ;; esac
[ "${#expected}" -eq 64 ] || fail 'invalid SHA256 checksum length'
if [ "$hash" = sha256sum ]; then
	actual=$(sha256sum "$tmp/bundle.zip") || fail 'cannot hash archive'
else
	actual=$(shasum -a 256 "$tmp/bundle.zip") || fail 'cannot hash archive'
fi
actual=${actual%% *}
expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
[ "$actual" = "$expected" ] || fail 'SHA256 mismatch; nothing has been installed'

# Validate before extraction; official bundles contain regular files only.
unzip -Z1 "$tmp/bundle.zip" > "$tmp/entries" || fail 'invalid ZIP archive'
while IFS= read -r entry; do
	case "$entry" in
	'' | /* | [A-Za-z]:* | *\\* | .. | ../* | */../* | */.. | ./* | */./*) fail "unsafe archive path: $entry" ;;
	esac
done < "$tmp/entries"
awk 'seen[$0]++ {exit 1}' "$tmp/entries" || fail 'duplicate archive paths'
unzip -Z -l "$tmp/bundle.zip" > "$tmp/listing" || fail 'cannot inspect ZIP permissions'
awk '$1 ~ /^l/ {exit 1}' "$tmp/listing" || fail 'archive contains a symbolic link'
mkdir "$tmp/payload"
unzip -q "$tmp/bundle.zip" -d "$tmp/payload" || fail 'cannot extract archive'
for cmd in supercli supercli-web; do
	[ -f "$tmp/payload/$cmd" ] && [ -s "$tmp/payload/$cmd" ] && [ ! -L "$tmp/payload/$cmd" ] || fail "archive is missing a regular $cmd binary"
	chmod 755 "$tmp/payload/$cmd"
done

# Stage on the destination filesystem, never copy through the live target path.
parent=$(dirname "$dir")
leaf=$(basename "$dir")
case "$leaf" in '' | . | .. | /) fail 'invalid destination directory' ;; esac
mkdir -p "$parent"
parent=$(cd "$parent" && pwd -P)
home=$(cd "$HOME" && pwd -P)
uid=$(id -u)
check=$parent
while :; do
	permissions=$(LC_ALL=C ls -ldn "$check" | awk '{print $1 ":" $3}')
	mode=${permissions%:*}
	owner=${permissions##*:}
	[ "$owner" = "$uid" ] || [ "$owner" = 0 ] || fail "untrusted destination parent owner: $check"
	case "$mode" in
	?????w* | ????????w*)
		case "$mode" in ?????????[tT]*) ;; *) fail "destination parent is writable by other users: $check" ;; esac
		;;
	esac
	[ "$check" != "$home" ] && [ "$check" != / ] || break
	check=$(dirname "$check")
done
publish_tmp=$(mktemp -d "$parent/.supercli-install.XXXXXX") || fail 'cannot create protected publication directory'
mkdir "$publish_tmp/$leaf"
cp -R "$tmp/payload/." "$publish_tmp/$leaf/" || fail 'cannot stage the portable files'
# Supplying the parent computes the final leaf once: an existing target is not
# treated as another container. No-clobber skips leave the source in staging.
mv -n "$publish_tmp/$leaf" "$parent/" || fail 'cannot publish the installation'
[ ! -e "$publish_tmp/$leaf" ] && [ ! -L "$publish_tmp/$leaf" ] || fail "destination appeared during download: $dir; nothing was overwritten"
dir="$parent/$leaf"
if [ "${SUPERCLI_NO_PATH:-0}" != 1 ]; then
	mkdir -p "$bin"
	for cmd in supercli supercli-web; do
		ln -s "$dir/$cmd" "$bin/" || fail "could not create $bin/$cmd; portable files are installed in $dir"
	done
	case ":$PATH:" in
	*":$bin:"*) ;;
	*)
		line='case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) export PATH="$HOME/.local/bin:$PATH" ;; esac'
		[ "$termux" != 1 ] || line='case ":$PATH:" in *":$PREFIX/bin:"*) ;; *) export PATH="$PREFIX/bin:$PATH" ;; esac'
		case "${SHELL:-}" in
		*/fish)
			line='fish_add_path "$HOME/.local/bin"'
			[ "$termux" != 1 ] || line='fish_add_path "$PREFIX/bin"'
			;;
		esac
		append_path_profile() {
			profile=$1
			if ! grep -Fqx "$line" "$profile" 2>/dev/null; then
				if mkdir -p "$(dirname "$profile")" && printf '\n# SuperCli user commands\n%s\n' "$line" >> "$profile"; then
					printf 'PATH configured in %s; open a new terminal to use the commands.\n' "$profile"
				else
					printf 'Could not update %s; add %s to PATH manually.\n' "$profile" "$bin" >&2
				fi
			fi
		}
		case "${SHELL:-}" in
		*/zsh) append_path_profile "${ZDOTDIR:-$HOME}/.zshrc" ;;
		*/fish) append_path_profile "${XDG_CONFIG_HOME:-$HOME/.config}/fish/config.fish" ;;
		*/bash)
			append_path_profile "$HOME/.bashrc"
			login_profile="$HOME/.profile"
			for candidate in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do
				if [ -f "$candidate" ] && [ -r "$candidate" ]; then login_profile=$candidate; break; fi
			done
			append_path_profile "$login_profile"
			;;
		*) append_path_profile "$HOME/.profile" ;;
		esac
		;;
	esac
fi
printf 'Installed SuperCli %s in %s\n' "$version" "$dir"
printf 'Start: %s/supercli\nWeb GUI: %s/supercli-web --no-window\n' "$dir" "$dir"
printf 'No application was started. First launch opens the existing setup flow.\n'
