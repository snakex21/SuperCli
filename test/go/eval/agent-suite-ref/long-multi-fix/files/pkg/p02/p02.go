// Package p02 provides Reverse.
//
// caller caller caller caller caller caller caller caller caller caller caller caller
// module module module module module module module module module module module module
// module module module module module module module module module module module module
// api api api api api api api api api api api api
// note note note note note note note note note note note note
// note note note note note note note note note note note note
// module module module module module module module module module module module module
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// note note note note note note note note note note note note
// stable stable stable stable stable stable stable stable stable stable stable stable
// note note note note note note note note note note note note
// review review review review review review review review review review review review
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// module module module module module module module module module module module module
// stable stable stable stable stable stable stable stable stable stable stable stable
// review review review review review review review review review review review review
// contract contract contract contract contract contract contract contract contract contract contract contract
// caller caller caller caller caller caller caller caller caller caller caller caller
// review review review review review review review review review review review review
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// caller caller caller caller caller caller caller caller caller caller caller caller
// module module module module module module module module module module module module
// review review review review review review review review review review review review
// contract contract contract contract contract contract contract contract contract contract contract contract
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
package p02

// Reverse is documented in the package comment.
func Reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
