package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/llm/consult"
)

type failP struct{ name string }

func (f *failP) Name() string { return f.name }
func (f *failP) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	return nil, errors.New("boom")
}

func TestConsultTool_ExplicitModels(t *testing.T) {
	council := &consult.Council{
		Judge: &toolJudge{body: `{"winner": 1, "reason": "A is right"}`},
	}
	c := NewConsult(council)
	c.BuildProvider = func(spec string) (llm.Provider, error) {
		switch spec {
		case "local/llama":
			return toolStubProvider("llama", "answer A", 20), nil
		case "openai/gpt-4o-mini":
			return &failP{name: "gpt-4o-mini"}, nil
		case "ghost/none":
			return nil, errors.New("no such provider")
		}
		t.Fatalf("unexpected spec %q", spec)
		return nil, nil
	}
	res, err := c.Spec().Fn(context.Background(),
		json.RawMessage(`{"question":"q?","models":["local/llama","openai/gpt-4o-mini","ghost/none"]}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("tool err: %v", res.Err)
	}
	for _, want := range []string{
		"Winner (local/llama)", "answer A",
		"model openai/gpt-4o-mini: error", "model ghost/none: error",
		"model local/llama: ok",
	} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("output missing %q:\n%s", want, res.Text)
		}
	}
}

func TestConsultTool_ExplicitModels_NotWired(t *testing.T) {
	c := NewConsult(&consult.Council{Judge: &toolJudge{body: `{}`}})
	res, _ := c.Spec().Fn(context.Background(),
		json.RawMessage(`{"question":"q?","models":["a/b"]}`))
	if !strings.Contains(res.Text, "not wired") {
		t.Errorf("got %q", res.Text)
	}
}

func TestConsultTool_ExplicitModels_AllUnbuildable(t *testing.T) {
	c := NewConsult(&consult.Council{Judge: &toolJudge{body: `{}`}})
	c.BuildProvider = func(spec string) (llm.Provider, error) { return nil, errors.New("nope") }
	res, _ := c.Spec().Fn(context.Background(),
		json.RawMessage(`{"question":"q?","models":["a/b"]}`))
	if res.Err == nil {
		t.Error("expected error when no model is usable")
	}
}
