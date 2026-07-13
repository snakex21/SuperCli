package webgui

// taskbarBadgePixels renders the tiny 16x16 overlay used by the native
// Windows taskbar button. It deliberately has no platform dependencies so the
// visual contract can be tested on every OS. Pixels are returned as BGRA.
func taskbarBadgePixels(count int) []byte {
	const size = 16
	pixels := make([]byte, size*size*4)
	label := taskbarBadgeLabel(count)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-7.5, float64(y)-7.5
			d2 := dx*dx + dy*dy
			if d2 > 56.25 { // radius 7.5
				continue
			}
			r, g, b := byte(232), byte(118), byte(61) // SuperCli accent
			if d2 > 43.56 {                           // restrained dark rim
				r, g, b = 55, 35, 29
			}
			setBadgePixel(pixels, size, x, y, r, g, b, 255)
		}
	}

	patterns := map[byte][]string{
		'0': {"111", "101", "101", "101", "111"},
		'1': {"010", "110", "010", "010", "111"},
		'2': {"111", "001", "111", "100", "111"},
		'3': {"111", "001", "111", "001", "111"},
		'4': {"101", "101", "111", "001", "001"},
		'5': {"111", "100", "111", "001", "111"},
		'6': {"111", "100", "111", "101", "111"},
		'7': {"111", "001", "010", "010", "010"},
		'8': {"111", "101", "111", "101", "111"},
		'9': {"111", "101", "111", "001", "111"},
		'+': {"000", "010", "111", "010", "000"},
	}
	width := len(label)*3 + maxInt(len(label)-1, 0)
	startX := (size - width) / 2
	startY := (size - 5) / 2
	for i := 0; i < len(label); i++ {
		glyph := patterns[label[i]]
		for y, row := range glyph {
			for x := 0; x < len(row); x++ {
				if row[x] == '1' {
					setBadgePixel(pixels, size, startX+i*4+x, startY+y, 255, 255, 255, 255)
				}
			}
		}
	}
	return pixels
}

func taskbarBadgeLabel(count int) string {
	if count <= 0 {
		return ""
	}
	if count > 9 {
		return "9+"
	}
	return string(rune('0' + count))
}

func setBadgePixel(pixels []byte, size, x, y int, r, g, b, a byte) {
	if x < 0 || y < 0 || x >= size || y >= size {
		return
	}
	offset := (y*size + x) * 4
	pixels[offset], pixels[offset+1], pixels[offset+2], pixels[offset+3] = b, g, r, a
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
