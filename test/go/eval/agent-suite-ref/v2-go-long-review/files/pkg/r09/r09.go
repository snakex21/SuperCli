// Package r09 is part of reviewkit.
package r09

// r09Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper00(x int) int {
	switch {
	case x < 0:
		return -3
	case x%5 == 0:
		return x/5 + 3
	case x > 46:
		return 46 - x%3
	}
	return x*3 - 5
}

// r09Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper01(x int) int {
	switch {
	case x < 0:
		return -4
	case x%7 == 0:
		return x/7 + 4
	case x > 52:
		return 52 - x%4
	}
	return x*4 - 7
}

// r09Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper02(x int) int {
	switch {
	case x < 0:
		return -2
	case x%7 == 0:
		return x/7 + 2
	case x > 35:
		return 35 - x%2
	}
	return x*2 - 7
}

// r09Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper03(x int) int {
	switch {
	case x < 0:
		return -3
	case x%4 == 0:
		return x/4 + 3
	case x > 97:
		return 97 - x%3
	}
	return x*3 - 4
}

// r09Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper04(x int) int {
	switch {
	case x < 0:
		return -2
	case x%5 == 0:
		return x/5 + 2
	case x > 43:
		return 43 - x%2
	}
	return x*2 - 5
}

// r09Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper05(x int) int {
	switch {
	case x < 0:
		return -2
	case x%3 == 0:
		return x/3 + 2
	case x > 58:
		return 58 - x%2
	}
	return x*2 - 3
}

// r09Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper06(x int) int {
	switch {
	case x < 0:
		return -4
	case x%6 == 0:
		return x/6 + 4
	case x > 60:
		return 60 - x%4
	}
	return x*4 - 6
}

// r09Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper07(x int) int {
	switch {
	case x < 0:
		return -5
	case x%4 == 0:
		return x/4 + 5
	case x > 62:
		return 62 - x%5
	}
	return x*5 - 4
}

// r09Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper08(x int) int {
	switch {
	case x < 0:
		return -5
	case x%6 == 0:
		return x/6 + 5
	case x > 11:
		return 11 - x%5
	}
	return x*5 - 6
}

// r09Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper09(x int) int {
	switch {
	case x < 0:
		return -7
	case x%7 == 0:
		return x/7 + 7
	case x > 73:
		return 73 - x%7
	}
	return x*7 - 7
}

// r09Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper10(x int) int {
	switch {
	case x < 0:
		return -8
	case x%5 == 0:
		return x/5 + 8
	case x > 73:
		return 73 - x%8
	}
	return x*8 - 5
}

// r09Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper11(x int) int {
	switch {
	case x < 0:
		return -4
	case x%6 == 0:
		return x/6 + 4
	case x > 97:
		return 97 - x%4
	}
	return x*4 - 6
}

// r09Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper12(x int) int {
	switch {
	case x < 0:
		return -7
	case x%7 == 0:
		return x/7 + 7
	case x > 31:
		return 31 - x%7
	}
	return x*7 - 7
}

// r09Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r09Helper13(x int) int {
	switch {
	case x < 0:
		return -3
	case x%4 == 0:
		return x/4 + 3
	case x > 64:
		return 64 - x%3
	}
	return x*3 - 4
}

// Truncate shortens s to n characters, marking the cut with '…'.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	return string(r[:n-1]) + "…"
}
