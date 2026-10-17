package session

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"supercli/internal/system/config"
)

var housekeepingSnapshotName = regexp.MustCompile(`^(clipboard|screen|desktop|window|qmp|browser)-[0-9]+\.(png|jpg|gif|webp|bmp|tiff|bin)$`)
var housekeepingSnapshotRef = regexp.MustCompile(`(?:clipboard|screen|desktop|window|qmp|browser)-[0-9]+\.(?:png|jpg|gif|webp|bmp|tiff|bin)`)

// Search all durable transcript/output/attachment/queue fields. Match filenames
// rather than only snapshot: URLs: results also contain absolute paths, JSON
// escapes and preview_path. False positives only keep extra files. No image
// hashing, no content-addressed copy assumption, no deletion of unknown names.
func (s *Store) snapshotReferences(ctx context.Context) (map[string]bool, error) {
	refs := map[string]bool{}
	queries := []string{
		`SELECT content, parts_json, tool_calls_json FROM messages`,
		`SELECT content FROM tool_outputs`,
		`SELECT paths_json FROM message_attachments`,
		`SELECT prompt, attachments_json FROM prompt_queue`,
		`SELECT phases_json, file_changes_json, tool_diag_json FROM session_turns`,
	}
	for _, query := range queries {
		rows, err := s.db.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, err
			}
			for _, value := range values {
				var text string
				switch v := value.(type) {
				case string:
					text = v
				case []byte:
					text = string(v)
				}
				for _, name := range housekeepingSnapshotRef.FindAllString(strings.ToLower(text), -1) {
					refs[name] = true
				}
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func (s *Store) PruneSnapshotFiles(ctx context.Context, c config.HousekeepingConfig, now time.Time) (int, error) {
	dir := filepath.Join(s.root, ".supercli", "snapshots")
	// Never follow a replaced directory, symlink or Windows junction.
	for _, path := range []string{s.root, filepath.Join(s.root, ".supercli"), dir} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return 0, nil
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		name string
		info os.FileInfo
	}
	var files []candidate
	var total int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !housekeepingSnapshotName.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		total += info.Size()
		files = append(files, candidate{entry.Name(), info})
	}
	eligible := false
	for _, file := range files {
		age := now.Sub(file.info.ModTime())
		if age >= 7*24*time.Hour && (age >= days(c.SnapshotMaxAgeDays) || total > c.SnapshotMaxBytes) {
			eligible = true
			break
		}
	}
	if !eligible {
		return 0, nil
	}
	refs, err := s.snapshotReferences(ctx)
	if err != nil {
		return 0, err
	} // Incomplete reference census means no deletion.
	sort.Slice(files, func(i, j int) bool { return files[i].info.ModTime().Before(files[j].info.ModTime()) })
	removed := 0
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		age := now.Sub(file.info.ModTime())
		// Age expiry OR size pressure, but always a seven-day in-flight grace.
		if refs[file.name] || age < 7*24*time.Hour || (age < days(c.SnapshotMaxAgeDays) && total <= c.SnapshotMaxBytes) {
			continue
		}
		path := filepath.Join(dir, file.name)
		current, err := os.Lstat(path)
		if err != nil {
			return removed, err
		}
		if !current.Mode().IsRegular() || !os.SameFile(file.info, current) || current.Size() != file.info.Size() || !current.ModTime().Equal(file.info.ModTime()) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		total -= current.Size()
		removed++
		log.Printf("housekeeping: deleted unreferenced snapshot path=%s bytes=%d age=%s", path, current.Size(), age)
	}
	return removed, nil
}
