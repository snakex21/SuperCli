package reflect

import (
	"context"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
)

func TestModelReflector_StripsSystemMessages(t *testing.T) {
	r := &ModelReflector{
		Provider:    echoProvider(t, "looks good"),
		HistoryTail: 10,
	}
	r.Prepare()
	hist := []llm.Message{
		{Role: llm.RoleSystem, Content: "you are cli"},
		{Role: llm.RoleUser, Content: "do X"},
		{Role: llm.RoleAssistant, Content: "doing X"},
		{Role: llm.RoleSystem, Content: "[reflection checkpoint @ step 1] all good"},
		{Role: llm.RoleUser, Content: "now do Y"},
	}
	tr := r.transcript(hist)
	if strings.Contains(tr, "you are cli") {
		t.Errorf("transcript contains system prompt: %q", tr)
	}
	if strings.Contains(tr, "reflection checkpoint") {
		t.Errorf("transcript contains prior reflection: %q", tr)
	}
	if !strings.Contains(tr, "do X") || !strings.Contains(tr, "now do Y") {
		t.Errorf("transcript missing user content: %q", tr)
	}
}

func TestModelReflector_TruncatesHistoryToTail(t *testing.T) {
	r := &ModelReflector{
		Provider:    echoProvider(t, "ok"),
		HistoryTail: 2,
	}
	r.Prepare()
	hist := []llm.Message{
		{Role: llm.RoleUser, Content: "1"},
		{Role: llm.RoleUser, Content: "2"},
		{Role: llm.RoleUser, Content: "3"},
		{Role: llm.RoleUser, Content: "4"},
	}
	tr := r.transcript(hist)
	if strings.Contains(tr, "] 1") || strings.Contains(tr, "] 2") {
		t.Errorf("transcript kept too many old messages: %q", tr)
	}
	if !strings.Contains(tr, "] 3") || !strings.Contains(tr, "] 4") {
		t.Errorf("transcript dropped the last 2: %q", tr)
	}
}

func TestModelReflector_DefaultsApplied(t *testing.T) {
	r := &ModelReflector{Provider: echoProvider(t, "ok")}
	r.Prepare()
	if r.tail != 5 {
		t.Errorf("tail = %d, want 5", r.tail)
	}
	if r.maxTok != 200 {
		t.Errorf("maxTok = %d, want 200", r.maxTok)
	}
}

func TestModelReflector_ReflectReturnsTrimmedText(t *testing.T) {
	r := &ModelReflector{Provider: echoProvider(t, "  working.  next: foo.\n")}
	r.Prepare()
	out, err := r.Reflect(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "x"},
	})
	if err != nil {
		t.Fatalf("Reflect: %v", err)
	}
	if out != "working.  next: foo." {
		t.Errorf("Reflect = %q, want trimmed", out)
	}
}

func TestModelReflector_NilProviderFails(t *testing.T) {
	var r *ModelReflector
	_, err := r.Reflect(context.Background(), nil)
	if err == nil {
		t.Error("expected error from nil receiver")
	}
	r2 := &ModelReflector{}
	_, err = r2.Reflect(context.Background(), nil)
	if err == nil {
		t.Error("expected error from nil provider")
	}
}

func TestModelReflector_StreamErrorPropagates(t *testing.T) {
	// Provider that emits a single delta with Err set, then
	// closes. Reflect should return the partial text and
	// the error.
	prov := &failingProvider{}
	r := &ModelReflector{Provider: prov, HistoryTail: 3}
	r.Prepare()
	_, err := r.Reflect(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "x"}})
	if err == nil {
		t.Error("expected error from stream failure")
	}
}

// echoProvider returns a stub provider that emits the
// provided text as a single delta. Used by the reflection
// tests so we don't need a real LLM.
func echoProvider(t *testing.T, text string) llm.Provider {
	t.Helper()
	return &echoReflectProvider{text: text}
}

type echoReflectProvider struct {
	mu    sync.Mutex
	text  string
	calls int
}

func (p *echoReflectProvider) Name() string { return "echo" }
func (p *echoReflectProvider) SupportsVision() bool {
	return false
}
func (p *echoReflectProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	ch := make(chan llm.Delta, 2)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Content: p.text}
		ch <- llm.Delta{FinishReason: "stop"}
	}()
	return ch, nil
}

type failingProvider struct{}

func (p *failingProvider) Name() string         { return "fail" }
func (p *failingProvider) SupportsVision() bool { return false }
func (p *failingProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 1)
	go func() {
		defer close(ch)
		ch <- llm.Delta{Err: errStr("stream died")}
	}()
	return ch, nil
}

type strErr string

func (s strErr) Error() string { return string(s) }
func errStr(s string) error    { return strErr(s) }
