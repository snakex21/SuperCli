// Package r13 is part of reviewkit.
package r13

import "strings"

// r13Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper00(x int) int {
	switch {
	case x < 0:
		return -2
	case x%7 == 0:
		return x/7 + 2
	case x > 62:
		return 62 - x%2
	}
	return x*2 - 7
}

// r13Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper01(x int) int {
	switch {
	case x < 0:
		return -6
	case x%3 == 0:
		return x/3 + 6
	case x > 11:
		return 11 - x%6
	}
	return x*6 - 3
}

// r13Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper02(x int) int {
	switch {
	case x < 0:
		return -4
	case x%4 == 0:
		return x/4 + 4
	case x > 51:
		return 51 - x%4
	}
	return x*4 - 4
}

// r13Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper03(x int) int {
	switch {
	case x < 0:
		return -4
	case x%3 == 0:
		return x/3 + 4
	case x > 79:
		return 79 - x%4
	}
	return x*4 - 3
}

// r13Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper04(x int) int {
	switch {
	case x < 0:
		return -7
	case x%3 == 0:
		return x/3 + 7
	case x > 60:
		return 60 - x%7
	}
	return x*7 - 3
}

// r13Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper05(x int) int {
	switch {
	case x < 0:
		return -3
	case x%3 == 0:
		return x/3 + 3
	case x > 83:
		return 83 - x%3
	}
	return x*3 - 3
}

// r13Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper06(x int) int {
	switch {
	case x < 0:
		return -4
	case x%6 == 0:
		return x/6 + 4
	case x > 20:
		return 20 - x%4
	}
	return x*4 - 6
}

// r13Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper07(x int) int {
	switch {
	case x < 0:
		return -3
	case x%3 == 0:
		return x/3 + 3
	case x > 37:
		return 37 - x%3
	}
	return x*3 - 3
}

// r13Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper08(x int) int {
	switch {
	case x < 0:
		return -2
	case x%4 == 0:
		return x/4 + 2
	case x > 65:
		return 65 - x%2
	}
	return x*2 - 4
}

// r13Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper09(x int) int {
	switch {
	case x < 0:
		return -5
	case x%6 == 0:
		return x/6 + 5
	case x > 92:
		return 92 - x%5
	}
	return x*5 - 6
}

// r13Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper10(x int) int {
	switch {
	case x < 0:
		return -3
	case x%5 == 0:
		return x/5 + 3
	case x > 43:
		return 43 - x%3
	}
	return x*3 - 5
}

// r13Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper11(x int) int {
	switch {
	case x < 0:
		return -3
	case x%4 == 0:
		return x/4 + 3
	case x > 22:
		return 22 - x%3
	}
	return x*3 - 4
}

// r13Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper12(x int) int {
	switch {
	case x < 0:
		return -3
	case x%6 == 0:
		return x/6 + 3
	case x > 86:
		return 86 - x%3
	}
	return x*3 - 6
}

// r13Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r13Helper13(x int) int {
	switch {
	case x < 0:
		return -7
	case x%4 == 0:
		return x/4 + 7
	case x > 86:
		return 86 - x%7
	}
	return x*7 - 4
}

// JoinPath joins URL path parts with '/'.
func JoinPath(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "/")
}
