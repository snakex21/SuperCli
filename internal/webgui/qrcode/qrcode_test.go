package qrcode

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestReedSolomonISOExample(t *testing.T) {
	data := []byte{32, 91, 11, 120, 209, 114, 220, 77, 67, 64, 236, 17, 236, 17, 236, 17}
	want := []byte{196, 35, 39, 119, 235, 215, 231, 226, 93, 23}
	got := rsRemainder(data, 10)
	if !bytes.Equal(got, want) {
		t.Fatalf("ISO version 1-M parity = %v, want %v", got, want)
	}
	if remainder := rsRemainder(append(data, got...), 10); !bytes.Equal(remainder, make([]byte, 10)) {
		t.Fatalf("complete RS block has nonzero remainder: %v", remainder)
	}
	if got := gfMultiply(0x80, 2); got != 0x1d {
		t.Fatalf("GF reduction = %#x, want 0x1d", got)
	}
}

func TestFormatBits(t *testing.T) {
	if got := formatBits(0); got != 0b101010000010010 {
		t.Fatalf("M mask 0 format = %015b, want 101010000010010", got)
	}
	want := []int{0x5412, 0x5125, 0x5e7c, 0x5b4b, 0x45f9, 0x40ce, 0x4f97, 0x4aa0}
	for mask, bits := range want {
		if got := formatBits(mask); got != bits {
			t.Errorf("M mask %d format = %015b, want %015b", mask, got, bits)
		}
	}
}

func TestVersionBits(t *testing.T) {
	if got := versionBits(7); got != 0b000111110010010100 {
		t.Fatalf("version 7 information = %018b, want 000111110010010100", got)
	}
	for i, want := range []int{0x07c94, 0x085bc, 0x09a99, 0x0a4d3} {
		if got := versionBits(i + 7); got != want {
			t.Errorf("version %d information = %018b, want %018b", i+7, got, want)
		}
	}
}

func TestVersionSelection(t *testing.T) {
	capacities := []int{14, 26, 42, 62, 84, 106, 122, 152, 180, 213}
	for i, capacity := range capacities {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			minimum := 0
			if i > 0 {
				minimum = capacities[i-1] + 1
			}
			for _, length := range []int{minimum, capacity} {
				c := mustEncode(t, strings.Repeat("a", length))
				if want := 17 + 4*(i+1); c.Size != want {
					t.Errorf("%d bytes: size = %d, want %d (version %d)", length, c.Size, want, i+1)
				}
			}
		})
	}
	for _, length := range []int{214, 1000} {
		if c, err := Encode(strings.Repeat("a", length)); err == nil || c != nil {
			t.Errorf("Encode(%d bytes) = (%v, %v), want nil and an error", length, c, err)
		}
	}
}

func TestUTF8ByteCapacity(t *testing.T) {
	for _, test := range []struct {
		text string
		size int
	}{
		{strings.Repeat("é", 7), 21},
		{strings.Repeat("é", 8), 25},
		{strings.Repeat("界", 71), 57},
		{"Hello, 世界 🌍", 25},
	} {
		if c := mustEncode(t, test.text); c.Size != test.size {
			t.Errorf("Encode(%q) size = %d, want %d", test.text, c.Size, test.size)
		}
	}
	if _, err := Encode(strings.Repeat("界", 72)); err == nil {
		t.Error("216 UTF-8 bytes should not fit")
	}
}

func TestByteModeData(t *testing.T) {
	for _, test := range []struct {
		text    string
		version int
	}{
		{"", 1},
		{"é", 1},
		{strings.Repeat("x", 14), 1},
		{strings.Repeat("x", 213), 10},
	} {
		data := encodeData(test.text, test.version)
		countBits := 8
		if test.version == 10 {
			countBits = 16
		}
		if readBits(data, 0, 4) != 4 || readBits(data, 4, countBits) != len(test.text) {
			t.Fatalf("invalid mode/count for %d bytes, version %d", len(test.text), test.version)
		}
		for i := 0; i < len(test.text); i++ {
			if got := readBits(data, 4+countBits+i*8, 8); got != int(test.text[i]) {
				t.Fatalf("byte %d = %#x, want %#x", i, got, test.text[i])
			}
		}
		end := 4 + countBits + len(test.text)*8
		aligned := min((end+4+7)/8*8, len(data)*8)
		if got := readBits(data, end, aligned-end); got != 0 {
			t.Errorf("terminator/alignment bits = %b, want zero", got)
		}
		for i := aligned / 8; i < len(data); i++ {
			want := byte(0xec)
			if (i-aligned/8)%2 != 0 {
				want = 0x11
			}
			if data[i] != want {
				t.Errorf("padding byte %d = %#x, want %#x", i, data[i], want)
			}
		}
	}
}

