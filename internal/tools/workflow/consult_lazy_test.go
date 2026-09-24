package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/llm/consult"
)

func TestConsultLazyPoolRespectsRequestedCount(t *testing.T) {
	for _, tc := range []struct{ n, max, want int }{
		{1, 0, 1}, {0, 0, 3}, {20, 0, 3}, {3, 1, 1},
	} {
		t.Run(fmt.Sprintf("n%d_max%d", tc.n, tc.max), func(t *testing.T) {
			c := NewConsult(&consult.Council{
				Judge: &toolJudge{body: "{\"winner\": 1, \"reason\": \"ok\"}"},
				LoadSamples: func() []llm.Provider {
					return []llm.Provider{toolStubProvider("a", "a", 1), toolStubProvider("b", "b", 1), toolStubProvider("c", "c", 1)}
				},
			})
			c.MaxN = tc.max
			got := -1
			c.OnResult = func(r consult.Result) { got = len(r.Candidates) }
			r, err := c.Spec().Fn(context.Background(), json.RawMessage(fmt.Sprintf("{\"question\":\"q\",\"n\":%d}", tc.n)))
			if err != nil || r.Err != nil || got != tc.want {
				t.Fatalf("candidates=%d want %d; result=%+v err=%v", got, tc.want, r, err)
			}
		})
	}
}

func TestConsultExplicitModelsBypassLazyPool(t *testing.T) {
	c := NewConsult(&consult.Council{
		Judge:       &toolJudge{},
		LoadSamples: func() []llm.Provider { t.Fatal("unused fallback initialized"); return nil },
	})
	c.BuildProvider = func(name string) (llm.Provider, error) { return toolStubProvider(name, "selected", 1), nil }
	r, err := c.Spec().Fn(context.Background(), json.RawMessage("{\"question\":\"q\",\"models\":[\"chosen\"]}"))
	if err != nil || r.Err != nil {
		t.Fatalf("%+v %v", r, err)
	}
}
