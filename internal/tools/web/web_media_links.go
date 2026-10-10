package web

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

// Keep page-declared asset URLs that HTML-to-text otherwise discards. This
// is declared-link extraction only: it never fetches assets, executes scripts or
// guesses provider-specific paths. Download still applies normal SSRF rules.
const mediaHeadBytes = 64 << 10
const mediaURLBytes = 1024
const mediaResultBytes = 2048
const mediaTitleRunes = 200

// Media mode deliberately omits page prose and does not resolve links through
// another request. Sites and filenames are never inferred from the provider.
func formatFetchedMedia(body, contentType string, base *url.URL, media string) string {
	var result strings.Builder
	fmt.Fprintf(&result, "URL: %s\n", base.String())
	if fetchedHTML(body, contentType) {
		if title := htmlTitle(body); title != "" {
			fmt.Fprintf(&result, "Title: %s\n", truncateRunes(title, mediaTitleRunes))
		}
	}
	result.WriteByte('\n')
	if media == "" {
		result.WriteString("No declared media URLs or download links returned from the bounded first 64 KiB scan. Use mode=text for page content, or read another page.")
	} else {
		result.WriteString(media)
		result.WriteString("Page-declared file candidates: use web_download with an exact URL of the requested format and a new requested local file path to save one. Another lookup is unnecessary; web_download checks the response. Use web_fetch(mode=text) only when page content is needed.")
	}
	return result.String()
}

var mediaAttributes = regexp.MustCompile(`(?is)([^\s=/<>"]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>]+))`)
var mediaDownloadAttribute = regexp.MustCompile(`(?i)(?:^|\s)download(?:\s|/?>|$)`)

func fetchedHTML(body, contentType string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml") {
		return true
	}
	if ct == "" || strings.Contains(ct, "octet-stream") {
		text := strings.TrimSpace(body)
		return len(text) >= 14 && strings.EqualFold(text[:14], "<!doctype html") || len(text) >= 5 && strings.EqualFold(text[:5], "<html")
	}
	return false
}

func fetchedMediaURLs(body, contentType string, base *url.URL, maxChars int) string {
	if base == nil || maxChars <= 0 {
		return ""
	}
	if len(body) > mediaHeadBytes {
		body = body[:mediaHeadBytes]
	}
	if !fetchedHTML(body, contentType) {
		return ""
	}
	budget := min(mediaResultBytes, maxChars)
	var result strings.Builder
	var seen []string
	appendURL := func(kind, content string) {
		raw := resolveDeclaredMediaURL(content, base)
		if raw == "" || len(seen) == 4 {
			return
		}
		for _, prior := range seen {
			if prior == raw {
				return
			}
		}
		line := "- " + kind + ": " + raw + "\n"
		prefix := ""
		if result.Len() == 0 {
			prefix = "Declared media URLs:\n"
		}
		if result.Len()+len(prefix)+len(line) > budget {
			return
		}
		result.WriteString(prefix)
		result.WriteString(line)
		seen = append(seen, raw)
	}
	// Scan tags in the bounded prefix, skipping comments and raw text. Tags
	// inside scripts are not page metadata, and their </head> is not an end.
	pos := 0
	linksPos := 0
	for scanned := 0; scanned < 128; scanned++ {
		start, end, tagName, closing := nextMediaHTMLTag(body, pos)
		if end == 0 {
			break
		}
		pos = end
		if closing && strings.EqualFold(tagName, "head") || !closing && strings.EqualFold(tagName, "body") {
			linksPos = end
			break
		}
		if !closing && mediaHiddenElement(tagName) {
			pos = skipMediaHiddenElement(body, start, pos, tagName)
			continue
		}
		if closing || !strings.EqualFold(tagName, "meta") {
			continue
		}
		tag := body[start:end]
		property, name, content := "", "", ""
		for _, attr := range mediaAttributes.FindAllStringSubmatch(tag, -1) {
			value := attr[2]
			if value == "" {
				value = attr[3]
			}
			if value == "" {
				value = attr[4]
			}
			switch strings.ToLower(attr[1]) {
			case "property":
				property = strings.ToLower(value)
			case "name":
				name = strings.ToLower(value)
			case "content":
				content = html.UnescapeString(value)
			}
		}
		if property == "" {
			property = name
		}
		kind := ""
		switch property {
		case "og:image", "og:image:url", "og:image:secure_url", "twitter:image", "twitter:image:src":
			kind = "image"
		case "og:video", "og:video:url", "og:video:secure_url":
			kind = "video"
		case "og:audio", "og:audio:url", "og:audio:secure_url":
			kind = "audio"
		}
		if kind != "" {
			appendURL(kind, content)
		}
		if len(seen) == 4 {
			break
		}
	}
	// Fill the remaining slots with actual body download/media declarations.
	// This second pass leaves useful OG/Twitter assets first, regardless of
	// where the site's PDF/ZIP links occur, and never inspects script payloads.
	pos = linksPos
	parentMedia := ""
	for scanned := 0; scanned < 512 && len(seen) < 4; scanned++ {
		start, end, tagName, closing := nextMediaHTMLTag(body, pos)
		if end == 0 {
			break
		}
		pos = end
		if !closing && mediaHiddenElement(tagName) {
			pos = skipMediaHiddenElement(body, start, pos, tagName)
			continue
		}
		if closing {
			if strings.EqualFold(tagName, "audio") || strings.EqualFold(tagName, "video") {
				parentMedia = ""
			}
			continue
		}
		name := ""
		switch {
		case strings.EqualFold(tagName, "a"):
			name = "a"
		case strings.EqualFold(tagName, "audio"):
			name = "audio"
		case strings.EqualFold(tagName, "video"):
			name = "video"
		case strings.EqualFold(tagName, "source"):
			name = "source"
		default:
			continue
		}
		href, src, mediaType, download := declaredLinkAttributes(body[start:end])
		switch name {
		case "a":
			if raw := resolveDeclaredMediaURL(href, base); raw != "" && (download || hasDirectFileExtension(raw)) {
				appendURL("file", raw)
			}
		case "audio", "video":
			parentMedia = name
			appendURL(name, src)
		case "source":
			kind := parentMedia
			if kind == "" {
				if strings.HasPrefix(mediaType, "audio/") {
					kind = "audio"
				} else if strings.HasPrefix(mediaType, "video/") {
					kind = "video"
				}
			}
			if kind != "" {
				appendURL(kind, src)
			}
		}
	}
	return result.String()
}

