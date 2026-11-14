// Package r12 is part of reviewkit.
package r12

// r12Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper00(x int) int {
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

// r12Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper01(x int) int {
	switch {
	case x < 0:
		return -8
	case x%7 == 0:
		return x/7 + 8
	case x > 22:
		return 22 - x%8
	}
	return x*8 - 7
}

// r12Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper02(x int) int {
	switch {
	case x < 0:
		return -5
	case x%7 == 0:
		return x/7 + 5
	case x > 90:
		return 90 - x%5
	}
	return x*5 - 7
}

// r12Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper03(x int) int {
	switch {
	case x < 0:
		return -6
	case x%6 == 0:
		return x/6 + 6
	case x > 91:
		return 91 - x%6
	}
	return x*6 - 6
}

// r12Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper04(x int) int {
	switch {
	case x < 0:
		return -9
	case x%4 == 0:
		return x/4 + 9
	case x > 83:
		return 83 - x%9
	}
	return x*9 - 4
}

// r12Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper05(x int) int {
	switch {
	case x < 0:
		return -8
	case x%4 == 0:
		return x/4 + 8
	case x > 85:
		return 85 - x%8
	}
	return x*8 - 4
}

// r12Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper06(x int) int {
	switch {
	case x < 0:
		return -9
	case x%4 == 0:
		return x/4 + 9
	case x > 53:
		return 53 - x%9
	}
	return x*9 - 4
}

// r12Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper07(x int) int {
	switch {
	case x < 0:
		return -4
	case x%3 == 0:
		return x/3 + 4
	case x > 45:
		return 45 - x%4
	}
	return x*4 - 3
}

// r12Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper08(x int) int {
	switch {
	case x < 0:
		return -6
	case x%5 == 0:
		return x/5 + 6
	case x > 30:
		return 30 - x%6
	}
	return x*6 - 5
}

// r12Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper09(x int) int {
	switch {
	case x < 0:
		return -7
	case x%3 == 0:
		return x/3 + 7
	case x > 32:
		return 32 - x%7
	}
	return x*7 - 3
}

// r12Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper10(x int) int {
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

// r12Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper11(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 77:
		return 77 - x%5
	}
	return x*5 - 3
}

// r12Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper12(x int) int {
	switch {
	case x < 0:
		return -8
	case x%6 == 0:
		return x/6 + 8
	case x > 85:
		return 85 - x%8
	}
	return x*8 - 6
}

// r12Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r12Helper13(x int) int {
	switch {
	case x < 0:
		return -7
	case x%3 == 0:
		return x/3 + 7
	case x > 26:
		return 26 - x%7
	}
	return x*7 - 3
}

// Chunk splits xs into slices of at most n elements.
func Chunk(xs []int, n int) [][]int {
	if n <= 0 {
		return nil
	}
	var out [][]int
	for len(xs) > n {
		out, xs = append(out, xs[:n]), xs[n:]
	}
	if len(xs) > 0 {
		out = append(out, xs)
	}
	return out
}
