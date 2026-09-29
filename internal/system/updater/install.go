package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type fileRecord struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type receipt struct {
	Version   string       `json:"version"`
	Stage     string       `json:"stage"`
	Files     []fileRecord `json:"files"`
	Installed bool         `json:"installed"`
}

func validDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size
}
func (m *Manager) binaryNames() []string {
	if m.OS == "windows" {
		return []string{"supercli.exe", "supercli-web.exe"}
	}
	return []string{"supercli", "supercli-web"}
}
func (m *Manager) root(create bool) (string, error) {
	if !filepath.IsAbs(m.Dir) {
		return "", fmt.Errorf("application directory must be absolute")
	}
	root := filepath.Join(m.Dir, "supercli-updates")
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = os.Mkdir(root, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		info, err = os.Lstat(root)
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("update directory must be a real directory")
	}
	return root, nil
}
func (m *Manager) lock() (func(), error) {
	root, err := m.root(true)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(root, "operation.lock")
	if info, err := os.Lstat(lockPath); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, fmt.Errorf("invalid update lock file")
	}
	return lockFile(lockPath)
}
func (m *Manager) loadReceipt() (receipt, error) {
	var rec receipt
	root, err := m.root(false)
	if err != nil {
		return rec, err
	}
	file, err := os.Open(filepath.Join(root, "ready.json"))
	if err != nil {
		return rec, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		return rec, fmt.Errorf("invalid update receipt")
	}
	if err = decodeStrict(raw, &rec); err != nil {
		return rec, err
	}
	if _, err = semver(rec.Version); err != nil {
		return rec, err
	}
	if !strings.HasPrefix(rec.Stage, ".stage-") || filepath.Base(rec.Stage) != rec.Stage || strings.ContainsAny(rec.Stage, "/\\") {
		return rec, fmt.Errorf("invalid staging directory")
	}
	expected := m.binaryNames()
	if len(rec.Files) != len(expected) {
		return rec, fmt.Errorf("incomplete update receipt")
	}
	for i, entry := range rec.Files {
		if entry.Name != expected[i] || !validDigest(entry.SHA256) || entry.Size <= 0 || entry.Size > maxArchiveBytes {
			return rec, fmt.Errorf("invalid staged file record")
		}
	}
	return rec, nil
}
func (m *Manager) saveReceipt(rec receipt) error {
	root, err := m.root(true)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".receipt-*.json")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp, filepath.Join(root, "ready.json"))
}
func verifyFiles(dir string, entries []fileRecord) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid update file directory")
	}
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name)
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != entry.Size {
			return fmt.Errorf("staged binary is missing, modified or not regular: %s", entry.Name)
		}
		file, err := os.Open(full)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, io.LimitReader(file, entry.Size+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), entry.SHA256) {
			return fmt.Errorf("binary checksum mismatch: %s", entry.Name)
		}
	}
	return nil
}
func (m *Manager) verifyStage(rec receipt) error {
	root, err := m.root(false)
	if err != nil {
		return err
	}
	return verifyFiles(filepath.Join(root, rec.Stage), rec.Files)
}
func (m *Manager) verifyInstalled(rec receipt) error { return verifyFiles(m.Dir, rec.Files) }

