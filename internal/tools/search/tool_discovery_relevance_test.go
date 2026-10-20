package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func relevanceDiscoveryFixture(t testing.TB, indexed bool) *ToolSearcher {
	t.Helper()
	reg := NewRegistry()
	for _, spec := range []Tool{
		{Name: "mcp_mail.search", Description: "Search current messages and report mailbox statistics. Statystyki skrzynki pocztowej.", Schema: `{"type":"object","description":"` + strings.Repeat("mail contract ", 700) + `","properties":{"query":{"type":"string"}}}`},
		{Name: "mcp_drive.search", Description: "Search files and documents in cloud drive.", Schema: `{"type":"object","properties":{"query":{"type":"string"}}}`},
		{Name: "mcp_disk.search", Description: "Search files and documents on local disk.", Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`},
		{Name: "list_dir", Description: "List directory entries and file names.", Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`},
		{Name: "read_lines", Description: "Read lines from a text file.", Schema: `{"type":"object","properties":{"path":{"type":"string"}}}`},
	} {
		spec.Fn = func(context.Context, json.RawMessage) (Result, error) {
			t.Error("discovery executed a tool")
			return Result{}, nil
		}
		reg.MustRegister(spec)
	}
	var index *Index
	if indexed {
		var err error
		index, err = NewInMemoryIndex()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = index.Close() })
	}
	searcher := NewToolSearcher(reg, index)
	if indexed {
		if err := searcher.RebuildIndex(); err != nil {
			t.Fatal(err)
		}
	}
	return searcher
}

// Identical deterministic sequence before and after the discovery policy change.
// Counts are actual response/schema bytes, not token or HTTP estimates.
func TestDiscoveryRelevanceMeasurement(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		t.Run(fmt.Sprint(indexed), func(t *testing.T) {
			s := relevanceDiscoveryFixture(t, indexed)
			bytes, schemas, activations := 0, 0, 0
			for _, query := range []string{
				"current conversation telemetry token usage statistics",
				"statystyki bieżącej rozmowy zużycie tokenów",
				"report request latency performance statistics",
				"search files",
			} {
				s.Registry.ResetVisibility()
				raw, _ := json.Marshal(searchArgs{Query: query})
				result, err := s.execute(context.Background(), raw)
				if err != nil || result.Err != nil {
					t.Fatalf("execute: %v / %v", err, result.Err)
				}
				var response discoveryResponse
				if err := json.Unmarshal([]byte(result.Text), &response); err != nil {
					t.Fatal(err)
				}
				bytes += len(result.Text)
				for _, hit := range response.Matches {
					schemas += len(hit.Schema)
				}
				active := 0
				for _, name := range s.Registry.Names() {
					if s.Registry.IsActive(name) {
						active++
					}
				}
				activations += active
				if active != 0 || (query != "search files" && len(response.Matches) != 0) {
					t.Fatalf("irrelevant/ambiguous discovery changed active schemas: query=%q active=%d matches=%d", query, active, len(response.Matches))
				}
				if query == "search files" && len(response.Matches) != 2 {
					t.Fatal("valid ambiguous candidates were lost")
				}
				t.Logf("query=%q response_bytes=%d active=%d", query, len(result.Text), active)
			}
			if schemas != 0 {
				t.Fatalf("unselected candidates leaked %d schema bytes", schemas)
			}
			t.Logf("sequence response_bytes=%d schema_bytes=%d activations=%d", bytes, schemas, activations)
		})
	}
}

