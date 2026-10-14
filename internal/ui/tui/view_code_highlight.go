package tui

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// Fenced code blocks are highlighted with chroma. Colours go through the
// palette renderer's termenv profile, so NO_COLOR / --no-color (Ascii)
// output carries no escapes and 16/256-colour terminals get degraded colours.
// Highlighted lines are width-independent; the role block wraps them later
// with ANSI-aware ansi.Wrap like every other markdown line.

const (
	codeHighlightCacheMax = 128
	codeLexerCacheMax     = 256
	codeTabSpaces         = "    " // lipgloss default tab width
	codeStyleDark         = "monokai"
	codeStyleLight        = "github"
)

type codeHighlightKey struct {
	lang, code string
	profile    termenv.Profile
	dark       bool
}

var codeHighlight struct {
	mu     sync.Mutex
	lexers map[string]chroma.Lexer // nil value = no lexer for the tag
	out    map[codeHighlightKey][]string
}

// isFenceLine reports whether line opens or closes a fenced code block.
func isFenceLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

// fenceLanguage extracts the language tag of an opening fence:
// "```go", "``` Python title=x", "```{.rust}" → go, python, rust.
func fenceLanguage(line string) string {
	tag := strings.TrimPrefix(strings.TrimSpace(line), "```")
	fields := strings.Fields(tag)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(strings.Trim(fields[0], "{}.`"))
}

// renderFencedBlock renders lines[start] (an opening fence) through the
// matching closing fence, or through the end of input for an unfinished
// streaming block. It returns the rendered text and the next line index.
func renderFencedBlock(lines []string, start int, p Palette) (string, int) {
	end := start + 1
	for end < len(lines) && !isFenceLine(lines[end]) {
		end++
	}
	code := make([]string, 0, end-start-1)
	for _, line := range lines[start+1 : end] {
		code = append(code, strings.TrimRight(strings.ReplaceAll(line, "\t", codeTabSpaces), " \t\r"))
	}
	var b strings.Builder
	b.WriteString(renderMarkdownLine(lines[start], p))
	highlighted := highlightCodeLines(fenceLanguage(lines[start]), code, p)
	for i, line := range lines[start+1 : end] {
		b.WriteByte('\n')
		if highlighted != nil {
			b.WriteString(highlighted[i])
		} else {
			// Unchanged pre-highlighting rendering for plain blocks.
			b.WriteString(renderMarkdownLine(line, p))
		}
	}
	if end >= len(lines) {
		return b.String(), end
	}
	b.WriteByte('\n')
	b.WriteString(renderMarkdownLine(lines[end], p))
	return b.String(), end + 1
}

// highlightCodeLines returns one coloured string per input line, or nil when
// the block must render plain (no colours, unknown language, lexer failure).
func highlightCodeLines(lang string, lines []string, p Palette) []string {
	return highlightCodeLinesFor(lang, lines, p.renderColors())
}

func highlightCodeLinesFor(lang string, lines []string, colors renderColors) []string {
	if lang == "" || len(lines) == 0 {
		return nil
	}
	if colors.profile == termenv.Ascii {
		return nil
	}
	key := codeHighlightKey{lang: lang, code: strings.Join(lines, "\n"), profile: colors.profile, dark: colors.dark}
	codeHighlight.mu.Lock()
	if out, ok := codeHighlight.out[key]; ok {
		codeHighlight.mu.Unlock()
		return out
	}
	lexer := cachedLexer(lang)
	codeHighlight.mu.Unlock()
	if lexer == nil {
		return nil
	}
	out := tokenizeCode(lexer, key, len(lines))
	codeHighlight.mu.Lock()
	if codeHighlight.out == nil || len(codeHighlight.out) >= codeHighlightCacheMax {
		codeHighlight.out = make(map[codeHighlightKey][]string)
	}
	codeHighlight.out[key] = out
	codeHighlight.mu.Unlock()
	return out
}

// cachedLexer resolves a fence tag by name/alias/extension once. Caller holds mu.
func cachedLexer(lang string) chroma.Lexer {
	if lexer, ok := codeHighlight.lexers[lang]; ok {
		return lexer
	}
	lexer := lexers.Get(lang)
	if lexer != nil && lexer.Config() != nil && lexer.Config().Name == "plaintext" {
		lexer = nil
	}
	if lexer != nil {
		lexer = chroma.Coalesce(lexer)
	}
	if codeHighlight.lexers == nil || len(codeHighlight.lexers) >= codeLexerCacheMax {
		codeHighlight.lexers = make(map[string]chroma.Lexer)
	}
	codeHighlight.lexers[lang] = lexer
	return lexer
}

func tokenizeCode(lexer chroma.Lexer, key codeHighlightKey, lineCount int) []string {
	style := styles.Get(codeStyleDark)
	if !key.dark {
		style = styles.Get(codeStyleLight)
	}
	it, err := lexer.Tokenise(nil, key.code+"\n")
	if err != nil {
		return nil
	}
	out := make([]string, 0, lineCount+1)
	var cur strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		// Escapes never span a newline, so wrapping and the gutter stay clean.
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			if part != "" {
				cur.WriteString(styleCodeToken(key.profile, style.Get(tok.Type), part))
			}
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	if len(out) < lineCount {
		return nil
	}
	out = out[:lineCount]
	// Never let a lexer alter the visible text; fall back to plain if it did.
	if ansi.Strip(strings.Join(out, "\n")) != key.code {
		return nil
	}
	return out
}

func styleCodeToken(profile termenv.Profile, entry chroma.StyleEntry, text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	s := profile.String(text)
	if entry.Colour.IsSet() {
		s = s.Foreground(profile.Color(entry.Colour.String()))
	}
	if entry.Bold == chroma.Yes {
		s = s.Bold()
	}
	if entry.Italic == chroma.Yes {
		s = s.Italic()
	}
	if entry.Underline == chroma.Yes {
		s = s.Underline()
	}
	return s.String()
}
