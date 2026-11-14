package r05

import (
	"errors"
	"testing"
)

func TestHiddenReview(t *testing.T) {
	calls := 0
	sentinel := errors.New("sentinel")
	err := Retry(3, func() error { calls++; return sentinel })
	if calls != 3 || err != sentinel {
		t.Fatal("Retry", calls, err)
	}
}
