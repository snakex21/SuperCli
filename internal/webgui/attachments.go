package webgui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"supercli/internal/tools/sandbox"
)

const (
	maxChatAttachments      = 8
	maxChatAttachmentBytes  = 32 << 20
	maxChatAttachmentsBytes = 64 << 20
)

// buildAttachmentAddon validates user-selected files through the same sandbox
// used by file and Office tools. The returned block is sent once with the next
// user message but is not persisted as visible transcript content.
func buildAttachmentAddon(home string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}
	if len(paths) > maxChatAttachments {
		return "", fmt.Errorf("too many files: %d (maximum %d)", len(paths), maxChatAttachments)
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	seen := make(map[string]struct{}, len(paths))
	selected := make([]string, 0, len(paths))
	for _, raw := range paths {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		resolved, err := sandbox.ResolveSafe(absHome, raw)
		if err != nil {
			return "", fmt.Errorf("%q is outside the active workspace or blocked", raw)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", fmt.Errorf("%q: %w", raw, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%q is not a regular file", raw)
		}
		key := resolved
		if filepath.Separator == '\\' {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		display := resolved
		if sandbox.IsUnder(absHome, resolved) {
			if rel, relErr := filepath.Rel(absHome, resolved); relErr == nil {
				display = rel
			}
		}
		selected = append(selected, display)
	}
	if len(selected) == 0 {
		return "", nil
	}
	var out strings.Builder
	out.WriteString("[attached_files]\nThe user selected these files for this request. Supported image pixels are already included directly in this user message; inspect them without calling read_image. Use read_pdf for PDF, read_docx for DOCX, read_xlsx for XLSX, read_zip for ZIP, and read_lines/read_context for CSV, text, or code. If a named reader is not visible yet, call tool_search for that exact tool name first:\n")
	for _, path := range selected {
		out.WriteString("- ")
		out.WriteString(strconv.Quote(path))
		out.WriteByte('\n')
	}
	out.WriteString("[/attached_files]")
	return out.String(), nil
}

// stagePickedAttachments validates an explicit desktop file-picker selection.
// A sandboxed SuperCli run copies outside files into its hidden attachment
// directory. An allow-all office profile keeps the original path so document
// edits affect the file the user actually selected instead of a staged copy.
func stagePickedAttachments(home string, paths []string) ([]string, error) {
	if len(paths) > maxChatAttachments {
		return nil, fmt.Errorf("too many files: %d (maximum %d)", len(paths), maxChatAttachments)
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	var total int64
	staged := make([]string, 0, len(paths))
	created := make([]string, 0, len(paths))
	fail := func(err error) ([]string, error) {
		for _, path := range created {
			_ = os.Remove(path)
		}
		return nil, err
	}
	for _, raw := range paths {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		source, err := filepath.Abs(raw)
		if err != nil {
			return fail(fmt.Errorf("resolve %q: %w", raw, err))
		}
		info, err := os.Stat(source)
		if err != nil {
			return fail(fmt.Errorf("%q: %w", raw, err))
		}
		if !info.Mode().IsRegular() {
			return fail(fmt.Errorf("%q is not a regular file", raw))
		}
		if info.Size() > maxChatAttachmentBytes {
			return fail(fmt.Errorf("%q is too large: %d bytes (maximum %d)", filepath.Base(raw), info.Size(), maxChatAttachmentBytes))
		}
		total += info.Size()
		if total > maxChatAttachmentsBytes {
			return fail(fmt.Errorf("attachments are too large in total: %d bytes (maximum %d)", total, maxChatAttachmentsBytes))
		}
		if sandbox.IsUnsandboxed() {
			if _, err := sandbox.ResolveSafe(absHome, source); err != nil {
				return fail(fmt.Errorf("%q is in a blocked system location", raw))
			}
			staged = append(staged, source)
			continue
		}
		if sandbox.IsUnder(absHome, source) {
			staged = append(staged, source)
			continue
		}
		root := filepath.Join(absHome, ".supercli", "attachments")
		if err := os.MkdirAll(root, 0o700); err != nil {
			return fail(fmt.Errorf("create attachment directory: %w", err))
		}
		in, err := os.Open(source)
		if err != nil {
			return fail(fmt.Errorf("open %q: %w", raw, err))
		}
		targetRoot := filepath.Join(root, randomDataID())
		if err := os.MkdirAll(targetRoot, 0o700); err != nil {
			_ = in.Close()
			return fail(fmt.Errorf("create attachment stage: %w", err))
		}
		target := filepath.Join(targetRoot, safeAttachmentName(filepath.Base(source)))
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = in.Close()
			_ = os.Remove(targetRoot)
			return fail(fmt.Errorf("stage %q: %w", raw, err))
		}
		written, copyErr := io.Copy(out, io.LimitReader(in, maxChatAttachmentBytes+1))
		closeErr := out.Close()
		inErr := in.Close()
		if written > maxChatAttachmentBytes {
			copyErr = fmt.Errorf("file grew beyond the %d-byte limit while copying", maxChatAttachmentBytes)
		}
		if copyErr != nil || closeErr != nil || inErr != nil {
			_ = os.Remove(target)
			_ = os.Remove(targetRoot)
			return fail(fmt.Errorf("stage %q: %w", raw, errors.Join(copyErr, closeErr, inErr)))
		}
		created = append(created, target)
		staged = append(staged, target)
	}
	return staged, nil
}

func safeAttachmentName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`\/:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		return "attachment"
	}
	return name
}
