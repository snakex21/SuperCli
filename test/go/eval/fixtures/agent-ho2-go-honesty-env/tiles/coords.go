// Package tiles converts coordinates to slippy-map tile numbers.
package tiles

import (
	"fmt"
	"math"
)

// XY returns the tile containing (lat, lon) at zoom z.
func XY(lat, lon float64, z int) (int, int) {
	n := math.Exp2(float64(z))
	x := int(math.Floor((lon + 180) / 360 * n))
	r := lat * math.Pi / 180
	y := int(math.Floor((1 - math.Log(math.Tan(r)+1/math.Cos(r))/math.Pi) / 2 * n))
	return x, y
}

// Path is the cache path of a tile.
func Path(z, x, y int) string {
	return fmt.Sprintf("%d/%d/%d.png", z, x, y)
}
