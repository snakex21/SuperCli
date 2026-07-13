package office

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ledongthuc/pdf"

	"supercli/internal/tools/fileops"
)

// Default bounds for the read_pdf tool. PDFs
// can be huge (a 1000-page legal brief is
// common) so the page cap is generous. 256 MB
// on disk / 4 MB rendered text covers
// everything from a memo to a full book.
const (
	DefaultMaxPdfBytes  = 256 * 1024 * 1024 // 256 MB on disk
	DefaultMaxPdfPages  = 1000
	DefaultMaxPdfOutput = 4 * 1024 * 1024 // 4 MB rendered text
)

// ReadPdfTool extracts the text content of a
// PDF file. The implementation is pure Go
// (github.com/ledongthuc/pdf, no cgo), so the
// binary stays self-contained. There is no
// shelling out to pdftotext, no poppler, no
// external .NET runtime, no temporary files.
//
// Output format: one section per page, with a
// header line "--- Page N ---" separating
// pages. Text is rendered in reading order
// (top-to-bottom, left-to-right) using the
// PDF's embedded font tables.
//
// Safety: the file is read-only — we open
// the file, build a Reader, walk pages, and
// close. The size cap is enforced at stat
// time; the page cap is enforced during the
// walk.
type ReadPdfTool struct {
	BaseDir        string
	MaxPdfBytes    int64
	MaxPages       int
	MaxOutputBytes int64
}

// NewReadPdf returns a ReadPdfTool with
// default bounds. Pass 0 for maxBytes to use
// the default. baseDir is the directory the
// tool resolves relative paths against.
func NewReadPdf(baseDir string, maxBytes int64) *ReadPdfTool {
	if baseDir == "" {
		baseDir = "."
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxPdfBytes
	}
	return &ReadPdfTool{
		BaseDir:        baseDir,
		MaxPdfBytes:    maxBytes,
		MaxPages:       DefaultMaxPdfPages,
		MaxOutputBytes: DefaultMaxPdfOutput,
	}
}

// Spec returns the Tool descriptor.
func (t *ReadPdfTool) Spec() Tool {
	return Tool{
		Name:        "read_pdf",
		Description: "Read a PDF file and extract its text. Pure Go (ledongthuc/pdf, no cgo, no shell-out). Renders pages in reading order separated by '--- Page N ---' headers. Bounds: 256 MB on disk, 1000 pages, 4 MB output.",
		Schema: `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the PDF file."},
    "max_pages": {"type": "integer", "description": "Cap on pages to render (default 1000)."}
  },
  "required": ["path"]
}`,
		Fn: t.Execute,
	}
}

// Execute reads the PDF and returns the
// extracted text.
func (t *ReadPdfTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var params struct {
		Path     string `json:"path"`
		MaxPages int    `json:"max_pages"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{Err: fmt.Errorf("read_pdf: bad args: %w", err)}, err
	}
	if params.Path == "" {
		err := fmt.Errorf("read_pdf: path is required")
		return Result{Err: err}, err
	}
	maxP := params.MaxPages
	if maxP <= 0 {
		maxP = t.MaxPages
	}

	full, err := resolveSandboxed(t.BaseDir, params.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("read_pdf: %w", err)}, nil
	}
	info, err := os.Stat(full)
	if err != nil {
		err = fmt.Errorf("read_pdf: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	if info.IsDir() {
		err := fmt.Errorf("read_pdf: %q is a directory, not a pdf", full)
		return Result{Err: err}, err
	}
	if info.Size() > t.MaxPdfBytes {
		err := fmt.Errorf("read_pdf: file too large: %d > %d", info.Size(), t.MaxPdfBytes)
		return Result{Err: err}, err
	}

	f, err := os.Open(full)
	if err != nil {
		err = fmt.Errorf("read_pdf: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	defer f.Close()
	reader, err := pdf.NewReader(f, info.Size())
	if err != nil {
		return Result{Err: fmt.Errorf("read_pdf: parse %q: %w", full, err)}, err
	}

	text, err := t.renderPages(reader, maxP)
	if err != nil {
		return Result{Err: fmt.Errorf("read_pdf: %w", err)}, err
	}
	return Result{Text: text}, nil
}

// renderPages walks the first n pages of the
// PDF and renders their text. Pages are
// separated by a "--- Page N ---" header so
// the model can reference individual pages
// in its answer.
func (t *ReadPdfTool) renderPages(reader *pdf.Reader, maxPages int) (string, error) {
	total := reader.NumPage()
	if total > maxPages {
		total = maxPages
	}
	var out strings.Builder
	for i := 1; i <= total; i++ {
		if err := ctxErr(); err != nil {
			return "", err
		}
		page := reader.Page(i)
		text, err := t.renderPage(page)
		if err != nil {
			// Don't fail the whole read for one
			// bad page — log and continue.
			text = fmt.Sprintf("[read_pdf: page %d error: %v]", i, err)
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		fmt.Fprintf(&out, "--- Page %d ---\n", i)
		out.WriteString(text)
		if int64(out.Len()) > t.MaxOutputBytes {
			return "", fmt.Errorf("rendered text exceeds %d bytes", t.MaxOutputBytes)
		}
	}
	return out.String(), nil
}

// renderPage extracts the plain text of a
// single page. We build the font map from
// the page's font list (ledongthuc/pdf's
// GetPlainText needs a font resolver to
// decode text-show operators).
func (t *ReadPdfTool) renderPage(page pdf.Page) (string, error) {
	names := page.Fonts()
	fonts := make(map[string]*pdf.Font, len(names))
	for _, name := range names {
		f := page.Font(name)
		fonts[name] = &f
	}
	text, err := page.GetPlainText(fonts)
	if err != nil {
		return "", fmt.Errorf("get plain text: %w", err)
	}
	return text, nil
}

// ctxErr is a placeholder for future context
// propagation. Kept here so we have one
// place to add ctx cancellation if we ever
// thread it through to the ledongthuc/pdf
// library.
func ctxErr() error { return nil }
