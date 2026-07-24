package fileops

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// The line-oriented tools (read_lines, read_context, edit_line,
// edit_lines, insert_after, delete_lines) and the whole-file tools
// (write_file, patch_file) all assume UTF-8 text. Applied to a binary
// file they do not merely fail — they DESTROY it:
//
//   - write_file replaces a zip archive (a .docx IS a zip) with plain
//     text, with no backup;
//   - the line editors split a deflate stream on "\n", rewrite it and
//     always append a trailing newline, so the CRCs no longer match and
//     a stray 0x0A lands past the End-Of-Central-Directory record —
//     Word then reports unreadable content;
//   - read_lines hands raw bytes to the JSON encoder, where invalid
//     UTF-8 becomes U+FFFD, so the model physically cannot send them
//     back unchanged.
//
// This file is the single gate that stops all of that. It costs the
// model nothing until it actually points a text tool at a binary file;
// the whole payload is in the error message, which names the tool that
// WOULD work.

// binarySniffBytes is how much of a file's head is inspected. 8 KB is
// enough to catch a NUL byte or invalid UTF-8 in every real-world binary
// format while staying a single cheap read.
const binarySniffBytes = 8 * 1024

// binaryKind describes one recognizable binary format and the tool the
// model should have reached for instead.
type binaryKind struct {
	what   string // "a Word document (binary zip archive)"
	advice string // "Use read_docx / edit_docx — ..."
}

// binaryExts maps a lower-case extension to its advice.
//
// The list is deliberately short. It only needs to cover formats that
// (a) users routinely ask an agent to edit, (b) have an unambiguous
// extension, and (c) have a right answer to point at. Everything else —
// fonts, object files, media, unknown blobs — is caught by the content
// sniff below, which does not depend on names at all. The extension is
// only a fallback for the one case content cannot answer: a file that
// does not exist yet (write_file creating "report.docx" from text).
var binaryExts = map[string]binaryKind{
	".docx": {"a Word document (binary zip archive)", "Use read_docx / edit_docx — they preserve formatting and make a backup."},
	".xlsx": {"an Excel workbook (binary zip archive)", "Use read_xlsx / edit_xlsx — they preserve formulas and make a backup."},
	".pptx": {"a PowerPoint presentation (binary zip archive)", "No text tool can edit it; read_zip can list its parts."},
	".doc":  {"a legacy binary Word document", "No tool here can edit it; ask the user to save it as .docx first."},
	".xls":  {"a legacy binary Excel workbook", "No tool here can edit it; ask the user to save it as .xlsx first."},
	".ppt":  {"a legacy binary PowerPoint file", "No tool here can edit it; ask the user to save it as .pptx first."},
	".pdf":  {"a PDF document (binary)", "Use read_pdf to read it; a PDF cannot be edited as text."},
	".zip":  {"a zip archive (binary)", "Use read_zip to list or read its entries."},

	".png":  {"an image file (binary)", "Use read_image to look at it."},
	".jpg":  {"an image file (binary)", "Use read_image to look at it."},
	".jpeg": {"an image file (binary)", "Use read_image to look at it."},
	".gif":  {"an image file (binary)", "Use read_image to look at it."},
	".bmp":  {"an image file (binary)", "Use read_image to look at it."},
	".webp": {"an image file (binary)", "Use read_image to look at it."},
	".ico":  {"an image file (binary)", "Use read_image to look at it."},

	".exe":   {"a compiled executable (binary)", "It has no text form; do not open it as lines."},
	".dll":   {"a compiled library (binary)", "It has no text form; do not open it as lines."},
	".so":    {"a compiled library (binary)", "It has no text form; do not open it as lines."},
	".dylib": {"a compiled library (binary)", "It has no text form; do not open it as lines."},

	".db":      {"a database file (binary)", "Query it with ctx_execute (for example sqlite3), not as text."},
	".sqlite":  {"a database file (binary)", "Query it with ctx_execute (for example sqlite3), not as text."},
	".sqlite3": {"a database file (binary)", "Query it with ctx_execute (for example sqlite3), not as text."},

	".gz":  {"a compressed archive (binary)", "Unpack it with ctx_execute before reading it as text."},
	".bz2": {"a compressed archive (binary)", "Unpack it with ctx_execute before reading it as text."},
	".xz":  {"a compressed archive (binary)", "Unpack it with ctx_execute before reading it as text."},
	".7z":  {"a compressed archive (binary)", "Unpack it with ctx_execute before reading it as text."},
	".rar": {"a compressed archive (binary)", "Unpack it with ctx_execute before reading it as text."},
	".tar": {"an archive (binary)", "Unpack it with ctx_execute before reading it as text."},
}

