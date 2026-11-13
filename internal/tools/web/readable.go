package web

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// readable.go: DOM-based main-content extraction for web_fetch.
//
// The model usually sees only a bounded window of a fetched page, so the
// first characters must be the article rather than menus, language lists,
// cookie banners and footers. Extraction is deliberately heuristic and
// conservative: a block is dropped as boilerplate only when it holds a
// minority of the page text, so listing pages and unusual layouts keep their
// substance. Links are rendered as a separate, bounded list because page
// navigation (for example a "Next" pager) is otherwise invisible to a model
// that cannot click.

type pageLink struct {
	Text string
	URL  string
}

type readablePage struct {
	Title       string
	Description string
	Text        string
	// Pager holds next/previous navigation links found anywhere in the page,
	// including inside dropped <nav> blocks.
	Pager []pageLink
	// Links are the unique http(s) links of the retained content, in order.
	Links []pageLink
	// Data is a compact rendering of schema.org JSON-LD (prices, dates, ...).
	Data []string
	// JSRendered reports that the static HTML carries almost no readable
	// text while relying on scripts; ScriptData then holds bounded inline
	// script payloads, which frequently contain the page data.
	JSRendered bool
	ScriptData string
}

const (
	readableMaxLinks          = 200
	readableMaxPager          = 6
	readableMaxDataLines      = 12
	readableDataLineRunes     = 300
	readableScriptDataChars   = 6000
	readableJSRenderedMinText = 200
	// A boilerplate candidate is dropped only when it holds at most this share
	// of the content root's text, so a page that is "all list" survives.
	readableBoilerplateShare = 0.4
	readableLinkBlockShare   = 0.7
)

// extractReadable parses an HTML document and returns its readable parts.
// base resolves relative links; it may be nil.
func extractReadable(src string, base *url.URL) readablePage {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return readablePage{Title: htmlTitle(src), Text: htmlToText(src)}
	}
	var page readablePage
	var ogTitle string
	var ldJSON, inlineScripts []string
	scriptCount := 0
	walk(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		switch n.DataAtom {
		case atom.Title:
			if page.Title == "" {
				page.Title = collapseSpace(nodeText(n))
			}
		case atom.Meta:
			name := strings.ToLower(attr(n, "name") + attr(n, "property"))
			content := collapseSpace(attr(n, "content"))
			switch name {
			case "description":
				page.Description = content
			case "og:description":
				if page.Description == "" {
					page.Description = content
				}
			case "og:title":
				ogTitle = content
			}
		case atom.Script:
			scriptCount++
			typ := strings.ToLower(strings.TrimSpace(attr(n, "type")))
			body := rawText(n)
			switch {
			case typ == "application/ld+json":
				ldJSON = append(ldJSON, body)
			case attr(n, "src") == "" && strings.TrimSpace(body) != "" &&
				(typ == "" || strings.Contains(typ, "javascript") || strings.Contains(typ, "json") || typ == "module"):
				inlineScripts = append(inlineScripts, body)
			}
			return false
		}
		return true
	})
	if page.Title == "" {
		page.Title = ogTitle
	}
	page.Pager = pagerLinks(doc, base)
	page.Data = structuredData(ldJSON)

	root := contentRoot(doc)
	pruneBoilerplate(root)
	var r textRenderer
	r.base = base
	r.render(root)
	page.Text = r.String()
	page.Links = r.links

	if utf8.RuneCountInString(page.Text) < readableJSRenderedMinText && scriptCount > 0 {
		page.JSRendered = true
		page.ScriptData = scriptPayload(inlineScripts)
	}
	return page
}

// walk visits n and its descendants in document order. visit returns false
// to skip a node's children.
func walk(n *html.Node, visit func(*html.Node) bool) {
	if !visit(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func rawText(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

// nodeText concatenates visible text below n (scripts and styles excluded).
func nodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) bool {
		switch c.Type {
		case html.TextNode:
			b.WriteString(c.Data)
			b.WriteByte(' ')
		case html.ElementNode:
			switch c.DataAtom {
			case atom.Script, atom.Style, atom.Noscript, atom.Template:
				return false
			}
		}
		return true
	})
	return b.String()
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// textStats returns the visible text length below n and the part of it that
// sits inside links. Both are counted in bytes of whitespace-collapsed text.
func textStats(n *html.Node) (total, linked int) {
	var visit func(c *html.Node, inLink bool)
	visit = func(c *html.Node, inLink bool) {
		switch c.Type {
		case html.TextNode:
			l := len(collapseSpace(c.Data))
			total += l
			if inLink {
				linked += l
			}
			return
		case html.ElementNode:
			switch c.DataAtom {
			case atom.Script, atom.Style, atom.Noscript, atom.Template:
				return
			case atom.A:
				inLink = true
			}
		}
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			visit(k, inLink)
		}
	}
	visit(n, false)
	return total, linked
}

