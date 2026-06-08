package tools

import "math"

// expInline exists so search_index.go can stay free of the
// math import. The Go compiler inlines tiny helpers like this
// automatically; we just need a single call site.
func expInline(x float64) float64 { return math.Exp(x) }
