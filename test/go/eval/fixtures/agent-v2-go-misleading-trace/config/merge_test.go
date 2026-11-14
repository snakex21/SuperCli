package config

import "testing"

func TestMergeOverride(t *testing.T) {
	m := Merge(Base(), Config{Name: "blog", Vars: map[string]string{"year": "2027"}})
	if m.Name != "blog" || m.Vars["year"] != "2027" || m.Vars["brand"] != "Acme" {
		t.Fatalf("Merge = %+v", m)
	}
}
