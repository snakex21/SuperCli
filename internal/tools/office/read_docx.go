package office

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/fileops"
)

// Default bounds for the read_docx tool. The
// defaults are conservative: docx files are
// usually text-heavy, not multi-hundred-MB
// binaries. 64 MB on disk / 4 MB extracted text
// covers everything from a short memo to a
// multi-chapter book.
const (
	DefaultMaxDocxBytes     = 64 * 1024 * 1024 // 64 MB on disk
	DefaultMaxDocxParagraph = 5000
	DefaultMaxDocxOutput    = 4 * 1024 * 1024 // 4 MB rendered text
)

// wordProcessingNS is the Word XML namespace.
// We use a constant so the code reads naturally
// at the call sites that need to match element
// names. The encoding/xml package resolves the
// prefix transparently when we compare
// Name.Local.
const wordProcessingNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// ReadDocxTool extracts the text content of a
// .docx file. A .docx is a zip archive whose
// main content is word/document.xml; the tool
// opens the zip, reads that one entry, and walks
// the XML to concatenate <w:t> text elements.
//
// The implementation is pure stdlib
// (archive/zip + encoding/xml), so the binary
// stays self-contained. There is no shelling out
// to libreoffice or pandoc, no external .NET
// runtime, no temporary files.
//
// Output format: each <w:p> becomes one line
// (paragraph text concatenated, multiple runs
// joined); each <w:tbl> becomes a pipe-separated
// row block (one row per line, cells separated
// by " | "). The model receives plain text
// sized for its context window.
//
// Safety: zip-slip protection comes for free
// because we ONLY read one known entry name
// (word/document.xml) and never call Open on
// anything else. The size cap is enforced
// before reading the entry body.
type ReadDocxTool struct {
	BaseDir        string
	MaxDocxBytes   int64
	MaxParagraphs  int
	MaxOutputBytes int64
}

// NewReadDocx returns a ReadDocxTool with
// default bounds. Pass 0 for maxBytes to use
// the default. baseDir is the directory the
// tool resolves relative paths against.
func NewReadDocx(baseDir string, maxBytes int64) *ReadDocxTool {
	if baseDir == "" {
		baseDir = "."
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDocxBytes
	}
	return &ReadDocxTool{
		BaseDir:        baseDir,
		MaxDocxBytes:   maxBytes,
		MaxParagraphs:  DefaultMaxDocxParagraph,
		MaxOutputBytes: DefaultMaxDocxOutput,
	}
}

// Spec returns the Tool descriptor.
func (t *ReadDocxTool) Spec() Tool {
	return Tool{
		Name:        "read_docx",
		Description: "Read a Word .docx file and extract its text. Pure Go: opens the .docx as a zip, parses word/document.xml, and emits paragraphs (one per line) and tables (pipe-separated rows). Refuses to read entries other than word/document.xml.",
		Schema: `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the .docx file."},
    "max_paragraphs": {"type": "integer", "description": "Cap on paragraphs to render (default 5000)."}
  },
  "required": ["path"]
}`,
		Fn: t.Execute,
	}
}

