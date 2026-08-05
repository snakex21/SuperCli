package fileops

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// The line-oriented tools (read_lines, read_context) and the
// whole-file tools
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

// Not everything that fails to be UTF-8 is binary, and that distinction
// is the whole point of the classification below. On a Polish Windows a
// perfectly ordinary source file arrives as CP1250 (Notepad's "ANSI"),
// CP852 (cmd.exe) or UTF-16 (PowerShell 5.1 writes UTF-16 for `>`,
// Out-File and Set-Content). Treating those as binary locked the model
// out of files it had just created with its own shell tool, with no tool
// left to repair them — and the recorded way out of that trap was `del`.
//
// So: NUL bytes mean binary. Everything else is text in some encoding,
// and the error message says which one and how to get back to UTF-8.

// binarySniffBytes is how much of a file's head is inspected. 8 KB is
// enough to catch a NUL byte in every real-world binary format while
// staying a single cheap read.
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

// textExts are extensions whose files are supposed to BE source or prose.
// When one of them turns out to contain NUL bytes the honest diagnosis is
// almost always an encoding problem, not "this is a binary format", so it
// gets its own wording. The file is still refused — a NUL byte really
// does break the line tools — but the advice must not send the model to
// the shell, because "inspect it with ctx_execute" is how a session ended
// with `del` on three finished files.
var textExts = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".jsx": true,
	".ts": true, ".tsx": true, ".css": true, ".scss": true, ".less": true,
	".html": true, ".htm": true, ".xml": true, ".svg": true, ".vue": true,
	".go": true, ".py": true, ".rb": true, ".rs": true, ".java": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true,
	".cs": true, ".php": true, ".swift": true, ".kt": true, ".lua": true,
	".sh": true, ".bash": true, ".ps1": true, ".bat": true, ".cmd": true,
	".sql": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".ini": true, ".cfg": true, ".conf": true, ".env": true,
	".md": true, ".txt": true, ".csv": true, ".tsv": true, ".log": true,
	".rst": true, ".tex": true, ".gitignore": true, ".dockerfile": true,
}

// brokenTextError is the message for a file that SHOULD be text by its
// name but carries NUL bytes. It names the real suspect — the encoding —
// instead of claiming the file is a binary format, and the way out it
// offers destroys nothing.
func brokenTextError(path string) error {
	return fmt.Errorf("%s contains NUL bytes, so it is not UTF-8 text and the line tools would mangle it. "+
		"In a source file that is an encoding problem or a truncated write, not a binary format. "+
		"Do not delete it: write the intended UTF-8 content to a NEW file and compare, or ask the user what encoding it is in",
		filepath.Base(path))
}

// genericBinary is used when the content proves a file is binary but the
// name gives no hint about which reader would work.
var genericBinary = binaryKind{
	what:   "a binary file",
	advice: "Text tools would corrupt it. Use the matching reader if one fits (read_image, read_pdf, read_zip, read_docx), otherwise ask the user — do not delete or overwrite it.",
}

// utf16Error is the message for a file that IS text, just not in UTF-8.
// Calling it "binary" was false and it misled the model; naming the
// encoding and the one tool that still works turns a dead end into a
// two-step repair.
func utf16Error(path string) error {
	return fmt.Errorf("%s is UTF-16 text, not UTF-8 (PowerShell's >, Out-File and Set-Content write UTF-16). "+
		"The line tools need UTF-8, but write_file still accepts this file: re-save the same text with write_file, then edit it normally",
		filepath.Base(path))
}

// EnsureTextFile reports an error when path is (or, for a file that does
// not exist yet, is named as) a binary file that the text tools would
// destroy. It is the guard in front of every line/patch/write operation.
//
// Content outranks the name: a file whose first bytes are valid UTF-8
// is treated as text even if it is called ".docx", because the extension
// can lie and the bytes cannot. The extension table supplies the wording
// of the error, and is a signal in its own right in the two cases the
// content cannot settle: a file that cannot be read (it does not exist
// yet, and the caller is about to CREATE it from text), and bytes that
// are neither valid UTF-8 nor NUL-bearing, where the name is the only
// thing that can tell a CP1250 note from a short .bz2.
func EnsureTextFile(path string) error {
	return ensurePath(path, false)
}

// EnsureOverwritableFile is the guard for whole-file WRITES, and it is
// deliberately more permissive than EnsureTextFile: a file that is text
// in the wrong encoding may be overwritten, because replacing it with
// UTF-8 is exactly the repair the user wants. Only NUL-bearing content —
// a real binary, where overwriting destroys data — is refused.
//
// Without this a UTF-16 file has no way out at all: read_lines,
// patch_file and write_file all refuse it and create_file refuses it for
// existing, which leaves only the shell.
func EnsureOverwritableFile(path string) error {
	return ensurePath(path, true)
}

func ensurePath(path string, overwriting bool) error {
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
	return checkBytes(path, head, truncated, overwriting)
}

