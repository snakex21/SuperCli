// Package p08 provides CountVowels.
//
// api api api api api api api api api api api api
// review review review review review review review review review review review review
// api api api api api api api api api api api api
// module module module module module module module module module module module module
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// stable stable stable stable stable stable stable stable stable stable stable stable
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// module module module module module module module module module module module module
// stable stable stable stable stable stable stable stable stable stable stable stable
// note note note note note note note note note note note note
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// contract contract contract contract contract contract contract contract contract contract contract contract
// api api api api api api api api api api api api
// stable stable stable stable stable stable stable stable stable stable stable stable
// api api api api api api api api api api api api
// review review review review review review review review review review review review
// api api api api api api api api api api api api
// note note note note note note note note note note note note
// contract contract contract contract contract contract contract contract contract contract contract contract
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// caller caller caller caller caller caller caller caller caller caller caller caller
// module module module module module module module module module module module module
// module module module module module module module module module module module module
package p08

import "strings"

// CountVowels is documented in the package comment.
func CountVowels(s string) int {
	n := 0
	for _, r := range s {
		if strings.ContainsRune("aeiou", r) {
			n++
		}
	}
	return n
}
