package search

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func discoveryFixture(t testing.TB, count int) *ToolSearcher {
	t.Helper()
	reg := NewRegistry()
	descriptions := []string{
		"Read project files and search directory paths. Return matching content with line numbers.",
		"Write project files and update source code. Apply an exact edit to an existing file.",
		"Inspect project metadata, directory names and repository status without modifying files.",
		"Search remote documents and read indexed project information with bounded results.",
	}
	for i := 0; i < count; i++ {
		reg.MustRegister(Tool{Name: fmt.Sprintf("mcp_project_%04d", i), Description: descriptions[i%len(descriptions)], Schema: `{"type":"object","properties":{"path":{"type":"string"},"query":{"type":"string"},"limit":{"type":"integer"}},"required":["path"]}`, Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }})
	}
	return NewToolSearcher(reg, nil)
}

func BenchmarkToolDiscovery(b *testing.B) {
	for _, count := range []int{64, 512} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s := discoveryFixture(b, count)
			for _, kind := range []string{"intent", "exact"} {
				b.Run(kind, func(b *testing.B) {
					query := "read project files directory paths"
					if kind == "exact" {
						query = "mcp_project_0032"
					}
					args, _ := json.Marshal(searchArgs{Query: query, Limit: 3})
					b.ReportAllocs()
					for b.Loop() {
						result, err := s.execute(context.Background(), args)
						if err != nil || result.Err != nil || !strings.Contains(result.Text, "matches") {
							b.Fatalf("%+v %v", result, err)
						}
					}
				})
			}
		})
	}
}

func (s *ToolSearcher) previousLexicalFallback(query string, limit int) []SearchResult {
	if s.Registry == nil {
		return nil
	}
	qTokens := previousLexTokens(query)
	if len(qTokens) == 0 {
		return nil
	}
	type scored struct {
		name   string
		server string
		score  int
	}
	var ranked []scored
	for _, name := range s.Registry.Names() {
		// Meta-tools are gateways, never useful search answers.
		if name == "tool_search" || name == "invoke_tool" {
			continue
		}
		t, ok := s.Registry.Get(name)
		if !ok {
			continue
		}
		haystack := make(map[string]struct{})
		for _, w := range previousLexTokens(t.Name + " " + t.Description) {
			haystack[w] = struct{}{}
		}
		overlap := 0
		for _, q := range qTokens {
			if _, ok := haystack[q]; ok {
				overlap++
			}
		}
		if overlap > 0 {
			ranked = append(ranked, scored{name: name, server: classifyServer(name), score: overlap})
		}
	}
	// Sort by descending overlap, then name for determinism.
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && (ranked[j].score > ranked[j-1].score ||
			(ranked[j].score == ranked[j-1].score && ranked[j].name < ranked[j-1].name)); j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]SearchResult, 0, len(ranked))
	for _, r := range ranked {
		// Normalize overlap count to a 0..1 score so the shape
		// matches FTS hits. Cap denominator at len(qTokens).
		out = append(out, SearchResult{
			Name:   r.name,
			Server: r.server,
			Score:  float64(r.score) / float64(len(qTokens)),
		})
	}
	return out
}

// previousLexTokens lowercases s and splits it into word tokens of 3+
// chars, dropping a few common stopwords that add no signal to
// a tool query. Used only by the lexical fallback.
func previousLexTokens(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(f) < 3 {
			continue
		}
		switch f {
		case "the", "and", "for", "with", "into", "from", "use", "all":
			continue
		}
		out = append(out, f)
	}
	return out
}

func TestLexicalTokensPreservePreviousSemantics(t *testing.T) {
	samples := []string{"", "a ab abc", "the and for with into from use all", "read_READ.read/file:file", "Zażółć 中文 😀", "KEY İTEM ẞtraße", string([]byte{0xff, 'R', 'E', 'A', 'D', 0, 0x80})}
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	samples = append(samples, string(all))
	rng := rand.New(rand.NewSource(41))
	for i := 0; i < 500; i++ {
		buf := make([]byte, rng.Intn(256))
		for j := range buf {
			buf[j] = byte(rng.Intn(256))
		}
		samples = append(samples, string(buf))
	}
	for _, sample := range samples {
		got, want := lexTokens(sample), previousLexTokens(sample)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("tokens for %q: got=%q want=%q", sample, got, want)
		}
	}
}

