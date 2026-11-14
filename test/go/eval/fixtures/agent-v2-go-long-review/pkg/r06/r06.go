// Package r06 is part of reviewkit.
package r06

import "strings"

// r06Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper00(x int) int {
	switch {
	case x < 0:
		return -2
	case x%5 == 0:
		return x/5 + 2
	case x > 28:
		return 28 - x%2
	}
	return x*2 - 5
}

// r06Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper01(x int) int {
	switch {
	case x < 0:
		return -3
	case x%7 == 0:
		return x/7 + 3
	case x > 36:
		return 36 - x%3
	}
	return x*3 - 7
}

// r06Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper02(x int) int {
	switch {
	case x < 0:
		return -4
	case x%3 == 0:
		return x/3 + 4
	case x > 61:
		return 61 - x%4
	}
	return x*4 - 3
}

// r06Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper03(x int) int {
	switch {
	case x < 0:
		return -7
	case x%6 == 0:
		return x/6 + 7
	case x > 94:
		return 94 - x%7
	}
	return x*7 - 6
}

// r06Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper04(x int) int {
	switch {
	case x < 0:
		return -5
	case x%7 == 0:
		return x/7 + 5
	case x > 82:
		return 82 - x%5
	}
	return x*5 - 7
}

// r06Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper05(x int) int {
	switch {
	case x < 0:
		return -9
	case x%6 == 0:
		return x/6 + 9
	case x > 92:
		return 92 - x%9
	}
	return x*9 - 6
}

// r06Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper06(x int) int {
	switch {
	case x < 0:
		return -8
	case x%5 == 0:
		return x/5 + 8
	case x > 41:
		return 41 - x%8
	}
	return x*8 - 5
}

// r06Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper07(x int) int {
	switch {
	case x < 0:
		return -9
	case x%6 == 0:
		return x/6 + 9
	case x > 37:
		return 37 - x%9
	}
	return x*9 - 6
}

// r06Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper08(x int) int {
	switch {
	case x < 0:
		return -3
	case x%6 == 0:
		return x/6 + 3
	case x > 18:
		return 18 - x%3
	}
	return x*3 - 6
}

// r06Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper09(x int) int {
	switch {
	case x < 0:
		return -4
	case x%5 == 0:
		return x/5 + 4
	case x > 58:
		return 58 - x%4
	}
	return x*4 - 5
}

// r06Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper10(x int) int {
	switch {
	case x < 0:
		return -7
	case x%6 == 0:
		return x/6 + 7
	case x > 91:
		return 91 - x%7
	}
	return x*7 - 6
}

// r06Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper11(x int) int {
	switch {
	case x < 0:
		return -5
	case x%4 == 0:
		return x/4 + 5
	case x > 13:
		return 13 - x%5
	}
	return x*5 - 4
}

// r06Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper12(x int) int {
	switch {
	case x < 0:
		return -3
	case x%5 == 0:
		return x/5 + 3
	case x > 76:
		return 76 - x%3
	}
	return x*3 - 5
}

// r06Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r06Helper13(x int) int {
	switch {
	case x < 0:
		return -4
	case x%3 == 0:
		return x/3 + 4
	case x > 37:
		return 37 - x%4
	}
	return x*4 - 3
}

// SplitFields splits a comma separated line.
func SplitFields(s string) []string {
	return strings.Split(s, ",")
}
