package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/llm"
)

// The previous search is an independent reference for full-message matching
// and a benchmark baseline. It deliberately retains the original full preview.
func legacyTranscriptSearch(c *chat, query string) []transcriptMatch {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	matches := make([]transcriptMatch, 0, 8)
	for i, m := range c.msgs {
		plain := strings.Join(strings.Fields(m.text), " ")
		if strings.Contains(strings.ToLower(plain), q) {
			matches = append(matches, transcriptMatch{MessageIndex: i, Role: m.role, Preview: plain})
		}
	}
	return matches
}

func boundedTranscriptSearchReference(rows []transcriptMatch) []transcriptMatch {
	if rows == nil {
		return nil
	}
	out := append(make([]transcriptMatch, 0, len(rows)), rows...)
	for i := range out {
		runes := []rune(out[i].Preview)
		if len(runes) > transcriptSearchPreviewRunes {
			out[i].Preview = string(runes[:transcriptSearchPreviewRunes]) + "…"
		}
	}
	return out
}

func assertTranscriptSearchParity(t *testing.T, c *chat, query string) []transcriptMatch {
	t.Helper()
	want := boundedTranscriptSearchReference(legacyTranscriptSearch(c, query))
	got := c.search(query)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("search %q differs: got %#v want %#v", query, got, want)
	}
	return got
}

func TestTranscriptSearchCachePreservesUnicodeAndFullMessageMatching(t *testing.T) {
	c := newChat(80)
	c.addUser("Alpha\t\r\n beta\u00a0gamma")
	c.addAssistant("Zażółć GĘŚLĄ 日本語 😀 é\u2003spaces")
	c.addToolResult("read_lines", "line one\nline two", "")
	c.addAssistant(strings.Repeat("日😀x ", 5000) + " TARGET-at-the-end")
	for _, query := range []string{"", " ", "ALPHA BETA", "gamma", "gęślą", "日本語", "😀", "é spaces", "line two", "target-at-the-end", "missing"} {
		rows := assertTranscriptSearchParity(t, &c, query)
		for _, row := range rows {
			if !utf8.ValidString(row.Preview) || utf8.RuneCountInString(row.Preview) > transcriptSearchPreviewRunes+1 || len(row.Preview) > 4*transcriptSearchPreviewRunes+len("…") {
				t.Fatalf("unbounded/invalid UTF-8 preview: %d bytes", len(row.Preview))
			}
		}
	}
	rows := c.search("target-at-the-end")
	if len(rows) != 1 || rows[0].MessageIndex != 3 || strings.Contains(strings.ToLower(rows[0].Preview), "target-at-the-end") {
		t.Fatal("truncating the preview changed a match beyond its boundary")
	}
}

func TestTranscriptSearchMenuRedrawAndCursorReuseCurrentResult(t *testing.T) {
	m := New(Options{NoColor: true, Language: "en"})
	m.width, m.height = 80, 36
	m.chat.addAssistant("needle first\nsecond line")
	m.chat.addUser("needle second")
	next, _ := m.openTranscriptSearchMenu()
	m = next.(Model)
	m.menu.filter = "needle"
	first := m.filteredTranscriptRows()
	key := m.chat.searchCache.key
	for _, event := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyHome}, {Type: tea.KeyEnd}, {Type: tea.KeyPgUp}, {Type: tea.KeyPgDown}} {
		next, _ = m.Update(event)
		m = next.(Model)
		_ = m.View()
		rows := m.filteredTranscriptRows()
		if len(rows) != 2 || &rows[0] != &first[0] || m.chat.searchCache.key != key {
			t.Fatal("cursor movement or a value-receiver redraw rebuilt the search")
		}
	}
	if allocs := testing.AllocsPerRun(25, func() { _ = m.filteredTranscriptRows() }); allocs != 0 {
		t.Fatalf("unchanged filter allocated %.1f times per search", allocs)
	}
	m.menu.filter = " NEEdLE "
	if rows := m.filteredTranscriptRows(); &rows[0] != &first[0] {
		t.Fatal("an equivalent normalized query discarded the cached matches")
	}
	m.menu.filter = "second"
	assertTranscriptSearchParity(t, &m.chat, m.menu.filter)
	if m.chat.searchCache.key == key {
		t.Fatal("changed query reused the previous result")
	}
	m.menu.filter = ""
	if m.filteredTranscriptRows() != nil || m.chat.searchCache.valid || m.chat.searchCache.matches != nil || m.chat.searchCache.key.first != nil {
		t.Fatal("an empty filter retained previous results or history references")
	}
}

