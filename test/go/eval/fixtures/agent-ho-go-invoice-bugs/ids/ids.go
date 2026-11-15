// Package ids formats document numbers.
package ids

import "fmt"

// InvoiceNumber formats a sequence number, e.g. InvoiceNumber(42) = "INV-000042".
func InvoiceNumber(seq int) string {
	return fmt.Sprintf("INV-%05d", seq)
}

// CreditNoteNumber formats a credit note number, e.g. "CN-0042".
func CreditNoteNumber(seq int) string {
	return fmt.Sprintf("CN-%04d", seq)
}