func resolveDeclaredMediaURL(content string, base *url.URL) string {
	if len(content) > mediaURLBytes {
		return ""
	}
	candidate, err := url.Parse(strings.TrimSpace(content))
	if err != nil || candidate.String() == "" || candidate.Path == "" && candidate.RawQuery == "" && candidate.Fragment != "" {
		return ""
	}
	resolved := base.ResolveReference(candidate)
	resolved.Fragment = ""
	raw := resolved.String()
	if resolved.User != nil || len(raw) > mediaURLBytes {
		return ""
	}
	if _, err := validateFetchURL(raw); err != nil {
		return ""
	}
	return raw
}

func declaredLinkAttributes(tag string) (href, src, mediaType string, download bool) {
	haveHref, haveSrc, haveType := false, false, false
	for _, attr := range mediaAttributes.FindAllStringSubmatch(tag, -1) {
		value := attr[2]
		if value == "" {
			value = attr[3]
		}
		if value == "" {
			value = attr[4]
		}
		value = html.UnescapeString(value)
		switch strings.ToLower(attr[1]) {
		case "href":
			if !haveHref {
				href, haveHref = value, true
			}
		case "src":
			if !haveSrc {
				src, haveSrc = value, true
			}
		case "type":
			if !haveType {
				mediaType, haveType = strings.ToLower(value), true
			}
		case "download":
			download = true
		}
	}
	if !download && mediaDownloadAttribute.MatchString(tag) {
		// Mask all complete valued attributes so "download" inside a quoted
		// data/title/href value cannot become a boolean attribute.
		download = mediaDownloadAttribute.MatchString(mediaAttributes.ReplaceAllString(tag, " "))
	}
	return
}

func mediaHiddenElement(name string) bool {
	for _, hidden := range []string{"script", "style", "title", "textarea", "noscript", "template", "svg", "iframe"} {
		if strings.EqualFold(name, hidden) {
			return true
		}
	}
	return false
}

func skipMediaHiddenElement(body string, start, end int, name string) int {
	if strings.EqualFold(name, "svg") && strings.HasSuffix(strings.TrimSpace(body[start:end]), "/>") {
		return end
	}
	if strings.EqualFold(name, "title") {
		return skipMediaRawText(body, end, name)
	}
	return skipErrorHTMLNoise(body, end, name)
}