// contentRoot picks the element most likely to hold the main content:
// <main>/role=main, else the largest <article>, else <body>.
func contentRoot(doc *html.Node) *html.Node {
	var mainEl, body *html.Node
	var articles []*html.Node
	walk(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		switch {
		case n.DataAtom == atom.Body && body == nil:
			body = n
		case (n.DataAtom == atom.Main || strings.EqualFold(attr(n, "role"), "main")) && mainEl == nil:
			mainEl = n
		case n.DataAtom == atom.Article:
			articles = append(articles, n)
		}
		return true
	})
	const minRootText = 200
	if mainEl != nil {
		if total, _ := textStats(mainEl); total >= minRootText {
			return mainEl
		}
	}
	var best *html.Node
	bestLen := 0
	for _, a := range articles {
		if total, _ := textStats(a); total > bestLen {
			best, bestLen = a, total
		}
	}
	if best != nil && bestLen >= minRootText {
		// Several sibling articles (a feed or listing) are all content.
		if len(articles) > 1 && best.Parent != nil && best.Parent != body {
			siblings := 0
			for _, a := range articles {
				if a.Parent == best.Parent {
					siblings++
				}
			}
			if siblings > 1 {
				return best.Parent
			}
		}
		if len(articles) == 1 || bestLen*2 >= totalOf(articles) {
			return best
		}
	}
	if body != nil {
		return body
	}
	return doc
}

func totalOf(nodes []*html.Node) int {
	sum := 0
	for _, n := range nodes {
		t, _ := textStats(n)
		sum += t
	}
	return sum
}

// Tags whose content is never readable page text.
var dropAlways = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true,
	atom.Svg: true, atom.Iframe: true, atom.Canvas: true, atom.Object: true,
	atom.Embed: true, atom.Button: true, atom.Select: true, atom.Input: true,
	atom.Textarea: true, atom.Link: true, atom.Meta: true, atom.Head: true,
}

// Structural boilerplate dropped when it is a minority of the content.
var dropMinority = map[atom.Atom]bool{
	atom.Nav: true, atom.Aside: true, atom.Footer: true, atom.Form: true,
	atom.Dialog: true, atom.Menu: true,
}

var boilerplateRoles = map[string]bool{
	"navigation": true, "banner": true, "contentinfo": true, "complementary": true,
	"search": true, "menu": true, "menubar": true, "dialog": true,
	"alertdialog": true, "toolbar": true,
}

// boilerplateWords are matched against the dash/underscore separated parts of
// class and id tokens ("site-nav" and "cookie_banner" match, "canvas" does not).
var boilerplateWords = map[string]bool{
	"nav": true, "navbar": true, "navigation": true, "menu": true, "breadcrumb": true,
	"breadcrumbs": true, "footer": true, "sidebar": true, "cookie": true, "cookies": true,
	"consent": true, "gdpr": true, "share": true, "sharing": true, "social": true,
	"advert": true, "advertisement": true, "ads": true, "promo": true, "newsletter": true,
	"popup": true, "modal": true, "skip": true, "jump": true, "editsection": true,
	"navbox": true, "catlinks": true, "interlanguage": true, "toc": true, "dropdown": true,
	"banner": true, "related": true, "subscribe": true, "toolbar": true, "noprint": true,
}

func boilerplateAttr(n *html.Node) bool {
	for _, value := range []string{attr(n, "class"), attr(n, "id")} {
		for _, token := range strings.Fields(strings.ToLower(value)) {
			for _, part := range strings.FieldsFunc(token, func(r rune) bool { return r == '-' || r == '_' }) {
				if boilerplateWords[part] {
					return true
				}
			}
		}
	}
	return false
}

func hiddenNode(n *html.Node) bool {
	if _, ok := attrOK(n, "hidden"); ok {
		return true
	}
	if strings.EqualFold(attr(n, "aria-hidden"), "true") {
		return true
	}
	style := strings.ToLower(strings.ReplaceAll(attr(n, "style"), " ", ""))
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
}

