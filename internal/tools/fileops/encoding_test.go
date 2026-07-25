package fileops

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// --- fixtures for the encodings a Windows user actually produces ---

// utf16LE encodes s the way PowerShell 5.1 does when a command is
// redirected with ">", Out-File or Set-Content: UTF-16 little-endian
// with a BOM. The bytes below are why `powershell '// hello' > d.js`
// produced a file the text tools refused to touch.
func utf16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 0, 2+2*len(units))
	buf = append(buf, 0xFF, 0xFE) // BOM
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return buf
}

func utf16BE(s string) []byte {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 0, 2+2*len(units))
	buf = append(buf, 0xFE, 0xFF) // BOM
	for _, u := range units {
		buf = append(buf, byte(u>>8), byte(u))
	}
	return buf
}

func utf16LENoBOM(s string) []byte {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 0, 2*len(units))
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return buf
}

// The single-byte code points were read off the live Windows code pages
// (System.Text.Encoding.GetEncoding(1250|852)), not from memory:
//
//	          ą     ę     ś     ż     ó     ł
//	CP1250   0xB9  0xEA  0x9C  0xBF  0xF3  0xB3
//	CP852    0xA5  0xA9  0x98  0xBE  0xA2  0x88
//
// Neither sequence is valid UTF-8 and neither contains a NUL byte —
// that combination is exactly what the guard used to call "binary".
var (
	cp1250Polish = []byte{0xB9, 0xEA, 0x9C, 0xBF, 0xF3, 0xB3}
	cp852Polish  = []byte{0xA5, 0xA9, 0x98, 0xBE, 0xA2, 0x88}
)

func legacyFile(t *testing.T, dir, name string, polish []byte) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("// notatki: ")
	b.Write(polish)
	b.WriteString("\nconst x = 1;\n")
	b.WriteString("// druga linia ")
	b.Write(polish)
	b.WriteString("\n")
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- FIX 1 + FIX 2: the main regression ---

