package csvio

import (
	"strings"
	"testing"
)

func TestHiddenQuoted(t *testing.T) {
	in := "no,sku,desc,qty,unit\n3,W-1,\"Widget, large\",2,19.99\n4,Q-9,\"The \"\"best\"\" nut\",1,0.5\n"
	lines, err := ReadLines(strings.NewReader(in))
	if err != nil || len(lines) != 2 {
		t.Fatalf("ReadLines = %+v, %v", lines, err)
	}
	if lines[0].Desc != "Widget, large" || lines[0].Unit != 1999 || lines[0].Qty != 2 || lines[0].No != 3 {
		t.Fatalf("line 0 = %+v", lines[0])
	}
	if lines[1].Desc != `The "best" nut` || lines[1].Unit != 50 {
		t.Fatalf("line 1 = %+v", lines[1])
	}
	if _, err := ReadLines(strings.NewReader("no,sku,desc,qty,unit\n1,A,B,x,1\n")); err == nil {
		t.Fatal("bad qty must still fail")
	}
}
