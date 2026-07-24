package office

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
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
		Description: "Read a Word .docx file and extract its current text (accepted base plus tracked insertions, without tracked deletions). Pure Go. Set selectors=true for stable paragraph paths used by precise edits; formatting=true adds compact style hints. Headers, footers and review comments are opt-in.",
		Schema: `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the .docx file."},
    "max_paragraphs": {"type": "integer", "description": "Cap on paragraphs to render (default 5000)."},
    "selectors": {"type": "boolean", "description": "Emit direct body paragraphs as /body/p[N] [style=ID] text."},
    "formatting": {"type": "boolean", "description": "With selectors, include compact direct-format hints such as alignment, font, size, bold, color and spacing."},
    "include_headers": {"type": "boolean", "description": "Also extract word/header*.xml text."},
    "include_footers": {"type": "boolean", "description": "Also extract word/footer*.xml text."},
    "include_comments": {"type": "boolean", "description": "Also list Word review comments with id and author."}
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
		Path            string `json:"path"`
		MaxParagraphs   int    `json:"max_paragraphs"`
		Selectors       bool   `json:"selectors"`
		Formatting      bool   `json:"formatting"`
		IncludeHeaders  bool   `json:"include_headers"`
		IncludeFooters  bool   `json:"include_footers"`
		IncludeComments bool   `json:"include_comments"`
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

	full, err := resolveSandboxed(t.BaseDir, params.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("read_docx: %w", err)}, nil
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
	var text string
	if params.Selectors {
		text, err = t.renderDocumentSelectors(body, maxP, params.Formatting)
	} else {
		text, err = t.renderDocument(body, maxP)
	}
	if err != nil {
		return Result{Err: fmt.Errorf("read_docx: %w", err)}, err
	}
	if params.IncludeHeaders || params.IncludeFooters {
		parts, partErr := listDocxStoryParts(full, params.IncludeHeaders, params.IncludeFooters, t.MaxDocxBytes)
		if partErr != nil {
			return Result{Err: fmt.Errorf("read_docx: %w", partErr)}, partErr
		}
		var stories strings.Builder
		for _, name := range parts {
			part, readErr := readZipEntry(full, name, t.MaxDocxBytes)
			if readErr != nil {
				return Result{Err: fmt.Errorf("read_docx: %s: %w", name, readErr)}, readErr
			}
			rendered, renderErr := renderDocxStoryPart(part, maxP, t.MaxOutputBytes)
			if renderErr != nil {
				return Result{Err: fmt.Errorf("read_docx: %s: %w", name, renderErr)}, renderErr
			}
			fmt.Fprintf(&stories, "\n== %s ==\n%s\n", name, rendered)
		}
		if int64(len(text)+stories.Len()) > t.MaxOutputBytes {
			return Result{Err: fmt.Errorf("read_docx: rendered document and stories exceed %d bytes", t.MaxOutputBytes)}, nil
		}
		text += stories.String()
	}
	if params.IncludeComments {
		comments, exists, readErr := readOptionalZipEntry(full, docxCommentsEntry, t.MaxDocxBytes)
		if readErr != nil {
			return Result{Err: fmt.Errorf("read_docx: comments: %w", readErr)}, readErr
		}
		if exists {
			rendered, renderErr := renderDocxComments(comments, t.MaxOutputBytes-int64(len(text)))
			if renderErr != nil {
				return Result{Err: fmt.Errorf("read_docx: comments: %w", renderErr)}, renderErr
			}
			text += rendered
		}
	}
	return Result{Text: text}, nil
}

func (t *ReadDocxTool) renderDocumentSelectors(data []byte, maxParagraphs int, formatting bool) (string, error) {
	paragraphs, err := collectDocxParagraphLocations(data)
	if err != nil {
		return "", err
	}
	if len(paragraphs) > maxParagraphs {
		paragraphs = paragraphs[:maxParagraphs]
	}
	var out strings.Builder
	for _, paragraph := range paragraphs {
		style := paragraph.style
		if style == "" {
			style = "Normal"
		}
		text := strings.ReplaceAll(paragraph.text, "\n", `\n`)
		text = strings.ReplaceAll(text, "\t", `\t`)
		format := ""
		if formatting {
			format = docxParagraphFormatSummary(paragraph.frag)
		}
		fmt.Fprintf(&out, "%s [style=%s%s] %s\n", paragraph.selector, style, format, text)
		if int64(out.Len()) > t.MaxOutputBytes {
			return "", fmt.Errorf("rendered selector text exceeds %d bytes", t.MaxOutputBytes)
		}
	}
	return out.String(), nil
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
