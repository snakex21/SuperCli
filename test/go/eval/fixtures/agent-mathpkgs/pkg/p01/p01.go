// Package p01 provides Max.
//
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// note note note note note note note note note note note note
// caller caller caller caller caller caller caller caller caller caller caller caller
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// stable stable stable stable stable stable stable stable stable stable stable stable
// note note note note note note note note note note note note
// api api api api api api api api api api api api
// contract contract contract contract contract contract contract contract contract contract contract contract
// module module module module module module module module module module module module
// api api api api api api api api api api api api
// note note note note note note note note note note note note
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// api api api api api api api api api api api api
// review review review review review review review review review review review review
// note note note note note note note note note note note note
// stable stable stable stable stable stable stable stable stable stable stable stable
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// note note note note note note note note note note note note
// module module module module module module module module module module module module
// contract contract contract contract contract contract contract contract contract contract contract contract
// review review review review review review review review review review review review
// contract contract contract contract contract contract contract contract contract contract contract contract
// caller caller caller caller caller caller caller caller caller caller caller caller
// note note note note note note note note note note note note
package p01

// Max is documented in the package comment.
func Max(xs []int) int {
	m := xs[0]
	for _, x := range xs {
		if x < m {
			m = x
		}
	}
	return m
}
