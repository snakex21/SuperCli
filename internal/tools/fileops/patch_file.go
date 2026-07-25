package fileops

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PatchChange is one exact text replacement in a file.
// expected_count defaults to 1 when zero (callers should normalize).
type PatchChange struct {
	Old           string
	New           string
	ExpectedCount int
}

// PatchResult describes a successful PatchFile application.
type PatchResult struct {
	Replacements int
	BeforeHash   string
	AfterHash    string
	Changed      bool
	// Note is a diagnostic addendum for the success message. Empty for an
	// ordinary patch; set only when the result is genuinely surprising and
	// the model cannot see it from hashes (see pureInsertionNote).
	Note string
	// Duplicated reports that the patch was a pure insertion of text the
	// file already contained. The write succeeded, but nothing new exists
	// because of it, so the agent loop must not bank it as progress.
	Duplicated bool
}

// minInsertionNoteLen is the shortest inserted text worth counting. Below it
// the tail is punctuation or a newline, which occurs everywhere in every file:
// the count would be noise, and noise on the success path is exactly what this
// note must not become.
const minInsertionNoteLen = 8

// pureInsertionNote returns a note when a change appended text to its own
// anchor (new starts with old) and that appended text now occurs more than once
// in the file. This is the single fact that distinguishes "I made the edit"
// from "I made the edit for the 23rd time", and the model never had it: a
// successful patch reported changed=true plus two hashes it cannot compare.
// Cost is one substring count, paid only for pure insertions.
func pureInsertionNote(content string, changes []PatchChange) (string, bool) {
	for _, ch := range changes {
		if len(ch.New) <= len(ch.Old) || !strings.HasPrefix(ch.New, ch.Old) {
			continue
		}
		tail := ch.New[len(ch.Old):]
		if len(strings.TrimSpace(tail)) < minInsertionNoteLen {
			continue
		}
		if n := strings.Count(content, tail); n > 1 {
			return fmt.Sprintf(
				"note: pure insertion; the inserted text now occurs %d times in this file", n), true
		}
	}
	return "", false
}

// FileSHA256 returns the hex-encoded SHA-256 of the file contents.
func FileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", FileErr(err, path)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// PatchFile applies exact string replacements to an existing file.
// All changes are validated and applied in memory first; the file is
// written only after every change succeeds (atomic from the caller's
// perspective). baseHash, when non-empty, must match the file's SHA-256
// before any change; otherwise nothing is written.
//
// expected_count of 0 is treated as 1. The number of non-overlapping
// occurrences of Old must match ExpectedCount exactly.
func PatchFile(path string, changes []PatchChange, baseHash string) (PatchResult, error) {
	if path == "" {
		return PatchResult{}, fmt.Errorf("fileops.PatchFile: empty path")
	}
	if len(changes) == 0 {
		return PatchResult{}, fmt.Errorf("fileops.PatchFile: changes is empty")
	}
	release := LockMutationPaths(path)
	defer release()

	data, err := os.ReadFile(path)
	if err != nil {
		return PatchResult{}, FileErr(err, path)
	}
	// Without this the failure is misleading: searching a compressed
	// stream for a sentence found in the document reports "found 0", the
	// model concludes the text is absent and escalates to write_file.
	if err := ensureTextBytes(path, data, false); err != nil {
		return PatchResult{}, err
	}
	beforeHash := sha256.Sum256(data)
	beforeHex := hex.EncodeToString(beforeHash[:])
	if baseHash != "" && !strings.EqualFold(baseHash, beforeHex) {
		return PatchResult{}, fmt.Errorf("fileops.PatchFile: base_hash mismatch (file changed); nothing written")
	}

	content := string(data)
	total := 0
	for i, ch := range changes {
		if ch.Old == "" {
			return PatchResult{}, fmt.Errorf("fileops.PatchFile: change %d: old is empty; nothing written", i)
		}
		want := ch.ExpectedCount
		if want <= 0 {
			want = 1
		}
		got := strings.Count(content, ch.Old)
		if got != want {
			// The bare count is unactionable: the model regenerated the whole
			// patch instead of correcting it. Spend tokens here — on the
			// failure path only — to say what actually differs.
			return PatchResult{}, fmt.Errorf(
				"fileops.PatchFile: change %d: expected %d occurrence(s) of old, found %d; nothing written%s",
				i, want, got, patchFailureHint(content, changes, i, want, got))
		}
		content = strings.Replace(content, ch.Old, ch.New, want)
		total += want
	}

	afterData := []byte(content)
	afterHash := sha256.Sum256(afterData)
	afterHex := hex.EncodeToString(afterHash[:])
	changed := content != string(data)
	if !changed {
		return PatchResult{
			Replacements: total,
			BeforeHash:   beforeHex,
			AfterHash:    afterHex,
			Changed:      false,
		}, nil
	}
	if err := os.WriteFile(path, afterData, 0o644); err != nil {
		return PatchResult{}, FileErr(err, path)
	}
	note, duplicated := pureInsertionNote(content, changes)
	return PatchResult{
		Replacements: total,
		BeforeHash:   beforeHex,
		AfterHash:    afterHex,
		Changed:      true,
		Note:         note,
		Duplicated:   duplicated,
	}, nil
}

// CreateFileExclusive creates a new file with content, creating parent
// directories as needed. It refuses to overwrite an existing path
// (O_CREATE|O_EXCL). On write failure after create, the incomplete file
// is removed.
func CreateFileExclusive(path, content string) error {
	if path == "" {
		return fmt.Errorf("fileops.CreateFileExclusive: empty path")
	}
	release := LockMutationPaths(path)
	defer release()

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return FileErr(err, dir)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("fileops.CreateFileExclusive: already exists %s", path)
		}
		return FileErr(err, path)
	}
	_, werr := f.Write([]byte(content))
	cerr := f.Close()
	if werr != nil {
		_ = os.Remove(path)
		return FileErr(werr, path)
	}
	if cerr != nil {
		_ = os.Remove(path)
		return FileErr(cerr, path)
	}
	return nil
}
