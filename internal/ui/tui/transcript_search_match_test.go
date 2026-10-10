package tui

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// Independent copy of the pre-streaming cold path, including its current
// bounded preview. No production matching or preview helper is used here.
func transcriptSearchColdReference(messages []msg, query string) []transcriptMatch {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	matches := make([]transcriptMatch, 0, 8)
	for i, m := range messages {
		plain := strings.Join(strings.Fields(m.text), " ")
		if strings.Contains(strings.ToLower(plain), q) {
			matches = append(matches, transcriptMatch{i, m.role, transcriptSearchReferencePreview(plain)})
		}
	}
	return matches
}

func transcriptSearchReferencePreview(plain string) string {
	runes := 0
	for offset := range plain {
		if runes == 512 {
			return plain[:offset] + "…"
		}
		runes++
	}
	return strings.Clone(plain)
}

func assertTranscriptSearchColdParity(t *testing.T, messages []msg, query string) {
	t.Helper()
	c := chat{msgs: messages}
	got, want := c.search(query), transcriptSearchColdReference(messages, query)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query %q: cold search differs\ngot  %#v\nwant %#v", query, got, want)
	}
	for i, m := range messages {
		plain := strings.Join(strings.Fields(m.text), " ")
		if got, want := transcriptSearchNormalizedPreview(m.text), transcriptSearchReferencePreview(plain); got != want {
			t.Fatalf("message %d: preview differs\ngot  %q\nwant %q", i, got, want)
		}
	}
}

func TestTranscriptSearchColdDifferential(t *testing.T) {
	messages := []msg{
		{role: roleUser, text: "\tAlpha\t\r\n beta\u00a0gamma\n"},
		{role: roleAssistant, text: "Zażółć GĘŚLĄ 日本語 😀 é\u2003spaces"},
		{role: roleSystem, text: "K İ Σ ẞ ſ STRASSE"},
		{role: roleDocument, text: "ababababc aaaaaaaaaaaaaaaaaaaaaaaaaaaaaab"},
		{role: roleAssistant, text: "\xffA\xc0\xaf\xed\xa0\x80\u2003Ω"},
		{role: roleSystem, text: strings.Repeat("日😀x ", 5000) + " TARGET-at-the-end"},
		{role: roleUser, text: "\u200bzero-width\ufeffBOM\x00NUL"},
		{role: roleAssistant, text: "\t\n\u2003"},
	}
	for _, query := range []string{
		"", " ", "\u2003", " ALPHA BETA ", "alpha  beta", "alpha\tbeta",
		"alpha\u00a0beta", "pha be", "GĘŚLĄ", "日本語", "😀", "é spaces",
		"é", "k i σ ß ſ", "ss", "strasse", "ſ", "s", "ababc", "aaaaab",
		"\xffa", "�a", "��", "Ω", "target-at-the-end", "missing", "\x00nul",
	} {
		assertTranscriptSearchColdParity(t, messages, query)
	}
	assertTranscriptSearchColdParity(t, nil, "needle")
	assertTranscriptSearchColdParity(t, nil, "")
	for _, bytes := range []int{16, 32, 63, 64, 65, 97, 128} {
		plain := "~" + strings.Repeat("a", bytes-1)
		overlap := strings.Repeat("a", bytes-1) + "c"
		mixed := strings.Repeat("aA", bytes)[:bytes-1] + "C"
		messages := []msg{
			{role: roleUser, text: plain + " early"},
			{role: roleAssistant, text: strings.Repeat("other words\t", 200) + plain},
			{role: roleSystem, text: strings.Repeat("a", 1999) + "b"},
			{role: roleDocument, text: mixed},
		}
		for _, query := range []string{plain, overlap, strings.ToUpper(overlap), mixed, "\u2003" + mixed + "\t"} {
			assertTranscriptSearchColdParity(t, messages, query)
		}
	}
}

func TestTranscriptSearchColdSelector(t *testing.T) {
	for _, tc := range []struct {
		query  string
		stdlib bool
	}{
		{"needle", false},
		{"alpha", false},
		{"~" + strings.Repeat("a", 63), false},
		{"~" + strings.Repeat("a", 64), true},
		{strings.Repeat("a", 15) + "c", true},
		{strings.Repeat("a", 31) + "c", true},
		{strings.Repeat("a", 63) + "c", true},
		{strings.Repeat("a", 64) + "c", true},
		{strings.Repeat("a", 96) + "c", true},
	} {
		if got := newTranscriptSearchPattern(tc.query).useStdlib; got != tc.stdlib {
			t.Fatalf("selector %q: stdlib=%v, want %v", tc.query, got, tc.stdlib)
		}
	}
}

