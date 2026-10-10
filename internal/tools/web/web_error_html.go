package web

import "strings"

// HTTP error pages are evidence of the failed request, not a source of scripts,
// hidden configuration or alternate API endpoints. Remove inert/raw blocks
// before the ordinary readable-text formatter. Unlike a closing-tag regexp,
// this also drops a script cut off by the error-body byte limit.
func readableHTMLError(body string) (title, text string) {
	var kept strings.Builder
	pos := 0
	inHead := false
	for scanned := 0; scanned < 512 && pos < len(body); scanned++ {
		start, end, name, closing := nextMediaHTMLTag(body, pos)
		if end == 0 {
			tail := body[pos:]
			if at := strings.IndexByte(tail, '<'); at >= 0 {
				tail = tail[:at] // Do not expose an unclosed comment or tag.
			}
			if !inHead {
				kept.WriteString(tail)
			}
			break
		}
		if !inHead {
			kept.WriteString(body[pos:start])
		}
		if strings.EqualFold(name, "head") {
			inHead = !closing
			pos = end
			continue
		}
		if !closing && strings.EqualFold(name, "body") {
			inHead = false
		}
		if !closing && strings.EqualFold(name, "title") {
			pos = skipMediaRawText(body, end, name)
			if title == "" {
				title = htmlTitle(body[start:pos])
			}
			continue
		}
		if !closing && errorHTMLNoiseElement(name) {
			kept.WriteByte('\n')
			if strings.EqualFold(name, "svg") && strings.HasSuffix(strings.TrimSpace(body[start:end]), "/>") {
				pos = end
				continue
			}
			pos = skipErrorHTMLNoise(body, end, name)
			continue
		}
		// Do not give attributes to the regex formatter: a '>' inside a quoted
		// attribute must not make hidden bootstrap values look like page prose.
		kept.WriteByte('\n')
		pos = end
	}
	page := kept.String()
	return title, htmlToText(page)
}

func errorHTMLNoiseElement(name string) bool {
	switch strings.ToLower(name) {
	case "script", "style", "textarea", "noscript", "template", "svg", "iframe", "nav":
		return true
	}
	return false
}

func skipErrorHTMLNoise(body string, pos int, name string) int {
	if strings.EqualFold(name, "script") || strings.EqualFold(name, "style") || strings.EqualFold(name, "textarea") || strings.EqualFold(name, "iframe") {
		return skipMediaRawText(body, pos, name)
	}
	depth := 1
	for scanned := 0; scanned < 512 && pos < len(body); scanned++ {
		start, end, tag, closing := nextMediaHTMLTag(body, pos)
		if end == 0 {
			return len(body)
		}
		pos = end
		if strings.EqualFold(tag, name) {
			if closing {
				depth--
				if depth == 0 {
					return pos
				}
			} else if !strings.EqualFold(name, "svg") || !strings.HasSuffix(strings.TrimSpace(body[start:end]), "/>") {
				depth++
			}
		} else if !closing && (strings.EqualFold(tag, "script") || strings.EqualFold(tag, "style") || strings.EqualFold(tag, "textarea")) {
			pos = skipMediaRawText(body, pos, tag)
		}
	}
	return len(body)
}
