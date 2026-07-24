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
		Description: "Edit or create a Word .docx file. Pure Go, no Word required. " +
			"Use this when the user wants to change a Word document: fix wording, do a find-and-replace, " +
			"add paragraphs or headings at the end, or create a brand-new document from text. " +
			"Actions: 'replace' (find/replace across the whole document; finds text even when Word split it " +
			"across formatting runs; optional header/footer inclusion), 'append' (add paragraphs at the end, optional style: Heading1-3 or bold; " +
			"by default new paragraphs match an existing paragraph of the same role), " +
			"'clone' (copy a paragraph selected as /body/p[N], optionally replace its text, and insert it after " +
			"another paragraph while preserving its formatting), " +
			"'replace_at' (replace exactly one selected paragraph while preserving its style), " +
			"'suggest_at' (the same precise edit as a minimal Word tracked change), " +
			"'comment_at' (attach a review comment to one selected paragraph without changing its text), " +
			"'create' (new .docx from text; lines starting with '# ', '## ', '### ' become headings). " +
			"Safety: before an existing file is changed, a backup copy is saved next to it as '<name>.bak', " +
			"and the write is atomic — tell the user the backup exists if they want to undo. " +
			"Everything not edited (images, styles, headers, tables) is preserved byte-for-byte. Mixed " +
			"per-word formatting is retained; replacement text inherits the first affected run's formatting. " +
			"Set style_mode='plain' only when document-style matching is not wanted. " +
			"Do NOT use this for spreadsheets (use edit_xlsx), " +
			"PDFs (cannot be edited), or plain text files (use the line-edit tools). " +
			"Set dry_run=true to validate and preview an edit without writing or creating a backup. " +
			"Do NOT use 'create' to overwrite an existing file the user did not ask to replace.",
		Schema: `{
  "type": "object",
  "properties": {
    "path":    {"type": "string", "description": "Path to the .docx file (relative paths resolve against the working directory)."},
    "action":  {"type": "string", "enum": ["replace", "replace_at", "suggest_at", "comment_at", "append", "clone", "create"], "description": "What to do."},
    "find":    {"type": "string", "description": "replace: the exact text to find (case-sensitive)."},
    "replace": {"type": "string", "description": "replace: the replacement text (may be empty to delete)."},
    "text":    {"type": "string", "description": "replace_at/clone/append/create: replacement or new content. Append/create use one paragraph per line; '# ', '## ', '### ' prefixes become headings."},
    "style":   {"type": "string", "description": "append: optional semantic style for all appended paragraphs: 'Heading1', 'Heading2', 'Heading3', 'bold', or 'normal'."},
    "style_mode": {"type": "string", "enum": ["match", "plain"], "description": "append: 'match' (default) clones paragraph/run formatting from a similar existing paragraph; 'plain' uses only the requested built-in style."},
    "source": {"type": "string", "description": "replace_at/suggest_at/comment_at/clone: selector from read_docx selectors=true."},
    "comment": {"type": "string", "description": "comment_at: review comment text."},
    "author": {"type": "string", "description": "suggest_at/comment_at: reviewer name (default SuperCli)."},
    "initials": {"type": "string", "description": "comment_at: reviewer initials (derived from author when omitted)."},
    "after": {"type": "string", "description": "clone: insert after /body/p[N]; defaults to source. Use 'end' to append before section properties."},
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
	Path           string `json:"path"`
	Action         string `json:"action"`
	Find           string `json:"find"`
	Replace        string `json:"replace"`
	Text           string `json:"text"`
	Style          string `json:"style"`
	StyleMode      string `json:"style_mode"`
	Source         string `json:"source"`
	After          string `json:"after"`
	DryRun         bool   `json:"dry_run"`
	IncludeHeaders bool   `json:"include_headers"`
	IncludeFooters bool   `json:"include_footers"`
	Comment        string `json:"comment"`
	Author         string `json:"author"`
	Initials       string `json:"initials"`
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
	switch p.Action {
	case "replace":
		return t.doReplace(full, p)
	case "replace_at":
		return t.doReplaceAt(full, p)
	case "suggest_at":
		return t.doSuggestAt(full, p)
	case "comment_at":
		return t.doCommentAt(full, p)
	case "append":
		return t.doAppend(full, p)
	case "clone":
		return t.doClone(full, p)
	case "create":
		return t.doCreate(full, p)
	default:
		err := fmt.Errorf("edit_docx: unknown action %q (want replace|replace_at|suggest_at|comment_at|append|clone|create)", p.Action)
		return Result{Err: err}, err
	}
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