func TestBlockInterleavingAndRemainderModules(t *testing.T) {
	// Explicit independent block layouts for every supported M version.
	layouts := []struct {
		lengths   []int
		ec        int
		total     int
		remainder int
	}{
		{[]int{16}, 10, 26, 0},
		{[]int{28}, 16, 44, 7},
		{[]int{44}, 26, 70, 7},
		{[]int{32, 32}, 18, 100, 7},
		{[]int{43, 43}, 24, 134, 7},
		{[]int{27, 27, 27, 27}, 16, 172, 7},
		{[]int{31, 31, 31, 31}, 18, 196, 0},
		{[]int{38, 38, 39, 39}, 22, 242, 0},
		{[]int{36, 36, 36, 37, 37}, 22, 292, 0},
		{[]int{43, 43, 43, 43, 44}, 26, 346, 0},
	}
	for i, layout := range layouts {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			var data []byte
			var blocks [][]byte
			for _, length := range layout.lengths {
				block := make([]byte, length)
				for j := range block {
					block[j] = byte((len(data)+j)*149 + 61)
				}
				blocks = append(blocks, block)
				data = append(data, block...)
			}
			got := interleave(data, versions[i+1])
			if len(got) != layout.total {
				t.Fatalf("codewords = %d, want %d", len(got), layout.total)
			}
			index := 0
			for column := 0; column < slices.Max(layout.lengths); column++ {
				for _, block := range blocks {
					if column < len(block) {
						if got[index] != block[column] {
							t.Fatalf("interleaved byte %d = %d, want %d", index, got[index], block[column])
						}
						index++
					}
				}
			}
			for b, block := range blocks {
				complete := slices.Clone(block)
				for column := 0; column < layout.ec; column++ {
					complete = append(complete, got[index+column*len(blocks)+b])
				}
				if remainder := rsRemainder(complete, layout.ec); !bytes.Equal(remainder, make([]byte, layout.ec)) {
					t.Errorf("block %d has invalid parity: %v", b, remainder)
				}
			}

			m := newMatrix(i + 1)
			free := 0
			for _, function := range m.function {
				if !function {
					free++
				}
			}
			if want := layout.total*8 + layout.remainder; free != want {
				t.Fatalf("data/remainder modules = %d, want %d", free, want)
			}
			m.placeData(bytes.Repeat([]byte{0xff}, layout.total))
			darkData := 0
			for j, dark := range m.modules {
				if !m.function[j] && dark {
					darkData++
				}
			}
			if darkData != layout.total*8 {
				t.Errorf("placed %d dark bits, want %d (remainder bits must be light)", darkData, layout.total*8)
			}
		})
	}
}

func TestFinderPatterns(t *testing.T) {
	for _, length := range []int{0, 107, 181} {
		c := mustEncode(t, strings.Repeat("x", length))
		for _, origin := range [][2]int{{0, 0}, {c.Size - 7, 0}, {0, c.Size - 7}} {
			for y := 0; y < 7; y++ {
				for x := 0; x < 7; x++ {
					want := x == 0 || y == 0 || x == 6 || y == 6 || (x >= 2 && x <= 4 && y >= 2 && y <= 4)
					if c.Dark(origin[0]+x, origin[1]+y) != want {
						t.Fatalf("size %d: incorrect finder module (%d,%d)", c.Size, origin[0]+x, origin[1]+y)
					}
				}
			}
			for y := -1; y <= 7; y++ {
				for x := -1; x <= 7; x++ {
					if (x == -1 || x == 7 || y == -1 || y == 7) && c.Dark(origin[0]+x, origin[1]+y) {
						t.Fatalf("size %d: dark finder separator at (%d,%d)", c.Size, origin[0]+x, origin[1]+y)
					}
				}
			}
		}
		if !c.Dark(8, c.Size-8) {
			t.Errorf("size %d: fixed dark module is light", c.Size)
		}
		for _, point := range [][2]int{{-1, 0}, {0, -1}, {c.Size, 0}, {0, c.Size}} {
			if c.Dark(point[0], point[1]) {
				t.Errorf("out-of-bounds module %v is dark", point)
			}
		}
	}
}

