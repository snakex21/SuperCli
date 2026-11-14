package r09

import "testing"

func TestHiddenReview(t *testing.T) {
	if Truncate("żółwiki", 4) != "żół…" || Truncate("abc", 3) != "abc" || Truncate("abcd", 3) != "ab…" || Truncate("żó", 2) != "żó" {
		t.Fatal("Truncate")
	}
}
