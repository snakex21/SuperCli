package web

import (
	"regexp"
	"strings"
)

// Sites increasingly answer "Accept: text/markdown" with a generated
// Markdown rendering of the whole page. It skips HTML extraction, so image
// galleries and repeated banners would fill the model's window. These
// helpers keep the Markdown but drop that noise.

var (
	markdownImage   = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownBlanks  = regexp.MustCompile(`\n{3,}`)
	frontMatterLine = regexp.MustCompile(`^([A-Za-z_]+):\s*"?(.*?)"?\s*$`)
)

func fetchedMarkdown(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/markdown") || strings.Contains(ct, "text/x-markdown")
}

// cleanMarkdown removes YAML front matter (returning its title and
// description), image embeds, empty bullets and repeated paragraphs.
func cleanMarkdown(text string) (title, description, body string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if end := strings.Index(rest, "\n---"); end >= 0 {
			for _, line := range strings.Split(rest[:end], "\n") {
				if m := frontMatterLine.FindStringSubmatch(line); m != nil {
					switch strings.ToLower(m[1]) {
					case "title":
						title = m[2]
					case "description":
						description = m[2]
					}
				}
			}
			text = strings.TrimPrefix(rest[end+len("\n---"):], "\n")
		}
	}
	text = markdownImage.ReplaceAllString(text, "")
	return title, description, dedupeParagraphs(text)
}

// dedupeParagraphs drops blank-line separated blocks that repeat an earlier
// block verbatim (carousels, duplicated banners, mobile/desktop copies) and
// bullets left empty after image removal.
func dedupeParagraphs(text string) string {
	blocks := strings.Split(text, "\n\n")
	seen := make(map[string]bool, len(blocks))
	out := blocks[:0]
	for _, block := range blocks {
		trimmed := strings.TrimSpace(block)
		if trimmed == "" || trimmed == "-" || trimmed == "*" {
			continue
		}
		if len(trimmed) >= 20 {
			if seen[trimmed] {
				continue
			}
			seen[trimmed] = true
		}
		out = append(out, trimmed)
	}
	return strings.TrimSpace(markdownBlanks.ReplaceAllString(strings.Join(out, "\n\n"), "\n\n"))
}
