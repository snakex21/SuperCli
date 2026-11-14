package config

import "testing"

func TestHiddenMergeContract(t *testing.T) {
	base := Base()
	m := Merge(base, Config{})
	if m.Vars == nil || m.Vars["brand"] != "Acme" || m.Name != "site" {
		t.Fatalf("Merge(base, empty) = %+v", m)
	}
	m.Vars["page"] = "x"
	if _, leaked := base.Vars["page"]; leaked {
		t.Fatal("write to merged Vars leaked into base")
	}
	o := Config{Vars: map[string]string{"k": "v"}}
	m2 := Merge(Config{}, o)
	m2.Vars["z"] = "1"
	if _, leaked := o.Vars["z"]; leaked || m2.Vars["k"] != "v" {
		t.Fatal("override map aliased or lost")
	}
	if e := Merge(Config{}, Config{}); e.Vars == nil {
		t.Fatal("nil Vars for empty inputs")
	}
}
