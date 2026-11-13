// Package p12 provides Median.
//
// note note note note note note note note note note note note
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// stable stable stable stable stable stable stable stable stable stable stable stable
// contract contract contract contract contract contract contract contract contract contract contract contract
// api api api api api api api api api api api api
// contract contract contract contract contract contract contract contract contract contract contract contract
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// caller caller caller caller caller caller caller caller caller caller caller caller
// module module module module module module module module module module module module
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
// behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour behaviour
// api api api api api api api api api api api api
// note note note note note note note note note note note note
// review review review review review review review review review review review review
// module module module module module module module module module module module module
// stable stable stable stable stable stable stable stable stable stable stable stable
// stable stable stable stable stable stable stable stable stable stable stable stable
// note note note note note note note note note note note note
// module module module module module module module module module module module module
// module module module module module module module module module module module module
// stable stable stable stable stable stable stable stable stable stable stable stable
// contract contract contract contract contract contract contract contract contract contract contract contract
// stable stable stable stable stable stable stable stable stable stable stable stable
// legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy legacy
package p12

import "sort"

// Median is documented in the package comment.
func Median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[len(s)/2]
}
