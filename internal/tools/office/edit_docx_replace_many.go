package office

import (
	"bytes"
	"fmt"
)

func (t *EditDocxTool) doReplaceMany(full string, p editDocxArgs) (Result, error) {
	if len(p.Replacements) == 0 {
		err := fmt.Errorf("edit_docx replace_many: 'replacements' must contain at least one change")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	parts := map[string][]byte{docxDocumentEntry: doc}
	if p.IncludeHeaders || p.IncludeFooters {
		storyNames, partErr := listDocxStoryParts(full, p.IncludeHeaders, p.IncludeFooters, t.MaxDocxBytes)
		if partErr != nil {
			return Result{Err: fmt.Errorf("edit_docx replace_many: %w", partErr)}, partErr
		}
		for _, name := range storyNames {
			part, readErr := readZipEntry(full, name, t.MaxDocxBytes)
			if readErr != nil {
				return Result{Err: fmt.Errorf("edit_docx replace_many: %s: %w", name, readErr)}, readErr
			}
			parts[name] = part
		}
	}
	originals := make(map[string][]byte, len(parts))
	for name, data := range parts {
		originals[name] = data
	}
	total := 0
	for i, replacement := range p.Replacements {
		if replacement.Find == "" {
			err := fmt.Errorf("edit_docx replace_many: replacement %d has an empty find string", i+1)
			return Result{Err: err}, err
		}
		matched := 0
		for name, data := range parts {
			changed, count, replaceErr := docxReplaceAll(data, replacement.Find, replacement.Replace)
			if replaceErr != nil {
				return Result{Err: fmt.Errorf("edit_docx replace_many: replacement %d in %s: %w", i+1, name, replaceErr)}, replaceErr
			}
			if count > 0 {
				parts[name] = changed
				matched += count
			}
		}
		if replacement.ExpectedCount > 0 && matched != replacement.ExpectedCount {
			err := fmt.Errorf("edit_docx replace_many: replacement %d expected %d occurrence(s) of %q but found %d; nothing was written", i+1, replacement.ExpectedCount, replacement.Find, matched)
			return Result{Err: err}, err
		}
		total += matched
	}
	updates := make(map[string][]byte)
	for name, data := range parts {
		if !bytes.Equal(data, originals[name]) {
			updates[name] = data
		}
	}
	if len(updates) == 0 {
		return Result{Text: fmt.Sprintf("None of the %d requested replacements matched in %s. Nothing was changed (no backup created).", len(p.Replacements), full)}, nil
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would apply %d replacement(s) from %d requested changes across %d document part(s) in %s. Nothing was written.", total, len(p.Replacements), len(updates), full)}, nil
	}
	backup, err := editZipEntriesInPlace(full, updates)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx replace_many: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Applied %d replacement(s) from %d requested changes across %d document part(s) in %s. Backup of the original saved as %s.", total, len(p.Replacements), len(updates), full, backup)}, nil
}
