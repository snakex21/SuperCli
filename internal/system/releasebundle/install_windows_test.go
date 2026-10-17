package releasebundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const windowsInstallerAsset = "supercli_1.2.3_windows_amd64.zip"

// This file deliberately runs Windows PowerShell, not pwsh. Every invocation is
// offline and has isolated HOME/work/TEMP. Portable cases use SUPERCLI_NO_PATH=1;
// PATH cases enable updates only inside the child, injecting an in-memory registry
// key. No test can open a real writable registry key. Downloads stay mocked.
type windowsInstallerEntry struct {
	name, data string
	mode       os.FileMode
	attributes uint32
	method     uint16
}

func windowsInstallerPayload() []windowsInstallerEntry {
	return []windowsInstallerEntry{
		{name: "supercli.exe", data: "not an executable: CLI fixture", mode: 0755},
		{name: "supercli-web.exe", data: "not an executable: GUI fixture", mode: 0755},
		{name: "README.md", data: "public readme"},
		{name: "LICENSE", data: "public license"},
		{name: "THIRD_PARTY_NOTICES.md", data: "public notices"},
		{name: "docs/", mode: os.ModeDir | 0755},
		{name: "docs/readme/en.md", data: "English documentation"},
		{name: "docs/configuration.md", data: "configuration documentation"},
		{name: "docs/screenshots/gui.jpg", data: "public screenshot"},
		{name: "supercli-data/skills/builtin-skills.zip", data: "public built-in skills"},
	}
}

type windowsInstallerRedirect struct {
	Location   string `json:"location"`
	Status     int    `json:"status"`
	NoResponse bool   `json:"noResponse"`
	OtherError bool   `json:"otherError"`
}

type windowsInstallerPathCase struct {
	Raw         *string `json:"raw"`
	Kind        string  `json:"kind"`
	Process     string  `json:"process"`
	ReadError   bool    `json:"readError"`
	WriteError  bool    `json:"writeError"`
	WantRaw     *string `json:"-"`
	WantKind    string  `json:"-"`
	WantProcess string  `json:"-"`
	WantWrites  int     `json:"-"`
	WantWarning string  `json:"-"`
}

type windowsInstallerOptions struct {
	pathCase                          *windowsInstallerPathCase
	redirects                         map[string]windowsInstallerRedirect
	routes                            map[string]string
	latest, defaultTarget, flatTarget bool
	version                           string
	nativeArch                        *int
	processArch, wow64Arch            string
	existing, race, failNetwork       string
	wantError                         string
	wantCalls                         int
	entries                           func(string) []windowsInstallerEntry
	archive                           []byte
	checksums                         func(string) string
	release                           func(map[string]any)
}

type windowsInstallerResult struct {
	RegistryRaw                                                  *string
	RegistryKind                                                 string
	RegistryOpens, RegistryReads, RegistryWrites, RegistryCloses int
	RegistryKindReads                                            int
	Success                                                      bool
	Error, Version                                               string
	Calls                                                        []string
	ProcessBefore, ProcessAfter                                  string
	UserBefore, UserAfter                                        string
	MachineBefore, MachineAfter                                  string
	TLSBefore, TLSAfter                                          string
	LocationBefore, LocationAfter                                string
}

// Nonstandard compression creates a real ZIP whose directory can be inspected,
// but whose entry stream cannot be extracted by .NET. No network or downloaded
// executable is needed to exercise extraction failure after partial staging.
type windowsInstallerWriteCloser struct{ io.Writer }

func (windowsInstallerWriteCloser) Close() error { return nil }

