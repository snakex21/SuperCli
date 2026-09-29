package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func updateFixture(t *testing.T, modify func(*Manifest, *[]byte)) *Manager {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, name := range []string{"supercli.exe", "supercli-web.exe"} {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0755)
		file, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		file.Write([]byte("new " + name))
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	bundle := buffer.Bytes()
	digest := sha256.Sum256(bundle)
	manifest := Manifest{Version: "1.0.1", Assets: []Asset{{OS: "windows", Arch: "amd64", File: bundleName("1.0.1", "windows", "amd64"), SHA256: hex.EncodeToString(digest[:]), Size: int64(len(bundle))}}}
	if modify != nil {
		modify(&manifest, &bundle)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			json.NewEncoder(w).Encode(release{Tag: "v1.0.1", URL: server.URL + "/release", Assets: []releaseAsset{{Name: ManifestName, URL: server.URL + "/manifest"}, {Name: manifest.Assets[0].File, URL: server.URL + "/bundle", Size: manifest.Assets[0].Size}}})
		case "/manifest":
			json.NewEncoder(w).Encode(manifest)
		case "/bundle":
			w.Write(bundle)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	manager := newManager(t.TempDir(), "1.0.0")
	manager.OS = "windows"
	manager.Arch = "amd64"
	manager.LatestURL = server.URL + "/latest"
	manager.Client = server.Client()
	return manager
}
func oldInstallation(t *testing.T, m *Manager) {
	t.Helper()
	for _, name := range m.binaryNames() {
		if err := os.WriteFile(filepath.Join(m.Dir, name), []byte("old "+name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	data := filepath.Join(m.Dir, "supercli-data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte("private configuration\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
func assertOldInstallation(t *testing.T, m *Manager) {
	t.Helper()
	for _, name := range m.binaryNames() {
		raw, err := os.ReadFile(filepath.Join(m.Dir, name))
		if err != nil || string(raw) != "old "+name {
			t.Fatalf("old executable changed %s: %q %v", name, raw, err)
		}
	}
}
func TestUpdateCheckDownloadInstallPreservesDataAndBackups(t *testing.T) {
	manager := updateFixture(t, nil)
	oldInstallation(t, manager)
	state, err := manager.Check(context.Background())
	if err != nil || state.Status != "available" || !state.Available || !state.Supported {
		t.Fatalf("check=%+v err=%v", state, err)
	}
	if _, err = os.Stat(filepath.Join(manager.Dir, "supercli-updates")); !os.IsNotExist(err) {
		t.Fatal("checking wrote local update data")
	}
	state, err = manager.Download(context.Background())
	if err != nil || state.Status != "downloaded" {
		t.Fatalf("download=%+v err=%v", state, err)
	}
	assertOldInstallation(t, manager)
	state, err = manager.Check(context.Background())
	if err != nil || state.Status != "downloaded" {
		t.Fatalf("download not retained: %+v %v", state, err)
	}
	state, err = manager.Install(context.Background())
	if err != nil || state.Status != "installed" || !state.RestartRequired {
		t.Fatalf("install=%+v err=%v", state, err)
	}
	for _, name := range manager.binaryNames() {
		raw, err := os.ReadFile(filepath.Join(manager.Dir, name))
		if err != nil || string(raw) != "new "+name {
			t.Fatalf("new executable missing %s: %q %v", name, raw, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(manager.Dir, "supercli-data", "config.toml"))
	if err != nil || string(raw) != "private configuration\n" {
		t.Fatal("portable data changed")
	}
	backups, err := filepath.Glob(filepath.Join(manager.Dir, "supercli-updates", "backup-1.0.1-*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup=%v %v", backups, err)
	}
	for _, name := range manager.binaryNames() {
		raw, err := os.ReadFile(filepath.Join(backups[0], name))
		if err != nil || string(raw) != "old "+name {
			t.Fatal("backup lost")
		}
	}
	state, err = manager.Install(context.Background())
	if err != nil || state.Status != "installed" {
		t.Fatalf("repeated install changed state: %+v %v", state, err)
	}
	state, err = manager.Check(context.Background())
	if err != nil || state.Available || !state.RestartRequired {
		t.Fatalf("running old version lost restart notice: %+v %v", state, err)
	}
	manager.CurrentVersion = "1.0.1"
	state, err = manager.Check(context.Background())
	if err != nil || state.Status != "current" || state.RestartRequired {
		t.Fatalf("new version incorrectly requests update: %+v %v", state, err)
	}
}
func TestUpdateRejectsInvalidManifestAndTamperedDownloads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*Manifest, *[]byte)
	}{
		{"wrong version", func(m *Manifest, _ *[]byte) { m.Version = "9.0.0" }},
		{"wrong filename", func(m *Manifest, _ *[]byte) { m.Assets[0].File = "other.zip" }},
		{"invalid digest", func(m *Manifest, _ *[]byte) { m.Assets[0].SHA256 = "invalid" }},
		{"wrong checksum", func(m *Manifest, _ *[]byte) { m.Assets[0].SHA256 = strings.Repeat("0", 64) }},
		{"short download", func(m *Manifest, _ *[]byte) { m.Assets[0].Size++ }},
		{"duplicate platform", func(m *Manifest, _ *[]byte) { m.Assets = append(m.Assets, m.Assets[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := updateFixture(t, tc.modify)
			oldInstallation(t, m)
			if _, err := m.Download(context.Background()); err == nil {
				t.Fatal("bad update accepted")
			}
			assertOldInstallation(t, m)
			if _, err := m.loadReceipt(); err == nil {
				t.Fatal("invalid download became installable")
			}
		})
	}
}
func TestUpdateTamperedStageCannotReplaceExistingBinaries(t *testing.T) {
	m := updateFixture(t, nil)
	oldInstallation(t, m)
	if _, err := m.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, err := m.loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(m.Dir, "supercli-updates", rec.Stage, rec.Files[0].Name)
	if err = os.WriteFile(staged, bytes.Repeat([]byte("x"), int(rec.Files[0].Size)), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Install(context.Background()); err == nil {
		t.Fatal("tampered stage installed")
	}
	assertOldInstallation(t, m)
}
func TestUpdateFailedPairReplacementRestoresFirstBinary(t *testing.T) {
	m := updateFixture(t, nil)
	oldInstallation(t, m)
	if _, err := m.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(m.Dir, "supercli-web.exe")
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("missing rollback: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(m.Dir, "supercli.exe"))
	if err != nil || string(raw) != "old supercli.exe" {
		t.Fatalf("first binary not restored: %q %v", raw, err)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("old supercli-web.exe"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(context.Background()); err != nil {
		t.Fatalf("retry lost stage: %v", err)
	}
}
func TestUpdateLocksAreReleasedWithoutStaleProcessFiles(t *testing.T) {
	m := updateFixture(t, nil)
	release, err := m.lock()
	if err != nil {
		t.Fatal(err)
	}
	if unlock, err := m.lock(); err == nil {
		unlock()
		release()
		t.Fatal("concurrent update accepted")
	}
	release()
	unlock, err := m.lock()
	if err != nil {
		t.Fatalf("completed update left stale lock: %v", err)
	}
	unlock()
}
func TestArchiveExtractionRejectsTraversalSymlinksDuplicatesAndMissingBinaries(t *testing.T) {
	for _, kind := range []string{"traversal", "symlink", "duplicate", "missing"} {
		t.Run(kind, func(t *testing.T) {
			m := updateFixture(t, nil)
			root, err := m.root(true)
			if err != nil {
				t.Fatal(err)
			}
			stage, err := os.MkdirTemp(root, ".stage-")
			if err != nil {
				t.Fatal(err)
			}
			var buffer bytes.Buffer
			archive := zip.NewWriter(&buffer)
			names := []string{"supercli.exe", "supercli-web.exe"}
			switch kind {
			case "traversal":
				names = append(names, "../outside")
			case "symlink":
				names = append(names, "shortcut")
			case "duplicate":
				names = append(names, "supercli.exe")
			case "missing":
				names = names[:1]
			}
			for _, name := range names {
				header := &zip.FileHeader{Name: name}
				header.SetMode(0755)
				if kind == "symlink" && name == "shortcut" {
					header.SetMode(os.ModeSymlink | 0777)
				}
				file, err := archive.CreateHeader(header)
				if err != nil {
					t.Fatal(err)
				}
				file.Write([]byte("binary bytes"))
			}
			if err = archive.Close(); err != nil {
				t.Fatal(err)
			}
			location := filepath.Join(stage, "bundle.zip")
			if err = os.WriteFile(location, buffer.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = m.extractBinaries(location, stage); err == nil {
				t.Fatal("unsafe or incomplete archive accepted")
			}
			if _, err = os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
				t.Fatal("archive escaped staging")
			}
		})
	}
}
func TestStableVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
		invalid         bool
	}{
		{"v1.0.1", "1.0.0", true, false}, {"1.0.0", "1.0.0", false, false}, {"0.9.9", "1.0.0", false, false}, {"1.10.0", "1.9.0", true, false}, {"1.0.0-beta", "0.9.0", false, true}, {"01.0.0", "1.0.0", false, true}, {"bad", "1.0.0", false, true},
	} {
		got, err := newer(tc.latest, tc.current)
		if (err != nil) != tc.invalid || got != tc.want {
			t.Fatalf("%s > %s: %v %v", tc.latest, tc.current, got, err)
		}
	}
}

func TestUpdateReusesVerifiedDownloadAndDoesNotRedownloadInstalledVersion(t *testing.T) {
	m := updateFixture(t, nil)
	oldInstallation(t, m)
	if _, err := m.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, err := m.loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Download(context.Background())
	if err != nil || state.Status != "downloaded" {
		t.Fatalf("%+v %v", state, err)
	}
	again, err := m.loadReceipt()
	if err != nil || again.Stage != rec.Stage {
		t.Fatal("repeated download replaced verified staging")
	}
	if _, err = m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err = m.Download(context.Background())
	if err != nil || state.Status != "installed" || !state.RestartRequired {
		t.Fatalf("%+v %v", state, err)
	}
	stages, _ := filepath.Glob(filepath.Join(m.Dir, "supercli-updates", ".stage-*"))
	if len(stages) != 1 {
		t.Fatalf("unnecessary downloads: %v", stages)
	}
}
func TestStrictUpdateJSONRejectsTrailingValues(t *testing.T) {
	for _, raw := range []string{`{"version":"1.0.1","assets":[]} {}`, `{"version":"1.0.1","assets":[]} invalid`, `{"version":"1.0.1","assets":[],"unknown":true}`} {
		if err := decodeStrict([]byte(raw), new(Manifest)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