func attrOK(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// linkDenseBlock reports navigation-like blocks: many links and little text
// outside them (menus, language lists, tag clouds, footers of links).
func linkDenseBlock(n *html.Node, total, linked int) bool {
	switch n.DataAtom {
	case atom.Ul, atom.Ol, atom.Table, atom.Div, atom.Section, atom.Header, atom.Dl:
	default:
		return false
	}
	if total == 0 {
		return false
	}
	links := 0
	walk(n, func(c *html.Node) bool {
		if c.Type == html.ElementNode && c.DataAtom == atom.A {
			links++
			return false
		}
		return true
	})
	return links >= 6 && float64(linked) >= 0.75*float64(total)
}

// pruneBoilerplate removes non-content subtrees below root in place.
func pruneBoilerplate(root *html.Node) {
	rootTotal, _ := textStats(root)
	limit := int(readableBoilerplateShare * float64(rootTotal))
	var visit func(n *html.Node)
	visit = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.CommentNode {
				n.RemoveChild(c)
			} else if c.Type == html.ElementNode {
				if dropAlways[c.DataAtom] {
					n.RemoveChild(c)
				} else if minorityBoilerplate(c, limit) {
					n.RemoveChild(c)
				} else {
					visit(c)
				}
			}
			c = next
		}
	}
	visit(root)
}

func minorityBoilerplate(n *html.Node, limit int) bool {
	candidate := hiddenNode(n) || dropMinority[n.DataAtom] ||
		boilerplateRoles[strings.ToLower(attr(n, "role"))] || boilerplateAttr(n)
	total, linked := textStats(n)
	if !candidate && !linkDenseBlock(n, total, linked) {
		return false
	}
	if candidate && total > 0 && float64(linked) >= 0.75*float64(total) {
		// A marked navigation block made almost entirely of links (shop
		// category sidebars) may be large; it is still not the content.
		return float64(total) <= readableLinkBlockShare/readableBoilerplateShare*float64(limit)
	}
	return total <= limit
}

var pagerWords = []string{"next", "previous", "prev", "older", "newer", "następna", "następne",
	"poprzednia", "poprzednie", "dalej", "wstecz", "»", "›", "→", "«", "‹", "←"}

// pagerLinks finds next/previous navigation anywhere in the document, so a
// model can follow pagination even though <nav> blocks are not rendered.
func pagerLinks(doc *html.Node, base *url.URL) []pageLink {
	var out []pageLink
	seen := map[string]bool{}
	walk(doc, func(n *html.Node) bool {
		if len(out) >= readableMaxPager {
			return false
		}
		if n.Type != html.ElementNode || n.DataAtom != atom.A {
			return true
		}
		rel := strings.ToLower(attr(n, "rel"))
		text := collapseSpace(nodeText(n))
		label := strings.ToLower(text + " " + attr(n, "aria-label") + " " + attr(n, "class") + " " + parentClass(n))
		isPager := strings.Contains(rel, "next") || strings.Contains(rel, "prev")
		if !isPager && utf8.RuneCountInString(text) <= 24 {
			for _, w := range pagerWords {
				if strings.Contains(label, w) {
					isPager = true
					break
				}
			}
		}
		if !isPager {
			return false
		}
		u := resolveLink(attr(n, "href"), base)
		if u == "" || seen[u] {
			return false
		}
		seen[u] = true
		if text == "" {
			text = collapseSpace(attr(n, "aria-label") + " " + rel)
		}
		out = append(out, pageLink{Text: truncateRunes(text, 60), URL: u})
		return false
	})
	return out
}

func parentClass(n *html.Node) string {
	if n.Parent != nil && n.Parent.Type == html.ElementNode {
		return attr(n.Parent, "class")
	}
	return ""
}

// resolveLink returns an absolute http(s) URL without a fragment, or "" for
// script/mail links, pure same-page anchors and unparsable values.
func resolveLink(href string, base *url.URL) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return ""
	}
	u.Fragment = ""
	u.RawFragment = ""
	if base != nil && u.String() == strings.SplitN(base.String(), "#", 2)[0] {
		return ""
	}
	if len(u.String()) > webSearchMaxResultURL {
		return ""
	}
	return u.String()
}

// textRenderer turns a pruned DOM into compact Markdown-like text.
type textRenderer struct {
	b       strings.Builder
	base    *url.URL
	links   []pageLink
	seen    map[string]bool
	pending string // separator to emit before the next text ("\n", "\n\n")
	listDep int
	inPre   bool
}

