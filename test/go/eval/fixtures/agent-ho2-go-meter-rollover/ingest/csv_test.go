package ingest

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	rs, err := Readings(strings.NewReader("meter,at,value\nM-1,2027-03-01T06:00:00Z,42\n"))
	if err != nil || len(rs) != 1 || rs[0].Value != 42 {
		t.Fatalf("%v %v", rs, err)
	}
	ms, err := Meters(strings.NewReader("meter,customer,model\nM-1,C-1,EM-5\n"))
	if err != nil || ms[0].Model != "EM-5" {
		t.Fatalf("%v %v", ms, err)
	}
	if _, err := Meters(strings.NewReader("id,model\n")); err == nil {
		t.Fatal("want header error")
	}
}