// ensureTextBytes is EnsureTextFile for callers that already hold the
// file's bytes (readLines, PatchFile), so the guard costs no extra I/O.
// data may be the whole file or just its head; truncated says whether
// more bytes follow, which matters only for a multi-byte rune cut in
// half at the boundary.
func ensureTextBytes(path string, data []byte, truncated bool) error {
	return checkBytes(path, data, truncated, false)
}

func checkBytes(path string, data []byte, truncated, overwriting bool) error {
	if len(data) > binarySniffBytes {
		data = data[:binarySniffBytes]
		truncated = true
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch classify(data, truncated) {
	case verdictText:
		return nil

	case verdictUTF16:
		// Text in the wrong encoding. Readable by a human, unusable by
		// the line tools — but safe to overwrite with UTF-8.
		if overwriting {
			return nil
		}
		return utf16Error(path)

	case verdictLegacy:
		// No NUL and not valid UTF-8: a single-byte code page (CP1250,
		// CP852, latin-1). That is ordinary text and every tool may work
		// on it. The one exception is a name that independently claims a
		// binary format — the extension is the only corroboration left
		// for formats whose head can be NUL-free (a short .bz2, an
		// uncompressed .pdf), and a plain-text file does not carry those
		// names by accident.
		if kind, ok := binaryExts[ext]; ok {
			return binaryError(path, kind)
		}
		return nil

	default: // verdictBinary
		if kind, ok := binaryExts[ext]; ok {
			return binaryError(path, kind)
		}
		if textExts[ext] {
			return brokenTextError(path)
		}
		return binaryError(path, genericBinary)
	}
}

// binaryError builds the one message the model sees. It names the file,
// says what it actually is, and points at the tool that works — the
// whole fix is here, so it costs tokens only when a tool is misapplied.
func binaryError(path string, kind binaryKind) error {
	return fmt.Errorf("%s is %s, not text. %s", filepath.Base(path), kind.what, kind.advice)
}

// verdict is what the content sniff concluded about a file's bytes.
type verdict int

const (
	verdictText   verdict = iota // valid UTF-8: every tool works
	verdictLegacy                // no NUL, not UTF-8: a single-byte code page
	verdictUTF16                 // UTF-16 text: readable, but not by these tools
	verdictBinary                // NUL bytes: a real binary, tools would destroy it
)

// classify applies the content test. An empty file is text (nothing to
// corrupt). The order matters: UTF-16 is checked before NUL, because
// UTF-16 is FULL of NUL bytes and would otherwise be mistaken for a
// binary — which is exactly the bug this replaces.
func classify(head []byte, truncated bool) verdict {
	if len(head) == 0 {
		return verdictText
	}
	switch {
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE, 0x00, 0x00}):
		// UTF-32 LE shares UTF-16 LE's first two bytes. Do not wave it
		// through as UTF-16: that would let write_file overwrite it.
		return verdictBinary
	case bytes.HasPrefix(head, []byte{0xFF, 0xFE}), bytes.HasPrefix(head, []byte{0xFE, 0xFF}):
		return verdictUTF16
	}
	if bytes.IndexByte(head, 0) >= 0 {
		if looksLikeBOMlessUTF16(head) {
			return verdictUTF16
		}
		return verdictBinary
	}
	if truncated {
		head = trimPartialRune(head)
	}
	if utf8.Valid(head) {
		return verdictText
	}
	return verdictLegacy
}

// looksLikeBOMlessUTF16 reports whether NUL-bearing bytes are UTF-16 text
// written without a BOM (.NET and Java writers do this).
//
// The test is deliberately narrow, because a false positive costs more
// than a false negative: a UTF-16 verdict lets write_file overwrite the
// file, so mistaking a real binary for UTF-16 would destroy it. Three
// conditions have to hold at once, for one of the two endiannesses:
//
//   - no 16-bit unit is 00 00 — that alone rules out every format whose
//     header contains two aligned NUL bytes, i.e. essentially all of them
//     (padding, lengths and offsets are NUL runs);
//   - every high byte is <= 0x2F, so all characters sit below U+3000:
//     Latin, Polish, Cyrillic, Greek and punctuation, but not the random
//     high bytes of compressed or executable data;
//   - at least a third of the units are plain ASCII, and no unit is a
//     control character other than tab/CR/LF.
func looksLikeBOMlessUTF16(head []byte) bool {
	if len(head) < 32 {
		return false // too little evidence to risk it
	}
	// littleEndian=true reads the high byte second (UTF-16 LE).
	for _, littleEndian := range []bool{true, false} {
		if utf16Units(head, littleEndian) {
			return true
		}
	}
	return false
}

func utf16Units(head []byte, littleEndian bool) bool {
	units, ascii := 0, 0
	for i := 0; i+1 < len(head); i += 2 {
		lo, hi := head[i], head[i+1]
		if !littleEndian {
			lo, hi = hi, lo
		}
		units++
		if hi > 0x2F {
			return false
		}
		if hi != 0 {
			continue
		}
		// A pure-ASCII unit: it must be a character text can contain.
		if lo == 0 {
			return false // NUL character: this is not text
		}
		if lo < 0x20 && lo != '\t' && lo != '\n' && lo != '\r' {
			return false
		}
		ascii++
	}
	return units > 0 && ascii*3 >= units
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
