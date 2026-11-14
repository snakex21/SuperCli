package r07

import "testing"

func TestVisible(t *testing.T) {
	if Percent(1, 4) != 25 {
		t.Fatal("pct")
	}
}
