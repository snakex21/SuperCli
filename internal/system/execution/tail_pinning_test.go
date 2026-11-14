package execution

import "testing"

func TestTailPinningDefaultsByBackend(t *testing.T) {
	on, off := true, false
	cases := []struct {
		override *bool
		local    bool
		want     bool
	}{
		{nil, false, true}, {nil, true, false},
		{&off, false, false}, {&on, true, true},
	}
	for _, c := range cases {
		if got := TailPinning(c.override, c.local); got != c.want {
			t.Fatalf("TailPinning(%v, local=%v) = %v, want %v", c.override, c.local, got, c.want)
		}
	}
}
