package csvio

import (
	"strings"
	"testing"
)

func TestReadLines(t *testing.T) {
	lines, err := ReadLines(strings.NewReader("no,sku,desc,qty,unit\n1,A-1,Bolt,10,0.25\n\n2,B-2,Nut,5,0.1\n"))
	if err != nil || len(lines) != 2 || lines[1].Unit != 10 || lines[0].Desc != "Bolt" {
		t.Fatalf("ReadLines = %+v, %v", lines, err)
	}
}