// genericBinary is used when the content proves a file is binary but the
// name gives no hint about which reader would work.
var genericBinary = binaryKind{
	what:   "a binary file",
	advice: "Text tools would corrupt it; inspect it with ctx_execute instead.",
}

// EnsureTextFile reports an error when path is (or, for a file that does
// not exist yet, is named as) a binary file that the text tools would
// destroy. It is the guard in front of every line/patch/write operation.
//
// Content outranks the name: a file whose first bytes are valid UTF-8
// with no NUL is treated as text even if it is called ".docx", because
// the extension can lie and the bytes cannot. The extension table is
// consulted only for the wording of the error, and as the sole signal
// when the file cannot be read (typically: it does not exist yet, and
// the caller is about to CREATE it from text).
func EnsureTextFile(path string) error {
	head, truncated, err := readHead(path, binarySniffBytes)
	if err != nil {
		// Unreadable (missing / permission / directory). Callers surface
		// their own FileErr for those; the only thing worth refusing here
		// is creating a known binary format out of plain text.
		if kind, ok := binaryExts[strings.ToLower(filepath.Ext(path))]; ok {
			return binaryError(path, kind)
		}
		return nil
	}
	return ensureTextBytes(path, head, truncated)
}

// ensureTextBytes is EnsureTextFile for callers that already hold the
// file's bytes (readLines, PatchFile), so the guard costs no extra I/O.
// data may be the whole file or just its head; truncated says whether
// more bytes follow, which matters only for a multi-byte rune cut in
// half at the boundary.
func ensureTextBytes(path string, data []byte, truncated bool) error {
	if len(data) > binarySniffBytes {
		data = data[:binarySniffBytes]
		truncated = true
	}
	if !looksBinary(data, truncated) {
		return nil
	}
	kind, ok := binaryExts[strings.ToLower(filepath.Ext(path))]
	if !ok {
		kind = genericBinary
	}
	return binaryError(path, kind)
}

// binaryError builds the one message the model sees. It names the file,
// says what it actually is, and points at the tool that works — the
// whole fix is here, so it costs tokens only when a tool is misapplied.
func binaryError(path string, kind binaryKind) error {
	return fmt.Errorf("%s is %s, not text. %s", filepath.Base(path), kind.what, kind.advice)
}

// looksBinary applies the content test: a NUL byte anywhere, or bytes
// that are not valid UTF-8. An empty file is text (nothing to corrupt).
func looksBinary(head []byte, truncated bool) bool {
	if len(head) == 0 {
		return false
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	if truncated {
		head = trimPartialRune(head)
	}
	return !utf8.Valid(head)
}

// trimPartialRune drops a multi-byte UTF-8 sequence that the sniff
// window cut in half, so a Polish "ż" straddling the 8 KB boundary is
// not mistaken for binary garbage.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && i < len(b); i++ {
		j := len(b) - 1 - i
		c := b[j]
		if c < utf8.RuneSelf {
			return b // last byte is a complete ASCII rune
		}
		if c&0xC0 == 0xC0 {
			// Start byte of a sequence: keep it only if complete.
			if r, size := utf8.DecodeRune(b[j:]); r == utf8.RuneError && size <= 1 {
				return b[:j]
			}
			return b
		}
		// Continuation byte — walk back to its start byte.
	}
	return b
}

// readHead reads at most limit bytes from the start of path. truncated
// reports that the file has more bytes than were read.
func readHead(path string, limit int) (head []byte, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if info, statErr := f.Stat(); statErr == nil && info.IsDir() {
		return nil, false, fmt.Errorf("is_directory %s", path)
	}
	buf := make([]byte, limit)
	n := 0
	for n < limit {
		read, readErr := f.Read(buf[n:])
		n += read
		if readErr != nil {
			break
		}
		if read == 0 {
			break
		}
	}
	return buf[:n], n == limit, nil
}
