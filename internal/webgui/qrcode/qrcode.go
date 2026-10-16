// Package qrcode encodes UTF-8 text as ISO/IEC 18004 QR Codes in byte mode,
// with error correction level M and versions 1 through 10.
package qrcode

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a square QR Code without a quiet zone. Coordinates start at the
// top-left corner. A Code returned by Encode is safe for concurrent reading.
type Code struct {
	Size    int // Number of modules along each side.
	modules []bool
}

// Dark reports whether the module at (x, y) is dark. It returns false for
// coordinates outside the code.
func (c *Code) Dark(x, y int) bool {
	return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.modules[y*c.Size+x]
}

type versionSpec struct {
	dataBytes int
	ecBytes   int // Per block.
	blocks    int
	alignment []int
}

// Level M block parameters from ISO/IEC 18004. Short blocks precede long
// blocks; the latter contain one additional data codeword each.
var versions = [...]versionSpec{
	{}, // Versions are numbered from 1.
	{16, 10, 1, nil},
	{28, 16, 1, []int{6, 18}},
	{44, 26, 1, []int{6, 22}},
	{64, 18, 2, []int{6, 26}},
	{86, 24, 2, []int{6, 30}},
	{108, 16, 4, []int{6, 34}},
	{124, 18, 4, []int{6, 22, 38}},
	{154, 22, 4, []int{6, 24, 42}},
	{182, 22, 5, []int{6, 26, 46}},
	{216, 26, 5, []int{6, 28, 50}},
}

// Encode encodes text's UTF-8 bytes using error correction level M. It chooses
// the smallest version from 1 through 10 and the lowest-penalty mask (breaking
// ties by mask number). No ECI header is added. Text exceeding 213 bytes returns
// an error, even if it consists of fewer than 213 Unicode characters.
func Encode(text string) (*Code, error) {
	version := 1
	for ; version < len(versions); version++ {
		countBits := 8
		if version >= 10 {
			countBits = 16
		}
		if len(text) <= (versions[version].dataBytes*8-4-countBits)/8 {
			break
		}
	}
	if version == len(versions) {
		return nil, errors.New("qrcode: text exceeds version 10-M byte capacity (213 bytes)")
	}

	m := newMatrix(version)
	m.placeData(interleave(encodeData(text, version), versions[version]))
	bestMask, bestPenalty := 0, int(^uint(0)>>1)
	for mask := 0; mask < 8; mask++ {
		m.applyMask(mask)
		m.drawFormat(mask)
		if score := m.penalty(); score < bestPenalty {
			bestMask, bestPenalty = mask, score
		}
		m.applyMask(mask) // XOR again to restore the unmasked data.
	}
	m.applyMask(bestMask)
	m.drawFormat(bestMask)
	return m.Code, nil
}

