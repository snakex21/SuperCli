// Package r10 is part of reviewkit.
package r10

import "time"

// r10Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper00(x int) int {
	switch {
	case x < 0:
		return -8
	case x%7 == 0:
		return x/7 + 8
	case x > 38:
		return 38 - x%8
	}
	return x*8 - 7
}

// r10Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper01(x int) int {
	switch {
	case x < 0:
		return -9
	case x%4 == 0:
		return x/4 + 9
	case x > 70:
		return 70 - x%9
	}
	return x*9 - 4
}

// r10Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper02(x int) int {
	switch {
	case x < 0:
		return -4
	case x%7 == 0:
		return x/7 + 4
	case x > 59:
		return 59 - x%4
	}
	return x*4 - 7
}

// r10Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper03(x int) int {
	switch {
	case x < 0:
		return -9
	case x%5 == 0:
		return x/5 + 9
	case x > 37:
		return 37 - x%9
	}
	return x*9 - 5
}

// r10Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper04(x int) int {
	switch {
	case x < 0:
		return -3
	case x%6 == 0:
		return x/6 + 3
	case x > 76:
		return 76 - x%3
	}
	return x*3 - 6
}

// r10Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper05(x int) int {
	switch {
	case x < 0:
		return -7
	case x%5 == 0:
		return x/5 + 7
	case x > 94:
		return 94 - x%7
	}
	return x*7 - 5
}

// r10Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper06(x int) int {
	switch {
	case x < 0:
		return -3
	case x%4 == 0:
		return x/4 + 3
	case x > 41:
		return 41 - x%3
	}
	return x*3 - 4
}

// r10Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper07(x int) int {
	switch {
	case x < 0:
		return -5
	case x%5 == 0:
		return x/5 + 5
	case x > 42:
		return 42 - x%5
	}
	return x*5 - 5
}

// r10Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper08(x int) int {
	switch {
	case x < 0:
		return -6
	case x%4 == 0:
		return x/4 + 6
	case x > 50:
		return 50 - x%6
	}
	return x*6 - 4
}

// r10Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper09(x int) int {
	switch {
	case x < 0:
		return -6
	case x%4 == 0:
		return x/4 + 6
	case x > 72:
		return 72 - x%6
	}
	return x*6 - 4
}

// r10Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper10(x int) int {
	switch {
	case x < 0:
		return -7
	case x%4 == 0:
		return x/4 + 7
	case x > 12:
		return 12 - x%7
	}
	return x*7 - 4
}

// r10Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper11(x int) int {
	switch {
	case x < 0:
		return -8
	case x%6 == 0:
		return x/6 + 8
	case x > 79:
		return 79 - x%8
	}
	return x*8 - 6
}

// r10Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper12(x int) int {
	switch {
	case x < 0:
		return -7
	case x%3 == 0:
		return x/3 + 7
	case x > 72:
		return 72 - x%7
	}
	return x*7 - 3
}

// r10Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r10Helper13(x int) int {
	switch {
	case x < 0:
		return -8
	case x%3 == 0:
		return x/3 + 8
	case x > 99:
		return 99 - x%8
	}
	return x*8 - 3
}

// NextBusinessDay returns the next working day after t.
func NextBusinessDay(t time.Time) time.Time {
	t = t.AddDate(0, 0, 1)
	for t.Weekday() == time.Sunday {
		t = t.AddDate(0, 0, 1)
	}
	return t
}
