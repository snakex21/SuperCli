package users

import (
	"errors"
	"testing"
)

func TestHiddenEmailEndToEnd(t *testing.T) {
	var s Store
	u := s.Add("ann", "ann@example.com")
	if u.Email != "ann@example.com" || u.ID != 1 {
		t.Fatalf("user = %+v", u)
	}
	s.Add("bob", "bob@example.org")
	want := "id,name,email\n1,ann,ann@example.com\n2,bob,bob@example.org\n"
	if got := s.ExportCSV(); got != want {
		t.Fatalf("csv %q", got)
	}
	name, email, err := ParseCreate(" cid , cid@example.net ")
	if err != nil || name != "cid" || email != "cid@example.net" {
		t.Fatalf("parse = %q %q %v", name, email, err)
	}
	for _, bad := range []string{"cid", "cid,", "cid,nomail", ",x@y", ""} {
		if _, _, err := ParseCreate(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ParseCreate(%q) err = %v", bad, err)
		}
	}
}