func TestTranscriptSearchInvalidatesMessageMutationsAndCopiedModels(t *testing.T) {
	operations := []struct {
		name   string
		change func(*chat)
	}{
		{"user", func(c *chat) { c.addUser("needle user") }},
		{"assistant", func(c *chat) { c.addAssistant("needle assistant") }},
		{"system", func(c *chat) { c.addSystem("needle system") }},
		{"tool", func(c *chat) { c.addToolResult("read_lines", "needle tool", "") }},
		{"document", func(c *chat) { c.addDocument("needle document") }},
		{"completion", func(c *chat) { c.addCompletion("needle completion", 12) }},
		{"remove", func(c *chat) { c.removeLastSystem("needle transient") }},
		{"fold", func(c *chat) { c.toggleMessage(0) }},
		{"thinking", func(c *chat) { c.toggleThinking() }},
		{"flush", func(c *chat) { c.appendCurrent("needle current"); c.flushCurrent() }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			c := newChat(80)
			c.msgs = make([]msg, 0, 16)
			c.addAssistant("needle original\nsecond line")
			c.addSystem("needle transient")
			before := append([]transcriptMatch(nil), c.search("needle")...)
			published := c.searchCache.matches
			key := c.searchCache.key
			copied := c
			operation.change(&copied)
			if c.searchCache.valid || c.searchCache.matches != nil || c.searchCache.key.first != nil || c.searchCache.revision == key.revision {
				t.Fatal("a mutation through a copied chat did not invalidate shared search state")
			}
			assertTranscriptSearchParity(t, &copied, "needle")
			assertTranscriptSearchParity(t, &c, "needle")
			if !reflect.DeepEqual(published, before) {
				t.Fatal("a new search modified an already published result")
			}
		})
	}
}

func TestTranscriptSearchResetLoadAndUnfinishedStream(t *testing.T) {
	c := newChat(80)
	c.addAssistant("needle complete")
	first := c.search("needle")
	c.appendCurrent("needle unfinished")
	if rows := c.search("needle"); len(rows) != 1 || &rows[0] != &first[0] {
		t.Fatal("unfinished stream changed the original completed-only search")
	}
	c.flushCurrent()
	if rows := assertTranscriptSearchParity(t, &c, "needle"); len(rows) != 2 {
		t.Fatal("flushed stream did not join the searchable transcript")
	}
	c.msgs = nil
	if len(assertTranscriptSearchParity(t, &c, "needle")) != 0 {
		t.Fatal("clearing the message slice reused previous matches")
	}
	c.addUser("needle old")
	c.search("needle")
	c.msgs = []msg{{role: roleAssistant, text: "different loaded transcript"}}
	if len(assertTranscriptSearchParity(t, &c, "needle")) != 0 {
		t.Fatal("same-length replacement history reused previous matches")
	}
	m := New(Options{NoColor: true, Language: "en"})
	m.chat.addAssistant("needle old session")
	m.chat.search("needle")
	oldCache := m.chat.searchCache
	m.applyResumedTranscript(&resumedTranscript{ID: "synthetic-loaded", Seqs: []int{1}, Messages: []llm.Message{{Role: llm.RoleAssistant, Content: "loaded session answer"}}})
	if m.chat.searchCache == oldCache || len(assertTranscriptSearchParity(t, &m.chat, "needle")) != 0 {
		t.Fatal("session loading retained the old session's search")
	}
	m.chat = newChat(80)
	if len(assertTranscriptSearchParity(t, &m.chat, "needle")) != 0 {
		t.Fatal("new chat retained a previous result")
	}
}

func transcriptSearchBenchmarkFixture() chat {
	c := newChat(80)
	c.msgs = make([]msg, 0, 1703)
	payload := strings.Repeat("Generated field ALPHA \t beta.\n", 100)
	for i := 0; i < 1703; i++ {
		tail := "ordinary"
		if i%17 == 0 {
			tail = "NEEDLE"
		}
		prefix := fmt.Sprintf("%04d ", i)
		text := prefix + payload[:2000-len(prefix)-len(tail)] + tail
		c.msgs = append(c.msgs, msg{role: role(i % 3), text: text})
	}
	return c
}

var transcriptSearchBenchmarkResult []transcriptMatch

func BenchmarkTranscriptSearchHistory1703(b *testing.B) {
	for _, query := range []string{"needle", "missing"} {
		b.Run(query, func(b *testing.B) {
			for _, variant := range []string{"original", "cold", "warm"} {
				b.Run(variant, func(b *testing.B) {
					c := transcriptSearchBenchmarkFixture()
					want := boundedTranscriptSearchReference(legacyTranscriptSearch(&c, query))
					if got := c.search(query); !reflect.DeepEqual(got, want) {
						b.Fatal("benchmark fixture search differs from the original")
					}
					b.SetBytes(1703 * 2000)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						switch variant {
						case "original":
							transcriptSearchBenchmarkResult = legacyTranscriptSearch(&c, query)
						case "cold":
							c.invalidateTranscriptSearch()
							transcriptSearchBenchmarkResult = c.search(query)
						case "warm":
							transcriptSearchBenchmarkResult = c.search(query)
						}
					}
				})
			}
		})
	}
}
