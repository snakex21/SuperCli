package stats

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNoop_DiscardsEverything(t *testing.T) {
	r := NewNoop()
	r.StartStep(1)
	r.RecordTokens(10, 5)
	r.RecordTools([]string{"foo", "bar"})
	r.RecordSources(map[string]int{"a": 1})
	r.EndStep()
	if got := r.Snapshot(); got != nil {
		t.Errorf("Noop.Snapshot = %v, want nil", got)
	}
}

func TestMemory_RecordsTurn(t *testing.T) {
	r := NewMemory()
	r.StartStep(1)
	r.RecordTokens(100, 25)
	r.RecordTools([]string{"foo", "bar"})
	r.RecordSources(map[string]int{"claude_md": 200, "memory": 50})
	r.EndStep()
	turns := r.Snapshot()
	if len(turns) != 1 {
		t.Fatalf("len = %d", len(turns))
	}
	if turns[0].Step != 1 {
		t.Errorf("Step = %d", turns[0].Step)
	}
	if turns[0].TokensIn != 100 || turns[0].TokensOut != 25 {
		t.Errorf("tokens = %+v", turns[0])
	}
	if turns[0].Tools[0] != "bar" || turns[0].Tools[1] != "foo" {
		t.Errorf("tools not sorted: %v", turns[0].Tools)
	}
	if turns[0].Sources["claude_md"] != 200 {
		t.Errorf("sources = %+v", turns[0].Sources)
	}
}

func TestMemory_EndStep_RecordsDuration(t *testing.T) {
	r := NewMemory()
	r.StartStep(1)
	r.EndStep()
	turns := r.Snapshot()
	if turns[0].DurationMs < 0 {
		t.Errorf("DurationMs = %d", turns[0].DurationMs)
	}
}

func TestMemory_ToolsUnique(t *testing.T) {
	r := NewMemory()
	r.StartStep(1)
	r.RecordTools([]string{"a", "b", "a", "c", "b"})
	r.EndStep()
	turns := r.Snapshot()
	if len(turns[0].Tools) != 3 {
		t.Errorf("tools = %v, want 3 unique", turns[0].Tools)
	}
}

func TestMemory_RecordBeforeStart_IsNoop(t *testing.T) {
	r := NewMemory()
	r.RecordTokens(10, 5)
	r.RecordTools([]string{"x"})
	r.RecordSources(map[string]int{"a": 1})
	r.EndStep()
	if turns := r.Snapshot(); len(turns) != 0 {
		t.Errorf("Snapshot = %v, want empty", turns)
	}
}

func TestMemory_Reset(t *testing.T) {
	r := NewMemory()
	r.StartStep(1)
	r.EndStep()
	r.Reset()
	if turns := r.Snapshot(); len(turns) != 0 {
		t.Errorf("after Reset: %v", turns)
	}
}

func TestMemory_Concurrent(t *testing.T) {
	r := NewMemory()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.StartStep(1)
			r.RecordTokens(1, 1)
			r.EndStep()
		}()
	}
	wg.Wait()
	// We don't assert an exact count because of races between
	// StartStep/EndStep across goroutines, but the recorder
	// must not panic.
}

func TestSum(t *testing.T) {
	turns := []Turn{
		{Step: 1, TokensIn: 10, TokensOut: 5},
		{Step: 2, TokensIn: 20, TokensOut: 3},
	}
	total := Sum(turns)
	if total.Turns != 2 || total.TokensIn != 30 || total.TokensOut != 8 {
		t.Errorf("Sum = %+v", total)
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")
	turns := []Turn{{Step: 1, TokensIn: 10, TokensOut: 5, Tools: []string{"x"}}}
	if err := Save(path, turns); err != nil {
		t.Fatalf("Save: %v", err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(back) != 1 || back[0].Step != 1 {
		t.Errorf("back = %+v", back)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	dir := t.TempDir()
	turns, err := Load(filepath.Join(dir, "missing.json"))
	if err != nil {
		t.Errorf("Load(missing): %v", err)
	}
	if turns != nil {
		t.Errorf("turns = %v", turns)
	}
}

func TestLoad_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")
	os.WriteFile(path, []byte("not json"), 0o644)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error on corrupt file")
	}
}

func TestPrint_Empty(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, nil)
	if !strings.Contains(buf.String(), "no stats") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestPrint_NonEmpty(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, []Turn{
		{Step: 1, TokensIn: 100, TokensOut: 50, DurationMs: 1234, Tools: []string{"a"}},
		{Step: 2, TokensIn: 200, TokensOut: 75, DurationMs: 567, Tools: nil},
	})
	out := buf.String()
	if !strings.Contains(out, "step") {
		t.Errorf("missing header: %q", out)
	}
	if !strings.Contains(out, "total: 2 turns") {
		t.Errorf("missing total: %q", out)
	}
	if !strings.Contains(out, "a") {
		t.Errorf("missing tools: %q", out)
	}
	if !strings.Contains(out, "-") {
		t.Errorf("missing dash for empty tools: %q", out)
	}
}

func TestJSON_RoundtripWithImage(t *testing.T) {
	turn := Turn{
		Step:       1,
		TokensIn:   10,
		TokensOut:  5,
		Tools:      []string{"read_image"},
		Sources:    map[string]int{"memory": 100},
		StartedAt:  parseTime("2026-06-06T12:00:00Z"),
		DurationMs: 1234,
	}
	buf, err := json.Marshal(turn)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back Turn
	if err := json.Unmarshal(buf, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Step != 1 || back.TokensIn != 10 {
		t.Errorf("back = %+v", back)
	}
}
