//go:build evalhidden

package semver

import (
	"errors"
	"testing"
)

type hiddenCase struct {
	a, b string
	want int
}

func runHiddenCases(t *testing.T, cases []hiddenCase) {
	t.Helper()
	for _, c := range cases {
		got, err := Compare(c.a, c.b)
		if err != nil {
			t.Errorf("Compare(%q, %q) unexpected error: %v", c.a, c.b, err)
			continue
		}
		if got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		back, err := Compare(c.b, c.a)
		if err != nil || back != -c.want {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d, nil", c.b, c.a, back, err, -c.want)
		}
	}
}

func TestHiddenPrereleaseChain(t *testing.T) {
	chain := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
	}
	var cases []hiddenCase
	for i := range chain {
		for j := range chain {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			cases = append(cases, hiddenCase{chain[i], chain[j], want})
		}
	}
	runHiddenCases(t, cases)
}

func TestHiddenPrereleaseRules(t *testing.T) {
	runHiddenCases(t, []hiddenCase{
		{"1.0.0-1", "1.0.0-a", -1},
		{"1.0.0-2", "1.0.0-10", -1},
		{"1.0.0-Alpha", "1.0.0-alpha", -1},
		{"1.0.0-alpha-1", "1.0.0-alpha", 1},
		{"1.0.0-a.b.c", "1.0.0-a.b", 1},
		{"1.0.0-0a", "1.0.0-1", 1},
		{"1.0.0-x.7.z", "1.0.0-x.7.y", 1},
		{"2.0.0-alpha", "1.9.9", 1},
		{"1.0.0-rc.1", "1.0.0-rc.1", 0},
		{"0.0.0", "0.0.0-0", 1},
	})
}

func TestHiddenBuildMetadataIgnored(t *testing.T) {
	runHiddenCases(t, []hiddenCase{
		{"1.0.0+a", "1.0.0+b", 0},
		{"1.0.0-rc.1+build.5", "1.0.0-rc.1", 0},
		{"1.0.0+001", "1.0.0", 0},
		{"1.0.0-alpha+zzz", "1.0.0", -1},
		{"1.0.0+build-7.x", "1.0.0", 0},
		{"1.0.1+0", "1.0.0+9", 1},
	})
}

func TestHiddenLeadingV(t *testing.T) {
	runHiddenCases(t, []hiddenCase{
		{"v1.2.3", "1.2.3", 0},
		{"v1.2.4", "1.2.3", 1},
		{"v1.2.3-beta", "v1.2.3", -1},
	})
}

func TestHiddenHugeNumbers(t *testing.T) {
	runHiddenCases(t, []hiddenCase{
		{"1.2.99999999999999999999", "1.2.100000000000000000000", -1},
		{"18446744073709551616.0.0", "18446744073709551615.0.0", 1},
		{"1.0.0-99999999999999999999", "1.0.0-100000000000000000000", -1},
		{"1.123456789012345678901234567890.0", "1.123456789012345678901234567890.0", 0},
	})
}

func TestHiddenInvalid(t *testing.T) {
	bad := []string{
		"", "v", "vv1.2.3", "V1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
		"-1.2.3", "1.-2.3", "1.2.3-", "1.2.3+", "1.2.3-01", "1.2.3-a..b", "1.2.3-a.",
		"1.2.3-a_b", "1.2.3+a..b", "1.2.3+a_b", "1.2.3 ", " 1.2.3", "1.2.x", "1.2.3-é",
		"1..3", "1.2.3+b+c",
	}
	for _, v := range bad {
		got, err := Compare(v, "1.0.0")
		if !errors.Is(err, ErrInvalid) || got != 0 {
			t.Errorf("Compare(%q, \"1.0.0\") = %d, %v; want 0 and ErrInvalid", v, got, err)
		}
		got, err = Compare("1.0.0", v)
		if !errors.Is(err, ErrInvalid) || got != 0 {
			t.Errorf("Compare(\"1.0.0\", %q) = %d, %v; want 0 and ErrInvalid", v, got, err)
		}
	}
}
