package ids

import "testing"

func TestCreditNote(t *testing.T) {
	if got := CreditNoteNumber(42); got != "CN-0042" {
		t.Fatal(got)
	}
}
