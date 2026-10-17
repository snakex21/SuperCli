package releasebundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func unixInstallerShell(t *testing.T) string {
	t.Helper()
	if shell, err := exec.LookPath("bash"); err == nil {
		return shell
	}
	if runtime.GOOS == "windows" {
		if git, err := exec.LookPath("git.exe"); err == nil {
			shell := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe")
			if _, err := os.Stat(shell); err == nil {
				return shell
			}
		}
	}
	t.Skip("a POSIX shell is required for offline installer execution")
	return ""
}

func unixInstallerFixture(t *testing.T, platform string, files map[string]string, symlink bool) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"bin", "home", "tmp"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if files == nil {
		files = map[string]string{"supercli": "CLI fixture, never execute", "supercli-web": "GUI fixture, never execute", "LICENSE": "public", "supercli-data/skills/builtin-skills.zip": "skill pack fixture"}
	}
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	for name, content := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		if symlink && name == "supercli" {
			header.SetMode(os.ModeSymlink | 0o777)
		}
		out, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("bundle.zip", data.String())
	write("SHA256SUMS", fmt.Sprintf("%x  supercli_1.2.3_%s.zip\n", sha256.Sum256(data.Bytes()), platform))
	write("home/.bashrc", "# original profile\n")
	write("bin/uname", "#!/bin/sh\ncase \"$1\" in -s) printf '%s\\n' \"$FIXTURE_OS\";; -m) printf '%s\\n' \"$FIXTURE_ARCH\";; *) exit 2;; esac\n")
	write("bin/curl", `#!/bin/sh
url= output=
while [ "$#" -gt 0 ]; do
 case "$1" in -o) output=$2; shift 2;; https://*) url=$1; shift;; *) shift;; esac
done
printf '%s\n' "$url" >> "$FIXTURE_ROOT/requests"
[ "${FIXTURE_DOWNLOAD_FAIL:-0}" != 1 ] || exit 22
case "$url" in
 https://github.com/snakex21/SuperCli/releases/latest) printf 'https://github.com/snakex21/SuperCli/releases/tag/v1.2.3';;
 "https://github.com/snakex21/SuperCli/releases/download/v1.2.3/supercli_1.2.3_${FIXTURE_PLATFORM}.zip")
  if [ "${FIXTURE_RACE:-0}" = 1 ]; then mkdir -p "$HOME/.local/share/supercli"; printf keep > "$HOME/.local/share/supercli/private-data"; fi
  if [ "${FIXTURE_LINK_RACE:-0}" = 1 ]; then mkdir -p "$HOME/.local/bin/supercli"; printf keep > "$HOME/.local/bin/supercli/private-data"; fi
  cp "$FIXTURE_ROOT/bundle.zip" "$output";;
 https://github.com/snakex21/SuperCli/releases/download/v1.2.3/SHA256SUMS) cp "$FIXTURE_ROOT/SHA256SUMS" "$output";;
 *) printf 'unexpected download: %s\n' "$url" >&2; exit 23;;
esac
`)
	write("bin/mv", `#!/bin/sh
if [ "${FIXTURE_PUBLISH_RACE:-0}" = 1 ]; then
 mkdir -p "$FIXTURE_ROOT/existing"
 printf keep > "$FIXTURE_ROOT/existing/private-data"
 ln -s "$FIXTURE_ROOT/existing" "$HOME/.local/share/supercli"
fi
exec "$FIXTURE_REAL_MV" "$@"
`)
	write("bin/ls", `#!/bin/sh
if [ "${FIXTURE_UNTRUSTED:-0}" = 1 ]; then
 "$FIXTURE_REAL_LS" "$@" | sed '1s/^d........./drwxrwxrwx/'
else
 exec "$FIXTURE_REAL_LS" "$@"
fi
`)
	return root
}