// nextMediaHTMLTag returns one actual tag without allocating a lowercase copy
// of the document. Unclosed comments/tags end the bounded scan.
func nextMediaHTMLTag(body string, pos int) (start, end int, name string, closing bool) {
	for pos < len(body) {
		rel := strings.IndexByte(body[pos:], '<')
		if rel < 0 {
			return
		}
		start = pos + rel
		if strings.HasPrefix(body[start:], "<!--") {
			stop := strings.Index(body[start+4:], "-->")
			if stop < 0 {
				return 0, 0, "", false
			}
			pos = start + 4 + stop + 3
			continue
		}
		if start+1 < len(body) && (body[start+1] == '!' || body[start+1] == '?') {
			if mediaTagNameEnd(body, start+2, "doctype") > 0 {
				pos = mediaHTMLTagEnd(body, start+2+len("doctype"))
				if pos == 0 {
					return 0, 0, "", false
				}
			} else {
				stop := strings.IndexByte(body[start+2:], '>')
				if stop < 0 {
					return 0, 0, "", false
				}
				pos = start + 2 + stop + 1
			}
			continue
		}
		pos = start + 1
		closing = false
		if pos < len(body) && body[pos] == '/' {
			closing = true
			pos++
		}
		nameStart := pos
		for pos < len(body) && (body[pos] >= 'a' && body[pos] <= 'z' || body[pos] >= 'A' && body[pos] <= 'Z' || pos > nameStart && (body[pos] >= '0' && body[pos] <= '9' || body[pos] == '-')) {
			pos++
		}
		if pos == nameStart {
			pos = start + 1
			continue
		}
		name = body[nameStart:pos]
		if pos < len(body) && !strings.ContainsRune(" \t\r\n\f/>", rune(body[pos])) {
			pos = start + 1
			continue
		}
		end = mediaHTMLTagEnd(body, pos)
		if end == 0 {
			return 0, 0, "", false
		}
		return start, end, name, closing
	}
	return 0, 0, "", false
}

// Script comment-like text has escaped and double-escaped states. A matching
// end tag in double-escaped text returns to escaped text; it does not end the
// element (HTML tokenizer 13.2.5.18-31). Other raw-text elements close directly.
func skipMediaRawText(body string, pos int, name string) int {
	script := strings.EqualFold(name, "script")
	state, dashes := 0, 0 // 0=data, 1=escaped, 2=double-escaped
	for pos < len(body) {
		c := body[pos]
		if c == '<' {
			if script && state == 0 && strings.HasPrefix(body[pos:], "<!--") {
				state, dashes = 1, 2
				pos += 4
				continue
			}
			if pos+1 < len(body) && body[pos+1] == '/' {
				if endName := mediaTagNameEnd(body, pos+2, name); endName > 0 {
					if script && state == 2 {
						state, dashes = 1, 0
						pos = endName + 1
						continue
					}
					_, end, closingName, closing := nextMediaHTMLTag(body, pos)
					if closing && strings.EqualFold(closingName, name) {
						return end
					}
					return len(body)
				}
			} else if script && state == 1 {
				if endName := mediaTagNameEnd(body, pos+1, "script"); endName > 0 {
					state, dashes = 2, 0
					pos = endName + 1
					continue
				}
			}
		}
		if script && state != 0 {
			if c == '-' {
				dashes = min(dashes+1, 2)
			} else {
				if c == '>' && dashes == 2 {
					state = 0
				}
				dashes = 0
			}
		}
		pos++
	}
	return len(body)
}

// Check the full name and delimiter before parsing the end tag. Near-matches
// such as thousands of </scriptx occurrences must not rescan the tail.
func mediaTagNameEnd(body string, pos int, name string) int {
	end := pos + len(name)
	if end >= len(body) || !strings.EqualFold(body[pos:end], name) {
		return 0
	}
	if !strings.ContainsRune(" \t\r\n\f/>", rune(body[end])) {
		return 0
	}
	return end
}

func mediaHTMLTagEnd(body string, pos int) int {
	var quote byte
	for ; pos < len(body); pos++ {
		c := body[pos]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
		} else if c == '\'' || c == '"' {
			quote = c
		} else if c == '>' {
			return pos + 1
		}
	}
	return 0
}
