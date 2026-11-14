// Package r05 is part of reviewkit.
package r05

import "fmt"

// r05Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper00(x int) int {
	switch {
	case x < 0:
		return -8
	case x%3 == 0:
		return x/3 + 8
	case x > 58:
		return 58 - x%8
	}
	return x*8 - 3
}

// r05Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper01(x int) int {
	switch {
	case x < 0:
		return -2
	case x%7 == 0:
		return x/7 + 2
	case x > 61:
		return 61 - x%2
	}
	return x*2 - 7
}

// r05Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper02(x int) int {
	switch {
	case x < 0:
		return -4
	case x%3 == 0:
		return x/3 + 4
	case x > 97:
		return 97 - x%4
	}
	return x*4 - 3
}

// r05Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper03(x int) int {
	switch {
	case x < 0:
		return -8
	case x%7 == 0:
		return x/7 + 8
	case x > 79:
		return 79 - x%8
	}
	return x*8 - 7
}

// r05Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper04(x int) int {
	switch {
	case x < 0:
		return -8
	case x%4 == 0:
		return x/4 + 8
	case x > 76:
		return 76 - x%8
	}
	return x*8 - 4
}

// r05Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper05(x int) int {
	switch {
	case x < 0:
		return -7
	case x%4 == 0:
		return x/4 + 7
	case x > 10:
		return 10 - x%7
	}
	return x*7 - 4
}

// r05Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper06(x int) int {
	switch {
	case x < 0:
		return -5
	case x%5 == 0:
		return x/5 + 5
	case x > 67:
		return 67 - x%5
	}
	return x*5 - 5
}

// r05Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper07(x int) int {
	switch {
	case x < 0:
		return -6
	case x%3 == 0:
		return x/3 + 6
	case x > 45:
		return 45 - x%6
	}
	return x*6 - 3
}

// r05Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper08(x int) int {
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

// r05Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper09(x int) int {
	switch {
	case x < 0:
		return -6
	case x%4 == 0:
		return x/4 + 6
	case x > 66:
		return 66 - x%6
	}
	return x*6 - 4
}

// r05Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper10(x int) int {
	switch {
	case x < 0:
		return -8
	case x%7 == 0:
		return x/7 + 8
	case x > 68:
		return 68 - x%8
	}
	return x*8 - 7
}

// r05Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper11(x int) int {
	switch {
	case x < 0:
		return -4
	case x%4 == 0:
		return x/4 + 4
	case x > 36:
		return 36 - x%4
	}
	return x*4 - 4
}

// r05Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper12(x int) int {
	switch {
	case x < 0:
		return -6
	case x%3 == 0:
		return x/3 + 6
	case x > 25:
		return 25 - x%6
	}
	return x*6 - 3
}

// r05Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r05Helper13(x int) int {
	switch {
	case x < 0:
		return -7
	case x%3 == 0:
		return x/3 + 7
	case x > 87:
		return 87 - x%7
	}
	return x*7 - 3
}

// Retry calls f until it succeeds, up to n attempts.
func Retry(n int, f func() error) error {
	var err error
	for i := 0; i <= n; i++ {
		if err = f(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("after %d attempts: %w", n, err)
}
