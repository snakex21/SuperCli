package agent

import "strings"

// A comma/semicolon separates fields only before another key: value pair.
// Punctuation in globs, regexes and paths is data. Quoted text protects field-
// like punctuation too. Quotes remain literal, as in the existing text format;
// native JSON objects bypass this parser.
func splitInvokeArgText(text string) []string {
	var parts []string
	start, depth := 0, 0
	quoted, escaped := false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		// Regex escapes are data; an escaped bracket must not hide the next
		// field delimiter. Preserve both bytes in the eventual value.
		if c == '\\' && i+1 < len(text) && strings.ContainsRune(`\"[]{}`, rune(text[i+1])) {
			i++
			continue
		}
		switch c {
		case '"':
			if depth == 0 {
				quoted = true
			}
		case '{', '[':
			depth++
		case '}', ']':
			if depth > 0 {
				depth--
			}
		case '\n', ',', ';':
			if c != '\n' && (depth > 0 || !invokeTextFieldStart(text[i+1:])) {
				continue
			}
			if part := strings.TrimSpace(text[start:i]); part != "" {
				parts = append(parts, part)
			}
			start = i + 1
			// Each unquoted line is an independent field, even if its regex value
			// contains an unmatched bracket. The target validates its own values.
			depth = 0
		}
	}
	if part := strings.TrimSpace(text[start:]); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func invokeTextFieldStart(text string) bool {
	text = strings.TrimLeft(text, " \t\r")
	for i := 0; i < len(text); i++ {
		c := text[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
		if i == 0 && !letter {
			return false
		}
		if letter || c >= '0' && c <= '9' || c == '.' || c == '-' {
			continue
		}
		return strings.HasPrefix(strings.TrimLeft(text[i:], " \t"), ":")
	}
	return false
}