// SVG encodes text as a compact SVG with a white background, one path for all
// dark modules, and a four-module quiet zone. moduleSize is the positive width
// and height of each module in pixels; invalid or overflowing sizes error.
func SVG(text string, moduleSize int) (string, error) {
	if moduleSize <= 0 {
		return "", errors.New("qrcode: module size must be positive")
	}
	c, err := Encode(text)
	if err != nil {
		return "", err
	}
	size := c.Size + 8
	if moduleSize > int(^uint(0)>>1)/size {
		return "", errors.New("qrcode: SVG dimensions overflow int")
	}
	pixels := size * moduleSize
	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="100%%" height="100%%" fill="#fff"/><path fill="#000" d="`, pixels, pixels, size, size)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Dark(x, y) {
				fmt.Fprintf(&out, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	out.WriteString(`"/></svg>`)
	return out.String(), nil
}

type bitBuffer []bool

func (b *bitBuffer) appendBits(value, count int) {
	for i := count - 1; i >= 0; i-- {
		*b = append(*b, value>>i&1 != 0)
	}
}

func encodeData(text string, version int) []byte {
	capacity := versions[version].dataBytes
	var bits bitBuffer
	bits.appendBits(0b0100, 4) // Byte mode.
	countBits := 8
	if version >= 10 {
		countBits = 16
	}
	bits.appendBits(len(text), countBits)
	for i := 0; i < len(text); i++ {
		bits.appendBits(int(text[i]), 8)
	}
	bits.appendBits(0, min(4, capacity*8-len(bits))) // Terminator.
	bits.appendBits(0, (8-len(bits)%8)%8)

	data := make([]byte, len(bits)/8, capacity)
	for i, bit := range bits {
		if bit {
			data[i/8] |= 1 << (7 - i%8)
		}
	}
	for pad := byte(0xec); len(data) < capacity; pad ^= 0xec ^ 0x11 {
		data = append(data, pad)
	}
	return data
}

// gfMultiply multiplies in GF(256), reduced by x^8+x^4+x^3+x^2+1.
func gfMultiply(x, y byte) byte {
	a, b, product := int(x), int(y), 0
	for b != 0 {
		if b&1 != 0 {
			product ^= a
		}
		a <<= 1
		if a&0x100 != 0 {
			a ^= 0x11d
		}
		b >>= 1
	}
	return byte(product)
}

// rsRemainder returns degree Reed-Solomon parity codewords. The generator has
// roots alpha^0 through alpha^(degree-1), with alpha = 2 in GF(256).
func rsRemainder(data []byte, degree int) []byte {
	// Store the generator's coefficients in descending order, omitting its
	// leading coefficient of 1.
	divisor := make([]byte, degree)
	divisor[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range divisor {
			divisor[j] = gfMultiply(divisor[j], root)
			if j+1 < degree {
				divisor[j] ^= divisor[j+1]
			}
		}
		root = gfMultiply(root, 2)
	}

	result := make([]byte, degree)
	for _, value := range data {
		factor := value ^ result[0]
		copy(result, result[1:])
		result[degree-1] = 0
		for i, coefficient := range divisor {
			result[i] ^= gfMultiply(coefficient, factor)
		}
	}
	return result
}

func interleave(data []byte, spec versionSpec) []byte {
	blocks := make([][]byte, spec.blocks)
	parity := make([][]byte, spec.blocks)
	shortSize := len(data) / spec.blocks
	shortBlocks := spec.blocks - len(data)%spec.blocks
	offset := 0
	for i := range blocks {
		length := shortSize
		if i >= shortBlocks {
			length++
		}
		blocks[i] = data[offset : offset+length]
		parity[i] = rsRemainder(blocks[i], spec.ecBytes)
		offset += length
	}

	result := make([]byte, 0, len(data)+spec.blocks*spec.ecBytes)
	for i := 0; i <= shortSize; i++ {
		for _, block := range blocks {
			if i < len(block) {
				result = append(result, block[i])
			}
		}
	}
	for i := 0; i < spec.ecBytes; i++ {
		for _, block := range parity {
			result = append(result, block[i])
		}
	}
	return result
}

type matrix struct {
	*Code
	function []bool // Function modules must not be overwritten or masked.
}

func newMatrix(version int) *matrix {
	size := version*4 + 17
	m := &matrix{
		Code:     &Code{Size: size, modules: make([]bool, size*size)},
		function: make([]bool, size*size),
	}
	for i := 0; i < size; i++ {
		m.setFunction(6, i, i%2 == 0)
		m.setFunction(i, 6, i%2 == 0)
	}
	m.drawFinder(3, 3)
	m.drawFinder(size-4, 3)
	m.drawFinder(3, size-4)
	for _, y := range versions[version].alignment {
		for _, x := range versions[version].alignment {
			if (x == 6 && y == 6) || (x == 6 && y == size-7) || (x == size-7 && y == 6) {
				continue // These three positions overlap finder patterns.
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					m.setFunction(x+dx, y+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	m.drawFormat(0) // Reserve both copies before placing data.
	if version >= 7 {
		bits := versionBits(version)
		for i := 0; i < 18; i++ {
			x, y := size-11+i%3, i/3
			m.setFunction(x, y, bits>>i&1 != 0)
			m.setFunction(y, x, bits>>i&1 != 0)
		}
	}
	return m
}

func (m *matrix) setFunction(x, y int, dark bool) {
	m.modules[y*m.Size+x] = dark
	m.function[y*m.Size+x] = true
}

func (m *matrix) drawFinder(cx, cy int) {
	// The outermost (distance 4) ring is the white separator.
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			x, y := cx+dx, cy+dy
			if x >= 0 && y >= 0 && x < m.Size && y < m.Size {
				distance := max(abs(dx), abs(dy))
				m.setFunction(x, y, distance != 2 && distance != 4)
			}
		}
	}
}

// formatBits returns BCH(15,5) format information. Level M's two bits are 00.
func formatBits(mask int) int {
	remainder := mask
	for i := 0; i < 10; i++ {
		remainder = remainder<<1 ^ (remainder>>9)*0x537
	}
	return (mask<<10 | remainder) ^ 0x5412
}

// versionBits returns BCH(18,6) version information.
func versionBits(version int) int {
	remainder := version
	for i := 0; i < 12; i++ {
		remainder = remainder<<1 ^ (remainder>>11)*0x1f25
	}
	return version<<12 | remainder
}

func (m *matrix) drawFormat(mask int) {
	bits := formatBits(mask)
	for i := 0; i <= 5; i++ {
		m.setFunction(8, i, bits>>i&1 != 0)
	}
	m.setFunction(8, 7, bits>>6&1 != 0)
	m.setFunction(8, 8, bits>>7&1 != 0)
	m.setFunction(7, 8, bits>>8&1 != 0)
	for i := 9; i < 15; i++ {
		m.setFunction(14-i, 8, bits>>i&1 != 0)
	}
	for i := 0; i < 8; i++ {
		m.setFunction(m.Size-1-i, 8, bits>>i&1 != 0)
	}
	for i := 8; i < 15; i++ {
		m.setFunction(8, m.Size-15+i, bits>>i&1 != 0)
	}
	m.setFunction(8, m.Size-8, true) // Fixed dark module, not a format bit.
}

func (m *matrix) placeData(data []byte) {
	bitIndex := 0
	upward := true
	for right := m.Size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // Skip the vertical timing pattern.
		}
		for row := 0; row < m.Size; row++ {
			y := row
			if upward {
				y = m.Size - 1 - row
			}
			for dx := 0; dx < 2; dx++ {
				x := right - dx
				if m.function[y*m.Size+x] {
					continue
				}
				dark := false // Remainder bits are zero before masking.
				if bitIndex < len(data)*8 {
					dark = data[bitIndex/8]>>(7-bitIndex%8)&1 != 0
				}
				m.modules[y*m.Size+x] = dark
				bitIndex++
			}
		}
		upward = !upward
	}
}

func maskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	default: // Mask 7.
		return ((x+y)%2+x*y%3)%2 == 0
	}
}

