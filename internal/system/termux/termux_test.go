package termux

import (
	"strings"
	"testing"
)

func TestNameservers(t *testing.T) {
	got := nameservers("# comment\nnameserver 8.8.4.4\nsearch lan\nnameserver fe80::1%wlan0\nnameserver bogus\n")
	if strings.Join(got, ",") != "8.8.4.4:53,[fe80::1%wlan0]:53" {
		t.Fatal(got)
	}
	if got := nameservers(""); strings.Join(got, ",") != strings.Join(fallbackServers, ",") {
		t.Fatal(got)
	}
}

func TestIsTermux(t *testing.T) {
	if !isTermux("/data/data/com.termux/files/usr") || isTermux("") || isTermux("/usr") {
		t.Fatal("termux prefix detection")
	}
}
