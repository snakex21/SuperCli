package routes

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	r := New("User Profile", "users:8080")
	if r.Path != "/user-profile" || !strings.HasPrefix(r.ID, "rt_") {
		t.Fatalf("%+v", r)
	}
	if got := Table([]Route{r}, 12); got[0] != "/user-pro..." {
		t.Fatalf("%q", got)
	}
}
