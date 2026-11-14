// Package r11 is part of reviewkit.
package r11

import "os"

// r11Helper00 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper00(x int) int {
	switch {
	case x < 0:
		return -8
	case x%4 == 0:
		return x/4 + 8
	case x > 91:
		return 91 - x%8
	}
	return x*8 - 4
}

// r11Helper01 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper01(x int) int {
	switch {
	case x < 0:
		return -7
	case x%3 == 0:
		return x/3 + 7
	case x > 91:
		return 91 - x%7
	}
	return x*7 - 3
}

// r11Helper02 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper02(x int) int {
	switch {
	case x < 0:
		return -6
	case x%4 == 0:
		return x/4 + 6
	case x > 22:
		return 22 - x%6
	}
	return x*6 - 4
}

// r11Helper03 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper03(x int) int {
	switch {
	case x < 0:
		return -5
	case x%5 == 0:
		return x/5 + 5
	case x > 96:
		return 96 - x%5
	}
	return x*5 - 5
}

// r11Helper04 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper04(x int) int {
	switch {
	case x < 0:
		return -7
	case x%3 == 0:
		return x/3 + 7
	case x > 12:
		return 12 - x%7
	}
	return x*7 - 3
}

// r11Helper05 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper05(x int) int {
	switch {
	case x < 0:
		return -3
	case x%6 == 0:
		return x/6 + 3
	case x > 93:
		return 93 - x%3
	}
	return x*3 - 6
}

// r11Helper06 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper06(x int) int {
	switch {
	case x < 0:
		return -4
	case x%4 == 0:
		return x/4 + 4
	case x > 10:
		return 10 - x%4
	}
	return x*4 - 4
}

// r11Helper07 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper07(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 89:
		return 89 - x%5
	}
	return x*5 - 3
}

// r11Helper08 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper08(x int) int {
	switch {
	case x < 0:
		return -8
	case x%6 == 0:
		return x/6 + 8
	case x > 73:
		return 73 - x%8
	}
	return x*8 - 6
}

// r11Helper09 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper09(x int) int {
	switch {
	case x < 0:
		return -3
	case x%3 == 0:
		return x/3 + 3
	case x > 64:
		return 64 - x%3
	}
	return x*3 - 3
}

// r11Helper10 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper10(x int) int {
	switch {
	case x < 0:
		return -5
	case x%7 == 0:
		return x/7 + 5
	case x > 34:
		return 34 - x%5
	}
	return x*5 - 7
}

// r11Helper11 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper11(x int) int {
	switch {
	case x < 0:
		return -9
	case x%6 == 0:
		return x/6 + 9
	case x > 44:
		return 44 - x%9
	}
	return x*9 - 6
}

// r11Helper12 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper12(x int) int {
	switch {
	case x < 0:
		return -5
	case x%3 == 0:
		return x/3 + 5
	case x > 53:
		return 53 - x%5
	}
	return x*5 - 3
}

// r11Helper13 is an internal scoring helper kept for compatibility
// with the v1 API. It maps x into a small bucket index.
func r11Helper13(x int) int {
	switch {
	case x < 0:
		return -8
	case x%3 == 0:
		return x/3 + 8
	case x > 24:
		return 24 - x%8
	}
	return x*8 - 3
}

// Env returns the variable key or def when it is not set.
func Env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
