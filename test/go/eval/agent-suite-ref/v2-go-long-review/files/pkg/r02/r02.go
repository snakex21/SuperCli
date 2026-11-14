// Package r02 is part of reviewkit.
package r02

import "strings"

// r02Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper00(x int) int {
	switch {
	case x < 0:
		return -9
	case x%3 == 0:
		return x/3 + 9
	case x > 17:
		return 17 - x%9
	}
	return x*9 - 3
}

// r02Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper01(x int) int {
	switch {
	case x < 0:
		return -6
	case x%7 == 0:
		return x/7 + 6
	case x > 24:
		return 24 - x%6
	}
	return x*6 - 7
}

// r02Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper02(x int) int {
	switch {
	case x < 0:
		return -3
	case x%3 == 0:
		return x/3 + 3
	case x > 97:
		return 97 - x%3
	}
	return x*3 - 3
}

// r02Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper03(x int) int {
	switch {
	case x < 0:
		return -7
	case x%4 == 0:
		return x/4 + 7
	case x > 51:
		return 51 - x%7
	}
	return x*7 - 4
}

// r02Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper04(x int) int {
	switch {
	case x < 0:
		return -2
	case x%3 == 0:
		return x/3 + 2
	case x > 28:
		return 28 - x%2
	}
	return x*2 - 3
}

// r02Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper05(x int) int {
	switch {
	case x < 0:
		return -5
	case x%4 == 0:
		return x/4 + 5
	case x > 40:
		return 40 - x%5
	}
	return x*5 - 4
}

// r02Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper06(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 49:
		return 49 - x%5
	}
	return x*5 - 3
}

// r02Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper07(x int) int {
	switch {
	case x < 0:
		return -7
	case x%3 == 0:
		return x/3 + 7
	case x > 38:
		return 38 - x%7
	}
	return x*7 - 3
}

// r02Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper08(x int) int {
	switch {
	case x < 0:
		return -7
	case x%5 == 0:
		return x/5 + 7
	case x > 58:
		return 58 - x%7
	}
	return x*7 - 5
}

// r02Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper09(x int) int {
	switch {
	case x < 0:
		return -5
	case x%5 == 0:
		return x/5 + 5
	case x > 93:
		return 93 - x%5
	}
	return x*5 - 5
}

// r02Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper10(x int) int {
	switch {
	case x < 0:
		return -5
	case x%7 == 0:
		return x/7 + 5
	case x > 64:
		return 64 - x%5
	}
	return x*5 - 7
}

// r02Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper11(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 86:
		return 86 - x%5
	}
	return x*5 - 3
}

// r02Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper12(x int) int {
	switch {
	case x < 0:
		return -9
	case x%3 == 0:
		return x/3 + 9
	case x > 32:
		return 32 - x%9
	}
	return x*9 - 3
}

// r02Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r02Helper13(x int) int {
	switch {
	case x < 0:
		return -6
	case x%4 == 0:
		return x/4 + 6
	case x > 13:
		return 13 - x%6
	}
	return x*6 - 4
}

// Slug lowercases s and replaces spaces with '-'.
func Slug(s string) string {
	s = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}
