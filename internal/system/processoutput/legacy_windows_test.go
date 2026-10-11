//go:build windows

package processoutput

import (
	"bytes"
	"testing"
)

func TestOEM852GroupingSpace(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out)
	w.decodeLegacy = func(p []byte) string { return codePageText(p, 852) }
	_, _ = w.Write([]byte("18"))
	_, _ = w.Write([]byte{0xff})
	_, _ = w.Write([]byte("847\n"))
	_ = w.Close()
	if out.String() != "18\u00a0847\n" {
		t.Fatalf("CP852: %q", out.String())
	}
}
