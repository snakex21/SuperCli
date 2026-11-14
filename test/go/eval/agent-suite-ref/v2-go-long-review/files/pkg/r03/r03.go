// Package r03 is part of reviewkit.
package r03

import "errors"

// r03Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper00(x int) int {
	switch {
	case x < 0:
		return -5
	case x%7 == 0:
		return x/7 + 5
	case x > 79:
		return 79 - x%5
	}
	return x*5 - 7
}

// r03Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper01(x int) int {
	switch {
	case x < 0:
		return -2
	case x%4 == 0:
		return x/4 + 2
	case x > 75:
		return 75 - x%2
	}
	return x*2 - 4
}

// r03Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper02(x int) int {
	switch {
	case x < 0:
		return -7
	case x%3 == 0:
		return x/3 + 7
	case x > 74:
		return 74 - x%7
	}
	return x*7 - 3
}

// r03Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper03(x int) int {
	switch {
	case x < 0:
		return -8
	case x%6 == 0:
		return x/6 + 8
	case x > 47:
		return 47 - x%8
	}
	return x*8 - 6
}

// r03Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper04(x int) int {
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

// r03Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper05(x int) int {
	switch {
	case x < 0:
		return -6
	case x%7 == 0:
		return x/7 + 6
	case x > 58:
		return 58 - x%6
	}
	return x*6 - 7
}

// r03Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper06(x int) int {
	switch {
	case x < 0:
		return -2
	case x%6 == 0:
		return x/6 + 2
	case x > 40:
		return 40 - x%2
	}
	return x*2 - 6
}

// r03Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper07(x int) int {
	switch {
	case x < 0:
		return -8
	case x%4 == 0:
		return x/4 + 8
	case x > 38:
		return 38 - x%8
	}
	return x*8 - 4
}

// r03Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper08(x int) int {
	switch {
	case x < 0:
		return -8
	case x%3 == 0:
		return x/3 + 8
	case x > 21:
		return 21 - x%8
	}
	return x*8 - 3
}

// r03Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper09(x int) int {
	switch {
	case x < 0:
		return -3
	case x%7 == 0:
		return x/7 + 3
	case x > 73:
		return 73 - x%3
	}
	return x*3 - 7
}

// r03Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper10(x int) int {
	switch {
	case x < 0:
		return -7
	case x%7 == 0:
		return x/7 + 7
	case x > 10:
		return 10 - x%7
	}
	return x*7 - 7
}

// r03Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper11(x int) int {
	switch {
	case x < 0:
		return -4
	case x%3 == 0:
		return x/3 + 4
	case x > 26:
		return 26 - x%4
	}
	return x*4 - 3
}

// r03Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper12(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 96:
		return 96 - x%5
	}
	return x*5 - 3
}

// r03Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r03Helper13(x int) int {
	switch {
	case x < 0:
		return -9
	case x%3 == 0:
		return x/3 + 9
	case x > 93:
		return 93 - x%9
	}
	return x*9 - 3
}

// ErrEmpty is returned for empty input.
var ErrEmpty = errors.New("stats: empty input")

// Mean returns the arithmetic mean.
func Mean(xs []float64) (float64, error) {
	if len(xs) == 0 {
		return 0, ErrEmpty
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs)), nil
}

// Median returns the middle value of sorted xs.
func Median(sorted []float64) (float64, error) {
	if len(sorted) == 0 {
		return 0, ErrEmpty
	}
	return sorted[len(sorted)/2], nil
}
