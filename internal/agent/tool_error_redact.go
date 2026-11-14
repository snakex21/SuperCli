package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Limits for the args recorded in tool_errors.log. The log exists to
// learn failure patterns (tool + category + shape of the call), never
// to keep the payload, so both caps are deliberately small.
const (
	toolErrorArgsMaxBytes   = 512
	toolErrorArgsMaxStrRune = 120
)

// toolErrorContentKeys are argument fields that carry bulk user content
// (file bodies, patches, mail bodies, prompts). Their values are replaced
// by a size + short hash placeholder: enough to tell two calls apart,
// nothing of the content itself.
var toolErrorContentKeys = map[string]bool{
	"content": true, "contents": true, "patch": true, "diff": true,
	"old": true, "new": true, "oldstring": true, "newstring": true,
	"oldtext": true, "newtext": true, "replacement": true,
	"text": true, "body": true, "html": true, "data": true,
	"message": true, "prompt": true, "input": true, "stdin": true,
	"code": true, "script": true, "source": true, "markdown": true,
	"attachment": true, "attachments": true, "payload": true,
}

// toolErrorSecretKeyParts mark keys whose value is masked entirely.
var toolErrorSecretKeyParts = []string{
	"token", "password", "passwd", "secret", "authorization", "cookie",
	"credential", "apikey", "privatekey", "sessionid", "signature",
}

var (
	toolErrorURLRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.\-]*://[^\s"'<>]+`)
	// Inline secrets in free text such as shell commands:
	// "Authorization: Bearer xyz", "--password=xyz", "api_key=xyz".
	toolErrorInlineSecretRe = regexp.MustCompile(`(?i)\b(bearer\s+|basic\s+|(?:token|password|passwd|pwd|secret|api[_-]?key|access[_-]?key)\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s"'&]+)`)
)

// redactToolErrorArgs turns raw tool-call arguments into a short,
// privacy-safe summary for tool_errors.log: content fields become
// "<redacted N bytes sha256:xxxxxxxx>", secret-like keys are masked,
// secrets in URLs (userinfo, secret-named query params) are removed,
// remaining long strings are truncated and the whole result is capped
// at toolErrorArgsMaxBytes. Non-JSON input yields "<unparseable N bytes>".
func redactToolErrorArgs(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return fmt.Sprintf("<unparseable %d bytes>", len(raw))
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(redactToolErrorValue(v)); err != nil {
		return fmt.Sprintf("<unparseable %d bytes>", len(raw))
	}
	return capToolErrorBytes(strings.TrimSuffix(out.String(), "\n"), toolErrorArgsMaxBytes)
}

func redactToolErrorValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			nk := normalizeToolErrorKey(k)
			switch {
			case isToolErrorSecretKey(nk):
				out[k] = "<masked>"
			case toolErrorContentKeys[nk]:
				out[k] = toolErrorContentPlaceholder(val)
			default:
				out[k] = redactToolErrorValue(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactToolErrorValue(val)
		}
		return out
	case string:
		return truncateToolErrorString(redactToolErrorString(t))
	default:
		return v
	}
}

func normalizeToolErrorKey(k string) string {
	k = strings.ToLower(k)
	k = strings.ReplaceAll(k, "_", "")
	return strings.ReplaceAll(k, "-", "")
}

func isToolErrorSecretKey(nk string) bool {
	switch nk {
	case "key", "auth", "pass", "pwd", "pin", "otp":
		return true
	}
	if strings.HasSuffix(nk, "key") || strings.HasSuffix(nk, "auth") {
		return true
	}
	for _, p := range toolErrorSecretKeyParts {
		if strings.Contains(nk, p) {
			return true
		}
	}
	return false
}

func toolErrorContentPlaceholder(v any) string {
	var b []byte
	if s, ok := v.(string); ok {
		b = []byte(s)
	} else {
		b, _ = json.Marshal(v)
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("<redacted %d bytes sha256:%s>", len(b), hex.EncodeToString(sum[:])[:8])
}

func redactToolErrorString(s string) string {
	s = toolErrorURLRe.ReplaceAllStringFunc(s, redactToolErrorURL)
	return toolErrorInlineSecretRe.ReplaceAllString(s, "$1<masked>")
}

// redactToolErrorURL drops userinfo and masks secret-named query
// parameters. The query is rewritten by hand so the parameter order
// and non-secret values stay exactly as the model sent them.
func redactToolErrorURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<url>"
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	if u.RawQuery != "" {
		parts := strings.Split(u.RawQuery, "&")
		for i, p := range parts {
			name, _, hasVal := strings.Cut(p, "=")
			dn, derr := url.QueryUnescape(name)
			if derr != nil {
				dn = name
			}
			if hasVal && isToolErrorSecretKey(normalizeToolErrorKey(dn)) {
				parts[i] = name + "=REDACTED"
			}
		}
		u.RawQuery = strings.Join(parts, "&")
	}
	if u.Fragment != "" {
		// Fragments carry OAuth implicit-flow tokens (#access_token=...).
		u.Fragment = ""
		u.RawFragment = ""
	}
	return u.String()
}

func truncateToolErrorString(s string) string {
	if utf8.RuneCountInString(s) <= toolErrorArgsMaxStrRune {
		return s
	}
	r := []rune(s)
	return string(r[:toolErrorArgsMaxStrRune]) + fmt.Sprintf("...<truncated %d bytes>", len(s))
}

func capToolErrorBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("...<truncated %d bytes>", len(s))
}
