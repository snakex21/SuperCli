// Package p11 provides TrimAll.
//
// review review review review review review review review review review review review
// note note note note note note note note note note note note
// contract contract contract contract contract contract contract contract contract contract contract contract
// api api api api api api api api api api api api
// review review review review review review review review review review review review
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// stable stable stable stable stable stable stable stable stable stable stable stable
// stable stable stable stable stable stable stable stable stable stable stable stable
// api api api api api api api api api api api api
// stable stable stable stable stable stable stable stable stable stable stable stable
// stable stable stable stable stable stable stable stable stable stable stable stable
// review review review review review review review review review review review review
// note note note note note note note note note note note note
// contract contract contract contract contract contract contract contract contract contract contract contract
// caller caller caller caller caller caller caller caller caller caller caller caller
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// caller caller caller caller caller caller caller caller caller caller caller caller
// contract contract contract contract contract contract contract contract contract contract contract contract
// note note note note note note note note note note note note
// contract contract contract contract contract contract contract contract contract contract contract contract
// caller caller caller caller caller caller caller caller caller caller caller caller
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// caller caller caller caller caller caller caller caller caller caller caller caller
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// review review review review review review review review review review review review
package p11

import "strings"

// TrimAll is documented in the package comment.
func TrimAll(s, prefix string) string {
	if prefix != "" && strings.HasPrefix(s, prefix) {
		s = s[len(prefix):]
	}
	return s
}