func TestTimingAlignmentAndInformationPlacement(t *testing.T) {
	centers := [][]int{nil, {6, 18}, {6, 22}, {6, 26}, {6, 30}, {6, 34}, {6, 22, 38}, {6, 24, 42}, {6, 26, 46}, {6, 28, 50}}
	capacities := []int{14, 26, 42, 62, 84, 106, 122, 152, 180, 213}
	for v := 1; v <= 10; v++ {
		c := mustEncode(t, strings.Repeat("x", capacities[v-1]))
		for _, cy := range centers[v-1] {
			for _, cx := range centers[v-1] {
				if (cx == 6 && cy == 6) || (cx == 6 && cy == c.Size-7) || (cx == c.Size-7 && cy == 6) {
					continue
				}
				for y := -2; y <= 2; y++ {
					for x := -2; x <= 2; x++ {
						want := x == -2 || x == 2 || y == -2 || y == 2 || (x == 0 && y == 0)
						if c.Dark(cx+x, cy+y) != want {
							t.Fatalf("version %d: incorrect alignment at (%d,%d)", v, cx+x, cy+y)
						}
					}
				}
			}
		}
		for i := 8; i < c.Size-8; i++ {
			// The alignment patterns crossing timing lines have even centers,
			// so their middle rows/columns preserve the timing alternation.
			if c.Dark(i, 6) != (i%2 == 0) || c.Dark(6, i) != (i%2 == 0) {
				t.Errorf("version %d: incorrect timing module %d", v, i)
			}
		}
		var first, second int
		positions := [][2]int{{8, 0}, {8, 1}, {8, 2}, {8, 3}, {8, 4}, {8, 5}, {8, 7}, {8, 8}, {7, 8}, {5, 8}, {4, 8}, {3, 8}, {2, 8}, {1, 8}, {0, 8}}
		for i, p := range positions {
			if c.Dark(p[0], p[1]) {
				first |= 1 << i
			}
			x, y := c.Size-1-i, 8
			if i >= 8 {
				x, y = 8, c.Size-15+i
			}
			if c.Dark(x, y) {
				second |= 1 << i
			}
		}
		mask := (first ^ 0x5412) >> 10
		if mask > 7 || first != formatBits(mask) || second != first {
			t.Errorf("version %d: invalid M format copies %015b, %015b", v, first, second)
		}
		if v >= 7 {
			var topRight, bottomLeft int
			for row := 0; row < 6; row++ {
				for column := 0; column < 3; column++ {
					bit := 1 << (row*3 + column)
					if c.Dark(c.Size-11+column, row) {
						topRight |= bit
					}
					if c.Dark(row, c.Size-11+column) {
						bottomLeft |= bit
					}
				}
			}
			if topRight != versionBits(v) || bottomLeft != topRight {
				t.Errorf("version %d: incorrect version information copies", v)
			}
		}
	}
}

func TestMasks(t *testing.T) {
	want := []string{
		"101010/010101/101010/010101/101010/010101",
		"111111/000000/111111/000000/111111/000000",
		"100100/100100/100100/100100/100100/100100",
		"100100/001001/010010/100100/001001/010010",
		"111000/111000/000111/000111/111000/111000",
		"111111/100000/100100/101010/100100/100000",
		"111111/111000/110110/101010/101101/100011",
		"101010/000111/100011/010101/111000/011100",
	}
	for mask, pattern := range want {
		for y, row := range strings.Split(pattern, "/") {
			for x, bit := range row {
				if maskBit(mask, x, y) != (bit == '1') {
					t.Errorf("mask %d at (%d,%d) is incorrect", mask, x, y)
				}
			}
		}
		m := newMatrix(10)
		before := slices.Clone(m.modules)
		m.applyMask(mask)
		for i, function := range m.function {
			if function && m.modules[i] != before[i] {
				t.Fatalf("mask %d changed function module %d", mask, i)
			}
		}
		m.applyMask(mask)
		if !slices.Equal(m.modules, before) {
			t.Errorf("mask %d is not reversible", mask)
		}
	}
}

func TestPenaltyRules(t *testing.T) {
	for _, test := range []struct {
		line string
		want int
	}{
		{"10101", 0},
		{"00000", 3},
		{"111111", 4},
		{"1011101", 80}, // Both sides abut the light quiet zone.
		{"101110101", 40},
		{"10101110101", 0},     // Insufficient light on either side.
		{"11001111110011", 84}, // Scaled finder-like pattern and a run of six.
	} {
		line := make([]bool, len(test.line))
		for i, bit := range test.line {
			line[i] = bit == '1'
		}
		if got := linePenalty(line); got != test.want {
			t.Errorf("linePenalty(%s) = %d, want %d", test.line, got, test.want)
		}
	}
	for _, test := range []struct {
		modules []bool
		want    int
	}{
		{[]bool{true, true, true, true}, 103}, // N2 = 3, N4 = 100.
		{[]bool{false, false, false, false}, 103},
		{[]bool{true, false, false, true}, 0},
		{[]bool{true, false, false, false}, 50}, // 25% dark: five 5% steps.
	} {
		c := &Code{Size: 2, modules: test.modules}
		if got := c.penalty(); got != test.want {
			t.Errorf("penalty(%v) = %d, want %d", test.modules, got, test.want)
		}
	}
}

