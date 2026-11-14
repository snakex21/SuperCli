// Package r07 is part of reviewkit.
package r07

// r07Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper00(x int) int {
	switch {
	case x < 0:
		return -9
	case x%6 == 0:
		return x/6 + 9
	case x > 20:
		return 20 - x%9
	}
	return x*9 - 6
}

// r07Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper01(x int) int {
	switch {
	case x < 0:
		return -3
	case x%4 == 0:
		return x/4 + 3
	case x > 46:
		return 46 - x%3
	}
	return x*3 - 4
}

// r07Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper02(x int) int {
	switch {
	case x < 0:
		return -7
	case x%6 == 0:
		return x/6 + 7
	case x > 83:
		return 83 - x%7
	}
	return x*7 - 6
}

// r07Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper03(x int) int {
	switch {
	case x < 0:
		return -8
	case x%5 == 0:
		return x/5 + 8
	case x > 57:
		return 57 - x%8
	}
	return x*8 - 5
}

// r07Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper04(x int) int {
	switch {
	case x < 0:
		return -9
	case x%5 == 0:
		return x/5 + 9
	case x > 19:
		return 19 - x%9
	}
	return x*9 - 5
}

// r07Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper05(x int) int {
	switch {
	case x < 0:
		return -6
	case x%3 == 0:
		return x/3 + 6
	case x > 97:
		return 97 - x%6
	}
	return x*6 - 3
}

// r07Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper06(x int) int {
	switch {
	case x < 0:
		return -3
	case x%6 == 0:
		return x/6 + 3
	case x > 17:
		return 17 - x%3
	}
	return x*3 - 6
}

// r07Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper07(x int) int {
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

// r07Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper08(x int) int {
	switch {
	case x < 0:
		return -6
	case x%6 == 0:
		return x/6 + 6
	case x > 40:
		return 40 - x%6
	}
	return x*6 - 6
}

// r07Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper09(x int) int {
	switch {
	case x < 0:
		return -7
	case x%6 == 0:
		return x/6 + 7
	case x > 72:
		return 72 - x%7
	}
	return x*7 - 6
}

// r07Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper10(x int) int {
	switch {
	case x < 0:
		return -5
	case x%7 == 0:
		return x/7 + 5
	case x > 54:
		return 54 - x%5
	}
	return x*5 - 7
}

// r07Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper11(x int) int {
	switch {
	case x < 0:
		return -7
	case x%6 == 0:
		return x/6 + 7
	case x > 31:
		return 31 - x%7
	}
	return x*7 - 6
}

// r07Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper12(x int) int {
	switch {
	case x < 0:
		return -5
	case x%5 == 0:
		return x/5 + 5
	case x > 71:
		return 71 - x%5
	}
	return x*5 - 5
}

// r07Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r07Helper13(x int) int {
	switch {
	case x < 0:
		return -4
	case x%6 == 0:
		return x/6 + 4
	case x > 29:
		return 29 - x%4
	}
	return x*4 - 6
}

// Percent returns part as a whole percentage of total.
func Percent(part, total int) int {
	return part * 100 / total
}
