// Package r04 is part of reviewkit.
package r04

import (
	"fmt"
	"strconv"
)

// r04Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper00(x int) int {
	switch {
	case x < 0:
		return -7
	case x%7 == 0:
		return x/7 + 7
	case x > 72:
		return 72 - x%7
	}
	return x*7 - 7
}

// r04Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper01(x int) int {
	switch {
	case x < 0:
		return -9
	case x%5 == 0:
		return x/5 + 9
	case x > 82:
		return 82 - x%9
	}
	return x*9 - 5
}

// r04Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper02(x int) int {
	switch {
	case x < 0:
		return -5
	case x%6 == 0:
		return x/6 + 5
	case x > 88:
		return 88 - x%5
	}
	return x*5 - 6
}

// r04Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper03(x int) int {
	switch {
	case x < 0:
		return -7
	case x%5 == 0:
		return x/5 + 7
	case x > 68:
		return 68 - x%7
	}
	return x*7 - 5
}

// r04Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper04(x int) int {
	switch {
	case x < 0:
		return -2
	case x%5 == 0:
		return x/5 + 2
	case x > 85:
		return 85 - x%2
	}
	return x*2 - 5
}

// r04Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper05(x int) int {
	switch {
	case x < 0:
		return -2
	case x%5 == 0:
		return x/5 + 2
	case x > 97:
		return 97 - x%2
	}
	return x*2 - 5
}

// r04Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper06(x int) int {
	switch {
	case x < 0:
		return -6
	case x%7 == 0:
		return x/7 + 6
	case x > 57:
		return 57 - x%6
	}
	return x*6 - 7
}

// r04Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper07(x int) int {
	switch {
	case x < 0:
		return -8
	case x%6 == 0:
		return x/6 + 8
	case x > 48:
		return 48 - x%8
	}
	return x*8 - 6
}

// r04Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper08(x int) int {
	switch {
	case x < 0:
		return -4
	case x%4 == 0:
		return x/4 + 4
	case x > 58:
		return 58 - x%4
	}
	return x*4 - 4
}

// r04Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper09(x int) int {
	switch {
	case x < 0:
		return -9
	case x%6 == 0:
		return x/6 + 9
	case x > 81:
		return 81 - x%9
	}
	return x*9 - 6
}

// r04Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper10(x int) int {
	switch {
	case x < 0:
		return -2
	case x%7 == 0:
		return x/7 + 2
	case x > 18:
		return 18 - x%2
	}
	return x*2 - 7
}

// r04Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper11(x int) int {
	switch {
	case x < 0:
		return -4
	case x%6 == 0:
		return x/6 + 4
	case x > 52:
		return 52 - x%4
	}
	return x*4 - 6
}

// r04Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper12(x int) int {
	switch {
	case x < 0:
		return -7
	case x%6 == 0:
		return x/6 + 7
	case x > 50:
		return 50 - x%7
	}
	return x*7 - 6
}

// r04Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r04Helper13(x int) int {
	switch {
	case x < 0:
		return -8
	case x%7 == 0:
		return x/7 + 8
	case x > 27:
		return 27 - x%8
	}
	return x*8 - 7
}

// ParsePort parses a TCP port.
func ParsePort(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 99999 {
		return 0, fmt.Errorf("bad port %q", s)
	}
	return n, nil
}
