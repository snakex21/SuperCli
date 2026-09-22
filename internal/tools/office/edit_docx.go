// edit_docx.go implements the edit_docx tool: pure-Go editing
// of Word .docx files (replace text, append paragraphs, create
// new documents). A .docx is a zip archive whose main content
// is word/document.xml; the tool only rewrites that one entry
// and copies every other zip entry byte-for-byte, so styles,
// images, headers/footers and metadata survive untouched.
//
// Editing strategy: we do NOT re-encode the whole XML tree
// (encoding/xml round-trips mangle Word's namespaces).
// Instead we splice bytes:
//
//   - replace: scan word/document.xml for <w:p>...</w:p>
//     spans. For each paragraph, concatenate the text of all
//     its runs (so a search string split across runs is still
//     found), then splice only the affected text nodes. Run and
//     paragraph properties remain byte-for-byte intact. A
//     replacement spanning multiple runs inherits the first
//     affected run's formatting; unaffected text keeps its own.
//
//   - append: new paragraph XML is spliced in just before
//     <w:sectPr> (or </w:body>).
//
//   - create: a minimal valid .docx (content types, rels,
//     styles with Heading 1-3, document.xml) is built from
//     scratch.
//
// Markdown-ish input: lines starting with "# ", "## ", "### "
// become Heading1/2/3; everything else is a normal paragraph.
// Blank lines become empty paragraphs.
//
// Safety: writes go to a temp file which is swapped in only
// when complete, and an existing file is copied to
// "<name>.docx.bak" before being replaced.
package office

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"supercli/internal/tools/fileops"
)

const docxDocumentEntry = "word/document.xml"

// EditDocxTool edits or creates .docx files.
type EditDocxTool struct {
	BaseDir      string
	MaxDocxBytes int64
}

// NewEditDocx returns an EditDocxTool rooted at baseDir.
func NewEditDocx(baseDir string) *EditDocxTool {
	if baseDir == "" {
		baseDir = "."
	}
	return &EditDocxTool{BaseDir: baseDir, MaxDocxBytes: DefaultMaxDocxBytes}
}