func (m *Manager) Download(ctx context.Context) (State, error) {
	releaseLock, err := m.lock()
	if err != nil {
		return State{}, err
	}
	defer releaseLock()
	state, asset, downloadURL, err := m.latest(ctx)
	if err != nil {
		return state, err
	}
	if !state.Available {
		return state, nil
	}
	if !state.Supported {
		return state, fmt.Errorf("this release has no verified bundle for %s/%s", m.OS, m.Arch)
	}
	if rec, loadErr := m.loadReceipt(); loadErr == nil && rec.Version == state.LatestVersion {
		if rec.Installed && m.verifyInstalled(rec) == nil {
			state.Status = "installed"
			state.Available = false
			state.RestartRequired = true
			return state, nil
		}
		if !rec.Installed && m.verifyStage(rec) == nil {
			state.Status = "downloaded"
			return state, nil
		}
	}
	root, err := m.root(true)
	if err != nil {
		return state, err
	}
	stage, err := os.MkdirTemp(root, ".stage-")
	if err != nil {
		return state, err
	}
	// stage comes directly from MkdirTemp inside the verified portable root.
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(stage)
		}
	}()
	archivePath := filepath.Join(stage, "bundle.zip")
	if err = m.downloadArchive(ctx, downloadURL, archivePath, asset); err != nil {
		return state, err
	}
	rec := receipt{Version: state.LatestVersion, Stage: filepath.Base(stage)}
	rec.Files, err = m.extractBinaries(archivePath, stage)
	if err != nil {
		return state, err
	}
	if err = os.Remove(archivePath); err != nil {
		return state, err
	}
	if err = m.saveReceipt(rec); err != nil {
		return state, err
	}
	keep = true
	state.Status = "downloaded"
	return state, nil
}
func (m *Manager) downloadArchive(ctx context.Context, raw, destination string, asset Asset) error {
	if m.LatestURL == LatestURL {
		if err := githubURL(raw); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "SuperCli/"+m.CurrentVersion)
	resp, err := m.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bundle download: HTTP %d", resp.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, asset.Size+1))
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if size != asset.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), asset.SHA256) {
		return fmt.Errorf("downloaded bundle size or SHA-256 mismatch")
	}
	return nil
}
func (m *Manager) extractBinaries(archivePath, stage string) ([]fileRecord, error) {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	wanted := m.binaryNames()
	found := map[string]fileRecord{}
	var total int64
	for _, entry := range archive.File {
		name := entry.Name
		if name == "" || strings.ContainsRune(name, '\\') || strings.HasPrefix(name, "/") || path.Clean(name) == ".." || strings.HasPrefix(path.Clean(name), "../") || entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe archive entry")
		}
		selected := false
		for _, binary := range wanted {
			if name == binary {
				selected = true
			}
		}
		if !selected {
			continue
		}
		if _, duplicate := found[name]; duplicate {
			return nil, fmt.Errorf("duplicate binary in bundle")
		}
		if !entry.Mode().IsRegular() || entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > uint64(maxArchiveBytes) {
			return nil, fmt.Errorf("invalid binary in bundle")
		}
		total += int64(entry.UncompressedSize64)
		if total > maxArchiveBytes {
			return nil, fmt.Errorf("expanded bundle exceeds size limit")
		}
		input, err := entry.Open()
		if err != nil {
			return nil, err
		}
		output, err := os.OpenFile(filepath.Join(stage, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if err != nil {
			input.Close()
			return nil, err
		}
		hash := sha256.New()
		size, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, int64(entry.UncompressedSize64)+1))
		if copyErr == nil {
			copyErr = output.Sync()
		}
		outputErr := output.Close()
		inputErr := input.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if outputErr != nil {
			return nil, outputErr
		}
		if inputErr != nil {
			return nil, inputErr
		}
		if size != int64(entry.UncompressedSize64) {
			return nil, fmt.Errorf("extracted binary size differs")
		}
		found[name] = fileRecord{Name: name, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}
	}
	if len(found) != len(wanted) {
		return nil, fmt.Errorf("bundle must contain both SuperCli binaries")
	}
	records := make([]fileRecord, 0, len(wanted))
	for _, name := range wanted {
		records = append(records, found[name])
	}
	return records, nil
}

func (m *Manager) Install(ctx context.Context) (State, error) {
	state := State{CurrentVersion: m.CurrentVersion, Status: "downloaded"}
	releaseLock, err := m.lock()
	if err != nil {
		return state, err
	}
	defer releaseLock()
	rec, err := m.loadReceipt()
	if err != nil {
		return state, fmt.Errorf("no verified download is ready: %w", err)
	}
	state.LatestVersion = rec.Version
	isNew, err := newer(rec.Version, m.CurrentVersion)
	if err != nil {
		return state, err
	}
	if !isNew {
		state.Status = "current"
		return state, nil
	}
	if rec.Installed {
		if err = m.verifyInstalled(rec); err != nil {
			return state, err
		}
		state.Status = "installed"
		state.RestartRequired = true
		return state, nil
	}
	if err = m.verifyStage(rec); err != nil {
		return state, err
	}
	if err = ctx.Err(); err != nil {
		return state, err
	}
	root, err := m.root(false)
	if err != nil {
		return state, err
	}
	backup, err := os.MkdirTemp(root, "backup-"+rec.Version+"-")
	if err != nil {
		return state, err
	}
	stage := filepath.Join(root, rec.Stage)
	// Only these two validated names can be replaced. Data/config are untouched.
	var moved, installed []string
	rollback := func(cause error) error {
		var failures []error
		for i := len(installed) - 1; i >= 0; i-- {
			name := installed[i]
			if err := os.Rename(filepath.Join(m.Dir, name), filepath.Join(stage, name)); err != nil {
				failures = append(failures, err)
			}
		}
		for i := len(moved) - 1; i >= 0; i-- {
			name := moved[i]
			if err := os.Rename(filepath.Join(backup, name), filepath.Join(m.Dir, name)); err != nil {
				failures = append(failures, err)
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("install failed; recover executable backups from %s: %w", backup, errors.Join(append([]error{cause}, failures...)...))
		}
		return fmt.Errorf("install failed; previous executables restored: %w", cause)
	}
	for _, record := range rec.Files {
		full := filepath.Join(m.Dir, record.Name)
		info, statErr := os.Lstat(full)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return state, rollback(statErr)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return state, rollback(fmt.Errorf("installed binary is not regular"))
		}
		if err = os.Rename(full, filepath.Join(backup, record.Name)); err != nil {
			return state, rollback(err)
		}
		moved = append(moved, record.Name)
	}
	for _, record := range rec.Files {
		if err = os.Rename(filepath.Join(stage, record.Name), filepath.Join(m.Dir, record.Name)); err != nil {
			return state, rollback(err)
		}
		installed = append(installed, record.Name)
	}
	rec.Installed = true
	if err = m.saveReceipt(rec); err != nil {
		return state, rollback(err)
	}
	state.Supported = true
	state.Status = "installed"
	state.RestartRequired = true
	return state, nil
}
