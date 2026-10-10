package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const mediaDeleteNamespace = ".session-media-delete"
const maxMediaDeleteRecoveryEntries = 1024
const maxMediaDeleteIntentBytes = 1024

var ErrMediaDeleteRecovery = errors.New("session media delete recovery requires attention")

type mediaDeleteIntent struct {
	Format       int    `json:"format"`
	OperationID  string `json:"operation_id"`
	OriginalHash string `json:"original_hash"`
}

func validMediaDeleteHash(value string) bool {
	if len(value) != 32 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (s *Store) mediaDeletePaths(intent mediaDeleteIntent) (quarantinedSessionMedia, error) {
	if intent.Format != 1 || !validMediaDeleteHash(intent.OperationID) || !validMediaDeleteHash(intent.OriginalHash) {
		return quarantinedSessionMedia{}, fmt.Errorf("%w: invalid intent identity", ErrMediaDeleteRecovery)
	}
	root := filepath.Join(s.root, mediaDeleteNamespace)
	media := quarantinedSessionMedia{
		original:  filepath.Join(s.root, sessionMediaDirName, intent.OriginalHash),
		directory: filepath.Join(root, intent.OperationID+".media"),
		journal:   filepath.Join(root, intent.OperationID+".json"),
		intent:    intent,
	}
	for _, path := range []string{root, filepath.Dir(media.original), media.original, media.directory, media.journal} {
		if err := checkMediaDeletePath(path); err != nil {
			return quarantinedSessionMedia{}, err
		}
	}
	for _, path := range []string{root, filepath.Dir(media.original), media.directory} {
		if info, err := os.Lstat(path); err == nil && !info.IsDir() {
			return quarantinedSessionMedia{}, fmt.Errorf("%w: expected media directory %s", ErrMediaDeleteRecovery, path)
		} else if err != nil && !os.IsNotExist(err) {
			return quarantinedSessionMedia{}, err
		}
	}
	return media, nil
}

// Namespace, parents and leaves may not redirect an exact recovery operation.
func checkMediaDeletePath(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return fmt.Errorf("%w: unexpected media delete path %s", ErrMediaDeleteRecovery, path)
	}
	return nil
}

// HasMediaDeleteRecovery performs one lookup only. Engines call it once when
// opening a cached Store, never for each model turn, greeting or transcript row.
func (s *Store) HasMediaDeleteRecovery() (bool, error) {
	if s == nil || s.root == "" {
		return false, errors.New("session media store root is empty")
	}
	info, err := os.Lstat(filepath.Join(s.root, mediaDeleteNamespace))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%w: media delete namespace is not a plain directory", ErrMediaDeleteRecovery)
	}
	return true, nil
}

func (s *Store) ensureMediaDeleteSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS session_media_deletions (
		operation_id TEXT PRIMARY KEY,
		original_hash TEXT NOT NULL,
		quarantine_name TEXT NOT NULL
	)`)
	return err
}

// prepareMediaDelete records a SQL commit marker in the deletion transaction
// and publishes a synced portable intent BEFORE its caller may rename bytes.
// The table has no session FK: the committed deletion must retain the marker.
func (s *Store) prepareMediaDelete(ctx context.Context, tx *sql.Tx, originalHash string) (quarantinedSessionMedia, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return quarantinedSessionMedia{}, err
	}
	media, err := s.mediaDeletePaths(mediaDeleteIntent{Format: 1, OperationID: hex.EncodeToString(id[:]), OriginalHash: originalHash})
	if err != nil {
		return quarantinedSessionMedia{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO session_media_deletions(operation_id, original_hash, quarantine_name) VALUES(?,?,?)`, media.intent.OperationID, originalHash, filepath.Base(media.directory)); err != nil {
		return quarantinedSessionMedia{}, err
	}
	namespace := filepath.Dir(media.journal)
	if err := os.MkdirAll(namespace, 0o700); err != nil {
		return media, err
	}
	if err := syncMediaDeleteDir(s.root); err != nil {
		return media, err
	}
	data, err := json.Marshal(media.intent)
	if err != nil {
		return media, err
	}
	temporary := strings.TrimSuffix(media.journal, ".json") + ".tmp"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return media, err
	}
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err == nil {
		err = renameMediaDelete(temporary, media.journal)
	}
	if err != nil {
		// This exact staging file was exclusively created by this invocation.
		_ = os.Remove(temporary)
	}
	return media, err
}

