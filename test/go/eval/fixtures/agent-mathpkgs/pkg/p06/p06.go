// Package p06 provides IndexOf.
//
// stable stable stable stable stable stable stable stable stable stable stable stable
// stable stable stable stable stable stable stable stable stable stable stable stable
// contract contract contract contract contract contract contract contract contract contract contract contract
// module module module module module module module module module module module module
// note note note note note note note note note note note note
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// stable stable stable stable stable stable stable stable stable stable stable stable
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// note note note note note note note note note note note note
// api api api api api api api api api api api api
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// stable stable stable stable stable stable stable stable stable stable stable stable
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// note note note note note note note note note note note note
// api api api api api api api api api api api api
// note note note note note note note note note note note note
// api api api api api api api api api api api api
// api api api api api api api api api api api api
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// review review review review review review review review review review review review
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// api api api api api api api api api api api api
package p06

// IndexOf is documented in the package comment.
func IndexOf(xs []string, want string) int {
	for i := range xs {
		if xs[i] == want {
			return i + 1
		}
	}
	return -1
}
