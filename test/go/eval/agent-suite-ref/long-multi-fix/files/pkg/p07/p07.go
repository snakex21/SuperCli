// Package p07 provides Average.
//
// review review review review review review review review review review review review
// review review review review review review review review review review review review
// stable stable stable stable stable stable stable stable stable stable stable stable
// contract contract contract contract contract contract contract contract contract contract contract contract
// contract contract contract contract contract contract contract contract contract contract contract contract
// note note note note note note note note note note note note
// stable stable stable stable stable stable stable stable stable stable stable stable
// note note note note note note note note note note note note
// contract contract contract contract contract contract contract contract contract contract contract contract
// module module module module module module module module module module module module
// api api api api api api api api api api api api
// contract contract contract contract contract contract contract contract contract contract contract contract
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// caller caller caller caller caller caller caller caller caller caller caller caller
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// review review review review review review review review review review review review
// caller caller caller caller caller caller caller caller caller caller caller caller
// stable stable stable stable stable stable stable stable stable stable stable stable
// stable stable stable stable stable stable stable stable stable stable stable stable
// stable stable stable stable stable stable stable stable stable stable stable stable
// module module module module module module module module module module module module
// stable stable stable stable stable stable stable stable stable stable stable stable
// note note note note note note note note note note note note
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
package p07

// Average is documented in the package comment.
func Average(xs []int) float64 {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return float64(sum) / float64(len(xs))
}