func (m *matrix) applyMask(mask int) {
	for y := 0; y < m.Size; y++ {
		for x := 0; x < m.Size; x++ {
			i := y*m.Size + x
			if !m.function[i] && maskBit(mask, x, y) {
				m.modules[i] = !m.modules[i]
			}
		}
	}
}

// penalty applies all four mask evaluation rules to the complete symbol,
// including its format and version information.
func (c *Code) penalty() int {
	score, dark := 0, 0
	line := make([]bool, c.Size)
	for y := 0; y < c.Size; y++ {
		copy(line, c.modules[y*c.Size:(y+1)*c.Size])
		score += linePenalty(line)
		for x := 0; x < c.Size; x++ {
			if c.Dark(x, y) {
				dark++
			}
			if x+1 < c.Size && y+1 < c.Size {
				color := c.Dark(x, y)
				if c.Dark(x+1, y) == color && c.Dark(x, y+1) == color && c.Dark(x+1, y+1) == color {
					score += 3 // N2: each monochrome 2x2 square.
				}
			}
		}
	}
	for x := 0; x < c.Size; x++ {
		for y := 0; y < c.Size; y++ {
			line[y] = c.Dark(x, y)
		}
		score += linePenalty(line)
	}
	total := c.Size * c.Size
	score += abs(dark*20-total*10) / total * 10 // N4: 5% steps away from 50%.
	return score
}

func linePenalty(line []bool) int {
	// Alternating light/dark run lengths, beginning with a possibly empty
	// light run. Score N1 before adding virtual white borders for N3.
	runs := []int{0}
	last := false
	for _, dark := range line {
		if dark != last {
			runs = append(runs, 0)
			last = dark
		}
		runs[len(runs)-1]++
	}
	score := 0
	for _, length := range runs {
		if length >= 5 {
			score += length - 2 // N1: 3 for five, then 1 per extra module.
		}
	}

	// N3: dark/light/dark/light/dark in the ratio 1:1:3:1:1,
	// with at least four units of light on either side. The quiet zone
	// outside the symbol counts as light. Each qualifying side costs 40.
	runs[0] += len(line)
	if last {
		runs = append(runs, len(line))
	} else {
		runs[len(runs)-1] += len(line)
	}
	for i := 1; i+5 < len(runs); i += 2 {
		unit := runs[i]
		if runs[i+1] == unit && runs[i+2] == unit*3 && runs[i+3] == unit && runs[i+4] == unit {
			if runs[i-1] >= unit*4 && runs[i+5] >= unit {
				score += 40
			}
			if runs[i+5] >= unit*4 && runs[i-1] >= unit {
				score += 40
			}
		}
	}
	return score
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
