package webgui

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/account/fx"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/storage/memory"
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
		if name == "data/project-cleanup.json" && file.UncompressedSize64 > 4096 {
			return dataBackupMeta{}, errors.New("backup project checkpoint preference exceeds 4096 bytes")
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
	if err := validateProjectCleanupBackup(filepath.Join(stage, "data")); err != nil {
		return dataBackupMeta{}, fmt.Errorf("backup project checkpoint preference: %w", err)
	}
	if err := validateCurrencyRatesBackup(filepath.Join(stage, "data")); err != nil {
		return dataBackupMeta{}, fmt.Errorf("backup currency rates: %w", err)
	}
	return manifest, nil
}

func validateProjectCleanupBackup(dataRoot string) error {
	info, err := os.Lstat(filepath.Join(dataRoot, "project-cleanup.json"))
	if os.IsNotExist(err) {
		return nil // Older archives have no preference; the default keeps history.
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("project checkpoint preference must be a regular file")
	}
	_, err = memory.LoadProjectCheckpointCleanup(dataRoot)
	return err
}

// A rate backup is one self-contained SQLite snapshot, never a live DB plus
// sidecars. Revalidate before applying a pending import as well as on upload.
func validateCurrencyRatesBackup(dataRoot string) error {
	filename := filepath.Join(dataRoot, dataCurrencyRatesFile)
	info, err := os.Lstat(filename)
	if os.IsNotExist(err) {
		return nil // Older archives do not contain historical exchange rates.
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("currency rate snapshot must be a regular file")
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(filename)}
	if !strings.HasPrefix(uri.Path, "/") {
		uri.Path = "/" + uri.Path
	}
	uri.RawQuery = "mode=ro&immutable=1&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return fmt.Errorf("invalid currency rate database: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("invalid currency rate database: %s", integrity)
	}
	var validSchema bool
	if err := db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM sqlite_schema WHERE name='currency_days' AND type='table')
		AND EXISTS(SELECT 1 FROM pragma_table_info('currency_days') WHERE name='id' AND upper(type)='INTEGER' AND pk=1)
		AND EXISTS(SELECT 1 FROM pragma_index_list('currency_days') l WHERE l."unique"=1 AND l.partial=0
			AND (SELECT COUNT(*) FROM pragma_index_info(l.name))=1
			AND (SELECT name FROM pragma_index_info(l.name) WHERE seqno=0)='usage_day')`).Scan(&validSchema); err != nil {
		return fmt.Errorf("invalid currency rate schema: %w", err)
	}
	if !validSchema {
		return errors.New("currency rate schema requires a table, ID primary key and unique usage day")
	}
	rows, err := db.QueryContext(ctx, "SELECT id, usage_day, publication_day, source, multipliers FROM currency_days ORDER BY id")
	if err != nil {
		return fmt.Errorf("invalid currency rate schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var day, publication, source, raw string
		if err := rows.Scan(&id, &day, &publication, &source, &raw); err != nil {
			return err
		}
		if id <= 0 {
			return errors.New("currency rate snapshot contains an invalid row ID")
		}
		var multipliers map[string]float64
		if err := json.Unmarshal([]byte(raw), &multipliers); err != nil {
			return fmt.Errorf("invalid currency rate multipliers for %s: %w", day, err)
		}
		if err := fx.ValidateSnapshot(day, publication, source, multipliers); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return validateCurrencyQuoteBackup(ctx, db)
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
	case "sessions.db", "memory.db", "supercli.db", dataCurrencyRatesFile, "projects.json", "workspace.json", "webgui-settings.json", "project-cleanup.json", llmprompt.UserInstructionsFile, folderIndexFile, folderIndexCacheFile, "schedules.json":
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