// RecoverMediaDeletes requires the same StoreGate used by checkpoint commit
// and transcript deletion. It scans only the reserved namespace, in one
// bounded read; unknown identities, links, overflow and occupied restore paths
// fail closed. SQLite commit markers, not SID reuse or age, decide recovery.
func (s *Store) RecoverMediaDeletes(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("session.Store.RecoverMediaDeletes: nil store")
	}
	pending, err := s.HasMediaDeleteRecovery()
	if err != nil || !pending {
		return err
	}
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	namespace := filepath.Join(s.root, mediaDeleteNamespace)
	dir, err := os.Open(namespace)
	if os.IsNotExist(err) {
		return nil // another committed cleanup completed before admission
	}
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(maxMediaDeleteRecoveryEntries + 1)
	closeErr := dir.Close()
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if err := errors.Join(closeErr, readErr); err != nil {
		return err
	}
	if len(entries) > maxMediaDeleteRecoveryEntries {
		return fmt.Errorf("%w: too many pending media-delete entries", ErrMediaDeleteRecovery)
	}
	if len(entries) == 0 {
		return removeEmptyMediaDeleteNamespace(namespace)
	}
	var result error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		name := entry.Name()
		ext := filepath.Ext(name)
		id := strings.TrimSuffix(name, ext)
		if !validMediaDeleteHash(id) || (ext != ".json" && ext != ".media" && ext != ".tmp") {
			result = errors.Join(result, fmt.Errorf("%w: unknown media delete entry %s", ErrMediaDeleteRecovery, name))
			continue
		}
		if ext == ".media" {
			if _, err := os.Lstat(filepath.Join(namespace, id+".json")); err != nil && !os.IsNotExist(err) {
				result = errors.Join(result, err)
			} else if os.IsNotExist(err) {
				if _, mediaErr := os.Lstat(filepath.Join(namespace, name)); !os.IsNotExist(mediaErr) {
					result = errors.Join(result, fmt.Errorf("%w: media without its durable intent %s", ErrMediaDeleteRecovery, name))
				}
			}
			continue
		}
		if ext == ".tmp" {
			// Rename requires a published .json first. A lone regular .tmp
			// therefore contains metadata only, never detached media.
			if err := s.retireMediaDeleteStaging(namespace, id); err != nil {
				result = errors.Join(result, err)
			}
			continue
		}
		media, err := s.readMediaDeleteIntent(filepath.Join(namespace, name), id)
		if os.IsNotExist(err) {
			continue // concurrent committed cleanup retired the exact intent
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		committed, err := s.mediaDeleteCommitted(ctx, media)
		if err == nil {
			if committed {
				err = s.finishCommittedMediaDelete(ctx, media)
			} else {
				err = restoreQuarantinedSessionMedia([]quarantinedSessionMedia{media})
			}
		}
		result = errors.Join(result, err)
	}
	return errors.Join(result, removeEmptyMediaDeleteNamespace(namespace))
}

