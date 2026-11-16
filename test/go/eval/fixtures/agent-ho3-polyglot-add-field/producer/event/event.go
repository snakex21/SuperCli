// Package event converts internal orders into the OrderEvent wire format
// described by schema/order_event.schema.json (consumed by web/ and analytics/).
package event

import (
	"encoding/json"
	"fmt"
)

// Order is an order row as stored by the order service.
type Order struct {
	ID         string
	SKU        string
	Quantity   int
	PriceCents int64
	// Currency is the ISO 4217 code; rows created before 2025 have it empty
	// (those orders were all in PLN).
	Currency string
}

// OrderEvent is the published JSON event.
type OrderEvent struct {
	ID         string `json:"id"`
	SKU        string `json:"sku"`
	Quantity   int    `json:"quantity"`
	PriceCents int64  `json:"price_cents"`
}

// FromOrder builds the event for an order.
func FromOrder(o Order) (OrderEvent, error) {
	if o.Quantity < 1 {
		return OrderEvent{}, fmt.Errorf("order %s: quantity must be positive", o.ID)
	}
	return OrderEvent{ID: o.ID, SKU: o.SKU, Quantity: o.Quantity, PriceCents: o.PriceCents}, nil
}

// Encode renders one event as a JSON line.
func Encode(e OrderEvent) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