func TestLowestPenaltyMask(t *testing.T) {
	for _, length := range []int{0, 14, 107, 213} {
		text := strings.Repeat("x", length)
		got := mustEncode(t, text)
		version := (got.Size - 17) / 4
		m := newMatrix(version)
		m.placeData(interleave(encodeData(text, version), versions[version]))
		bestScore := int(^uint(0) >> 1)
		var best []bool
		for mask := 0; mask < 8; mask++ {
			m.applyMask(mask)
			m.drawFormat(mask)
			if score := m.penalty(); score < bestScore {
				bestScore = score
				best = slices.Clone(m.modules)
			}
			m.applyMask(mask)
		}
		if !slices.Equal(got.modules, best) {
			t.Errorf("%d bytes: selected mask is not the first minimum-penalty mask", length)
		}
	}
}

func TestSVG(t *testing.T) {
	text := "<&世界>"
	svg, err := SVG(text, 4)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		XMLName xml.Name
		Width   int    `xml:"width,attr"`
		Height  int    `xml:"height,attr"`
		ViewBox string `xml:"viewBox,attr"`
		Rects   []struct {
			Width  string `xml:"width,attr"`
			Height string `xml:"height,attr"`
			Fill   string `xml:"fill,attr"`
		} `xml:"rect"`
		Paths []struct {
			Fill string `xml:"fill,attr"`
			D    string `xml:"d,attr"`
		} `xml:"path"`
	}
	if err := xml.Unmarshal([]byte(svg), &doc); err != nil {
		t.Fatalf("invalid SVG: %v", err)
	}
	c := mustEncode(t, text)
	size := c.Size + 8
	if doc.XMLName.Local != "svg" || doc.XMLName.Space != "http://www.w3.org/2000/svg" || doc.Width != size*4 || doc.Height != size*4 || doc.ViewBox != fmt.Sprintf("0 0 %d %d", size, size) {
		t.Errorf("incorrect SVG root/dimensions: %+v", doc)
	}
	if len(doc.Rects) != 1 || doc.Rects[0].Width != "100%" || doc.Rects[0].Height != "100%" || doc.Rects[0].Fill != "#fff" {
		t.Fatalf("incorrect white background: %+v", doc.Rects)
	}
	if len(doc.Paths) != 1 || doc.Paths[0].Fill != "#000" {
		t.Fatalf("want exactly one dark path: %+v", doc.Paths)
	}
	seen := make(map[[2]int]bool)
	for _, command := range strings.Split(strings.TrimSuffix(doc.Paths[0].D, "z"), "z") {
		var x, y int
		if n, err := fmt.Sscanf(command, "M%d %dh1v1h-1", &x, &y); n != 2 || err != nil {
			t.Fatalf("invalid dark-square command %q: %v", command, err)
		}
		if x < 4 || y < 4 || x >= size-4 || y >= size-4 || !c.Dark(x-4, y-4) || seen[[2]int{x, y}] {
			t.Fatalf("incorrect/repeated dark module at (%d,%d)", x, y)
		}
		seen[[2]int{x, y}] = true
	}
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Dark(x, y) != seen[[2]int{x + 4, y + 4}] {
				t.Fatalf("SVG disagrees with encoded module (%d,%d)", x, y)
			}
		}
	}
	for _, moduleSize := range []int{0, -1, int(^uint(0) >> 1)} {
		if svg, err := SVG("", moduleSize); err == nil || svg != "" {
			t.Errorf("SVG with module size %d should return an error", moduleSize)
		}
	}
	if svg, err := SVG(strings.Repeat("x", 214), 1); err == nil || svg != "" {
		t.Error("SVG should propagate oversized text errors")
	}
}

func mustEncode(t *testing.T, text string) *Code {
	t.Helper()
	c, err := Encode(text)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readBits(data []byte, start, count int) int {
	value := 0
	for i := start; i < start+count; i++ {
		value = value<<1 | int(data[i/8]>>(7-i%8)&1)
	}
	return value
}
