package tools

import (
	"html"
	"regexp"
	"strings"
)

// htmltext.go: dependency-free HTML → readable-text extraction
// shared by web_fetch (page content) and outlook_mail (HTMLBody
// fallback). A real DOM parser would be nicer but would pull in
// a new dependency; a tag-stripping pass is good enough for
// "give the model something readable".

var (
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	reHTMLTitle   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	// Tags that imply a line break when removed.
	reHTMLBreak = regexp.MustCompile(`(?i)<(/p|/div|/li|/tr|/h[1-6]|/blockquote|/section|/article|/ul|/ol|/table|br\s*/?|p\b[^>]*|h[1-6]\b[^>]*)>`)
	reHTMLLi    = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	reHTMLTag   = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpaces    = regexp.MustCompile(`[ \t\r\f\v]+`)
	reNewlines  = regexp.MustCompile(`\n{3,}`)
)

// dropBlocks removes whole noise elements (<script>…</script>,
// <style>…</style>, <head>, <nav>, …) including their content.
// Done per-tag because Go regexp has no backreferences.
func dropBlocks(s string) string {
	for _, tag := range []string{"script", "style", "noscript", "template", "svg", "iframe", "head", "nav"} {
		re := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>.*?</` + tag + `\s*>`)
		s = re.ReplaceAllString(s, " ")
	}
	return s
}

// htmlTitle extracts the <title> text, entity-decoded and trimmed.
// Empty string when absent.
func htmlTitle(s string) string {
	m := reHTMLTitle.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(reHTMLTag.ReplaceAllString(m[1], "")))
}

// htmlToText converts an HTML document/fragment to plain readable
// text: noise blocks removed, block boundaries become newlines,
// list items become "- " bullets, entities decoded, whitespace
// collapsed.
func htmlToText(s string) string {
	s = reHTMLComment.ReplaceAllString(s, " ")
	s = dropBlocks(s)
	s = reHTMLLi.ReplaceAllString(s, "\n- ")
	s = reHTMLBreak.ReplaceAllString(s, "\n")
	s = reHTMLTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)

	// Collapse horizontal whitespace, trim each line, cap blank runs.
	s = reSpaces.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	s = strings.Join(lines, "\n")
	s = reNewlines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
