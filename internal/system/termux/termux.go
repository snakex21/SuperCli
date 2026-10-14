// Package termux lets the static Linux release run inside Termux on Android.
// Android has no /etc/resolv.conf, so Go's built-in resolver would ask
// localhost:53 and every web/provider request would fail. The fix lives in
// termux_linux.go; this file holds the platform-independent parsing.
package termux

import (
	"net"
	"strings"
)

// fallbackServers are used only when Termux's resolv.conf lists none.
var fallbackServers = []string{"8.8.8.8:53", "1.1.1.1:53"}

// nameservers returns host:53 addresses from resolv.conf content.
func nameservers(conf string) []string {
	var out []string
	for _, line := range strings.Split(conf, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		host, _, _ := strings.Cut(fields[1], "%")
		if net.ParseIP(host) != nil {
			out = append(out, net.JoinHostPort(fields[1], "53"))
		}
	}
	if len(out) == 0 {
		return fallbackServers
	}
	return out
}

// isTermux reports whether prefix is a Termux installation prefix.
func isTermux(prefix string) bool {
	return strings.Contains(prefix, "/com.termux/")
}
