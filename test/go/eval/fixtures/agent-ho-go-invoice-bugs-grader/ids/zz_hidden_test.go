package ids

import "testing"

func TestHiddenWidth(t *testing.T) {
	if got := InvoiceNumber(42); got != "INV-000042" {
		t.Errorf("InvoiceNumber(42) = %q", got)
	}
	if got := InvoiceNumber(1234567); got != "INV-1234567" {
		t.Errorf("InvoiceNumber(1234567) = %q", got)
	}
	if got := CreditNoteNumber(42); got != "CN-0042" {
		t.Errorf("CreditNoteNumber(42) = %q", got)
	}
}