// The production failure, reproduced end to end: the agent creates a
// file with its own shell tool, PowerShell 5.1 writes it as UTF-16, and
// from then on every editing tool refuses it as "binary". The content is
// pure ASCII JavaScript — calling it binary is simply false.
func TestUTF16File_IsNotCalledBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d.js")
	if err := os.WriteFile(path, utf16LE("// hello\nconsole.log(1);\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := EnsureTextFile(path)
	if err == nil {
		t.Fatal("UTF-16 accepted by the line-tool guard (it cannot read UTF-16)")
	}
	msg := err.Error()
	if strings.Contains(msg, "binary") {
		t.Errorf("UTF-16 text described as binary: %q", msg)
	}
	if !strings.Contains(msg, "UTF-16") {
		t.Errorf("message does not name the real cause (UTF-16): %q", msg)
	}
	if !strings.Contains(msg, "write_file") {
		t.Errorf("message does not point at the tool that works: %q", msg)
	}

	// FIX 3: the way out. Overwriting the file with UTF-8 is precisely
	// the repair, so write_file must not refuse it — otherwise the file
	// is stuck in a state no tool can fix and the model reaches for the
	// shell (which is how three finished files got deleted).
	if _, err := WriteFile(path, "// hello\nconsole.log(1);\n"); err != nil {
		t.Fatalf("write_file could not repair a UTF-16 file: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "// hello\nconsole.log(1);\n" {
		t.Fatalf("content after repair = %q", got)
	}
	// ...and once repaired it is an ordinary file again.
	if _, err := ReadLines(path, 1, 2); err != nil {
		t.Fatalf("ReadLines after repair: %v", err)
	}
}

func TestUTF16_BigEndianAndNoBOM(t *testing.T) {
	dir := t.TempDir()
	be := filepath.Join(dir, "be.js")
	if err := os.WriteFile(be, utf16BE("// hello world\nconst answer = 42;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTextFile(be); err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Errorf("UTF-16 BE: err = %v, want a UTF-16 message", err)
	}

	// No BOM: only the byte pattern is left to go on.
	nobom := filepath.Join(dir, "nobom.js")
	body := strings.Repeat("const value = 12345;\n", 8)
	if err := os.WriteFile(nobom, utf16LENoBOM(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTextFile(nobom); err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Errorf("UTF-16 LE without BOM: err = %v, want a UTF-16 message", err)
	}
	if _, err := WriteFile(nobom, "const value = 12345;\n"); err != nil {
		t.Fatalf("write_file could not repair a BOM-less UTF-16 file: %v", err)
	}
}

// UTF-32 starts with the same FF FE as UTF-16 LE. It must NOT be waved
// through as UTF-16 text, because that would let write_file overwrite it.
func TestUTF32BOM_StaysBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "u32.txt")
	data := []byte{0xFF, 0xFE, 0x00, 0x00}
	for _, r := range "hello world" {
		data = append(data, byte(r), 0x00, 0x00, 0x00)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before := hashFile(t, path)
	if _, err := WriteFile(path, "text"); err == nil {
		t.Error("write_file overwrote a UTF-32 file")
	}
	if after := hashFile(t, path); after != before {
		t.Error("UTF-32 file was modified")
	}
}

// --- FIX 1: legacy single-byte code pages are text, not binary ---

func TestLegacyCodePages_AllToolsWork(t *testing.T) {
	pages := []struct {
		name   string
		polish []byte
	}{
		{"cp1250", cp1250Polish}, // Notepad "ANSI" on a Polish Windows
		{"cp852", cp852Polish},   // cmd.exe OEM output
	}
	for _, page := range pages {
		t.Run(page.name, func(t *testing.T) {
			dir := t.TempDir()
			path := legacyFile(t, dir, "script.js", page.polish)

			if err := EnsureTextFile(path); err != nil {
				t.Fatalf("EnsureTextFile refused %s text: %v", page.name, err)
			}
			if _, err := ReadLines(path, 1, 3); err != nil {
				t.Fatalf("ReadLines: %v", err)
			}
			if _, err := ReadContext(path, 2, 1); err != nil {
				t.Fatalf("ReadContext: %v", err)
			}
			if _, err := PatchFile(path, []PatchChange{{Old: "const x = 1;", New: "const x = 2;"}}, ""); err != nil {
				t.Fatalf("PatchFile: %v", err)
			}
			if _, err := EditLine(path, 2, "const x = 3;"); err != nil {
				t.Fatalf("EditLine: %v", err)
			}
			if _, err := WriteFile(path, "// przepisane jako UTF-8: zażółć\n"); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
		})
	}
}

// A legacy code page in a file NAMED like a binary document is still
// refused: the extension is the only corroboration available for bytes
// that are neither valid UTF-8 nor NUL-bearing, and formats such as a
// small .bz2 or an uncompressed .pdf can look exactly like that.
func TestLegacyBytes_BinaryExtensionStillRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "raport.pdf")
	body := append([]byte("%PDF-1.4\n"), cp1250Polish...)
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTextFile(path); err == nil {
		t.Error("a non-UTF-8 .pdf was accepted as text")
	}
	if _, err := WriteFile(path, "text"); err == nil {
		t.Error("write_file overwrote a non-UTF-8 .pdf")
	}
}

// --- FIX 4: wording for a source file that really is broken ---

// A real NUL byte in a .ts file is a true positive (it happened in
// OmniRoute-main) — it stays refused. But the message must talk about
// encoding and must not send the model to the shell, because "inspect it
// with ctx_execute" is how a `del` of three finished files started.
func TestSourceFileWithNUL_RefusedWithEncodingAdvice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.ts")
	data := append([]byte("export const a = 1;\n"), 0x00)
	data = append(data, []byte("export const b = 2;\n")...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before := hashFile(t, path)

	err := EnsureTextFile(path)
	if err == nil {
		t.Fatal("a file with a NUL byte was accepted as text")
	}
	msg := err.Error()
	if !strings.Contains(msg, "index.ts") {
		t.Errorf("message does not name the file: %q", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "encoding") {
		t.Errorf("message does not mention encoding: %q", msg)
	}
	if strings.Contains(msg, "ctx_execute") {
		t.Errorf("message sends the model to the shell: %q", msg)
	}
	if _, err := WriteFile(path, "export const a = 1;\n"); err == nil {
		t.Error("write_file overwrote a NUL-bearing file")
	}
	if after := hashFile(t, path); after != before {
		t.Error("the file was modified")
	}
}

func TestGenericBinaryAdvice_IsNonDestructive(t *testing.T) {
	if strings.Contains(genericBinary.advice, "ctx_execute") {
		t.Errorf("generic advice still routes to the shell: %q", genericBinary.advice)
	}
	if !strings.Contains(strings.ToLower(genericBinary.advice), "delete") {
		t.Errorf("generic advice does not warn against deleting: %q", genericBinary.advice)
	}
}

// --- no regression in protection: every listed format stays refused ---

func minimalZip(t *testing.T, comment string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("<?xml version=\"1.0\"?><d>" + comment + "</d>")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func minimalPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, G: 10, B: 10, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func minimalJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func minimalGIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func minimalGzip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte("Zażółć gęślą jaźń, raport kwartalny\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// minimalTar builds a real 512-byte ustar header plus a padded data
// block — a tar is mostly NUL padding, which is what catches it.
func minimalTar() []byte {
	block := make([]byte, 512)
	copy(block, "notes.txt")
	copy(block[100:], "0000644\x00")
	copy(block[124:], "00000000015\x00") // size
	copy(block[148:], "        ")        // checksum placeholder
	block[156] = '0'
	copy(block[257:], "ustar\x0000")
	data := make([]byte, 512)
	copy(data, "hello, world\n")
	return append(append(block, data...), make([]byte, 1024)...)
}

// realisticFixtures returns one fixture per extension in binaryExts.
// Each is the real magic-byte layout of that format (the zip/png/jpeg/
// gif/gzip ones are produced by the standard library, so they are the
// genuine article).
func realisticFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	ole2 := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 64)...)
	elf := append([]byte{0x7F, 'E', 'L', 'F', 2, 1, 1, 0}, make([]byte, 56)...)
	machO := append([]byte{0xCF, 0xFA, 0xED, 0xFE, 0x07, 0x00, 0x00, 0x01}, make([]byte, 56)...)
	mz := append([]byte{'M', 'Z', 0x90, 0x00, 0x03, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00}, make([]byte, 116)...)
	sqlite := append([]byte("SQLite format 3\x00"), make([]byte, 96)...)
	png := minimalPNG(t)

	// A PDF as any real producer writes one: a Flate-compressed content
	// stream between the ASCII objects.
	pdf := append([]byte("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n1 0 obj\n<< /Length 40 /Filter /FlateDecode >>\nstream\n"),
		[]byte{0x78, 0x9C, 0x00, 0x0A, 0x00, 0xF5, 0xFF, 'H', 'e', 'l', 'l', 'o', 0x01, 0x00, 0x1C, 0x00}...)
	pdf = append(pdf, []byte("\nendstream\nendobj\ntrailer\n%%EOF\n")...)

	return map[string][]byte{
		".docx": minimalZip(t, "Umowa najmu"),
		".xlsx": minimalZip(t, "Arkusz"),
		".pptx": minimalZip(t, "Prezentacja"),
		".zip":  minimalZip(t, "Archiwum"),
		".doc":  ole2,
		".xls":  ole2,
		".ppt":  ole2,
		".pdf":  pdf,

		".png":  png,
		".jpg":  minimalJPEG(t),
		".jpeg": minimalJPEG(t),
		".gif":  minimalGIF(t),
		".bmp":  append([]byte{'B', 'M', 0x46, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x36, 0x00, 0x00, 0x00}, make([]byte, 60)...),
		".webp": append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00"), make([]byte, 40)...),
		".ico":  append([]byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10, 0x00, 0x00}, png...),

		".exe":   mz,
		".dll":   mz,
		".so":    elf,
		".dylib": machO,

		".db":      sqlite,
		".sqlite":  sqlite,
		".sqlite3": sqlite,

		".gz":  minimalGzip(t),
		".bz2": append([]byte("BZh91AY&SY"), []byte{0x8C, 0x9A, 0x2F, 0x3D, 0x00, 0x00, 0x02, 0x51, 0x80, 0x40}...),
		".xz":  append([]byte{0xFD, '7', 'z', 'X', 'Z', 0x00, 0x00, 0x04, 0xE6, 0xD6, 0xB4, 0x46}, make([]byte, 40)...),
		".7z":  append([]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C, 0x00, 0x04}, make([]byte, 40)...),
		".rar": append([]byte("Rar!\x1A\x07\x00"), make([]byte, 40)...),
		".tar": minimalTar(),
	}
}

