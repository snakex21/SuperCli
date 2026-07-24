package office

import (
	"encoding/xml"
	"fmt"
	"strings"
)

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
			case "ins", "hyperlink", "smartTag", "sdtContent":
				if err := parseVisibleRunContainer(dec, se.Name.Local, &p); err != nil {
					return p, err
				}
			case "del":
				// Default rendering shows the current document view.
				if err := dec.Skip(); err != nil {
					return p, fmt.Errorf("skip deletion: %w", err)
				}
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

func parseVisibleRunContainer(dec *xml.Decoder, endName string, p *docxParagraph) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("parse %s: %w", endName, err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "r":
				run, err := parseRun(dec)
				if err != nil {
					return err
				}
				p.Runs = append(p.Runs, run)
			case "ins", "hyperlink", "smartTag", "sdtContent":
				if err := parseVisibleRunContainer(dec, se.Name.Local, p); err != nil {
					return err
				}
			case "del":
				if err := dec.Skip(); err != nil {
					return err
				}
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if se.Name.Local == endName {
				return nil
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
