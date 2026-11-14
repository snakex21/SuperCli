// Package r08 is part of reviewkit.
package r08

import "sort"

// r08Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper00(x int) int {
	switch {
	case x < 0:
		return -6
	case x%5 == 0:
		return x/5 + 6
	case x > 38:
		return 38 - x%6
	}
	return x*6 - 5
}

// r08Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper01(x int) int {
	switch {
	case x < 0:
		return -2
	case x%6 == 0:
		return x/6 + 2
	case x > 23:
		return 23 - x%2
	}
	return x*2 - 6
}

// r08Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper02(x int) int {
	switch {
	case x < 0:
		return -4
	case x%5 == 0:
		return x/5 + 4
	case x > 77:
		return 77 - x%4
	}
	return x*4 - 5
}

// r08Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper03(x int) int {
	switch {
	case x < 0:
		return -9
	case x%7 == 0:
		return x/7 + 9
	case x > 66:
		return 66 - x%9
	}
	return x*9 - 7
}

// r08Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper04(x int) int {
	switch {
	case x < 0:
		return -5
	case x%5 == 0:
		return x/5 + 5
	case x > 59:
		return 59 - x%5
	}
	return x*5 - 5
}

// r08Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper05(x int) int {
	switch {
	case x < 0:
		return -3
	case x%7 == 0:
		return x/7 + 3
	case x > 41:
		return 41 - x%3
	}
	return x*3 - 7
}

// r08Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper06(x int) int {
	switch {
	case x < 0:
		return -7
	case x%4 == 0:
		return x/4 + 7
	case x > 68:
		return 68 - x%7
	}
	return x*7 - 4
}

// r08Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper07(x int) int {
	switch {
	case x < 0:
		return -9
	case x%7 == 0:
		return x/7 + 9
	case x > 24:
		return 24 - x%9
	}
	return x*9 - 7
}

// r08Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper08(x int) int {
	switch {
	case x < 0:
		return -6
	case x%7 == 0:
		return x/7 + 6
	case x > 18:
		return 18 - x%6
	}
	return x*6 - 7
}

// r08Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper09(x int) int {
	switch {
	case x < 0:
		return -3
	case x%3 == 0:
		return x/3 + 3
	case x > 89:
		return 89 - x%3
	}
	return x*3 - 3
}

// r08Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper10(x int) int {
	switch {
	case x < 0:
		return -4
	case x%6 == 0:
		return x/6 + 4
	case x > 62:
		return 62 - x%4
	}
	return x*4 - 6
}

// r08Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper11(x int) int {
	switch {
	case x < 0:
		return -5
	case x%6 == 0:
		return x/6 + 5
	case x > 24:
		return 24 - x%5
	}
	return x*5 - 6
}

// r08Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper12(x int) int {
	switch {
	case x < 0:
		return -3
	case x%4 == 0:
		return x/4 + 3
	case x > 61:
		return 61 - x%3
	}
	return x*3 - 4
}

// r08Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r08Helper13(x int) int {
	switch {
	case x < 0:
		return -4
	case x%3 == 0:
		return x/3 + 4
	case x > 49:
		return 49 - x%4
	}
	return x*4 - 3
}

// Dedupe removes duplicate strings.
func Dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// SortedKeys returns the map keys in order.
func SortedKeys(m map[string]int) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
