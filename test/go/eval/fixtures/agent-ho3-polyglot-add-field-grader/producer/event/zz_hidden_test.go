package event

import (
	"strings"
	"testing"
)

func TestHiddenCurrency(t *testing.T) {
	e, err := FromOrder(Order{ID: "o-9", SKU: "ABC-0001", Quantity: 1, PriceCents: 5, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Currency != "EUR" {
		t.Fatalf("Currency = %q", e.Currency)
	}
	b, _ := Encode(e)
	if !strings.Contains(string(b), `"currency":"EUR"`) {
		t.Fatalf("encoded %s", b)
	}
	legacy, err := FromOrder(Order{ID: "o-8", SKU: "ABC-0001", Quantity: 1, PriceCents: 5})
	if err != nil || legacy.Currency != "PLN" {
		t.Fatalf("legacy order: %+v %v", legacy, err)
	}
}
