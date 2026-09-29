// Package updater installs verified release binaries beside the running executable.
// It never contacts the network until the user requests a check or download.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"supercli/internal/buildinfo"
)

const LatestURL = "https://api.github.com/repos/snakex21/SuperCli/releases/latest"
const ManifestName = "supercli-update.json"
const maxArchiveBytes int64 = 300 << 20

type State struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	Available       bool   `json:"available"`
	Supported       bool   `json:"supported"`
	URL             string `json:"url"`
	Asset           string `json:"asset,omitempty"`
	Size            int64  `json:"size,omitempty"`
	Status          string `json:"status"`
	RestartRequired bool   `json:"restart_required"`
}

type Asset struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Manifest struct {
	Version string  `json:"version"`
	Assets  []Asset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}
type release struct {
	Tag        string         `json:"tag_name"`
	URL        string         `json:"html_url"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type Manager struct {
	Dir            string
	CurrentVersion string
	OS, Arch       string
	Client         *http.Client
	LatestURL      string
}

func New() (*Manager, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	return newManager(filepath.Dir(executable), buildinfo.Version), nil
}

func (m *Manager) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many download redirects")
		}
		return githubURL(req.URL.String())
	}}
}
func githubURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return fmt.Errorf("invalid release URL")
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		if parsed.Port() == "" || parsed.Port() == "443" {
			return nil
		}
	}
	return fmt.Errorf("release URL is outside GitHub")
}
func (m *Manager) get(ctx context.Context, raw string, limit int64) ([]byte, error) {
	// An injected client/source is for local tests; normal launches only trust GitHub.
	if m.LatestURL == LatestURL {
		if err := githubURL(raw); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SuperCli/"+m.CurrentVersion)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	resp, err := m.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update request: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("update response exceeds size limit")
	}
	return data, nil
}
func semver(raw string) ([3]uint64, error) {
	var out [3]uint64
	parts := strings.Split(strings.TrimPrefix(raw, "v"), ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("invalid stable release version")
	}
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return out, fmt.Errorf("invalid stable release version")
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return out, fmt.Errorf("invalid stable release version")
			}
		}
		value, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return out, err
		}
		out[i] = value
	}
	return out, nil
}
func newer(latest, current string) (bool, error) {
	a, err := semver(latest)
	if err != nil {
		return false, err
	}
	b, err := semver(current)
	if err != nil {
		return false, err
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i], nil
		}
	}
	return false, nil
}
func bundleName(version, osName, arch string) string {
	return "supercli_" + strings.TrimPrefix(version, "v") + "_" + osName + "_" + arch + ".zip"
}

func (m *Manager) latest(ctx context.Context) (State, Asset, string, error) {
	state := State{CurrentVersion: m.CurrentVersion, Status: "current"}
	raw, err := m.get(ctx, m.LatestURL, 1<<20)
	if err != nil {
		return state, Asset{}, "", err
	}
	var rel release
	if err = json.Unmarshal(raw, &rel); err != nil {
		return state, Asset{}, "", err
	}
	if rel.Draft || rel.Prerelease {
		return state, Asset{}, "", fmt.Errorf("release is not stable")
	}
	state.LatestVersion = strings.TrimPrefix(rel.Tag, "v")
	state.Available, err = newer(state.LatestVersion, m.CurrentVersion)
	if err != nil {
		return state, Asset{}, "", err
	}
	state.URL = rel.URL
	if m.LatestURL == LatestURL {
		if err = githubURL(rel.URL); err != nil {
			return state, Asset{}, "", err
		}
	}
	if !state.Available {
		return state, Asset{}, "", nil
	}
	state.Status = "unsupported"
	var manifestURL string
	assets := map[string]releaseAsset{}
	for _, a := range rel.Assets {
		if _, duplicate := assets[a.Name]; duplicate {
			return state, Asset{}, "", fmt.Errorf("duplicate release asset")
		}
		assets[a.Name] = a
		if a.Name == ManifestName {
			manifestURL = a.URL
		}
	}
	if manifestURL == "" {
		return state, Asset{}, "", nil
	}
	raw, err = m.get(ctx, manifestURL, 1<<20)
	if err != nil {
		return state, Asset{}, "", err
	}
	var manifest Manifest
	if err = decodeStrict(raw, &manifest); err != nil {
		return state, Asset{}, "", err
	}
	if strings.TrimPrefix(manifest.Version, "v") != state.LatestVersion {
		return state, Asset{}, "", fmt.Errorf("manifest version differs from release")
	}
	wanted := bundleName(state.LatestVersion, m.OS, m.Arch)
	var selected Asset
	matches := 0
	for _, asset := range manifest.Assets {
		if asset.OS == m.OS && asset.Arch == m.Arch {
			selected = asset
			matches++
		}
	}
	if matches == 0 {
		return state, Asset{}, "", nil
	}
	if matches != 1 || selected.File != wanted || !validDigest(selected.SHA256) || selected.Size <= 0 || selected.Size > maxArchiveBytes {
		return state, Asset{}, "", fmt.Errorf("invalid update manifest asset")
	}
	remote, found := assets[selected.File]
	if !found || remote.Size != selected.Size {
		return state, Asset{}, "", fmt.Errorf("manifest asset is missing or size differs")
	}
	state.Supported = true
	state.Status = "available"
	state.Asset = selected.File
	state.Size = selected.Size
	return state, selected, remote.URL, nil
}
func (m *Manager) Check(ctx context.Context) (State, error) {
	state, _, _, err := m.latest(ctx)
	if err != nil {
		return state, err
	}
	if receipt, err := m.loadReceipt(); err == nil && receipt.Version == state.LatestVersion {
		if receipt.Installed && state.Available && m.verifyInstalled(receipt) == nil {
			state.Available = false
			state.Status = "installed"
			state.RestartRequired = true
		}
		if !receipt.Installed && state.Available && m.verifyStage(receipt) == nil {
			state.Status = "downloaded"
		}
	}
	return state, nil
}

// A signed-off manifest or receipt must contain exactly one complete JSON value.
func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing update JSON")
	}
	return nil
}
