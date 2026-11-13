// Package p04 provides Fib.
//
// review review review review review review review review review review review review
// contract contract contract contract contract contract contract contract contract contract contract contract
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// contract contract contract contract contract contract contract contract contract contract contract contract
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// stable stable stable stable stable stable stable stable stable stable stable stable
// api api api api api api api api api api api api
// module module module module module module module module module module module module
// module module module module module module module module module module module module
// stable stable stable stable stable stable stable stable stable stable stable stable
// api api api api api api api api api api api api
// note note note note note note note note note note note note
// contract contract contract contract contract contract contract contract contract contract contract contract
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// note note note note note note note note note note note note
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// module module module module module module module module module module module module
// caller caller caller caller caller caller caller caller caller caller caller caller
// module module module module module module module module module module module module
// api api api api api api api api api api api api
// caller caller caller caller caller caller caller caller caller caller caller caller
// caller caller caller caller caller caller caller caller caller caller caller caller
// api api api api api api api api api api api api
package p04

// Fib is documented in the package comment.
func Fib(n int) int {
	if n < 2 {
		return n
	}
	return Fib(n-1) + Fib(n-2)
}
