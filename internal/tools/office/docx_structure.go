package office

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

type docxParagraphLocation struct {
	docxParagraphTemplate
	selector string
}

// collectDocxParagraphLocations exposes stable selectors for both direct body
// paragraphs and paragraphs in top-level table cells. Nested tables remain
// untouched: their coordinates are easy to misinterpret and uncommon enough
// that silently editing one is worse than asking for a narrower feature later.
func collectDocxParagraphLocations(doc []byte) ([]docxParagraphLocation, error) {
	body := collectDocxParagraphTemplates(doc)
	locations := make([]docxParagraphLocation, 0, len(body))
	for i, paragraph := range body {
		locations = append(locations, docxParagraphLocation{
			docxParagraphTemplate: paragraph,
			selector:              fmt.Sprintf("/body/p[%d]", i+1),
		})
	}

	dec := xml.NewDecoder(bytes.NewReader(doc))
	inBody, inRow, inCell := false, false, false
	tableDepth, tableIndex, rowIndex, cellIndex, paragraphIndex := 0, 0, 0, 0, 0
	type openParagraph struct {
		start    int
		selector string
	}
	var active *openParagraph
	for {
		before := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse document structure: %w", err)
		}
		switch element := tok.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "body":
				inBody = true
			case "tbl":
				if inBody && tableDepth == 0 {
					tableIndex++
					rowIndex, cellIndex, paragraphIndex = 0, 0, 0
				}
				if inBody {
					tableDepth++
				}
			case "tr":
				if inBody && tableDepth == 1 {
					rowIndex++
					cellIndex = 0
					inRow = true
				}
			case "tc":
				if inBody && tableDepth == 1 && inRow {
					cellIndex++
					paragraphIndex = 0
					inCell = true
				}
			case "p":
				if inBody && tableDepth == 1 && inCell {
					paragraphIndex++
					active = &openParagraph{
						start:    before,
						selector: fmt.Sprintf("/body/tbl[%d]/tr[%d]/tc[%d]/p[%d]", tableIndex, rowIndex, cellIndex, paragraphIndex),
					}
				}
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "p":
				if active != nil {
					end := int(dec.InputOffset())
					frag := doc[active.start:end]
					gt := bytes.IndexByte(frag, '>')
					if gt < 0 {
						return nil, fmt.Errorf("malformed paragraph at %s", active.selector)
					}
					text, err := docxParagraphText(frag)
					if err != nil {
						return nil, fmt.Errorf("%s: %w", active.selector, err)
					}
					locations = append(locations, docxParagraphLocation{
						docxParagraphTemplate: docxParagraphTemplate{
							frag: frag, openTag: frag[:gt+1], style: docxParagraphStyle(frag), text: text,
							start: active.start, end: end,
						},
						selector: active.selector,
					})
					active = nil
				}
			case "tc":
				if inBody && tableDepth == 1 && inCell {
					inCell = false
				}
			case "tr":
				if inBody && tableDepth == 1 && inRow {
					inRow = false
				}
			case "tbl":
				if inBody && tableDepth > 0 {
					tableDepth--
				}
			case "body":
				inBody = false
			}
		}
	}
	sort.SliceStable(locations, func(i, j int) bool { return locations[i].start < locations[j].start })
	return locations, nil
}

func findDocxParagraphLocation(doc []byte, selector string) (docxParagraphLocation, error) {
	locations, err := collectDocxParagraphLocations(doc)
	if err != nil {
		return docxParagraphLocation{}, err
	}
	selector = strings.TrimSpace(selector)
	for _, location := range locations {
		if strings.EqualFold(location.selector, selector) {
			return location, nil
		}
	}
	return docxParagraphLocation{}, fmt.Errorf("selector %q was not found; use read_docx with selectors=true", selector)
}

func listDocxStoryParts(path string, headers, footers bool, maxPartBytes int64) ([]string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open docx stories: %w", err)
	}
	defer zr.Close()
	var names []string
	for _, file := range zr.File {
		name := strings.ToLower(file.Name)
		wanted := headers && strings.HasPrefix(name, "word/header") && strings.HasSuffix(name, ".xml")
		wanted = wanted || (footers && strings.HasPrefix(name, "word/footer") && strings.HasSuffix(name, ".xml"))
		if !wanted {
			continue
		}
		if int64(file.UncompressedSize64) > maxPartBytes {
			return nil, fmt.Errorf("docx part %s is too large: %d > %d", file.Name, file.UncompressedSize64, maxPartBytes)
		}
		names = append(names, file.Name)
	}
	sort.Strings(names)
	return names, nil
}

func renderDocxStoryPart(data []byte, maxParagraphs int, maxOutputBytes int64) (string, error) {
	var out strings.Builder
	pos, count := 0, 0
	for count < maxParagraphs {
		open, openEnd, selfClosing := findNextParagraph(data, pos)
		if open < 0 {
			break
		}
		if selfClosing {
			pos = openEnd
			continue
		}
		closeAt := bytes.Index(data[openEnd:], []byte("</w:p>"))
		if closeAt < 0 {
			return "", fmt.Errorf("malformed story part: unclosed paragraph")
		}
		end := openEnd + closeAt + len("</w:p>")
		text, err := docxParagraphText(data[open:end])
		if err != nil {
			return "", err
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(text)
		if int64(out.Len()) > maxOutputBytes {
			return "", fmt.Errorf("rendered story part exceeds %d bytes", maxOutputBytes)
		}
		count++
		pos = end
	}
	return out.String(), nil
}
