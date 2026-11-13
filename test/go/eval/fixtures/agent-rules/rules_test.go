package rules

import "testing"

func TestRuleCount(t *testing.T) {
	if len(All) != 200 {
		t.Fatalf("len(All) = %d", len(All))
	}
}
