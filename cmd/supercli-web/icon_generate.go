//go:build ignore

// Generates the multi-resolution Windows icon used by supercli-web.exe.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	sizes := []int{16, 32, 48, 256}
	frames := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		var buf bytes.Buffer
		_ = png.Encode(&buf, drawIcon(size))
		frames = append(frames, buf.Bytes())
	}

	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(frames)))
	offset := 6 + 16*len(frames)
	for i, frame := range frames {
		sizeByte := byte(sizes[i])
		if sizes[i] == 256 {
			sizeByte = 0
		}
		out.WriteByte(sizeByte)
		out.WriteByte(sizeByte)
		out.WriteByte(0)
		out.WriteByte(0)
		_ = binary.Write(&out, binary.LittleEndian, uint16(1))
		_ = binary.Write(&out, binary.LittleEndian, uint16(32))
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(frame)))
		_ = binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(frame)
	}
	for _, frame := range frames {
		out.Write(frame)
	}
	if err := os.WriteFile("supercli.ico", out.Bytes(), 0o644); err != nil {
		panic(err)
	}
}

func drawIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{R: 20, G: 20, B: 24, A: 255}
	orange := color.RGBA{R: 255, G: 125, B: 42, A: 255}
	white := color.RGBA{R: 245, G: 245, B: 247, A: 255}
	radius := size / 5
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := min(x, size-1-x)
			dy := min(y, size-1-y)
			if dx >= radius || dy >= radius || (dx-radius)*(dx-radius)+(dy-radius)*(dy-radius) <= radius*radius {
				img.Set(x, y, bg)
			}
		}
	}

	stroke := max(1, size/12)
	drawLine(img, size*28/100, size*30/100, size*48/100, size/2, stroke, orange)
	drawLine(img, size*48/100, size/2, size*28/100, size*70/100, stroke, orange)
	fillRect(img, size*52/100, size*66/100, size*76/100, size*66/100+stroke, white)
	return img
}

func drawLine(img *image.RGBA, x0, y0, x1, y1, width int, c color.Color) {
	steps := max(abs(x1-x0), abs(y1-y0))
	if steps == 0 {
		steps = 1
	}
	for i := 0; i <= steps; i++ {
		x := x0 + (x1-x0)*i/steps
		y := y0 + (y1-y0)*i/steps
		fillRect(img, x-width/2, y-width/2, x+(width+1)/2, y+(width+1)/2, c)
	}
}

func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	for y := max(0, y0); y < min(img.Bounds().Dy(), y1); y++ {
		for x := max(0, x0); x < min(img.Bounds().Dx(), x1); x++ {
			img.Set(x, y, c)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
