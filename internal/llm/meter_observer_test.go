package llm

import (
	"context"
	"testing"
)

func TestMeteredCallsRequestObserverWithResolvedPurpose(t *testing.T) {
	var got []string
	ctx := WithRequestObserver(context.Background(), func(purpose string, msgs []Message, tools []ToolDef) {
		got = append(got, purpose)
		if len(msgs) != 1 || len(tools) != 1 {
			t.Errorf("observer saw msgs=%d tools=%d", len(msgs), len(tools))
		}
	})
	p := Metered(&meterStub{name: "m", deltas: []Delta{{Content: "ok"}}}, "test", PurposeMain, func(CallStat) {})
	msgs := []Message{{Role: RoleUser, Content: "hi"}}
	tools := []ToolDef{{Name: "t"}}
	for _, c := range []context.Context{ctx, WithPurpose(ctx, PurposeAdvisor)} {
		ch, err := p.Complete(c, msgs, tools)
		if err != nil {
			t.Fatal(err)
		}
		drainMetered(t, ch)
	}
	if len(got) != 2 || got[0] != PurposeMain || got[1] != PurposeAdvisor {
		t.Fatalf("purposes=%v", got)
	}
}