func windowsInstallerZIP(t *testing.T, entries []windowsInstallerEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	w.RegisterCompressor(99, func(out io.Writer) (io.WriteCloser, error) {
		return windowsInstallerWriteCloser{out}, nil
	})
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: entry.method}
		mode := entry.mode
		if mode == 0 {
			mode = 0644
		}
		header.SetMode(mode)
		header.ExternalAttrs |= entry.attributes
		out, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(out, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func windowsInstallerWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func windowsInstallerTree(t *testing.T, root, exclude string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if exclude != "" && strings.EqualFold(path, exclude) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[rel] = "directory"
		} else {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[rel] = "file:" + string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func runWindowsInstaller(t *testing.T, options windowsInstallerOptions) windowsInstallerResult {
	t.Helper()
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell 5.1 (powershell.exe) is unavailable")
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range source {
		if b > 127 {
			t.Fatal("install.ps1 must be ASCII for BOM-less Windows PowerShell 5.1")
		}
	}
	root := t.TempDir()
	home := filepath.Join(root, "home with spaces")
	work := filepath.Join(root, "working directory")
	temp := filepath.Join(root, "temporary files")
	for _, dir := range []string{home, work, temp, filepath.Join(root, "runtime-profile")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(root, "new parent", "portable install")
	if options.defaultTarget {
		target = filepath.Join(home, "SuperCli")
	} else if options.flatTarget {
		target = filepath.Join(root, "portable install")
	}
	profile := filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1")
	protected := filepath.Join(root, "protected", "sentinel.txt")
	windowsInstallerWrite(t, profile, []byte("throw 'Profiles must neither be loaded nor modified'\r\n"))
	windowsInstallerWrite(t, filepath.Join(home, "unrelated", "supercli-data", "config.toml"), []byte("private data"))
	windowsInstallerWrite(t, filepath.Join(work, "keep.txt"), []byte("working directory data"))
	windowsInstallerWrite(t, protected, []byte("never overwrite this sentinel"))
	if options.existing == "file" {
		windowsInstallerWrite(t, target, []byte("existing target file"))
	} else if options.existing != "" {
		if err := os.MkdirAll(target, 0755); err != nil {
			t.Fatal(err)
		}
		if options.existing == "data" {
			windowsInstallerWrite(t, filepath.Join(target, "supercli.exe"), []byte("existing binary"))
			windowsInstallerWrite(t, filepath.Join(target, "supercli-data", "config.toml"), []byte("existing private config"))
		}
	}
	var existingBefore map[string]string
	if options.existing != "" {
		existingBefore = windowsInstallerTree(t, target, "")
	}
	homeBefore := windowsInstallerTree(t, home, target)
	workBefore := windowsInstallerTree(t, work, "")

	entries := windowsInstallerPayload()
	if options.entries != nil {
		entries = options.entries(root)
	}
	archive := options.archive
	if archive == nil {
		archive = windowsInstallerZIP(t, entries)
	}
	hash := sha256.Sum256(archive)
	digest := hex.EncodeToString(hash[:])
	sums := digest + "  " + windowsInstallerAsset + "\n"
	if options.checksums != nil {
		sums = options.checksums(digest)
	}
	version := options.version
	if version == "" {
		version = "v1.2.3"
	}
	api := "https://api.github.com/repos/snakex21/SuperCli/releases/tags/v" + strings.TrimPrefix(version, "v")
	if options.latest {
		api = "https://api.github.com/repos/snakex21/SuperCli/releases/latest"
	}
	release := map[string]any{
		"tag_name": "v1.2.3", "draft": false, "prerelease": false,
		"assets": []map[string]string{{"name": windowsInstallerAsset}, {"name": "SHA256SUMS"}},
	}
	if options.release != nil {
		options.release(release)
	}
	nativeArch := 9
	if options.nativeArch != nil {
		nativeArch = *options.nativeArch
	}
	if options.pathCase != nil {
		copy := *options.pathCase
		if copy.Raw != nil {
			raw := strings.ReplaceAll(*copy.Raw, "{TARGET}", target)
			copy.Raw = &raw
		}
		copy.Process = strings.ReplaceAll(copy.Process, "{TARGET}", target)
		options.pathCase = &copy
	}
	routes := map[string]string{
		api: "api",
		"https://github.com/snakex21/SuperCli/releases/download/v1.2.3/SHA256SUMS":               "sums",
		"https://github.com/snakex21/SuperCli/releases/download/v1.2.3/" + windowsInstallerAsset: "zip",
	}
	for uri, kind := range options.routes {
		routes[uri] = kind
	}
	redirects := make(map[string]windowsInstallerRedirect)
	for uri, redirect := range options.redirects {
		if redirect.Status == 0 {
			redirect.Status = 302
		}
		redirects[uri] = redirect
	}
	config := map[string]any{
		"pathCase": options.pathCase, "routes": routes, "redirects": redirects,
		"home": home, "target": target, "profile": profile, "nativeArch": nativeArch,
		"api": api, "base": "https://github.com/snakex21/SuperCli/releases/download/v1.2.3/",
		"asset": windowsInstallerAsset, "release": release,
		"failNetwork": options.failNetwork, "race": options.race,
	}
	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	windowsInstallerWrite(t, filepath.Join(root, "config.json"), configBytes)
	windowsInstallerWrite(t, filepath.Join(root, "fixture.zip"), archive)
	windowsInstallerWrite(t, filepath.Join(root, "SHA256SUMS"), []byte(sums))
	windowsInstallerWrite(t, filepath.Join(root, "install.ps1"), source)
	windowsInstallerWrite(t, filepath.Join(root, "run.ps1"), []byte(windowsInstallerHarness))

	// Remove inherited installation overrides, and redirect every fixture-related
	// environment path before PowerShell starts. PowerShell/.NET create startup
	// caches under USERPROFILE as well as AppData. Keep those in separate temporary
	// runtime subtrees; the harness sets the PS HOME/PROFILE variables to the strict
	// home/data baseline before the installer runs.
	blocked := map[string]bool{
		"SUPERCLI_VERSION": true, "SUPERCLI_DIR": true, "SUPERCLI_NO_PATH": true,
		"SUPERCLI_TEST_ROOT": true, "SUPERCLI_TEST_BIN": true, "HOME": true, "USERPROFILE": true,
		"APPDATA": true, "LOCALAPPDATA": true, "TEMP": true, "TMP": true,
	}
	if options.processArch != "" {
		blocked["PROCESSOR_ARCHITECTURE"] = true
	}
	if options.wow64Arch != "" {
		blocked["PROCESSOR_ARCHITEW6432"] = true
	}
	var env []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !blocked[strings.ToUpper(key)] {
			env = append(env, value)
		}
	}
	env = append(env, "SUPERCLI_NO_PATH=1", "SUPERCLI_TEST_ROOT="+root, "SUPERCLI_TEST_BIN="+target,
		"HOME="+home, "USERPROFILE="+filepath.Join(root, "runtime-profile"), "APPDATA="+filepath.Join(root, "runtime-data", "Roaming"),
		"LOCALAPPDATA="+filepath.Join(root, "runtime-data", "Local"), "TEMP="+temp, "TMP="+temp)
	if !options.latest {
		env = append(env, "SUPERCLI_VERSION="+version)
	}
	if !options.defaultTarget {
		env = append(env, "SUPERCLI_DIR="+target)
	}
	if options.processArch != "" {
		env = append(env, "PROCESSOR_ARCHITECTURE="+options.processArch)
	}
	if options.wow64Arch != "" {
		env = append(env, "PROCESSOR_ARCHITEW6432="+options.wow64Arch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(root, "run.ps1"))
	cmd.Dir = work
	cmd.Env = env
	output, runErr := cmd.CombinedOutput()
	resultBytes, err := os.ReadFile(filepath.Join(root, "result.json"))
	if err != nil {
		t.Fatalf("PowerShell harness did not report: %v (execution: %v)\n%s", err, runErr, output)
	}
	var result windowsInstallerResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("invalid PowerShell report: %v\n%s\n%s", err, resultBytes, output)
	}
	if !strings.HasPrefix(result.Version, "5.1.") {
		t.Fatalf("tests require Windows PowerShell 5.1, got %q", result.Version)
	}
	if options.wantError == "" {
		if runErr != nil || !result.Success {
			t.Fatalf("installer failed: %v; %s\n%s", runErr, result.Error, output)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.name, "/") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(entry.name)))
			if err != nil || string(data) != entry.data {
				t.Errorf("public payload %q not preserved: %q, %v", entry.name, data, err)
			}
		}
		installed := windowsInstallerTree(t, target, "")
		for path := range installed {
			if strings.EqualFold(filepath.Base(path), "SHA256SUMS") || strings.EqualFold(filepath.Base(path), windowsInstallerAsset) {
				t.Errorf("download staging leaked into installation: %s", path)
			}
		}
	} else {
		if runErr == nil || result.Success || !strings.Contains(result.Error, options.wantError) {
			t.Fatalf("expected failure containing %q, got success=%v, execution=%v, error=%q\n%s", options.wantError, result.Success, runErr, result.Error, output)
		}
		if options.existing == "" && options.race == "" {
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Errorf("failed installation touched target: %v", err)
			}
			if !options.defaultTarget && !options.flatTarget {
				if _, err := os.Lstat(filepath.Dir(target)); !os.IsNotExist(err) {
					t.Errorf("failed installation created a target ancestor: %v", err)
				}
			}
		}
	}
	if options.existing != "" {
		if after := windowsInstallerTree(t, target, ""); !reflect.DeepEqual(existingBefore, after) {
			t.Errorf("existing target changed: before=%v after=%v", existingBefore, after)
		}
	}
	if options.race != "" {
		path := target
		if options.race == "directory" {
			path = filepath.Join(target, "keep.txt")
			if tree := windowsInstallerTree(t, target, ""); len(tree) != 2 {
				t.Errorf("installer merged into racing directory: %v", tree)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "created by another installer" {
			t.Errorf("racing target overwritten: %q, %v", data, err)
		}
	}
	if len(result.Calls) != options.wantCalls {
		t.Errorf("expected %d mocked network calls, got %v", options.wantCalls, result.Calls)
	}
	if options.pathCase == nil {
		if result.ProcessBefore != result.ProcessAfter {
			t.Errorf("portable process PATH changed: %q -> %q", result.ProcessBefore, result.ProcessAfter)
		}
		if result.RegistryOpens != 0 {
			t.Errorf("portable install opened user registry %d times", result.RegistryOpens)
		}
	} else {
		pc := options.pathCase
		wantProcess := strings.ReplaceAll(pc.WantProcess, "{TARGET}", target)
		if result.ProcessAfter != wantProcess {
			t.Errorf("process PATH: got %q, want %q", result.ProcessAfter, wantProcess)
		}
		var wantRaw *string
		if pc.WantRaw != nil {
			value := strings.ReplaceAll(*pc.WantRaw, "{TARGET}", target)
			wantRaw = &value
		}
		if !reflect.DeepEqual(result.RegistryRaw, wantRaw) || result.RegistryKind != pc.WantKind {
			t.Errorf("raw PATH/kind changed incorrectly: raw=%v kind=%s; want raw=%v kind=%s", result.RegistryRaw, result.RegistryKind, wantRaw, pc.WantKind)
		}
		if result.RegistryOpens != 1 || result.RegistryReads != 1 || result.RegistryWrites != pc.WantWrites || result.RegistryCloses != 1 {
			t.Errorf("registry lifecycle: opens=%d reads=%d writes=%d closes=%d", result.RegistryOpens, result.RegistryReads, result.RegistryWrites, result.RegistryCloses)
		}
		wantKindReads := 0
		if pc.Raw != nil && !pc.ReadError {
			wantKindReads = 1
		}
		if result.RegistryKindReads != wantKindReads {
			t.Errorf("registry kind reads = %d, want %d", result.RegistryKindReads, wantKindReads)
		}
		if pc.WantWarning != "" && !strings.Contains(string(output), pc.WantWarning) {
			t.Errorf("expected warning %q, got %s", pc.WantWarning, output)
		}
	}
	for _, pair := range []struct{ label, before, after string }{
		{"user PATH", result.UserBefore, result.UserAfter},
		{"machine PATH", result.MachineBefore, result.MachineAfter},
		{"TLS settings", result.TLSBefore, result.TLSAfter},
		{"working directory", result.LocationBefore, result.LocationAfter},
	} {
		if pair.before != pair.after {
			t.Errorf("%s changed despite portable mode: %q -> %q", pair.label, pair.before, pair.after)
		}
	}
	if after := windowsInstallerTree(t, home, target); !reflect.DeepEqual(homeBefore, after) {
		t.Errorf("home/profile data changed: before=%v after=%v", homeBefore, after)
	}
	if after := windowsInstallerTree(t, work, ""); !reflect.DeepEqual(workBefore, after) {
		t.Errorf("working data changed: before=%v after=%v", workBefore, after)
	}
	if data, err := os.ReadFile(protected); err != nil || string(data) != "never overwrite this sentinel" {
		t.Errorf("protected data overwritten: %q, %v", data, err)
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".supercli-install-") {
			t.Errorf("installer scratch not cleaned: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestWindowsInstallerFreshOffline(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts windowsInstallerOptions
	}{
		{"pinned v and nested portable directory", windowsInstallerOptions{}},
		{"pinned bare version and default HOME", windowsInstallerOptions{version: "1.2.3", defaultTarget: true}},
		{"stable latest from GitHub API", windowsInstallerOptions{latest: true, defaultTarget: true}},
		{"binary checksum marker and uppercase hash", windowsInstallerOptions{checksums: func(hash string) string {
			return strings.Repeat("0", 64) + "  supercli_1.2.3_linux_amd64.zip\r\n" + strings.ToUpper(hash) + " *" + windowsInstallerAsset + "\r\n"
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.wantCalls = 3
			runWindowsInstaller(t, tc.opts)
		})
	}
}

func TestWindowsInstallerNativeArchitecture(t *testing.T) {
	for _, arch := range []int{0, 5, 6, 12, 65535} {
		t.Run(fmt.Sprint(arch), func(t *testing.T) {
			runWindowsInstaller(t, windowsInstallerOptions{nativeArch: &arch, processArch: "AMD64", wow64Arch: "AMD64", wantError: "Unsupported platform windows/"})
		})
	}
	t.Run("native API ignores 32-bit process environment", func(t *testing.T) {
		arch := os.Getenv("PROCESSOR_ARCHITEW6432")
		if arch == "" {
			arch = os.Getenv("PROCESSOR_ARCHITECTURE")
		}
		if !strings.EqualFold(arch, "AMD64") {
			t.Skip("native AMD64 success check requires an AMD64 Windows host")
		}
		native := -1 // Do not preload the mock native API type for this case.
		runWindowsInstaller(t, windowsInstallerOptions{nativeArch: &native, processArch: "x86", wow64Arch: "AMD64", wantCalls: 3})
	})
}

func TestWindowsInstallerRefusesExistingTargets(t *testing.T) {
	for _, existing := range []string{"file", "empty", "data"} {
		t.Run(existing, func(t *testing.T) {
			result := runWindowsInstaller(t, windowsInstallerOptions{existing: existing, defaultTarget: true, wantError: "Refusing to overwrite existing target"})
			if !strings.Contains(result.Error, "--update") || !strings.Contains(result.Error, "SUPERCLI_DIR") {
				t.Errorf("missing existing-user guidance: %s", result.Error)
			}
		})
	}
	for _, race := range []string{"file", "directory"} {
		t.Run("target created during download "+race, func(t *testing.T) {
			runWindowsInstaller(t, windowsInstallerOptions{race: race, flatTarget: true, wantError: "Refusing to overwrite existing target", wantCalls: 3})
		})
	}
}

func TestWindowsInstallerRejectsInvalidVersions(t *testing.T) {
	for _, version := range []string{"1.2", "01.2.3", "v1.02.3", "V1.2.3", "1.2.3-rc.1", "1.2.3+build", " 1.2.3", "1.2.3\n", "v1.2.3/../../other"} {
		t.Run(fmt.Sprintf("%q", version), func(t *testing.T) {
			runWindowsInstaller(t, windowsInstallerOptions{version: version, wantError: "Invalid version"})
		})
	}
}

func TestWindowsInstallerRejectsUnsupportedReleases(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		release       func(map[string]any)
	}{
		{"prerelease", "Unsupported release", func(r map[string]any) { r["prerelease"] = true }},
		{"draft", "Unsupported release", func(r map[string]any) { r["draft"] = true }},
		{"invalid stable tag", "Invalid version", func(r map[string]any) { r["tag_name"] = "v1.2.3-rc.1" }},
		{"missing stable metadata", "Invalid GitHub release response", func(r map[string]any) { delete(r, "draft") }},
		{"no Windows asset", "Unsupported release", func(r map[string]any) {
			r["assets"] = []map[string]string{{"name": "supercli_1.2.3_linux_amd64.zip"}, {"name": "SHA256SUMS"}}
		}},
		{"duplicate asset", "Unsupported release", func(r map[string]any) {
			r["assets"] = []map[string]string{{"name": windowsInstallerAsset}, {"name": windowsInstallerAsset}, {"name": "SHA256SUMS"}}
		}},
		{"no checksum asset", "Unsupported release", func(r map[string]any) {
			r["assets"] = []map[string]string{{"name": windowsInstallerAsset}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runWindowsInstaller(t, windowsInstallerOptions{latest: true, release: tc.release, wantError: tc.message, wantCalls: 1})
		})
	}
	t.Run("pinned release mismatch", func(t *testing.T) {
		runWindowsInstaller(t, windowsInstallerOptions{version: "2.0.0", wantError: "requested 2.0.0", wantCalls: 1})
	})
}

func TestWindowsInstallerVerifiesExactChecksum(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		sums          func(string) string
	}{
		{"hash mismatch", "SHA256 mismatch", func(string) string { return strings.Repeat("0", 64) + "  " + windowsInstallerAsset }},
		{"duplicate exact checksums", "found 2", func(hash string) string {
			return hash + "  " + windowsInstallerAsset + "\n" + hash + " *" + windowsInstallerAsset
		}},
		{"duplicate with invalid hash", "found 2", func(hash string) string {
			return hash + "  " + windowsInstallerAsset + "\ninvalid  " + windowsInstallerAsset
		}},
		{"different filename only", "found 0", func(hash string) string { return hash + "  other_" + windowsInstallerAsset }},
		{"filename prefix only", "found 0", func(hash string) string { return hash + "  " + windowsInstallerAsset + ".backup" }},
		{"filename path is not exact", "found 0", func(hash string) string { return hash + "  ./" + windowsInstallerAsset }},
		{"invalid hash", "Invalid SHA256", func(string) string { return "xyz  " + windowsInstallerAsset }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Invalid ZIP bytes prove checksum errors occur before ZIP inspection.
			runWindowsInstaller(t, windowsInstallerOptions{archive: []byte("not a ZIP"), checksums: tc.sums, wantError: tc.message, wantCalls: 3})
		})
	}
}

func TestWindowsInstallerRejectsUnsafeZIPs(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		entry         func(string) windowsInstallerEntry
	}{
		{"traversal", "Unsafe ZIP path", func(string) windowsInstallerEntry { return windowsInstallerEntry{name: "../escape.txt", data: "bad"} }},
		{"backslash traversal", "Unsafe ZIP path", func(string) windowsInstallerEntry {
			return windowsInstallerEntry{name: `docs\..\escape.txt`, data: "bad"}
		}},
		{"absolute drive path", "Unsafe ZIP path", func(root string) windowsInstallerEntry {
			return windowsInstallerEntry{name: filepath.ToSlash(filepath.Join(root, "protected", "sentinel.txt")), data: "bad"}
		}},
		{"rooted path", "Unsafe ZIP path", func(root string) windowsInstallerEntry {
			path := filepath.Join(root, "protected", "sentinel.txt")
			return windowsInstallerEntry{name: filepath.ToSlash(strings.TrimPrefix(path, filepath.VolumeName(path))), data: "bad"}
		}},
		{"symlink", "symlink, reparse point or special file", func(string) windowsInstallerEntry {
			return windowsInstallerEntry{name: "docs/link", data: "../../protected/sentinel.txt", mode: os.ModeSymlink | 0777}
		}},
		{"reparse point", "symlink, reparse point or special file", func(string) windowsInstallerEntry {
			return windowsInstallerEntry{name: "docs/link", data: "bad", attributes: 0x400}
		}},
		{"case-insensitive duplicate", "Duplicate ZIP path", func(string) windowsInstallerEntry { return windowsInstallerEntry{name: "readme.md", data: "bad"} }},
		{"file as parent directory", "Conflicting ZIP", func(string) windowsInstallerEntry { return windowsInstallerEntry{name: "supercli-data", data: "bad"} }},
		{"alternate data stream", "Unsafe ZIP path", func(string) windowsInstallerEntry {
			return windowsInstallerEntry{name: "supercli.exe:stream", data: "bad"}
		}},
		{"Windows trailing dot", "Unsafe ZIP path", func(string) windowsInstallerEntry { return windowsInstallerEntry{name: "README.md.", data: "bad"} }},
		{"Windows device name", "Unsafe ZIP path", func(string) windowsInstallerEntry { return windowsInstallerEntry{name: "docs/NUL.txt", data: "bad"} }},
		{"empty path segment", "Unsafe ZIP path", func(string) windowsInstallerEntry { return windowsInstallerEntry{name: "docs//file.txt", data: "bad"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runWindowsInstaller(t, windowsInstallerOptions{
				entries:   func(root string) []windowsInstallerEntry { return append(windowsInstallerPayload(), tc.entry(root)) },
				wantError: tc.message, wantCalls: 3,
			})
		})
	}
}

func TestWindowsInstallerRequiresBothExecutables(t *testing.T) {
	for _, exe := range []string{"supercli.exe", "supercli-web.exe"} {
		for _, kind := range []string{"missing", "directory", "empty"} {
			t.Run(exe+" "+kind, func(t *testing.T) {
				message := "Archive must contain the root file '" + exe + "'"
				if kind == "empty" {
					message = "Archive contains an empty '" + exe + "'"
				}
				runWindowsInstaller(t, windowsInstallerOptions{entries: func(string) []windowsInstallerEntry {
					var entries []windowsInstallerEntry
					for _, entry := range windowsInstallerPayload() {
						if entry.name == exe {
							switch kind {
							case "missing":
								continue
							case "directory":
								entry.name += "/"
								entry.mode = os.ModeDir | 0755
							}
							entry.data = ""
						}
						entries = append(entries, entry)
					}
					return entries
				}, wantError: message, wantCalls: 3})
			})
		}
	}
}

func TestWindowsInstallerDownloadAndExtractionFailures(t *testing.T) {
	for _, failure := range []string{"api", "sums", "zip"} {
		t.Run("download "+failure, func(t *testing.T) {
			calls := map[string]int{"api": 1, "sums": 2, "zip": 3}[failure]
			runWindowsInstaller(t, windowsInstallerOptions{failNetwork: failure, wantError: "mock download failure: " + failure, wantCalls: calls})
		})
	}
	t.Run("invalid ZIP after valid SHA256", func(t *testing.T) {
		runWindowsInstaller(t, windowsInstallerOptions{archive: []byte("invalid ZIP"), wantError: "SuperCli installation failed:", wantCalls: 3})
	})
	t.Run("entry extraction after partial staging", func(t *testing.T) {
		runWindowsInstaller(t, windowsInstallerOptions{entries: func(string) []windowsInstallerEntry {
			return append(windowsInstallerPayload(), windowsInstallerEntry{name: "docs/unsupported.txt", data: "unsupported compression", method: 99})
		}, wantError: "SuperCli installation failed:", wantCalls: 3})
	})
}

func TestWindowsInstallerPreservesRawUserPATH(t *testing.T) {
	text := func(value string) *string { return &value }
	const process = `C:\Windows\System32;C:\ProcessOnly`
	const references = `%USERPROFILE%\bin;%ANOTHER_REFERENCE%\tools`
	for _, tc := range []struct {
		name string
		path windowsInstallerPathCase
	}{
		{"REG_EXPAND_SZ raw references", windowsInstallerPathCase{Raw: text(references), Kind: "ExpandString", WantRaw: text(references + ";{TARGET}"), WantKind: "ExpandString", WantWrites: 1}},
		{"REG_SZ raw references", windowsInstallerPathCase{Raw: text(references), Kind: "String", WantRaw: text(references + ";{TARGET}"), WantKind: "String", WantWrites: 1}},
		{"REG_SZ reference is literal for deduplication", windowsInstallerPathCase{Raw: text(`%SUPERCLI_TEST_BIN%`), Kind: "String", WantRaw: text(`%SUPERCLI_TEST_BIN%;{TARGET}`), WantKind: "String", WantWrites: 1}},
		{"empty existing REG_SZ", windowsInstallerPathCase{Raw: text(""), Kind: "String", WantRaw: text("{TARGET}"), WantKind: "String", WantWrites: 1}},
		{"missing user PATH", windowsInstallerPathCase{Kind: "Unknown", WantRaw: text("{TARGET}"), WantKind: "ExpandString", WantWrites: 1}},
		{"existing expanded entry is not rewritten", windowsInstallerPathCase{Raw: text(`%SUPERCLI_TEST_BIN%\`), Kind: "ExpandString", WantRaw: text(`%SUPERCLI_TEST_BIN%\`), WantKind: "ExpandString"}},
		{"existing literal entry is not rewritten", windowsInstallerPathCase{Raw: text("{TARGET}"), Kind: "String", WantRaw: text("{TARGET}"), WantKind: "String", Process: process + ";{TARGET}", WantProcess: process + ";{TARGET}"}},
		{"read failure preserves registry and closes key", windowsInstallerPathCase{Raw: text(references), Kind: "ExpandString", ReadError: true, WantRaw: text(references), WantKind: "ExpandString", WantWarning: "mock registry read denied"}},
		{"write failure preserves registry and closes key", windowsInstallerPathCase{Raw: text(references), Kind: "ExpandString", WriteError: true, WantRaw: text(references), WantKind: "ExpandString", WantWarning: "mock registry write denied"}},
		{"unsupported registry kind is not overwritten", windowsInstallerPathCase{Raw: text("original value"), Kind: "MultiString", WantRaw: text("original value"), WantKind: "MultiString", WantWarning: "Unsupported user PATH registry value kind"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.path.Process == "" {
				tc.path.Process = process
			}
			if tc.path.WantProcess == "" {
				tc.path.WantProcess = process + ";{TARGET}"
			}
			runWindowsInstaller(t, windowsInstallerOptions{pathCase: &tc.path, wantCalls: 3})
		})
	}
}

func TestWindowsInstallerHTTPSRedirects(t *testing.T) {
	const latest = "https://api.github.com/repos/snakex21/SuperCli/releases/latest"
	const pinned = "https://api.github.com/repos/snakex21/SuperCli/releases/tags/v1.2.3"
	const base = "https://github.com/snakex21/SuperCli/releases/download/v1.2.3/"
	const apiHop = "https://api.github.com/repos/snakex21/SuperCli/releases/12345"
	const assetHop = "https://release-assets.githubusercontent.com/staged/archive.zip"
	const sumsFinal = "https://release-assets.githubusercontent.com/SHA256SUMS?signature=fixture"
	const zipFinal = "https://release-assets.githubusercontent.com/bundle.zip"
	for _, tc := range []struct {
		name    string
		options windowsInstallerOptions
	}{
		{"latest downgrade", windowsInstallerOptions{latest: true, redirects: map[string]windowsInstallerRedirect{latest: {Location: "http://api.github.com/unsafe"}}, wantError: "Refusing non-HTTPS", wantCalls: 1}},
		{"latest intermediate HTTPS then HTTP", windowsInstallerOptions{latest: true, redirects: map[string]windowsInstallerRedirect{
			latest: {Location: apiHop}, apiHop: {Location: "http://api.github.com/unsafe"},
		}, wantError: "Refusing non-HTTPS", wantCalls: 2}},
		{"checksum downgrade", windowsInstallerOptions{latest: true, redirects: map[string]windowsInstallerRedirect{
			base + "SHA256SUMS": {Location: "http://release-assets.githubusercontent.com/SHA256SUMS"},
		}, wantError: "Refusing non-HTTPS", wantCalls: 2}},
		{"bundle intermediate HTTPS then HTTP", windowsInstallerOptions{latest: true, redirects: map[string]windowsInstallerRedirect{
			base + windowsInstallerAsset: {Location: assetHop}, assetHop: {Location: "http://release-assets.githubusercontent.com/bundle.zip"},
		}, wantError: "Refusing non-HTTPS", wantCalls: 4}},
		{"HTTPS API checksum bundle and relative redirects", windowsInstallerOptions{latest: true, redirects: map[string]windowsInstallerRedirect{
			latest:                       {Location: "/repos/snakex21/SuperCli/releases/12345", Status: 301},
			base + "SHA256SUMS":          {Location: sumsFinal, Status: 303},
			base + windowsInstallerAsset: {Location: "//release-assets.githubusercontent.com/staged/archive.zip", Status: 307},
			assetHop:                     {Location: "../bundle.zip", Status: 308},
		}, routes: map[string]string{apiHop: "api", sumsFinal: "sums", zipFinal: "zip"}, wantCalls: 7}},
		{"pinned API HTTPS redirect with PS51 error record", windowsInstallerOptions{redirects: map[string]windowsInstallerRedirect{
			pinned: {Location: apiHop, Status: 302},
		}, routes: map[string]string{apiHop: "api"}, wantCalls: 4}},
		{"bounded redirect loop", windowsInstallerOptions{latest: true, redirects: map[string]windowsInstallerRedirect{
			latest: {Location: latest},
		}, wantError: "Too many HTTPS redirects (maximum 5)", wantCalls: 6}},
		{"redirect without Location", windowsInstallerOptions{latest: true, redirects: map[string]windowsInstallerRedirect{
			latest: {Location: ""},
		}, wantError: "HTTPS redirect has no Location", wantCalls: 1}},
		{"redirect error without response fails closed", windowsInstallerOptions{latest: true, redirects: map[string]windowsInstallerRedirect{
			latest: {Location: apiHop, NoResponse: true},
		}, wantError: "HTTPS request did not return an HTTP response", wantCalls: 1}},
		{"unrelated web error is not suppressed", windowsInstallerOptions{latest: true, redirects: map[string]windowsInstallerRedirect{
			latest: {Location: apiHop, OtherError: true},
		}, wantError: "mock web cmdlet redirect error", wantCalls: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) { runWindowsInstaller(t, tc.options) })
	}
}

const windowsInstallerHarness = `
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0
$root = $env:SUPERCLI_TEST_ROOT
$global:supercliTestConfig = [IO.File]::ReadAllText((Join-Path $root 'config.json')) | ConvertFrom-Json
$global:supercliTestCalls = New-Object 'System.Collections.Generic.List[string]'
Set-Variable -Name HOME -Scope Global -Force -Value $global:supercliTestConfig.home
Set-Variable -Name PROFILE -Scope Global -Force -Value $global:supercliTestConfig.profile
if ($HOME -cne $global:supercliTestConfig.home -or $env:SUPERCLI_NO_PATH -cne '1') {
    throw 'Fixture isolation was not established'
}
if ($global:supercliTestConfig.nativeArch -ge 0) {
    Add-Type -TypeDefinition ('namespace SuperCliInstaller { public static class NativePlatform { public static ushort Architecture() { return ' + $global:supercliTestConfig.nativeArch + '; } } }')
}
function global:Invoke-RestMethod { throw 'Unexpected unguarded REST request; all requests must use the HTTPS redirect helper' }
function global:Invoke-WebRequest {
    [CmdletBinding()]
    param([string] $Uri, [hashtable] $Headers, [switch] $UseBasicParsing, [string] $OutFile,
        [switch] $PassThru, [int] $MaximumRedirection = -1)
    $global:supercliTestCalls.Add('GET ' + $Uri)
    if (-not $UseBasicParsing -or $MaximumRedirection -ne 0) { throw 'Every request must disable automatic redirects and use basic parsing' }
    if (-not $Headers.ContainsKey('User-Agent')) { throw 'Missing GitHub User-Agent' }
    if ($OutFile -ne '' -and -not $PassThru) { throw 'File requests need PassThru to inspect redirects' }
    if (-not $Uri.StartsWith('https://')) { throw 'Mock refused a non-HTTPS request' }
    $redirectProperty = $global:supercliTestConfig.redirects.PSObject.Properties[$Uri]
    if ($null -ne $redirectProperty) {
        $redirect = $redirectProperty.Value
        if ($OutFile -ne '') { [IO.File]::WriteAllText($OutFile, 'redirect response body') }
        if (-not $redirect.noResponse) {
            Write-Output ([pscustomobject] @{ StatusCode = $redirect.status; Headers = @{ Location = $redirect.location }; Content = '' })
        }
        if ($redirect.status -in @(301, 302) -or $redirect.noResponse -or $redirect.otherError) {
            $id = 'MaximumRedirectExceeded'
            if ($redirect.otherError) { $id = 'UnexpectedWebError' }
            # Reproduce PS 5.1: output first, then a nonterminating ErrorRecord
            # with a plain Exception, not WebException.Response.
            $exception = New-Object Exception 'mock web cmdlet redirect error'
            $record = New-Object Management.Automation.ErrorRecord ($exception, $id, [Management.Automation.ErrorCategory]::InvalidOperation, $Uri)
            $PSCmdlet.WriteError($record)
        }
        return
    }
    $route = $global:supercliTestConfig.routes.PSObject.Properties[$Uri]
    if ($null -eq $route) { throw "Unexpected download endpoint: $Uri" }
    $kind = $route.Value
    if ($global:supercliTestConfig.failNetwork -eq $kind) {
        if ($OutFile -ne '') { [IO.File]::WriteAllText($OutFile, 'partial download') }
        throw "mock download failure: $kind"
    }
    if ($kind -eq 'api') {
        return ([pscustomobject] @{ StatusCode = 200; Headers = @{}; Content = ($global:supercliTestConfig.release | ConvertTo-Json -Depth 10) })
    }
    $source = Join-Path $env:SUPERCLI_TEST_ROOT 'SHA256SUMS'
    if ($kind -eq 'zip') { $source = Join-Path $env:SUPERCLI_TEST_ROOT 'fixture.zip' }
    [IO.File]::Copy($source, $OutFile, $true)
    if ($kind -eq 'zip' -and $global:supercliTestConfig.race -ne '') {
        $target = $global:supercliTestConfig.target
        if ($global:supercliTestConfig.race -eq 'directory') {
            [IO.Directory]::CreateDirectory($target) | Out-Null
            $target = Join-Path $target 'keep.txt'
        } else {
            [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
        }
        [IO.File]::WriteAllText($target, 'created by another installer')
    }
    return ([pscustomobject] @{ StatusCode = 200; Headers = @{}; Content = '' })
}
function global:Start-Process { throw 'Installer must not start downloaded executables' }
function global:Invoke-Item { throw 'Installer must not launch downloaded files' }
$global:supercliTestRegistryOpens = 0
$global:supercliTestRegistry = $null
$registryFactory = {
    $global:supercliTestRegistryOpens++
    if ($null -eq $global:supercliTestRegistry) { throw 'Portable mode must never open a registry key' }
    return $global:supercliTestRegistry
}
if ($null -ne $global:supercliTestConfig.pathCase) {
    $pc = $global:supercliTestConfig.pathCase
    $global:supercliTestRegistry = [pscustomobject] @{ Raw = $pc.raw; Kind = $pc.kind; Reads = 0; KindReads = 0; Writes = 0; Closes = 0; ReadError = $pc.readError; WriteError = $pc.writeError }
    $global:supercliTestRegistry | Add-Member ScriptMethod GetValue {
        param($name, $default, $options)
        $this.Reads++
        if ($name -cne 'Path' -or $options -ne [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) { throw 'Registry read must use raw, unexpanded PATH' }
        if ($this.ReadError) { throw 'mock registry read denied' }
        return $this.Raw
    }
    $global:supercliTestRegistry | Add-Member ScriptMethod GetValueKind {
        param($name)
        if ($name -cne 'Path') { throw 'Wrong registry value' }
        $this.KindReads++
        return ([Microsoft.Win32.RegistryValueKind] $this.Kind)
    }
    $global:supercliTestRegistry | Add-Member ScriptMethod SetValue {
        param($name, $value, $kind)
        if ($name -cne 'Path') { throw 'Wrong registry value' }
        if ($this.WriteError) { throw 'mock registry write denied' }
        $this.Writes++
        $this.Raw = $value
        $this.Kind = $kind.ToString()
    }
    $global:supercliTestRegistry | Add-Member ScriptMethod Dispose { $this.Closes++ }
    $env:Path = $pc.process
}
$processBefore = $env:Path
$userBefore = [Environment]::GetEnvironmentVariable('Path', [EnvironmentVariableTarget]::User)
$machineBefore = [Environment]::GetEnvironmentVariable('Path', [EnvironmentVariableTarget]::Machine)
$tlsBefore = [Net.ServicePointManager]::SecurityProtocol.ToString()
$locationBefore = (Get-Location).Path
$success = $false
$errorText = ''
try {
    $tokens = $null
    $parseErrors = $null
    $installer = Join-Path $root 'install.ps1'
    [void][Management.Automation.Language.Parser]::ParseFile($installer, [ref] $tokens, [ref] $parseErrors)
    if ($parseErrors.Count -ne 0) { throw ($parseErrors | Out-String) }
    if ($null -ne $global:supercliTestRegistry) { $env:SUPERCLI_NO_PATH = '0' }
    # Always inject the factory, including portable cases. Never use real HKCU.
    & $installer -OpenUserEnvironment $registryFactory
    $success = $true
} catch {
    $errorText = $_.Exception.Message
} finally {
    $report = [ordered] @{
        Success = $success; Error = $errorText; Version = $PSVersionTable.PSVersion.ToString()
        Calls = @($global:supercliTestCalls.ToArray())
        RegistryOpens = $global:supercliTestRegistryOpens
        ProcessBefore = $processBefore; ProcessAfter = $env:Path
        UserBefore = $userBefore; UserAfter = [Environment]::GetEnvironmentVariable('Path', [EnvironmentVariableTarget]::User)
        MachineBefore = $machineBefore; MachineAfter = [Environment]::GetEnvironmentVariable('Path', [EnvironmentVariableTarget]::Machine)
        TLSBefore = $tlsBefore; TLSAfter = [Net.ServicePointManager]::SecurityProtocol.ToString()
        LocationBefore = $locationBefore; LocationAfter = (Get-Location).Path
    }
    if ($null -ne $global:supercliTestRegistry) {
        $report.RegistryRaw = $global:supercliTestRegistry.Raw
        $report.RegistryKind = $global:supercliTestRegistry.Kind
        $report.RegistryReads = $global:supercliTestRegistry.Reads
        $report.RegistryKindReads = $global:supercliTestRegistry.KindReads
        $report.RegistryWrites = $global:supercliTestRegistry.Writes
        $report.RegistryCloses = $global:supercliTestRegistry.Closes
    }
    $env:Path = $processBefore
    $env:SUPERCLI_NO_PATH = '1'
    $json = $report | ConvertTo-Json -Depth 8
    [IO.File]::WriteAllText((Join-Path $root 'result.json'), $json, (New-Object Text.UTF8Encoding($false)))
}
if ($success) { exit 0 }
Write-Output $errorText
exit 1
`