func TestDiscoveryRelevancePLENAndAmbiguityBeforeLimit(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		for _, tc := range []struct {
			query string
			limit int
			want  string
		}{
			{"list directory", 0, "list_dir"},
			{"wylistuj katalog", 0, "list_dir"},
			{"read text lines", 0, "read_lines"},
			{"mcp_drive.search", 0, "mcp_drive.search"},
			{"MCP_DRIVE.SEARCH", 0, "mcp_drive.search"},
			{"search files", 1, ""},
			{"wyszukaj pliki", 1, ""},
		} {
			t.Run(fmt.Sprintf("%v/%s", indexed, tc.query), func(t *testing.T) {
				s := relevanceDiscoveryFixture(t, indexed)
				raw, _ := json.Marshal(searchArgs{Query: tc.query, Limit: tc.limit})
				result, err := s.execute(context.Background(), raw)
				if err != nil || result.Err != nil {
					t.Fatalf("execute: %v / %v", err, result.Err)
				}
				var response discoveryResponse
				if err := json.Unmarshal([]byte(result.Text), &response); err != nil {
					t.Fatal(err)
				}
				if len(response.Matches) != 1 {
					t.Fatalf("expected one visible result: %+v", response)
				}
				match := response.Matches[0]
				if tc.want != "" {
					if match.Name != tc.want || match.Schema == "" || !s.Registry.IsActive(tc.want) {
						t.Fatalf("known capability lost its contract/activation: %+v", match)
					}
				} else if match.Schema != "" || match.Signature != "" || match.Description == "" || !strings.Contains(response.Hint, "No tools were activated") {
					t.Fatalf("limit=1 hid ambiguity: %+v", response)
				}
				for _, name := range s.Registry.Names() {
					if s.Registry.IsActive(name) != (name == tc.want) {
						t.Fatalf("unrequested activation: %s", name)
					}
				}
			})
		}
	}
}

func TestDiscoveryRejectsSchemaOnlyHitsAndKeepsActiveSchemas(t *testing.T) {
	s := relevanceDiscoveryFixture(t, true)
	s.Registry.MustRegister(Tool{Name: "mcp_calendar.manage", Description: "Manage calendar events", Schema: `{"type":"object","description":"conversation telemetry token usage statistics"}`, Fn: func(context.Context, json.RawMessage) (Result, error) {
		t.Fatal("discovery started an unrelated server")
		return Result{}, nil
	}})
	if err := s.RebuildIndex(); err != nil {
		t.Fatal(err)
	}
	s.Registry.ActivateDiscovered("read_lines")
	revision := s.Registry.Revision()
	for _, query := range []string{"conversation telemetry token usage statistics", "current conversation statistics", "report conversation statistics", "statystyki bieżącej rozmowy", "search files", "read_lines", "read_lines"} {
		raw, _ := json.Marshal(searchArgs{Query: query})
		result, err := s.execute(context.Background(), raw)
		if err != nil || result.Err != nil {
			t.Fatalf("execute: %v / %v", err, result.Err)
		}
		var response discoveryResponse
		if err := json.Unmarshal([]byte(result.Text), &response); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(query, "conversation") || strings.Contains(query, "rozmowy") {
			if len(response.Matches) != 0 {
				t.Fatalf("schema boilerplate became a capability: %+v", response)
			}
		}
		for _, name := range s.Registry.Names() {
			if s.Registry.IsActive(name) != (name == "read_lines") {
				t.Fatalf("discovery changed existing schemas: %s", name)
			}
		}
		if s.Registry.Revision() != revision {
			t.Fatal("miss, ambiguous query or already-active exact name changed the registry revision")
		}
	}
}

func TestDiscoveryAmbiguousCaseFoldAndBoundedUnicodeMetadata(t *testing.T) {
	s := relevanceDiscoveryFixture(t, false)
	s.Registry.MustRegister(Tool{Name: "Read_Lines", Description: "Read lines from text. " + strings.Repeat("zażółć 中文 😀 ", 200), Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }})
	if exactToolNameHit(s.Registry, "READ_LINES") != nil {
		t.Fatal("case folding guessed one of two registered identities")
	}
	result, err := s.execute(context.Background(), json.RawMessage(`{"query":"READ_LINES","limit":1}`))
	if err != nil || result.Err != nil {
		t.Fatalf("execute: %v / %v", err, result.Err)
	}
	var response discoveryResponse
	if err := json.Unmarshal([]byte(result.Text), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Matches) != 1 || len([]rune(response.Matches[0].Description)) > 160 || response.Matches[0].Schema != "" {
		t.Fatalf("ambiguous metadata not bounded: %+v", response)
	}
	for _, name := range s.Registry.Names() {
		if s.Registry.IsActive(name) {
			t.Fatalf("case-fold ambiguity activated %s", name)
		}
	}
}
