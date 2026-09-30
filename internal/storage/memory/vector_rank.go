package memory

import (
	"encoding/binary"
	"math"
)

type vectorScore struct {
	id  string
	sim float64
}

func (s vectorScore) betterThan(other vectorScore) bool {
	if s.sim != other.sim {
		return s.sim > other.sim
	}
	return s.id < other.id
}

// The worst retained candidate is at the root, so scanning a large memory
// database needs only k ranking entries. Equal scores keep the old id order.
type vectorTopHeap []vectorScore

func (h vectorTopHeap) Len() int           { return len(h) }
func (h vectorTopHeap) Less(i, j int) bool { return h[j].betterThan(h[i]) }
func (h vectorTopHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *vectorTopHeap) Push(value any)    { *h = append(*h, value.(vectorScore)) }
func (h *vectorTopHeap) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	*h = (*h)[:last]
	return value
}

// Consume the database's row buffer before Next rather than cloning its bytes
// and allocating a float32 slice for every stored vector. The query norm is
// shared by the whole scan; the cosine formula and ranking remain unchanged.
func encodedCosine(query []float32, blob []byte, queryNorm float64) float64 {
	if queryNorm == 0 {
		return 0
	}
	var dot, norm float64
	for i, value := range query {
		v := float64(math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:])))
		dot += float64(value) * v
		norm += v * v
	}
	if norm == 0 {
		return 0
	}
	return dot / (queryNorm * math.Sqrt(norm))
}
