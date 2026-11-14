// SPDX-License-Identifier: MIT

package slices2

import "testing"

func TestFillerWeights(t *testing.T) {
	if got := Weight1(Ticket1{Count: 1}); got <= 0 {
		t.Fatalf("Weight1 = %d", got)
	}
	if got := Label1(Ticket1{ID: "x"}); got == "" {
		t.Fatal("empty label")
	}
}
