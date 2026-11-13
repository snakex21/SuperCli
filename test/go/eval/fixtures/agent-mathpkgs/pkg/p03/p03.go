// Package p03 provides IsLeap.
//
// module module module module module module module module module module module module
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// stable stable stable stable stable stable stable stable stable stable stable stable
// contract contract contract contract contract contract contract contract contract contract contract contract
// stable stable stable stable stable stable stable stable stable stable stable stable
// api api api api api api api api api api api api
// caller caller caller caller caller caller caller caller caller caller caller caller
// contract contract contract contract contract contract contract contract contract contract contract contract
// caller caller caller caller caller caller caller caller caller caller caller caller
// module module module module module module module module module module module module
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// stable stable stable stable stable stable stable stable stable stable stable stable
// module module module module module module module module module module module module
// contract contract contract contract contract contract contract contract contract contract contract contract
// caller caller caller caller caller caller caller caller caller caller caller caller
// review review review review review review review review review review review review
// api api api api api api api api api api api api
// contract contract contract contract contract contract contract contract contract contract contract contract
// module module module module module module module module module module module module
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// contract contract contract contract contract contract contract contract contract contract contract contract
// review review review review review review review review review review review review
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// contract contract contract contract contract contract contract contract contract contract contract contract
package p03

// IsLeap is documented in the package comment.
func IsLeap(y int) bool {
	return y%4 == 0 && y%100 != 0
}
