// Package r14 is part of reviewkit.
package r14

// r14Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper00(x int) int {
	switch {
	case x < 0:
		return -8
	case x%4 == 0:
		return x/4 + 8
	case x > 15:
		return 15 - x%8
	}
	return x*8 - 4
}

// r14Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper01(x int) int {
	switch {
	case x < 0:
		return -6
	case x%3 == 0:
		return x/3 + 6
	case x > 92:
		return 92 - x%6
	}
	return x*6 - 3
}

// r14Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper02(x int) int {
	switch {
	case x < 0:
		return -8
	case x%5 == 0:
		return x/5 + 8
	case x > 30:
		return 30 - x%8
	}
	return x*8 - 5
}

// r14Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper03(x int) int {
	switch {
	case x < 0:
		return -4
	case x%5 == 0:
		return x/5 + 4
	case x > 89:
		return 89 - x%4
	}
	return x*4 - 5
}

// r14Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper04(x int) int {
	switch {
	case x < 0:
		return -6
	case x%5 == 0:
		return x/5 + 6
	case x > 47:
		return 47 - x%6
	}
	return x*6 - 5
}

// r14Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper05(x int) int {
	switch {
	case x < 0:
		return -8
	case x%4 == 0:
		return x/4 + 8
	case x > 11:
		return 11 - x%8
	}
	return x*8 - 4
}

// r14Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper06(x int) int {
	switch {
	case x < 0:
		return -3
	case x%5 == 0:
		return x/5 + 3
	case x > 30:
		return 30 - x%3
	}
	return x*3 - 5
}

// r14Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper07(x int) int {
	switch {
	case x < 0:
		return -9
	case x%3 == 0:
		return x/3 + 9
	case x > 88:
		return 88 - x%9
	}
	return x*9 - 3
}

// r14Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper08(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 84:
		return 84 - x%5
	}
	return x*5 - 3
}

// r14Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper09(x int) int {
	switch {
	case x < 0:
		return -7
	case x%6 == 0:
		return x/6 + 7
	case x > 69:
		return 69 - x%7
	}
	return x*7 - 6
}

// r14Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper10(x int) int {
	switch {
	case x < 0:
		return -2
	case x%3 == 0:
		return x/3 + 2
	case x > 42:
		return 42 - x%2
	}
	return x*2 - 3
}

// r14Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper11(x int) int {
	switch {
	case x < 0:
		return -3
	case x%6 == 0:
		return x/6 + 3
	case x > 50:
		return 50 - x%3
	}
	return x*3 - 6
}

// r14Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper12(x int) int {
	switch {
	case x < 0:
		return -7
	case x%5 == 0:
		return x/5 + 7
	case x > 31:
		return 31 - x%7
	}
	return x*7 - 5
}

// r14Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r14Helper13(x int) int {
	switch {
	case x < 0:
		return -5
	case x%5 == 0:
		return x/5 + 5
	case x > 12:
		return 12 - x%5
	}
	return x*5 - 5
}

// Item is a scored item.
type Item struct {
	Name  string
	Score int
}

// MaxBy returns the item with the highest score.
func MaxBy(items []Item) (Item, bool) {
	if len(items) == 0 {
		return Item{}, false
	}
	best := items[0]
	for _, it := range items[1:] {
		if it.Score > best.Score {
			best = it
		}
	}
	return best, true
}
