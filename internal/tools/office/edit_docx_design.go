package office

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// docxDesign is deliberately an engine-side concern. The model selects one
// compact preset (and may override its accent); it does not have to emit dozens
// of low-level WordprocessingML formatting operations.
type docxDesign struct {
	Name          string
	Font          string
	DisplayFont   string
	Text          string
	Muted         string
	Accent        string
	AccentDark    string
	AccentLight   string
	Stripe        string
	Page          string
	Border        string
	HeaderFill    string
	HeaderText    string
	CenterCells   bool
	CenterTitle   bool
	Landscape     bool
	TableWidthDXA int
	HeaderHeight  int
	BodyHeight    int
}

func resolveDocxDesign(name, accent string, landscape *bool, maxTableColumns int) (docxDesign, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "auto" {
		name = "polished"
	}

	var design docxDesign
	switch name {
	case "plain":
		design = docxDesign{Name: "plain"}
	case "polished":
		design = docxDesign{
			Name: "polished", Font: "Aptos", DisplayFont: "Aptos Display",
			Text: "263238", Muted: "60717A", Accent: "356A78", AccentDark: "244A54",
			AccentLight: "1F4E78", Stripe: "F1F6F7", Page: "F8FAFA", Border: "7E9CA4",
			HeaderFill: "1F4E78", HeaderText: "FFFFFF", HeaderHeight: 620, BodyHeight: 560,
		}
	case "botanical":
		design = docxDesign{
			Name: "botanical", Font: "Aptos", DisplayFont: "Aptos Display",
			Text: "394239", Muted: "667065", Accent: "78956D", AccentDark: "4E6548",
			AccentLight: "C9D9C1", Stripe: "F1F5EE", Page: "F7FAF4", Border: "85917F",
			HeaderFill: "C9D9C1", HeaderText: "273127", CenterCells: true, CenterTitle: true, HeaderHeight: 720, BodyHeight: 650,
		}
	case "business":
		design = docxDesign{
			Name: "business", Font: "Aptos", DisplayFont: "Aptos Display",
			Text: "202A35", Muted: "5E6A78", Accent: "1F4E78", AccentDark: "163A5A",
			AccentLight: "C6D7E6", Stripe: "F1F5F9", Page: "FAFBFD", Border: "7891A8",
			HeaderFill: "1F4E78", HeaderText: "FFFFFF", HeaderHeight: 620, BodyHeight: 540,
		}
	case "minimal":
		design = docxDesign{
			Name: "minimal", Font: "Aptos", DisplayFont: "Aptos Display",
			Text: "2F3237", Muted: "71757C", Accent: "555B64", AccentDark: "34383E",
			AccentLight: "DADDE1", Stripe: "F7F7F8", Page: "FFFFFF", Border: "A7ABB1",
			HeaderFill: "555B64", HeaderText: "FFFFFF", HeaderHeight: 580, BodyHeight: 520,
		}
	default:
		return docxDesign{}, fmt.Errorf("unknown design %q (want polished|botanical|business|minimal|plain)", name)
	}

	accent = strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(accent), "#"))
	if accent != "" {
		if !docxColorRe.MatchString(accent) {
			return docxDesign{}, fmt.Errorf("accent_color %q must be six hexadecimal RGB digits", accent)
		}
		if design.Name == "plain" {
			return docxDesign{}, fmt.Errorf("accent_color cannot be used with design plain")
		}
		design.Accent = accent
		design.AccentDark = mixDocxColor(accent, "000000", 0.28)
		design.AccentLight = mixDocxColor(accent, "FFFFFF", 0.66)
		design.Stripe = mixDocxColor(accent, "FFFFFF", 0.91)
		design.Page = mixDocxColor(accent, "FFFFFF", 0.965)
		design.Border = mixDocxColor(accent, "3A3A3A", 0.22)
		if design.Name == "botanical" {
			design.HeaderFill = design.AccentLight
		} else {
			design.HeaderFill = accent
		}
		design.HeaderText = contrastingDocxText(design.HeaderFill)
	}

	if design.Name == "plain" {
		if landscape != nil && *landscape {
			return docxDesign{}, fmt.Errorf("landscape cannot be used with design plain")
		}
		design.TableWidthDXA = 9000
		return design, nil
	}
	if landscape != nil {
		design.Landscape = *landscape
	} else {
		// Four or more columns are materially easier to read in landscape. The
		// model therefore does not need another layout decision for schedules.
		design.Landscape = maxTableColumns >= 4
	}
	if design.Landscape {
		design.TableWidthDXA = 13900
	} else {
		design.TableWidthDXA = 9300
	}
	return design, nil
}