func TestTranscriptSearchColdUnicodeAndPreviewBoundaries(t *testing.T) {
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if unicode.IsSpace(r) {
			assertTranscriptSearchColdParity(t, []msg{{role: roleUser, text: string(r) + "A" + string(r) + string(r) + "B" + string(r)}}, "a b")
		}
		if lower := unicode.ToLower(r); lower != r {
			assertTranscriptSearchColdParity(t, []msg{{role: roleAssistant, text: "\t" + string(r) + "X\n"}}, string(lower)+"x")
		}
	}
	for _, text := range []string{
		strings.Repeat("x", 511), strings.Repeat("x", 512), strings.Repeat("x", 513),
		strings.Repeat("x", 512) + "\t\u2003\n",
		strings.Repeat("x", 511) + "\t\u2003Y",
		strings.Repeat("日", 512) + "\u00a0😀",
		strings.Repeat("\xff", 512) + "\n",
		strings.Repeat("\xff", 512) + "X",
	} {
		assertTranscriptSearchColdParity(t, []msg{{role: roleSystem, text: text}}, "x")
	}
	for _, q := range []string{strings.Repeat("K", 64), strings.Repeat("日", 22), strings.Repeat("\xff", 22)} {
		assertTranscriptSearchColdParity(t, []msg{{role: roleAssistant, text: "\n" + q + " end"}}, q)
	}
}

func TestTranscriptSearchColdRandomizedParity(t *testing.T) {
	rng := rand.New(rand.NewSource(20261008))
	atoms := []string{"a", "A", "B", "0", " ", "\t", "\n", "\r", "\v", "\f", "\u0085", "\u00a0", "\u2003", "\u2028", "\u3000", "日", "😀", "Σ", "K", "İ", "ſ", "é", "\xff", "\xc0", "\xaf", "\x00"}
	for iteration := 0; iteration < 2000; iteration++ {
		var text, query strings.Builder
		for n := rng.Intn(900); n > 0; n-- {
			text.WriteString(atoms[rng.Intn(len(atoms))])
		}
		if iteration%2 == 0 {
			plain := strings.ToLower(strings.Join(strings.Fields(text.String()), " "))
			runes := []rune(plain)
			if len(runes) > 0 {
				start := rng.Intn(len(runes))
				query.WriteString(string(runes[start:min(len(runes), start+1+rng.Intn(12))]))
			}
		} else {
			for n := rng.Intn(12); n > 0; n-- {
				query.WriteString(atoms[rng.Intn(len(atoms))])
			}
		}
		assertTranscriptSearchColdParity(t, []msg{{role: role(iteration % 4), text: text.String()}}, query.String())
	}
}

func TestTranscriptSearchColdWarmReuse(t *testing.T) {
	for _, query := range []string{"needle", "missing", "�a", strings.Repeat("a", 31) + "c"} {
		c := chat{msgs: []msg{
			{role: roleUser, text: "NEEDLE first"},
			{role: roleAssistant, text: "\xffA 日本語"},
			{role: roleSystem, text: strings.Repeat("a", 31) + "c"},
		}}
		published := c.search(query)
		if !reflect.DeepEqual(published, transcriptSearchColdReference(c.msgs, query)) {
			t.Fatal("warm fixture differs")
		}
		key := c.searchCache.key
		if allocs := testing.AllocsPerRun(50, func() { _ = c.search(query) }); allocs != 0 {
			t.Fatalf("warm query %q allocated %.1f times", query, allocs)
		}
		got := c.search(query)
		if c.searchCache.key != key || (len(got) > 0 && &got[0] != &published[0]) {
			t.Fatal("warm query rebuilt published results")
		}
	}
}

func FuzzTranscriptSearchColdParity(f *testing.F) {
	for _, seed := range [][2]string{
		{"Alpha\t\n beta", "ALPHA BETA"},
		{"K\u2003İ\xffA", "�a"},
		{strings.Repeat("a", 2048) + "b", strings.Repeat("a", 31) + "c"},
		{strings.Repeat("日😀 ", 400) + "late", "late"},
		{"\xff\xc0\xaf", "\xff"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, text, query string) {
		if len(text) > 128*1024 || len(query) > 4096 {
			t.Skip()
		}
		assertTranscriptSearchColdParity(t, []msg{{role: roleAssistant, text: text}}, query)
	})
}