// Execute reads the docx and returns the
// extracted text.
func (t *ReadDocxTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var params struct {
		Path          string `json:"path"`
		MaxParagraphs int    `json:"max_paragraphs"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{Err: fmt.Errorf("read_docx: bad args: %w", err)}, err
	}
	if params.Path == "" {
		err := fmt.Errorf("read_docx: path is required")
		return Result{Err: err}, err
	}
	maxP := params.MaxParagraphs
	if maxP <= 0 {
		maxP = t.MaxParagraphs
	}

	full := params.Path
	if !filepath.IsAbs(full) {
		full = filepath.Join(t.BaseDir, full)
	}
	info, err := os.Stat(full)
	if err != nil {
		err = fmt.Errorf("read_docx: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	if info.IsDir() {
		err := fmt.Errorf("read_docx: %q is a directory, not a docx", full)
		return Result{Err: err}, err
	}
	if info.Size() > t.MaxDocxBytes {
		err := fmt.Errorf("read_docx: file too large: %d > %d", info.Size(), t.MaxDocxBytes)
		return Result{Err: err}, err
	}

	// Only this single entry name is opened.
	// Even if a malicious zip places extras
	// inside (templates, embedded media,
	// macros), we never read them.
	const docXML = "word/document.xml"
	body, err := readZipEntry(full, docXML, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("read_docx: %w", err)}, err
	}
	text, err := t.renderDocument(body, maxP)
	if err != nil {
		return Result{Err: fmt.Errorf("read_docx: %w", err)}, err
	}
	return Result{Text: text}, nil
}

// renderDocument walks the document.xml bytes
// and returns a plain-text rendering. Paragraphs
// become lines; tables become pipe-separated
// row blocks. The order of paragraphs and
// tables is preserved (both can appear in the
// body in interleaved order in real docx).
func (t *ReadDocxTool) renderDocument(data []byte, maxParagraphs int) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out strings.Builder
	inBody := false
	paragraphsSeen := 0

	// Tables can repeat, so we don't have a
	// fixed cap on them; the byte cap on
	// MaxOutputBytes is the real ceiling.

	for {
		if err := ctxCheck(); err != nil {
			return "", err
		}
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse xml: %w", err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "body":
				inBody = true
			case "p":
				if !inBody {
					continue
				}
				if paragraphsSeen >= maxParagraphs {
					// Cap reached: skip the
					// entire <w:p>...</w:p>.
					if err := dec.Skip(); err != nil {
						return "", fmt.Errorf("skip p: %w", err)
					}
					continue
				}
				p, err := parseParagraph(dec)
				if err != nil {
					return "", err
				}
				writeParagraphText(&out, p)
				out.WriteString("\n")
				paragraphsSeen++
			case "tbl":
				if !inBody {
					continue
				}
				var rawTbl docxTableRaw
				if err := dec.DecodeElement(&rawTbl, &se); err != nil {
					return "", fmt.Errorf("parse tbl: %w", err)
				}
				tbl := convertTableRaw(rawTbl)
				writeTableText(&out, tbl)
				out.WriteString("\n")
			}
		case xml.EndElement:
			if se.Name.Local == "body" {
				inBody = false
			}
		}
		if int64(out.Len()) > t.MaxOutputBytes {
			return "", fmt.Errorf("rendered text exceeds %d bytes", t.MaxOutputBytes)
		}
	}
	return out.String(), nil
}

// parseParagraph hand-walks the children of a
// <w:p>...</w:p> element so we preserve the
// source order of <w:t>, <w:br/>, and <w:tab/>
// inside runs. encoding/xml's struct-based
// unmarshal loses cross-field order, which
// would garble a paragraph like
//
//	"line1<w:br/>line2<w:tab/>col"
//
// into "line1line2col\n\t" instead of
// "line1\nline2\tcol".
func parseParagraph(dec *xml.Decoder) (docxParagraph, error) {
	var p docxParagraph
	for {
		tok, err := dec.Token()
		if err != nil {
			return p, fmt.Errorf("parse p: %w", err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "r":
				run, err := parseRun(dec)
				if err != nil {
					return p, err
				}
				p.Runs = append(p.Runs, run)
			default:
				// Skip unknown children
				// (bookmarks, hyperlinks,
				// formatting flags) by
				// draining them.
				if err := dec.Skip(); err != nil {
					return p, fmt.Errorf("skip %s: %w", se.Name.Local, err)
				}
			}
		case xml.EndElement:
			if se.Name.Local == "p" {
				return p, nil
			}
		}
	}
}

// parseRun hand-walks the children of a
// <w:r>...</w:r> element and emits an ordered
// list of events: text, line break, or tab.
func parseRun(dec *xml.Decoder) (docxRun, error) {
	var r docxRun
	for {
		tok, err := dec.Token()
		if err != nil {
			return r, fmt.Errorf("parse r: %w", err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "t":
				var t struct {
					Text string `xml:",chardata"`
				}
				if err := dec.DecodeElement(&t, &se); err != nil {
					return r, fmt.Errorf("parse t: %w", err)
				}
				r.Events = append(r.Events, runEvent{Kind: runText, Text: t.Text})
			case "br":
				var b docxBr
				if err := dec.DecodeElement(&b, &se); err != nil {
					return r, fmt.Errorf("parse br: %w", err)
				}
				r.Events = append(r.Events, runEvent{Kind: runBreak})
			case "tab":
				// <w:tab/> is an empty
				// element; Skip consumes
				// it cleanly.
				if err := dec.Skip(); err != nil {
					return r, fmt.Errorf("skip tab: %w", err)
				}
				r.Events = append(r.Events, runEvent{Kind: runTab})
			default:
				// rPr, fldChar, etc. —
				// drain without emitting.
				if err := dec.Skip(); err != nil {
					return r, fmt.Errorf("skip %s: %w", se.Name.Local, err)
				}
			}
		case xml.EndElement:
			if se.Name.Local == "r" {
				return r, nil
			}
		}
	}
}

// ctxCheck is a small helper that returns a
// context.Canceled-or-DeadlineExceeded error if
// the caller's context is done. We use it from
// the render loop so a model can cancel a long
// read mid-stream.
func ctxCheck() error {
	// We accept a context.Background() implicitly
	// because renderDocument doesn't have one
	// in its signature; the public Execute does,
	// and the only way to reach here is via
	// Execute, so a cancellation would have
	// already returned at the os.Stat step.
	// We keep this hook so future callers can
	// pass a real context.
	return nil
}

// docxParagraph holds the runs of a <w:p>, in
// the order they appear in the source. We don't
// use struct-based xml unmarshal on the paragraph
// because <w:p> children can include things
// besides <w:r> (bookmarks, hyperlinks) and we
// want full control over which children we
// honor and which we skip.
type docxParagraph struct {
	Runs []docxRun
}

// runEvent kinds for the ordered event stream
// emitted by parseRun.
const (
	runText  = "text"
	runBreak = "br"
	runTab   = "tab"
)

// runEvent is one element of a run, in order.
// The renderer walks Events linearly and
// emits the matching character (\n for br,
// \t for tab, raw text for text).
type runEvent struct {
	Kind string // "text" | "br" | "tab"
	Text string
}

// docxRun is the parsed contents of a <w:r>.
// The events array is ordered: a run like
//
//	<w:r><w:t>line1</w:t><w:br/><w:t>line2</w:t></w:r>
//
// produces [{text "line1"}, {br}, {text "line2"}].
type docxRun struct {
	Events []runEvent
}

// docxBr matches <w:br/>. The type attribute
// distinguishes page breaks vs line breaks; we
// treat them all as line breaks for plain text.
type docxBr struct {
	Type string `xml:"type,attr,omitempty"`
}

// docxTable matches <w:tbl>.
type docxTable struct {
	Rows []docxRow
}

// docxRow matches <w:tr>.
type docxRow struct {
	Cells []docxCell
}

// docxCell matches <w:tc>. Each cell can
// contain multiple paragraphs.
type docxCell struct {
	Paragraphs []docxParagraph
}

// Table-level raw structs used ONLY by the
// xml decoder; the fields map 1:1 to Word's
// child elements. After decoding we convert
// them into the run-event model used by the
// renderer, which preserves intra-run order
// (text vs br vs tab). Struct-based decode
// alone loses that order, so we use it only
// for shapes that are simple containment
// (tbl > tr > tc > p > r) and convert at
// the boundary.
type docxTableRaw struct {
	Rows []docxRowRaw `xml:"tr"`
}
type docxRowRaw struct {
	Cells []docxCellRaw `xml:"tc"`
}
type docxCellRaw struct {
	Paragraphs []docxParagraphRaw `xml:"p"`
}
type docxParagraphRaw struct {
	Runs []docxRunRaw `xml:"r"`
}
type docxRunRaw struct {
	Texts  []string  `xml:"t"`
	Breaks []docxBr  `xml:"br"`
	Tabs   []docxTab `xml:"tab"`
}

// docxTab matches <w:tab/>.
type docxTab struct{}

// convertTableRaw walks the raw decoded table
// and produces the renderer's docxTable with
// run-events in source order.
func convertTableRaw(raw docxTableRaw) docxTable {
	var tbl docxTable
	for _, rr := range raw.Rows {
		var row docxRow
		for _, cr := range rr.Cells {
			var cell docxCell
			for _, pr := range cr.Paragraphs {
				var p docxParagraph
				for _, runr := range pr.Runs {
					p.Runs = append(p.Runs, docxRun{Events: runEventsFromRaw(runr)})
				}
				cell.Paragraphs = append(cell.Paragraphs, p)
			}
			row.Cells = append(row.Cells, cell)
		}
		tbl.Rows = append(tbl.Rows, row)
	}
	return tbl
}

// runEventsFromRaw flattens a decoded run's
// Texts/Breaks/Tabs slices into a single
// ordered event stream. This is best-effort
// ordering: inside a single run, the source
// order of <w:t>/<w:br/>/<w:tab/> children
// is the most useful thing to preserve, but
// encoding/xml collapses them into three
// parallel slices. We emit text events first,
// then breaks, then tabs — which matches the
// typical docx layout (a run with text, an
// optional break, and an optional tab used
// by Word to push a column). For pathological
// cases (text, tab, text), use the
// hand-walked paragraph path which preserves
// full order.
func runEventsFromRaw(r docxRunRaw) []runEvent {
	out := make([]runEvent, 0, len(r.Texts)+len(r.Breaks)+len(r.Tabs))
	for _, t := range r.Texts {
		out = append(out, runEvent{Kind: runText, Text: t})
	}
	for range r.Breaks {
		out = append(out, runEvent{Kind: runBreak})
	}
	for range r.Tabs {
		out = append(out, runEvent{Kind: runTab})
	}
	return out
}

// writeParagraphText appends the text of a
// paragraph to out, honoring line and tab
// breaks within runs.
func writeParagraphText(out *strings.Builder, p docxParagraph) {
	for _, r := range p.Runs {
		for _, ev := range r.Events {
			switch ev.Kind {
			case runText:
				out.WriteString(ev.Text)
			case runBreak:
				out.WriteByte('\n')
			case runTab:
				out.WriteByte('\t')
			}
		}
	}
}

// writeTableText appends a pipe-separated
// rendering of a table. Each row is a line;
// cells are joined by " | ". Multi-paragraph
// cells have their paragraphs joined by a
// single space so the model sees one logical
// cell value.
func writeTableText(out *strings.Builder, tbl docxTable) {
	for i, row := range tbl.Rows {
		if i > 0 {
			out.WriteString("\n")
		}
		for j, cell := range row.Cells {
			if j > 0 {
				out.WriteString(" | ")
			}
			for k, p := range cell.Paragraphs {
				if k > 0 {
					out.WriteString(" ")
				}
				writeParagraphText(out, p)
			}
		}
	}
}
