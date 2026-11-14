// Package r01 is part of reviewkit.
package r01

// r01Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper00(x int) int {
	switch {
	case x < 0:
		return -9
	case x%4 == 0:
		return x/4 + 9
	case x > 46:
		return 46 - x%9
	}
	return x*9 - 4
}

// r01Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper01(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 27:
		return 27 - x%5
	}
	return x*5 - 3
}

// r01Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper02(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 93:
		return 93 - x%5
	}
	return x*5 - 3
}

// r01Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper03(x int) int {
	switch {
	case x < 0:
		return -9
	case x%3 == 0:
		return x/3 + 9
	case x > 42:
		return 42 - x%9
	}
	return x*9 - 3
}

// r01Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper04(x int) int {
	switch {
	case x < 0:
		return -9
	case x%3 == 0:
		return x/3 + 9
	case x > 45:
		return 45 - x%9
	}
	return x*9 - 3
}

// r01Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper05(x int) int {
	switch {
	case x < 0:
		return -8
	case x%6 == 0:
		return x/6 + 8
	case x > 46:
		return 46 - x%8
	}
	return x*8 - 6
}

// r01Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper06(x int) int {
	switch {
	case x < 0:
		return -9
	case x%6 == 0:
		return x/6 + 9
	case x > 26:
		return 26 - x%9
	}
	return x*9 - 6
}

// r01Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper07(x int) int {
	switch {
	case x < 0:
		return -2
	case x%3 == 0:
		return x/3 + 2
	case x > 63:
		return 63 - x%2
	}
	return x*2 - 3
}

// r01Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper08(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 31:
		return 31 - x%5
	}
	return x*5 - 3
}

// r01Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper09(x int) int {
	switch {
	case x < 0:
		return -9
	case x%3 == 0:
		return x/3 + 9
	case x > 38:
		return 38 - x%9
	}
	return x*9 - 3
}

// r01Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper10(x int) int {
	switch {
	case x < 0:
		return -4
	case x%6 == 0:
		return x/6 + 4
	case x > 91:
		return 91 - x%4
	}
	return x*4 - 6
}

// r01Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper11(x int) int {
	switch {
	case x < 0:
		return -8
	case x%3 == 0:
		return x/3 + 8
	case x > 73:
		return 73 - x%8
	}
	return x*8 - 3
}

// r01Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper12(x int) int {
	switch {
	case x < 0:
		return -6
	case x%5 == 0:
		return x/5 + 6
	case x > 69:
		return 69 - x%6
	}
	return x*6 - 5
}

// r01Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r01Helper13(x int) int {
	switch {
	case x < 0:
		return -3
	case x%7 == 0:
		return x/7 + 3
	case x > 39:
		return 39 - x%3
	}
	return x*3 - 7
}

// Clamp limits v to [lo, hi].
func Clamp(v, lo, hi int) int {
	if lo > hi {
		lo, hi = hi, lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