// Narrowing the detector to NUL bytes must not open a hole. Every
// extension the guard knows about is checked with a realistic fixture:
// it must still be refused, and the file must come out byte-identical.
func TestEveryListedBinaryFormat_StillRefusedAndIntact(t *testing.T) {
	fixtures := realisticFixtures(t)
	for ext := range binaryExts {
		if _, ok := fixtures[ext]; !ok {
			t.Fatalf("no fixture for %s — extend realisticFixtures", ext)
		}
	}
	for ext, data := range fixtures {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "plik"+ext)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			before := hashFile(t, path)

			if err := EnsureTextFile(path); err == nil {
				t.Errorf("%s accepted as text", ext)
			}
			if _, err := ReadLines(path, 1, 3); err == nil {
				t.Errorf("ReadLines accepted %s", ext)
			}
			if _, err := PatchFile(path, []PatchChange{{Old: "a", New: "b"}}, ""); err == nil {
				t.Errorf("PatchFile accepted %s", ext)
			}
			if _, err := WriteFile(path, "zwykły tekst"); err == nil {
				t.Errorf("write_file overwrote %s", ext)
			}
			if _, err := EditLine(path, 1, "x"); err == nil {
				t.Errorf("EditLine accepted %s", ext)
			}
			if after := hashFile(t, path); after != before {
				t.Errorf("%s was modified (%s -> %s)", ext, before[:12], after[:12])
			}
		})
	}
}

// The reason the narrowing is safe, stated as a test: every one of those
// fixtures carries a NUL byte inside the sniff window, so dropping
// "invalid UTF-8" as a binary signal changes nothing for them. The two
// formats that can legitimately lack one within a short head (.pdf,
// .bz2) are covered by the extension corroboration instead.
func TestEveryListedBinaryFormat_CaughtByNULOrExtension(t *testing.T) {
	for ext, data := range realisticFixtures(t) {
		head := data
		if len(head) > binarySniffBytes {
			head = head[:binarySniffBytes]
		}
		if bytes.IndexByte(head, 0) < 0 {
			if _, known := binaryExts[ext]; !known {
				t.Errorf("%s has no NUL in its head and no extension entry — it would slip through", ext)
			}
			t.Logf("%s: no NUL in head, refused via the extension table", ext)
		}
	}
}