func contrastingDocxText(background string) string {
	value := func(pair string) int {
		n, _ := strconv.ParseInt(pair, 16, 0)
		return int(n)
	}
	r, g, b := value(background[0:2]), value(background[2:4]), value(background[4:6])
	if (299*r+587*g+114*b)/1000 >= 150 {
		return "202020"
	}
	return "FFFFFF"
}

func mixDocxColor(a, b string, bWeight float64) string {
	parse := func(s string) [3]int {
		var out [3]int
		for i := range 3 {
			value, _ := strconv.ParseInt(s[i*2:i*2+2], 16, 0)
			out[i] = int(value)
		}
		return out
	}
	left, right := parse(a), parse(b)
	var out strings.Builder
	for i := range 3 {
		value := int(float64(left[i])*(1-bWeight) + float64(right[i])*bWeight + 0.5)
		fmt.Fprintf(&out, "%02X", value)
	}
	return out.String()
}

func docxMaxTableColumns(text string) int {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	maxColumns := 0
	for i := 0; i < len(lines); i++ {
		if rows, _, ok := markdownTableAt(lines, i); ok && len(rows) > 0 && len(rows[0]) > maxColumns {
			maxColumns = len(rows[0])
		}
	}
	return maxColumns
}

func buildDocxDesignedTableXML(rows [][]string, header bool, design docxDesign) []byte {
	columns := len(rows[0])
	gridWidth := design.TableWidthDXA / columns
	cellPct := 5000 / columns
	var out bytes.Buffer
	out.WriteString(`<w:tbl><w:tblPr>`)
	out.WriteString(`<w:tblW w:w="5000" w:type="pct"/><w:tblLayout w:type="fixed"/>`)
	fmt.Fprintf(&out, `<w:tblBorders><w:top w:val="single" w:sz="14" w:color="%s"/><w:left w:val="single" w:sz="14" w:color="%s"/><w:bottom w:val="single" w:sz="14" w:color="%s"/><w:right w:val="single" w:sz="14" w:color="%s"/><w:insideH w:val="single" w:sz="8" w:color="%s"/><w:insideV w:val="single" w:sz="8" w:color="%s"/></w:tblBorders>`,
		design.Border, design.Border, design.Border, design.Border, design.Border, design.Border)
	out.WriteString(`<w:tblCellMar><w:top w:w="135" w:type="dxa"/><w:left w:w="150" w:type="dxa"/><w:bottom w:w="135" w:type="dxa"/><w:right w:w="150" w:type="dxa"/></w:tblCellMar>`)
	out.WriteString(`<w:tblLook w:val="04A0" w:firstRow="1" w:lastRow="0" w:firstColumn="0" w:lastColumn="0" w:noHBand="0" w:noVBand="1"/></w:tblPr><w:tblGrid>`)
	for range columns {
		fmt.Fprintf(&out, `<w:gridCol w:w="%d"/>`, gridWidth)
	}
	out.WriteString(`</w:tblGrid>`)
	for rowIndex, row := range rows {
		out.Write(buildDocxDesignedTableRowXML(row, header && rowIndex == 0, rowIndex, cellPct, design))
	}
	out.WriteString(`</w:tbl>`)
	return out.Bytes()
}

