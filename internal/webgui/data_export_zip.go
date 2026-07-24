package webgui

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	llmprompt "supercli/internal/llm/prompt"
)

func writeZip(dst io.Writer, root string) error {
	zw := zip.NewWriter(dst)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	})
	return errors.Join(err, zw.Close())
}

func readDataBackupMeta(archivePath string) (dataBackupMeta, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return dataBackupMeta{}, fmt.Errorf("invalid backup archive: %w", err)
	}
	defer zr.Close()
	for _, file := range zr.File {
		if filepath.ToSlash(file.Name) != dataBackupManifest || file.Mode()&os.ModeSymlink != 0 || file.UncompressedSize64 > 64<<10 {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return dataBackupMeta{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, (64<<10)+1))
		closeErr := rc.Close()
		if readErr != nil || closeErr != nil {
			return dataBackupMeta{}, errors.Join(readErr, closeErr)
		}
		if len(data) > 64<<10 {
			return dataBackupMeta{}, errors.New("backup manifest is too large")
		}
		var manifest dataBackupMeta
		if err := json.Unmarshal(data, &manifest); err != nil || manifest.Format != dataBackupFormat || manifest.App != "SuperCli" {
			return dataBackupMeta{}, errors.New("unsupported backup format")
		}
		return manifest, nil
	}
	return dataBackupMeta{}, errors.New("backup manifest is missing")
}

func extractDataBackup(archivePath, stage string) error {
	_, err := extractDataBackupMode(archivePath, stage, false)
	return err
}

func extractDataBackupMode(archivePath, stage string, allowSecrets bool) (dataBackupMeta, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return dataBackupMeta{}, fmt.Errorf("invalid backup archive: %w", err)
	}
	defer zr.Close()
	if len(zr.File) == 0 || len(zr.File) > maxDataBackupFiles {
		return dataBackupMeta{}, fmt.Errorf("backup contains an invalid number of files")
	}
	var total uint64
	var writtenTotal int64
	for _, file := range zr.File {
		rawName := filepath.ToSlash(file.Name)
		name := path.Clean(rawName)
		if rawName != name || !allowedBackupPathMode(name, allowSecrets) || file.Mode()&os.ModeSymlink != 0 {
			return dataBackupMeta{}, fmt.Errorf("backup contains unsupported path %q", file.Name)
		}
		total += file.UncompressedSize64
		if total > maxDataBackupBytes {
			return dataBackupMeta{}, fmt.Errorf("unpacked backup is too large")
		}
		full := filepath.Join(stage, filepath.FromSlash(name))
		if !pathInside(stage, full) {
			return dataBackupMeta{}, fmt.Errorf("unsafe backup path %q", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(full, 0o700); err != nil {
				return dataBackupMeta{}, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return dataBackupMeta{}, err
		}
		rc, err := file.Open()
		if err != nil {
			return dataBackupMeta{}, err
		}
		out, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			rc.Close()
			return dataBackupMeta{}, err
		}
		remaining := int64(maxDataBackupBytes) - writtenTotal
		written, copyErr := io.Copy(out, io.LimitReader(rc, remaining+1))
		writtenTotal += written
		if written > remaining {
			copyErr = errors.Join(copyErr, errors.New("unpacked backup is too large"))
		}
		err = errors.Join(copyErr, out.Close(), rc.Close())
		if err != nil {
			return dataBackupMeta{}, err
		}
	}
	manifestData, err := os.ReadFile(filepath.Join(stage, dataBackupManifest))
	if err != nil {
		return dataBackupMeta{}, errors.New("backup manifest is missing")
	}
	var manifest dataBackupMeta
	if err := json.Unmarshal(manifestData, &manifest); err != nil || manifest.Format != dataBackupFormat || manifest.App != "SuperCli" || manifest.Secrets != allowSecrets {
		return dataBackupMeta{}, errors.New("unsupported or unsafe backup format")
	}
	return manifest, nil
}

func allowedBackupPath(name string) bool {
	return allowedBackupPathMode(name, false)
}

func allowedBackupPathMode(name string, allowSecrets bool) bool {
	if name == dataBackupManifest {
		return true
	}
	if !strings.HasPrefix(name, "data/") || strings.Contains(name, "\\") {
		return false
	}
	rel := strings.TrimPrefix(name, "data/")
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		if !safeDataName(part) {
			return false
		}
	}
	switch rel {
	case "sessions.db", "memory.db", "supercli.db", "projects.json", "workspace.json", "webgui-settings.json", llmprompt.UserInstructionsFile, folderIndexFile, folderIndexCacheFile, "schedules.json":
		return true
	}
	if len(parts) >= 2 && (parts[0] == "memory" || parts[0] == "reflect" || parts[0] == "module-sources") {
		return true
	}
	if len(parts) >= 3 && parts[0] == "projects" && safeDataName(parts[1]) {
		if parts[2] == "memory.db" {
			return len(parts) == 3
		}
		if parts[2] == "memory" && len(parts) >= 4 {
			return true
		}
	}
	if !allowSecrets {
		return false
	}
	if rel == "config.toml" || rel == "models.json" || rel == "context_limits.json" || (len(parts) == 1 && isAuthBackupName(rel)) {
		return true
	}
	if len(parts) >= 2 && (parts[0] == "mcp" || parts[0] == "skills" || parts[0] == "tools" || parts[0] == "profiles") {
		return true
	}
	return false
}
