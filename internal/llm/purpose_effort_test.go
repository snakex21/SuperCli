package llm

import (
	"context"
	"testing"
)

func TestIndependentPurposeEffort(t *testing.T) {
	initial := ReasoningEffort()
	t.Cleanup(func() { _ = SetReasoningEffort(initial); clearReasoningEffortSupport() })
	_ = SetReasoningEffort("medium")
	parent := WithReasoningEffort(context.Background(), "low")
	model := "purpose-effort-fixture"
	SetReasoningEffortSupport(model, []string{"low", "medium", "high"})
	for _, tc := range []struct{ level, want string }{{"", "medium"}, {"max", "high"}, {"none", "low"}, {"high", "high"}} {
		t.Run("level="+tc.level, func(t *testing.T) {
			ctx := WithIndependentReasoningEffort(parent, tc.level)
			if got := ReasoningEffortForModel(model, ctx); got != tc.want {
				t.Fatalf("effort=%q want=%q", got, tc.want)
			}
			if ReasoningEffort(parent) != "low" || ReasoningEffort() != "medium" {
				t.Fatal("purpose effort leaked")
			}
		})
	}
}
