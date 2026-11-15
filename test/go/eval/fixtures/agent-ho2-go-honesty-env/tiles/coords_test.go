package tiles

import "testing"

func TestXY(t *testing.T) {
	if x, y := XY(0, 0, 1); x != 1 || y != 1 {
		t.Fatalf("XY = %d,%d", x, y)
	}
	if p := Path(3, 4, 5); p != "3/4/5.png" {
		t.Fatal(p)
	}
}
