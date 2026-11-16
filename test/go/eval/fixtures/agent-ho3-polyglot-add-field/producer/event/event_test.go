package event

import (
	"strings"
	"testing"
)

func TestFromOrder(t *testing.T) {
	e, err := FromOrder(Order{ID: "o-1", SKU: "ABC-0001", Quantity: 2, PriceCents: 1999})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"price_cents":1999`) {
		t.Fatalf("encoded %s", b)
	}
}

func TestRejectsZeroQuantity(t *testing.T) {
	if _, err := FromOrder(Order{ID: "o-2", Quantity: 0}); err == nil {
		t.Fatal("expected error")
	}
}