// String returns the rendered text without empty bullets (icon-only list
// items) and with at most one blank line between blocks.
func (r *textRenderer) String() string {
	lines := strings.Split(r.b.String(), "\n")
	out := lines[:0]
	blank := false
	inFence := false
	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		}
		if !inFence {
			line = strings.TrimRight(line, " ")
			if strings.TrimSpace(line) == "-" {
				continue
			}
			if strings.TrimSpace(line) == "" {
				if blank {
					continue
				}
				blank = true
				out = append(out, "")
				continue
			}
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func (r *textRenderer) block(sep string) {
	if r.b.Len() == 0 {
		return
	}
	if len(sep) > len(r.pending) {
		r.pending = sep
	}
}

func (r *textRenderer) write(s string) {
	if s == "" {
		return
	}
	if r.pending != "" {
		r.b.WriteString(r.pending)
		r.pending = ""
	}
	r.b.WriteString(s)
}

func (r *textRenderer) text(s string) {
	if r.inPre {
		r.write(s)
		return
	}
	leading := s != "" && strings.TrimLeftFunc(s, unicode.IsSpace) != s
	trailing := s != "" && strings.TrimRightFunc(s, unicode.IsSpace) != s
	words := collapseSpace(s)
	if words == "" {
		if leading && r.pending == "" && r.b.Len() > 0 && !endsWithSpace(&r.b) {
			r.b.WriteByte(' ')
		}
		return
	}
	if leading && r.pending == "" && r.b.Len() > 0 && !endsWithSpace(&r.b) {
		r.b.WriteByte(' ')
	}
	r.write(words)
	if trailing {
		r.b.WriteByte(' ')
	}
}

func endsWithSpace(b *strings.Builder) bool {
	s := b.String()
	if s == "" {
		return true
	}
	last := s[len(s)-1]
	return last == ' ' || last == '\n'
}

func (r *textRenderer) trimTrailingSpace() {
	s := r.b.String()
	trimmed := strings.TrimRight(s, " ")
	if len(trimmed) != len(s) {
		r.b.Reset()
		r.b.WriteString(trimmed)
	}
}

func (r *textRenderer) render(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		r.text(n.Data)
		return
	case html.ElementNode:
	case html.DocumentNode:
		r.children(n)
		return
	default:
		return
	}
	switch n.DataAtom {
	case atom.Br:
		r.trimTrailingSpace()
		r.write("")
		r.b.WriteString("\n")
		return
	case atom.Hr:
		r.block("\n\n")
		return
	case atom.Img:
		return
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0')
		text := collapseSpace(nodeText(n))
		if text != "" {
			r.trimTrailingSpace()
			r.block("\n\n")
			r.write(strings.Repeat("#", level) + " " + text)
			r.block("\n\n")
		}
		r.collectLinks(n)
		return
	case atom.Pre:
		r.trimTrailingSpace()
		r.block("\n\n")
		r.write("```\n")
		r.inPre = true
		r.children(n)
		r.inPre = false
		r.b.WriteString("\n```")
		r.block("\n\n")
		return
	case atom.Li:
		r.trimTrailingSpace()
		r.block("\n")
		r.write(strings.Repeat("  ", max(0, r.listDep-1)) + "- ")
		r.children(n)
		r.block("\n")
		return
	case atom.Ul, atom.Ol:
		r.block("\n\n")
		r.listDep++
		r.children(n)
		r.listDep--
		r.block("\n\n")
		return
	case atom.Tr:
		r.trimTrailingSpace()
		r.block("\n")
		r.children(n)
		r.block("\n")
		return
	case atom.Td, atom.Th:
		if r.pending == "" && r.b.Len() > 0 && !strings.HasSuffix(r.b.String(), "\n") {
			r.trimTrailingSpace()
			r.b.WriteString(" | ")
		}
		r.children(n)
		return
	case atom.Blockquote:
		r.block("\n\n")
		r.write("> ")
		r.children(n)
		r.block("\n\n")
		return
	case atom.A:
		r.children(n)
		r.addLink(n)
		return
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Main, atom.Header, atom.Footer,
		atom.Table, atom.Dl, atom.Figure, atom.Figcaption, atom.Address, atom.Details,
		atom.Summary, atom.Caption, atom.Dt, atom.Dd, atom.Fieldset, atom.Body:
		r.trimTrailingSpace()
		sep := "\n"
		if n.DataAtom == atom.P || n.DataAtom == atom.Table || n.DataAtom == atom.Section || n.DataAtom == atom.Article {
			sep = "\n\n"
		}
		r.block(sep)
		r.children(n)
		r.trimTrailingSpace()
		r.block(sep)
		return
	}
	r.children(n)
}

func (r *textRenderer) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		r.render(c)
	}
}

func (r *textRenderer) collectLinks(n *html.Node) {
	walk(n, func(c *html.Node) bool {
		if c.Type == html.ElementNode && c.DataAtom == atom.A {
			r.addLink(c)
			return false
		}
		return true
	})
}

