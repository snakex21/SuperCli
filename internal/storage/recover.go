package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Damaged database recovery (1.0.39). A corrupted SQLite file used to fail
// the app's startup with a raw "database disk image is malformed" and no way
// forward. Now the damaged file (with its -wal/-shm) is moved aside under a
// ".corrupt-<time>" name, never deleted, a fresh database is started, and
// the host reports where the copy is. A locked database (another process
// holds it) is never moved: the error says what to do instead.

// Recovery records one database that was moved aside at startup.
type Recovery struct {
	File   string    `json:"file"`   // the database path that was damaged
	Backup string    `json:"backup"` // where the damaged copy now lives
	Reason string    `json:"reason"` // the error that showed the damage
	At     time.Time `json:"at"`
}

var (
	recoveriesMu sync.Mutex
	recoveries   []Recovery
)

// Recoveries returns the databases moved aside in this process.
func Recoveries() []Recovery {
	recoveriesMu.Lock()
	defer recoveriesMu.Unlock()
	return append([]Recovery(nil), recoveries...)
}

// IsCorruptDB reports an error that means the file is not a usable database.
func IsCorruptDB(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"malformed", "not a database", "sqlite_corrupt", "sqlite_notadb", "file is encrypted"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// IsLockedDB reports a database held by another process or connection.
func IsLockedDB(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy")
}

// LockedDBError explains a locked database in words a user can act on.
func LockedDBError(path string, err error) error {
	return fmt.Errorf("database %s is locked by another process (is another SuperCli window or tool using this data folder?); close it and start again: %w", path, err)
}

// QuarantineDB moves a damaged database and its -wal/-shm files aside and
// records the recovery. Nothing is deleted.
func QuarantineDB(path string, reason error) (Recovery, error) {
	stamp := time.Now().UTC().Format("20060102-150405")
	backup := path + ".corrupt-" + stamp
	for i := 1; fileExists(backup); i++ {
		backup = fmt.Sprintf("%s.corrupt-%s-%d", path, stamp, i)
	}
	if err := os.Rename(path, backup); err != nil {
		return Recovery{}, fmt.Errorf("move damaged database aside: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if fileExists(path + suffix) {
			if err := os.Rename(path+suffix, backup+suffix); err != nil {
				return Recovery{}, fmt.Errorf("move damaged database %s aside: %w", suffix, err)
			}
		}
	}
	rec := Recovery{File: path, Backup: backup, At: time.Now()}
	if reason != nil {
		rec.Reason = reason.Error()
	}
	recoveriesMu.Lock()
	recoveries = append(recoveries, rec)
	recoveriesMu.Unlock()
	return rec, nil
}

// OpenRecovering runs open; when it fails because the file is damaged, the
// file is moved aside and open runs once more on a fresh database. A locked
// database gets a clear error and is left in place.
func OpenRecovering[T any](path string, open func() (T, error)) (T, error) {
	var zero T
	// A file that is not SQLite at all is moved aside before SQLite touches
	// it, so its -wal/-shm are kept exactly as they were.
	if bad := notSQLiteHeader(path); bad != nil {
		if _, qerr := QuarantineDB(path, bad); qerr != nil {
			return zero, errors.Join(bad, qerr)
		}
	}
	v, err := open()
	if err == nil {
		return v, nil
	}
	if IsLockedDB(err) && !IsCorruptDB(err) {
		return zero, LockedDBError(path, err)
	}
	if !IsCorruptDB(err) || !fileExists(path) {
		return zero, err
	}
	if _, qerr := QuarantineDB(path, err); qerr != nil {
		return zero, errors.Join(err, qerr)
	}
	return open()
}

// notSQLiteHeader returns an error when path exists, is non-empty and does
// not start with the SQLite file header.
func notSQLiteHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	header := make([]byte, 16)
	n, _ := io.ReadFull(f, header)
	if n == 0 {
		return nil // empty: SQLite initializes it
	}
	if n < 16 || string(header) != "SQLite format 3\x00" {
		return fmt.Errorf("file is not a database (bad header)")
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
