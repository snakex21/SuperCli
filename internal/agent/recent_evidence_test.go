package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func evidenceBatch(id, text string) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_lines", Arguments: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: id, Name: "read_lines", Content: text},
	}
}

func TestRecentEvidenceIsBoundedChronologicalAndNonMutating(t *testing.T) {
	m := []llm.Message{{Role: llm.RoleUser, Content: "old"}}
	m = append(m, evidenceBatch("old", "obsolete tiny finding")...)
	m = append(m, llm.Message{Role: llm.RoleAssistant, Content: "done"}, llm.Message{Role: llm.RoleUser, Content: "inspect"})
	for i := 0; i < 12; i++ {
		m = append(m, evidenceBatch(fmt.Sprint(i), strings.Repeat("finding ", 80))...)
	}
	m = append(m, llm.Message{Role: llm.RoleAssistant, Content: "finished"}, llm.Message{Role: llm.RoleUser, Content: "continue"})
	before, _ := json.Marshal(m)
	got := omitResolvedToolHistory(m)
	after, _ := json.Marshal(m)
	if string(before) != string(after) {
		t.Fatal("projection mutated canonical history")
	}
	cost := 0
	seen := map[string]bool{}
	for i, v := range got {
		if len(v.ToolCalls) > 0 {
			if i+1 >= len(got) || got[i+1].ToolCallID != v.ToolCalls[0].ID {
				t.Fatal("orphan call")
			}
			cost += toolEvidenceBytes(v)
		}
		if v.Role == llm.RoleTool {
			seen[v.ToolCallID] = true
			cost += toolEvidenceBytes(v)
		}
	}
	if cost > recentToolEvidenceBytes || len(seen) == 0 || !seen["11"] || seen["old"] || seen["0"] {
		t.Fatalf("cost=%d retained=%v", cost, seen)
	}
	// A completed follow-up with no tools retires prior-turn evidence too.
	m = append(m, llm.Message{Role: llm.RoleAssistant, Content: "explained"}, llm.Message{Role: llm.RoleUser, Content: "new question"})
	if requestContainsToolProtocol(omitResolvedToolHistory(m)) {
		t.Fatal("old evidence accumulated")
	}
}

func TestRecentEvidenceKeepsCompletePairsAndActiveTail(t *testing.T) {
	m := []llm.Message{{Role: llm.RoleUser, Content: "inspect"}}
	m = append(m, evidenceBatch("small", "specific reusable value")...)
	m = append(m, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
		{ID: "large", Name: "read_lines", Arguments: "{}"}, {ID: "paired", Name: "read_lines", Arguments: "{}"},
	}}, llm.Message{Role: llm.RoleTool, ToolCallID: "large", Content: strings.Repeat("x", recentToolEvidenceBytes)},
		llm.Message{Role: llm.RoleTool, ToolCallID: "paired", Content: "tiny"},
		llm.Message{Role: llm.RoleAssistant, Content: "finished"}, llm.Message{Role: llm.RoleUser, Content: "continue"})
	m = append(m, evidenceBatch("active", strings.Repeat("new", recentToolEvidenceBytes))...)
	got := omitResolvedToolHistory(m)
	ids := []string{}
	for _, v := range got {
		if v.Role == llm.RoleTool {
			ids = append(ids, v.ToolCallID)
		}
	}
	if !reflect.DeepEqual(ids, []string{"small", "paired", "active"}) {
		t.Fatal(ids)
	}
}

func TestRecentEvidenceDoesNotReplayMultimodalParts(t *testing.T) {
	m := []llm.Message{{Role: llm.RoleUser, Content: "inspect"}}
	m = append(m, evidenceBatch("image", "image result")...)
	m[2].Parts = []llm.ContentPart{{Type: llm.PartTypeImage, Image: &llm.ImageRef{MediaType: "image/png", Data: "AA=="}}}
	m = append(m, llm.Message{Role: llm.RoleAssistant, Content: "done"}, llm.Message{Role: llm.RoleUser, Content: "continue"})
	if requestContainsToolProtocol(omitResolvedToolHistory(m)) {
		t.Fatal("replayed image evidence")
	}
}

func TestOrchestratorDiscoveryUsesItsOwnRegistry(t *testing.T) {
	base := orchestratorBaseRegistry()
	// Replace fixture's placeholder closure with the real parent-bound searcher.
	base2 := tools.NewRegistry()
	for _, name := range base.Names() {
		if name != "tool_search" {
			spec, _ := base.Get(name)
			base2.MustRegister(spec)
		}
	}
	base2.MustRegister(tools.NewToolSearcher(base2, nil).Spec())
	out := OrchestratorRegistry(base2)
	search, _ := out.Get("tool_search")
	for _, q := range []string{"search_code", "read_many", "search_history"} {
		raw, _ := json.Marshal(map[string]string{"query": q})
		res, err := search.Fn(context.Background(), raw)
		if err != nil || res.Err != nil || !strings.Contains(res.Text, q) {
			t.Fatalf("%s: %+v %v", q, res, err)
		}
		if base2.IsVisible(q) {
			t.Fatalf("discovery changed parent's activation: %s", q)
		}
	}
	res, err := search.Fn(context.Background(), json.RawMessage("{\"query\":\"write_file\"}"))
	if err != nil || res.Err != nil {
		t.Fatalf("%+v %v", res, err)
	}
	var discovery struct {
		Matches []struct {
			Name string `json:"name"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(res.Text), &discovery); err != nil {
		t.Fatal(err)
	}
	for _, match := range discovery.Matches {
		if match.Name == "write_file" {
			t.Fatal("discovery advertised forbidden tool")
		}
	}
}