func buildDocxDesignedTableRowXML(row []string, header bool, rowIndex, cellPct int, design docxDesign) []byte {
	height := design.BodyHeight
	if header {
		height = design.HeaderHeight
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, `<w:tr><w:trPr><w:cantSplit/><w:trHeight w:val="%d" w:hRule="atLeast"/>`, height)
	if header {
		out.WriteString(`<w:tblHeader/>`)
	}
	out.WriteString(`</w:trPr>`)
	for _, value := range row {
		fill := "FFFFFF"
		if header {
			fill = design.HeaderFill
		} else if rowIndex%2 == 0 {
			fill = design.Stripe
		}
		fmt.Fprintf(&out, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="pct"/><w:vAlign w:val="center"/><w:shd w:val="clear" w:color="auto" w:fill="%s"/></w:tcPr>`, cellPct, fill)
		out.WriteString(`<w:p><w:pPr><w:spacing w:before="0" w:after="0" w:line="240" w:lineRule="auto"/>`)
		if header || design.CenterCells {
			out.WriteString(`<w:jc w:val="center"/>`)
		}
		out.WriteString(`</w:pPr><w:r><w:rPr>`)
		fmt.Fprintf(&out, `<w:rFonts w:ascii="%s" w:hAnsi="%s" w:eastAsia="%s"/><w:sz w:val="20"/><w:szCs w:val="20"/><w:color w:val="%s"/>`,
			xmlEscapeText(design.Font), xmlEscapeText(design.Font), xmlEscapeText(design.Font), design.Text)
		if header {
			fmt.Fprintf(&out, `<w:b/><w:color w:val="%s"/><w:spacing w:val="12"/>`, design.HeaderText)
		}
		out.WriteString(`</w:rPr>`)
		writeDocxInlineText(&out, strings.ReplaceAll(value, "<br>", "\n"))
		out.WriteString(`</w:r></w:p></w:tc>`)
	}
	out.WriteString(`</w:tr>`)
	return out.Bytes()
}

func writeDesignedDocx(path string, contentXML []byte, design docxDesign) error {
	if design.Name == "plain" {
		return writeMinimalDocx(path, contentXML)
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := writeDesignedDocxTo(file, contentXML, design); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeDesignedDocxTo(dst io.Writer, contentXML []byte, design docxDesign) error {
	zw := zip.NewWriter(dst)
	pageWidth, pageHeight, orientation := 12240, 15840, ""
	marginHorizontal, marginVertical := 900, 850
	if design.Landscape {
		pageWidth, pageHeight, orientation = 15840, 12240, ` w:orient="landscape"`
		marginHorizontal, marginVertical = 720, 680
	}
	body := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="` + wordProcessingNS + `"><w:background w:color="` + design.Page + `"/><w:body>` +
		string(contentXML) +
		fmt.Sprintf(`<w:sectPr><w:pgSz w:w="%d" w:h="%d"%s/><w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="480" w:footer="480" w:gutter="0"/><w:pgBorders w:offsetFrom="page"><w:top w:val="single" w:sz="10" w:space="18" w:color="%s"/><w:left w:val="single" w:sz="10" w:space="18" w:color="%s"/><w:bottom w:val="single" w:sz="10" w:space="18" w:color="%s"/><w:right w:val="single" w:sz="10" w:space="18" w:color="%s"/></w:pgBorders></w:sectPr>`,
			pageWidth, pageHeight, orientation, marginVertical, marginHorizontal, marginVertical, marginHorizontal,
			design.AccentLight, design.AccentLight, design.AccentLight, design.AccentLight) +
		`</w:body></w:document>`
	types := strings.Replace(docxContentTypes, `</Types>`, `  <Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>
</Types>`, 1)
	rels := strings.Replace(docxDocumentRels, `</Relationships>`, `  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/>
</Relationships>`, 1)
	settings := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:settings xmlns:w="` + wordProcessingNS + `"><w:displayBackgroundShape/><w:defaultTabStop w:val="720"/><w:compat/></w:settings>`
	entries := []struct{ name, data string }{
		{"[Content_Types].xml", types},
		{"_rels/.rels", docxRels},
		{"word/_rels/document.xml.rels", rels},
		{"word/styles.xml", designedDocxStyles(design)},
		{"word/settings.xml", settings},
		{docxDocumentEntry, body},
	}
	for _, entry := range entries {
		writer, err := zw.Create(entry.name)
		if err != nil {
			return err
		}
		if _, err := writer.Write([]byte(entry.data)); err != nil {
			return err
		}
	}
	return zw.Close()
}

func designedDocxStyles(design docxDesign) string {
	titleAlignment := "left"
	if design.CenterTitle {
		titleAlignment = "center"
	}
	font := xmlEscapeText(design.Font)
	displayFont := xmlEscapeText(design.DisplayFont)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="%s">
  <w:docDefaults>
    <w:rPrDefault><w:rPr><w:rFonts w:ascii="%s" w:hAnsi="%s" w:eastAsia="%s" w:cs="%s"/><w:color w:val="%s"/><w:sz w:val="22"/><w:szCs w:val="22"/><w:lang w:val="pl-PL" w:eastAsia="en-US"/></w:rPr></w:rPrDefault>
    <w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault>
  </w:docDefaults>
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="80" w:after="260"/><w:jc w:val="%s"/><w:pBdr><w:bottom w:val="single" w:sz="14" w:space="10" w:color="%s"/></w:pBdr></w:pPr><w:rPr><w:rFonts w:ascii="%s" w:hAnsi="%s"/><w:b/><w:color w:val="%s"/><w:sz w:val="46"/><w:szCs w:val="46"/><w:spacing w:val="24"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="260" w:after="100"/></w:pPr><w:rPr><w:rFonts w:ascii="%s" w:hAnsi="%s"/><w:b/><w:color w:val="%s"/><w:sz w:val="32"/><w:szCs w:val="32"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="200" w:after="80"/></w:pPr><w:rPr><w:b/><w:color w:val="%s"/><w:sz w:val="26"/><w:szCs w:val="26"/></w:rPr></w:style>
</w:styles>`,
		wordProcessingNS, font, font, font, font, design.Text,
		titleAlignment, design.Accent, displayFont, displayFont, design.AccentDark,
		displayFont, displayFont, design.AccentDark, design.Accent)
}