func (s *Store) readMediaDeleteIntent(path, id string) (quarantinedSessionMedia, error) {
	if err := checkMediaDeletePath(path); err != nil {
		return quarantinedSessionMedia{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return quarantinedSessionMedia{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxMediaDeleteIntentBytes+1))
	err = errors.Join(readErr, file.Close())
	if err != nil {
		return quarantinedSessionMedia{}, err
	}
	var intent mediaDeleteIntent
	if len(data) > maxMediaDeleteIntentBytes || json.Unmarshal(data, &intent) != nil || intent.OperationID != id {
		return quarantinedSessionMedia{}, fmt.Errorf("%w: malformed intent %s", ErrMediaDeleteRecovery, id)
	}
	return s.mediaDeletePaths(intent)
}

func (s *Store) mediaDeleteCommitted(ctx context.Context, media quarantinedSessionMedia) (bool, error) {
	var hash, name string
	err := s.db.QueryRowContext(ctx, `SELECT original_hash, quarantine_name FROM session_media_deletions WHERE operation_id = ?`, media.intent.OperationID).Scan(&hash, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		// A published intent implies its schema existed before rename. Do not
		// recreate a missing/damaged table and guess that a commit was absent.
		return false, fmt.Errorf("%w: cannot read commit marker: %w", ErrMediaDeleteRecovery, err)
	}
	if err == nil && (hash != media.intent.OriginalHash || name != filepath.Base(media.directory)) {
		return false, fmt.Errorf("%w: commit marker disagrees with its intent", ErrMediaDeleteRecovery)
	}
	return err == nil, err
}

func (s *Store) finishCommittedMediaDelete(ctx context.Context, media quarantinedSessionMedia) error {
	return s.finishCommittedMediaDeleteWith(ctx, media, os.RemoveAll)
}

func (s *Store) finishCommittedMediaDeleteWith(ctx context.Context, media quarantinedSessionMedia, remove func(string) error) error {
	info, err := os.Lstat(media.directory)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: captured media is not a plain directory", ErrMediaDeleteRecovery)
		}
		if committed, err := s.mediaDeleteCommitted(ctx, media); err != nil || !committed {
			return errors.Join(err, fmt.Errorf("%w: captured media has no matching committed marker", ErrMediaDeleteRecovery))
		}
	}
	if err := remove(media.directory); err != nil {
		return err // retain marker and intent after partial cleanup
	}
	if err := syncMediaDeleteDir(filepath.Dir(media.directory)); err != nil && !os.IsNotExist(err) {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM session_media_deletions WHERE operation_id = ? AND original_hash = ? AND quarantine_name = ?`, media.intent.OperationID, media.intent.OriginalHash, filepath.Base(media.directory))
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		// Idempotent retirement must not erase an unknown changed marker.
		if committed, err := s.mediaDeleteCommitted(ctx, media); err != nil || committed {
			return errors.Join(err, fmt.Errorf("%w: marker retirement did not complete", ErrMediaDeleteRecovery))
		}
	}
	return retireMediaDeleteJournal(media)
}

func retireMediaDeleteJournal(media quarantinedSessionMedia) error {
	if media.journal == "" {
		return nil // private rollback fixtures without a published intent
	}
	if err := os.Remove(media.journal); err != nil && !os.IsNotExist(err) {
		return err
	}
	namespace := filepath.Dir(media.journal)
	if err := syncMediaDeleteDir(namespace); err != nil && !os.IsNotExist(err) {
		return err
	}
	return removeEmptyMediaDeleteNamespace(namespace)
}

func removeEmptyMediaDeleteNamespace(namespace string) error {
	err := os.Remove(namespace) // never recursively remove unknown entries
	if err == nil {
		return syncMediaDeleteDir(filepath.Dir(namespace))
	}
	if os.IsNotExist(err) {
		return nil
	}
	dir, openErr := os.Open(namespace)
	if openErr != nil {
		return errors.Join(err, openErr)
	}
	entries, readErr := dir.ReadDir(1)
	closeErr := dir.Close()
	if len(entries) != 0 && readErr == nil && closeErr == nil {
		return nil // another exact operation still owns the namespace
	}
	return errors.Join(err, readErr, closeErr)
}

func (s *Store) retireMediaDeleteStaging(namespace, id string) error {
	for _, ext := range []string{".media", ".json"} {
		if _, err := os.Lstat(filepath.Join(namespace, id+ext)); !os.IsNotExist(err) {
			return fmt.Errorf("%w: staging entry has a published neighbor", ErrMediaDeleteRecovery)
		}
	}
	path := filepath.Join(namespace, id+".tmp")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxMediaDeleteIntentBytes {
		return fmt.Errorf("%w: invalid staging entry", ErrMediaDeleteRecovery)
	}
	return os.Remove(path)
}