// Spec returns the Tool descriptor.
func (t *EditDocxTool) Spec() Tool {
	return Tool{
		Name: "edit_docx",
		Description: "Primary native editor for Word .docx files. Use it directly; never use Python, PowerShell, shell, Word COM, or unpacked OOXML for a supported Word edit. " +
			"For any non-trivial change use action='batch' and send every operation in operations[] in one call; the whole batch is transactional, writes the DOCX once, and creates one .bak. " +
			"Read once with read_docx selectors=true, then batch precise edits. Operations run in array order. Text is UTF-8/Unicode and preserves ąćęłńóśźż and other scripts. " +
			"Available operations: replace/replace_many, replace_at, suggest_at, comment_at, append, insert_after, insert_table, table_add_row/table_add_rows, insert_image, format_at, insert_page_break, insert_link, delete_at, and clone. " +
			"Selectors come from read_docx. Pipe or tab text creates native editable tables; Markdown headings/lists/tables become native Word structures. PNG/JPEG images are embedded with aspect ratio and alt text. " +
			"For a new file use create exactly once. create applies a complete engine-side design in the same call: polished (default), botanical, business, minimal, or plain. Wide tables automatically use landscape. " +
			"Unedited styles, media, headers and document parts are preserved. Mixed per-word formatting is retained; replacement text inherits the first affected run. dry_run validates without changing the original.",
		Schema: `{
  "type": "object",
  "properties": {
    "path":    {"type": "string", "description": "Path to the .docx file (relative paths resolve against the working directory)."},
    "action":  {"type": "string", "enum": ["batch", "replace", "replace_many", "replace_at", "suggest_at", "comment_at", "append", "insert_after", "insert_table", "table_add_row", "table_add_rows", "insert_image", "format_at", "insert_page_break", "insert_link", "delete_at", "clone", "create"], "description": "Use batch for more than one change."},
    "operations": {"type": "array", "minItems": 1, "maxItems": 100, "description": "batch: all operations executed in order as one atomic edit. Each item repeats the same action-specific fields listed below, but never path, dry_run, operations, batch or create.", "items": {"type": "object", "description": "One normal edit_docx operation with action plus its fields.", "additionalProperties": true}},
    "find":    {"type": "string", "description": "replace: the exact text to find (case-sensitive)."},
    "replace": {"type": "string", "description": "replace: the replacement text (may be empty to delete)."},
    "replacements": {"type": "array", "description": "replace_many: exact replacements applied atomically in order.", "minItems": 1, "items": {"type": "object", "properties": {"find": {"type": "string"}, "replace": {"type": "string"}, "expected_count": {"type": "integer", "minimum": 1}}, "required": ["find", "replace"], "additionalProperties": false}},
    "text":    {"type": "string", "description": "Replacement/new content. Paragraph input supports headings, lists and Markdown tables. insert_table/table_add_row accept pipe- or tab-separated cells. Unicode is preserved."},
    "style":   {"type": "string", "description": "append: semantic style for appended paragraphs; format_at: Word paragraph style id."},
    "style_mode": {"type": "string", "enum": ["match", "plain"], "description": "append: 'match' (default) clones paragraph/run formatting from a similar existing paragraph; 'plain' uses only the requested built-in style."},
    "source": {"type": "string", "description": "Selector from read_docx selectors=true. table_add_row uses /body/tbl[N]; delete_at also accepts /body/tbl[N]/tr[N]."},
    "comment": {"type": "string", "description": "comment_at: review comment text."},
    "author": {"type": "string", "description": "suggest_at/comment_at: reviewer name (default SuperCli)."},
    "initials": {"type": "string", "description": "comment_at: reviewer initials (derived from author when omitted)."},
    "after": {"type": "string", "description": "Insertion point: /body/p[N] or /body/tbl[N]. Use 'end' (default for insert_table) to append before section properties."},
    "header_row": {"type": "boolean", "description": "insert_table: style the first row as a repeating header (default true)."},
    "image_path": {"type": "string", "description": "insert_image: PNG or JPEG path inside the workspace."},
    "width_cm": {"type": "number", "description": "insert_image: displayed width in centimeters (default 14.5, range 0.5..30); aspect ratio is preserved."},
    "alt_text": {"type": "string", "description": "insert_image: accessibility description; defaults to the image filename."},
    "design": {"type": "string", "enum": ["polished", "botanical", "business", "minimal", "plain"], "description": "create: complete visual preset applied during creation; default polished. Use botanical for green/natural references."},
    "accent_color": {"type": "string", "description": "create: optional six-digit RGB accent override; the engine derives coordinated tints automatically."},
    "landscape": {"type": "boolean", "description": "create: optional page orientation override; omitted means automatic landscape for tables with 4+ columns."},
    "bold": {"type": "boolean", "description": "format_at: enable or disable bold for text runs."},
    "italic": {"type": "boolean", "description": "format_at: enable or disable italic for text runs."},
    "underline": {"type": "boolean", "description": "format_at: enable or disable underline for text runs."},
    "font_size_pt": {"type": "number", "description": "format_at: font size in points, range 6..96."},
    "color": {"type": "string", "description": "format_at: six-digit RGB text color, for example 1F4E78."},
    "alignment": {"type": "string", "enum": ["left", "center", "right", "justify"], "description": "format_at: paragraph or table-cell paragraph alignment."},
    "url": {"type": "string", "description": "insert_link: absolute http, https or mailto URL. text is the visible link label."},
    "dry_run": {"type": "boolean", "description": "Validate and describe the change without writing the document."},
    "include_headers": {"type": "boolean", "description": "replace: also replace matching text in word/header*.xml."},
    "include_footers": {"type": "boolean", "description": "replace: also replace matching text in word/footer*.xml."}
  },
  "required": ["path", "action"]
}`,
		Fn: t.Execute,
	}
}

type editDocxArgs struct {
	Path           string                `json:"path"`
	Action         string                `json:"action"`
	Find           string                `json:"find"`
	Replace        string                `json:"replace"`
	Replacements   []editDocxReplacement `json:"replacements"`
	Text           string                `json:"text"`
	Style          string                `json:"style"`
	StyleMode      string                `json:"style_mode"`
	Source         string                `json:"source"`
	After          string                `json:"after"`
	DryRun         bool                  `json:"dry_run"`
	IncludeHeaders bool                  `json:"include_headers"`
	IncludeFooters bool                  `json:"include_footers"`
	Comment        string                `json:"comment"`
	Author         string                `json:"author"`
	Initials       string                `json:"initials"`
	HeaderRow      *bool                 `json:"header_row"`
	ImagePath      string                `json:"image_path"`
	WidthCM        float64               `json:"width_cm"`
	AltText        string                `json:"alt_text"`
	Design         string                `json:"design"`
	AccentColor    string                `json:"accent_color"`
	Landscape      *bool                 `json:"landscape"`
	Bold           *bool                 `json:"bold"`
	Italic         *bool                 `json:"italic"`
	Underline      *bool                 `json:"underline"`
	FontSizePT     float64               `json:"font_size_pt"`
	Color          string                `json:"color"`
	Alignment      string                `json:"alignment"`
	URL            string                `json:"url"`
	Operations     []editDocxOperation   `json:"operations"`
}