func runUnixInstaller(t *testing.T, root string, extra ...string) (string, error) {
	t.Helper()
	shell := unixInstallerShell(t)
	script, err := filepath.Abs("../../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(shell, "-c", `
set -eu
FIXTURE_ROOT=$(cd "$FIXTURE_ROOT" && pwd); export FIXTURE_ROOT
HOME="$FIXTURE_ROOT/home"; export HOME
TMPDIR="$FIXTURE_ROOT/tmp"; export TMPDIR
FIXTURE_REAL_MV=$(command -v mv); export FIXTURE_REAL_MV
FIXTURE_REAL_LS=$(command -v ls); export FIXTURE_REAL_LS
PATH="$FIXTURE_ROOT/bin:$PATH"; export PATH
exec sh "$FIXTURE_SCRIPT"
`)
	command.Env = append(os.Environ(), "FIXTURE_ROOT="+filepath.ToSlash(root), "FIXTURE_SCRIPT="+filepath.ToSlash(script), "FIXTURE_OS=Linux", "FIXTURE_ARCH=x86_64", "FIXTURE_PLATFORM=linux_amd64", "FIXTURE_RACE=0", "FIXTURE_DOWNLOAD_FAIL=0", "PREFIX=", "TERMUX_VERSION=", "SUPERCLI_DIR=", "SUPERCLI_VERSION=1.2.3", "SUPERCLI_NO_PATH=1", "SHELL=/bin/bash")
	command.Env = append(command.Env, extra...)
	out, err := command.CombinedOutput()
	return string(out), err
}

func TestUnixInstallerDetectsPlatformsAndPreservesPortablePayload(t *testing.T) {
	for _, tc := range []struct {
		name, system, arch, platform string
		termux                       bool
	}{
		{"Linux amd64", "Linux", "x86_64", "linux_amd64", false},
		{"Linux arm64", "Linux", "aarch64", "linux_arm64", false},
		{"macOS amd64", "Darwin", "x86_64", "darwin_amd64", false},
		{"macOS arm64", "Darwin", "arm64", "darwin_arm64", false},
		{"Termux arm64", "Linux", "aarch64", "android_arm64", true},
		{"Termux amd64", "Linux", "x86_64", "linux_amd64", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := unixInstallerFixture(t, tc.platform, nil, false)
			env := []string{"FIXTURE_OS=" + tc.system, "FIXTURE_ARCH=" + tc.arch, "FIXTURE_PLATFORM=" + tc.platform}
			rel := ".local/share/supercli"
			if tc.termux {
				env = append(env, "TERMUX_VERSION=fixture")
				rel = "supercli"
			}
			out, err := runUnixInstaller(t, root, env...)
			if err != nil {
				t.Fatalf("installer failed: %v\n%s", err, out)
			}
			for _, file := range []string{"supercli", "supercli-web", "LICENSE", "supercli-data/skills/builtin-skills.zip"} {
				if _, err := os.Stat(filepath.Join(root, "home", rel, file)); err != nil {
					t.Fatalf("portable payload missing %s: %v", file, err)
				}
			}
			profile, _ := os.ReadFile(filepath.Join(root, "home/.bashrc"))
			if string(profile) != "# original profile\n" {
				t.Fatal("SUPERCLI_NO_PATH changed the profile")
			}
			entries, _ := os.ReadDir(filepath.Join(root, "tmp"))
			if len(entries) != 0 {
				t.Fatal("staging files were not cleaned")
			}
		})
	}
}

func TestUnixInstallerRefusesUnsafeOrIncompleteDownloads(t *testing.T) {
	for _, name := range []string{"checksum", "duplicate checksum", "traversal", "symlink", "missing binary", "download", "destination race"} {
		t.Run(name, func(t *testing.T) {
			var files map[string]string
			if name == "traversal" {
				files = map[string]string{"../escaped": "bad"}
			} else if name == "missing binary" {
				files = map[string]string{"supercli": "fixture"}
			}
			root := unixInstallerFixture(t, "linux_amd64", files, name == "symlink")
			var env []string
			if name == "checksum" {
				os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(strings.Repeat("0", 64)+"  supercli_1.2.3_linux_amd64.zip\n"), 0o644)
			} else if name == "duplicate checksum" {
				sums, _ := os.ReadFile(filepath.Join(root, "SHA256SUMS"))
				os.WriteFile(filepath.Join(root, "SHA256SUMS"), append(sums, sums...), 0o644)
			} else if name == "download" {
				env = append(env, "FIXTURE_DOWNLOAD_FAIL=1")
			} else if name == "destination race" {
				env = append(env, "FIXTURE_RACE=1")
			}
			out, err := runUnixInstaller(t, root, env...)
			if err == nil {
				t.Fatalf("unsafe install succeeded: %s", out)
			}
			destination := filepath.Join(root, "home/.local/share/supercli")
			if name == "destination race" {
				data, _ := os.ReadFile(filepath.Join(destination, "private-data"))
				if string(data) != "keep" {
					t.Fatal("racing target data changed")
				}
			} else if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("failure created the installation: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "tmp/escaped")); !os.IsNotExist(err) {
				t.Fatal("archive escaped staging")
			}
		})
	}
}

func TestUnixInstallerRefusesExistingTargetsAndInvalidPlatforms(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"existing", nil},
		{"system", []string{"FIXTURE_OS=FreeBSD"}},
		{"architecture", []string{"FIXTURE_ARCH=armv7l"}},
		{"version", []string{"SUPERCLI_VERSION=1.2.3\nmalformed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := unixInstallerFixture(t, "linux_amd64", nil, false)
			if tc.name == "existing" {
				path := filepath.Join(root, "home/.local/share/supercli")
				os.MkdirAll(path, 0o755)
				os.WriteFile(filepath.Join(path, "private-data"), []byte("keep"), 0o644)
			}
			out, err := runUnixInstaller(t, root, tc.env...)
			if err == nil {
				t.Fatalf("invalid install succeeded: %s", out)
			}
			if requests, _ := os.ReadFile(filepath.Join(root, "requests")); len(requests) != 0 {
				t.Fatal("preflight failure made a download request")
			}
			if tc.name == "existing" {
				data, _ := os.ReadFile(filepath.Join(root, "home/.local/share/supercli/private-data"))
				if string(data) != "keep" {
					t.Fatal("existing data changed")
				}
			}
		})
	}
}

func TestUnixInstallerPublicationAndCommandRaces(t *testing.T) {
	for _, kind := range []string{"publication", "command", "shared parent"} {
		t.Run(kind, func(t *testing.T) {
			root := unixInstallerFixture(t, "linux_amd64", nil, false)
			env := []string{"SUPERCLI_NO_PATH=0"}
			switch kind {
			case "publication":
				env = append(env, "FIXTURE_PUBLISH_RACE=1")
			case "command":
				env = append(env, "FIXTURE_LINK_RACE=1")
			case "shared parent":
				env = append(env, "FIXTURE_UNTRUSTED=1")
			}
			out, err := runUnixInstaller(t, root, env...)
			if err == nil {
				t.Fatalf("unsafe publication succeeded: %s", out)
			}
			var existing string
			if kind == "publication" {
				existing = filepath.Join(root, "existing")
			} else if kind == "command" {
				existing = filepath.Join(root, "home/.local/bin/supercli")
			} else {
				if !strings.Contains(out, "writable by other users") {
					t.Fatalf("shared parent was not rejected: %s", out)
				}
				return
			}
			data, _ := os.ReadFile(filepath.Join(existing, "private-data"))
			if string(data) != "keep" {
				t.Fatal("racing existing directory was modified")
			}
			for _, file := range []string{"supercli", "supercli-web"} {
				if _, err := os.Stat(filepath.Join(existing, file)); !os.IsNotExist(err) {
					t.Fatalf("racing directory received %s: %v", file, err)
				}
			}
		})
	}
}

func TestUnixInstallerBashLoginProfileDoesNotShadowExistingSetup(t *testing.T) {
	for _, login := range []string{".bash_profile", ".bash_login", ".profile"} {
		t.Run(login, func(t *testing.T) {
			root := unixInstallerFixture(t, "darwin_arm64", nil, false)
			path := filepath.Join(root, "home", login)
			if err := os.WriteFile(path, []byte("# login setup\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := runUnixInstaller(t, root, "FIXTURE_OS=Darwin", "FIXTURE_ARCH=arm64", "FIXTURE_PLATFORM=darwin_arm64", "SUPERCLI_NO_PATH=0")
			if err != nil {
				t.Fatalf("login setup failed: %v\n%s", err, out)
			}
			data, _ := os.ReadFile(path)
			if !strings.HasPrefix(string(data), "# login setup\n") || !strings.Contains(string(data), `export PATH="$HOME/.local/bin:$PATH"`) {
				t.Fatalf("existing login setup was not preserved and configured: %q", data)
			}
			for _, higher := range []string{".bash_profile", ".bash_login"} {
				if higher == login {
					break
				}
				if _, err := os.Stat(filepath.Join(root, "home", higher)); !os.IsNotExist(err) {
					t.Fatalf("created %s, shadowing %s", higher, login)
				}
			}
		})
	}
}

func TestUnixInstallerLatestAndUserPathSetup(t *testing.T) {
	root := unixInstallerFixture(t, "linux_amd64", nil, false)
	out, err := runUnixInstaller(t, root, "SUPERCLI_VERSION=", "SUPERCLI_NO_PATH=0")
	if err != nil {
		t.Fatalf("latest install failed: %v\n%s", err, out)
	}
	profile, _ := os.ReadFile(filepath.Join(root, "home/.bashrc"))
	if !strings.HasPrefix(string(profile), "# original profile\n") || !strings.Contains(string(profile), `export PATH="$HOME/.local/bin:$PATH"`) {
		t.Fatalf("profile setup did not preserve existing content: %q", profile)
	}
	for _, file := range []string{"supercli", "supercli-web"} {
		if _, err := os.Stat(filepath.Join(root, "home/.local/bin", file)); err != nil {
			t.Fatalf("missing user command: %v", err)
		}
	}
}