func (r *textRenderer) addLink(n *html.Node) {
	if len(r.links) >= readableMaxLinks {
		return
	}
	u := resolveLink(attr(n, "href"), r.base)
	if u == "" {
		return
	}
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	if r.seen[u] {
		return
	}
	text := collapseSpace(nodeText(n))
	if text == "" {
		text = collapseSpace(attr(n, "aria-label") + " " + attr(n, "title"))
	}
	if text == "" {
		return
	}
	r.seen[u] = true
	r.links = append(r.links, pageLink{Text: truncateRunes(text, 80), URL: u})
}

// structuredData renders the useful facts of schema.org JSON-LD blocks
// (product prices and availability, publication dates, authors).
func structuredData(blocks []string) []string {
	var lines []string
	var visit func(v any)
	visit = func(v any) {
		if len(lines) >= readableMaxDataLines {
			return
		}
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				visit(item)
			}
		case map[string]any:
			if line := structuredLine(x); line != "" {
				lines = append(lines, truncateRunes(line, readableDataLineRunes))
			}
			for _, key := range []string{"@graph", "offers", "mainEntity", "itemListElement", "hasVariant"} {
				if child, ok := x[key]; ok {
					visit(child)
				}
			}
		}
	}
	for _, block := range blocks {
		var v any
		if json.Unmarshal([]byte(strings.TrimSpace(block)), &v) == nil {
			visit(v)
		}
	}
	return lines
}

var structuredTypes = map[string]bool{
	"product": true, "productgroup": true, "offer": true, "aggregateoffer": true,
	"article": true, "newsarticle": true, "blogposting": true, "techarticle": true,
	"report": true, "scholarlyarticle": true, "event": true, "softwareapplication": true,
	"softwaresourcecode": true, "book": true, "movie": true, "recipe": true,
	"course": true, "jobposting": true, "dataset": true, "review": true, "localbusiness": true,
}

func structuredLine(obj map[string]any) string {
	types := jsonStrings(obj["@type"])
	interesting := false
	for _, t := range types {
		if structuredTypes[strings.ToLower(t)] {
			interesting = true
		}
	}
	if !interesting {
		return ""
	}
	var parts []string
	add := func(label string, v any) {
		s := jsonScalar(v)
		if s != "" {
			parts = append(parts, label+": "+s)
		}
	}
	name := jsonScalar(obj["name"])
	if name == "" {
		name = jsonScalar(obj["headline"])
	}
	add("price", obj["price"])
	add("lowPrice", obj["lowPrice"])
	add("highPrice", obj["highPrice"])
	add("currency", obj["priceCurrency"])
	if avail := jsonScalar(obj["availability"]); avail != "" {
		parts = append(parts, "availability: "+strings.TrimPrefix(strings.TrimPrefix(avail, "https://schema.org/"), "http://schema.org/"))
	}
	add("sku", obj["sku"])
	add("brand", nameOf(obj["brand"]))
	add("seller", nameOf(obj["seller"]))
	add("author", nameOf(obj["author"]))
	add("published", obj["datePublished"])
	add("modified", obj["dateModified"])
	add("start", obj["startDate"])
	add("version", obj["softwareVersion"])
	if rating, ok := obj["aggregateRating"].(map[string]any); ok {
		add("rating", rating["ratingValue"])
		add("reviews", rating["reviewCount"])
	}
	if name == "" && len(parts) == 0 {
		return ""
	}
	line := strings.Join(types, "/")
	if name != "" {
		line += " " + fmt.Sprintf("%q", name)
	}
	if len(parts) > 0 {
		line += " — " + strings.Join(parts, "; ")
	}
	return line
}

func nameOf(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return x["name"]
	case []any:
		var names []string
		for _, item := range x {
			if s := jsonScalar(nameOf(item)); s != "" {
				names = append(names, s)
			}
		}
		return strings.Join(names, ", ")
	}
	return v
}

func jsonStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func jsonScalar(v any) string {
	switch x := v.(type) {
	case string:
		return collapseSpace(x)
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.4f", x), "0"), ".")
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return ""
}

// scriptPayload keeps the inline scripts most likely to carry page data
// (JSON state, data arrays), largest first, within a fixed budget.
func scriptPayload(scripts []string) string {
	type candidate struct {
		text  string
		score int
	}
	var cands []candidate
	for _, s := range scripts {
		s = collapseSpace(s)
		if len(s) < 40 {
			continue
		}
		score := strings.Count(s, `":`) + strings.Count(s, `": `)
		if score == 0 {
			continue
		}
		cands = append(cands, candidate{s, score})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	var b strings.Builder
	for _, c := range cands {
		remaining := readableScriptDataChars - utf8.RuneCountInString(b.String())
		if remaining <= 0 {
			break
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(truncateRunes(c.text, remaining))
	}
	return b.String()
}
