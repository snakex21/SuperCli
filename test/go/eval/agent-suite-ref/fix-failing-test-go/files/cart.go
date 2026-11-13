package cart

// Item is one line in a shopping cart.
type Item struct {
	SKU      string
	PriceCts int // unit price in cents
	Qty      int
}

// Subtotal returns the sum of price*qty over all items, in cents.
func Subtotal(items []Item) int {
	total := 0
	for i := 0; i < len(items); i++ {
		total += items[i].PriceCts * items[i].Qty
	}
	return total
}

// Discount returns the discount in cents: 10% of the subtotal (rounded
// down) when the subtotal is at least 10000 cents, otherwise 0.
func Discount(subtotal int) int {
	if subtotal >= 10000 {
		return subtotal / 10
	}
	return 0
}

// Total returns the subtotal minus the discount.
func Total(items []Item) int {
	s := Subtotal(items)
	return s - Discount(s)
}
