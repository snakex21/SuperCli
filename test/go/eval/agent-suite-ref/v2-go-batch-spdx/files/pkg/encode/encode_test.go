// SPDX-License-Identifier: MIT

package encode

import "testing"

func TestFillerWeights(t *testing.T) {
	if got := Weight1(Report1{Count: 1}); got <= 0 {
		t.Fatalf("Weight1 = %d", got)
	}
	if got := Label1(Report1{ID: "x"}); got == "" {
		t.Fatal("empty label")
	}
}
