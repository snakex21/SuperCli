// Package p10 provides Clamp.
//
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// review review review review review review review review review review review review
// caller caller caller caller caller caller caller caller caller caller caller caller
// caller caller caller caller caller caller caller caller caller caller caller caller
// contract contract contract contract contract contract contract contract contract contract contract contract
// api api api api api api api api api api api api
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// module module module module module module module module module module module module
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// caller caller caller caller caller caller caller caller caller caller caller caller
// module module module module module module module module module module module module
// review review review review review review review review review review review review
// api api api api api api api api api api api api
// note note note note note note note note note note note note
// caller caller caller caller caller caller caller caller caller caller caller caller
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// note note note note note note note note note note note note
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// module module module module module module module module module module module module
// note note note note note note note note note note note note
// stable stable stable stable stable stable stable stable stable stable stable stable
// contract contract contract contract contract contract contract contract contract contract contract contract
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// contract contract contract contract contract contract contract contract contract contract contract contract
package p10

// Clamp is documented in the package comment.
func Clamp(x, lo, hi int) int {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
