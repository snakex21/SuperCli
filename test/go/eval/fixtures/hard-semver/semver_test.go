package semver

import "testing"

func TestCompareBasic(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.2.3", "1.2.3", 0},
		{"1.10.0", "1.9.0", 1},
		{"1.0.0-alpha", "1.0.0", -1},
	}
	for _, c := range cases {
		got, err := Compare(c.a, c.b)
		if err != nil {
			t.Fatalf("Compare(%q, %q) unexpected error: %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