func TestLexicalDiscoveryPreservesRanking(t *testing.T) {
	s := discoveryFixture(t, 96)
	for _, tool := range []Tool{
		{Name: "tool_search", Description: "read files directory copper", Schema: "{}"},
		{Name: "invoke_tool", Description: "read files directory copper", Schema: "{}"},
		{Name: "read_copper", Description: "Read read read copper copper source; KEY path, 中文.", Schema: "{}"},
		{Name: "Read_copper", Description: "copper source code and directory", Schema: "{}"},
	} {
		tool.Fn = func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }
		s.Registry.MustRegister(tool)
	}
	queries := []string{"read project files directory paths", "read read read files", "copper copper source", "KEY path", "mcp project 0001", "read_READ.read/file:file", "the and with", "nothing_matches", "", string([]byte{0xff, 'r', 'e', 'a', 'd', 0x80})}
	for _, query := range queries {
		for _, limit := range []int{-1, 0, 1, 3, 8, 1000} {
			got, want := s.lexicalFallback(query, limit), s.previousLexicalFallback(query, limit)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("query=%q limit=%d got=%+v want=%+v", query, limit, got, want)
			}
		}
	}
	// No cached catalog: registration and explicit registry replacement are visible.
	if len(s.lexicalFallback("uniquecapability", 3)) != 0 {
		t.Fatal("unexpected initial hit")
	}
	s.Registry.MustRegister(Tool{Name: "late_extension", Description: "uniquecapability", Schema: "{}", Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }})
	if got := s.lexicalFallback("uniquecapability", 3); len(got) != 1 || got[0].Name != "late_extension" {
		t.Fatalf("late tool missing: %+v", got)
	}
	s.Registry = NewRegistry()
	if got := s.lexicalFallback("uniquecapability", 3); len(got) != 0 {
		t.Fatalf("stale/restricted-registry hit: %+v", got)
	}
}

func TestLexicalDiscoveryActivationAndUnavailableIndex(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			s := discoveryFixture(t, 64)
			if broken {
				idx, err := NewInMemoryIndex()
				if err != nil {
					t.Fatal(err)
				}
				if err = idx.Close(); err != nil {
					t.Fatal(err)
				}
				s.Index = idx
			}
			query := "read read project files directory paths"
			want := s.previousLexicalFallback(query, 3)
			args, _ := json.Marshal(searchArgs{Query: query, Limit: 3})
			result, err := s.execute(context.Background(), args)
			if err != nil || result.Err != nil {
				t.Fatalf("%+v %v", result, err)
			}
			var response struct {
				Matches []struct {
					Name, Server, Signature, Schema string
					Score                           float64
				}
			}
			if err := json.Unmarshal([]byte(result.Text), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Matches) != len(want) {
				t.Fatalf("matches=%+v want=%+v", response.Matches, want)
			}
			names := map[string]bool{}
			for i, hit := range response.Matches {
				tool, _ := s.Registry.Get(hit.Name)
				if hit.Name != want[i].Name || hit.Server != want[i].Server || hit.Score != want[i].Score || hit.Schema != tool.Schema || hit.Signature != toolSignature(tool.Name, tool.Schema) {
					t.Fatalf("changed result: %+v want=%+v", hit, want[i])
				}
				names[hit.Name] = true
			}
			for _, name := range s.Registry.Names() {
				if s.Registry.IsActive(name) != names[name] {
					t.Fatalf("wrong activation: %s", name)
				}
			}
		})
	}
}

func TestLexicalDiscoveryConcurrentQueries(t *testing.T) {
	s := discoveryFixture(t, 64)
	queries := []string{"read read files", "write source code", "inspect metadata status", "search remote documents"}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		query := queries[i%len(queries)]
		want := s.previousLexicalFallback(query, 8)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				if got := s.lexicalFallback(query, 8); !reflect.DeepEqual(got, want) {
					t.Errorf("concurrent query %q changed ranking", query)
					return
				}
			}
		}()
	}
	wg.Wait()
}
