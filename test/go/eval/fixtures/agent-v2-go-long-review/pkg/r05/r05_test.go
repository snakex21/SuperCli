package r05

import "testing"

func TestVisible(t *testing.T) {
	if Retry(2, func() error { return nil }) != nil {
		t.Fatal("retry")
	}
}
