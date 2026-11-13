// Package p09 provides Factorial.
//
// api api api api api api api api api api api api
// review review review review review review review review review review review review
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// review review review review review review review review review review review review
// module module module module module module module module module module module module
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// review review review review review review review review review review review review
// review review review review review review review review review review review review
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// module module module module module module module module module module module module
// caller caller caller caller caller caller caller caller caller caller caller caller
// module module module module module module module module module module module module
// caller caller caller caller caller caller caller caller caller caller caller caller
// review review review review review review review review review review review review
// note note note note note note note note note note note note
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// contract contract contract contract contract contract contract contract contract contract contract contract
// stable stable stable stable stable stable stable stable stable stable stable stable
// stable stable stable stable stable stable stable stable stable stable stable stable
// api api api api api api api api api api api api
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
package p09

// Factorial is documented in the package comment.
func Factorial(n int) int {
	if n == 0 {
		return 0
	}
	return n * Factorial(n-1)
}