type editDocxOperation struct {
	Action         string                `json:"action"`
	Find           string                `json:"find"`
	Replace        string                `json:"replace"`
	Replacements   []editDocxReplacement `json:"replacements"`
	Text           string                `json:"text"`
	Style          string                `json:"style"`
	StyleMode      string                `json:"style_mode"`
	Source         string                `json:"source"`
	After          string                `json:"after"`
	IncludeHeaders bool                  `json:"include_headers"`
	IncludeFooters bool                  `json:"include_footers"`
	Comment        string                `json:"comment"`
	Author         string                `json:"author"`
	Initials       string                `json:"initials"`
	HeaderRow      *bool                 `json:"header_row"`
	ImagePath      string                `json:"image_path"`
	WidthCM        float64               `json:"width_cm"`
	AltText        string                `json:"alt_text"`
	Design         string                `json:"design"`
	AccentColor    string                `json:"accent_color"`
	Landscape      *bool                 `json:"landscape"`
	Bold           *bool                 `json:"bold"`
	Italic         *bool                 `json:"italic"`
	Underline      *bool                 `json:"underline"`
	FontSizePT     float64               `json:"font_size_pt"`
	Color          string                `json:"color"`
	Alignment      string                `json:"alignment"`
	URL            string                `json:"url"`
}

func (op editDocxOperation) args() editDocxArgs {
	return editDocxArgs{
		Action: op.Action, Find: op.Find, Replace: op.Replace, Replacements: op.Replacements,
		Text: op.Text, Style: op.Style, StyleMode: op.StyleMode, Source: op.Source, After: op.After,
		IncludeHeaders: op.IncludeHeaders, IncludeFooters: op.IncludeFooters,
		Comment: op.Comment, Author: op.Author, Initials: op.Initials, HeaderRow: op.HeaderRow,
		ImagePath: op.ImagePath, WidthCM: op.WidthCM, AltText: op.AltText,
		Design: op.Design, AccentColor: op.AccentColor, Landscape: op.Landscape,
		Bold: op.Bold, Italic: op.Italic, Underline: op.Underline, FontSizePT: op.FontSizePT,
		Color: op.Color, Alignment: op.Alignment, URL: op.URL,
	}
}

// Execute dispatches on action.
func (t *EditDocxTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var p editDocxArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return Result{Err: fmt.Errorf("edit_docx: bad args: %w", err)}, err
	}
	full, err := resolveSandboxed(t.BaseDir, p.Path)
	if err != nil {
		err = fmt.Errorf("edit_docx: %w", err)
		return Result{Err: err}, err
	}
	release := fileops.LockMutationPaths(full)
	defer release()
	return t.executeResolved(full, p)
}

func (t *EditDocxTool) executeResolved(full string, p editDocxArgs) (Result, error) {
	switch p.Action {
	case "batch":
		return t.doBatch(full, p)
	case "replace":
		return t.doReplace(full, p)
	case "replace_many":
		return t.doReplaceMany(full, p)
	case "replace_at":
		return t.doReplaceAt(full, p)
	case "suggest_at":
		return t.doSuggestAt(full, p)
	case "comment_at":
		return t.doCommentAt(full, p)
	case "append":
		return t.doAppend(full, p)
	case "insert_after":
		return t.doInsertAfter(full, p)
	case "insert_table":
		return t.doInsertTable(full, p)
	case "table_add_row":
		return t.doTableAddRow(full, p)
	case "table_add_rows":
		return t.doTableAddRow(full, p)
	case "insert_image":
		return t.doInsertImage(full, p)
	case "format_at":
		return t.doFormatAt(full, p)
	case "insert_page_break":
		return t.doInsertPageBreak(full, p)
	case "insert_link":
		return t.doInsertLink(full, p)
	case "delete_at":
		return t.doDeleteAt(full, p)
	case "clone":
		return t.doClone(full, p)
	case "create":
		return t.doCreate(full, p)
	default:
		err := fmt.Errorf("edit_docx: unknown action %q", p.Action)
		return Result{Err: err}, err
	}
}

type editDocxReplacement struct {
	Find          string `json:"find"`
	Replace       string `json:"replace"`
	ExpectedCount int    `json:"expected_count"`
}

func (t *EditDocxTool) loadDocumentXML(full string) ([]byte, error) {
	info, err := os.Stat(full)
	if err != nil {
		return nil, fileops.FileErr(err, full)
	}
	if info.Size() > t.MaxDocxBytes {
		return nil, fmt.Errorf("file too large: %d > %d", info.Size(), t.MaxDocxBytes)
	}
	return readZipEntry(full, docxDocumentEntry, t.MaxDocxBytes)
}
