package desktopfiles

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func dibFixture(height int32, bpp uint16) []byte {
	stride := (int(bpp) + 31) / 32 * 4
	data := make([]byte, 40+2*stride)
	binary.LittleEndian.PutUint32(data, 40)
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[8:], uint32(height))
	binary.LittleEndian.PutUint16(data[12:], 1)
	binary.LittleEndian.PutUint16(data[14:], bpp)
	// Storage row zero blue, storage row one red.
	data[40] = 255
	data[40+stride+2] = 255
	return data
}
func TestClipboardDIBPixelsAndOrientation(t *testing.T) {
	for _, bpp := range []uint16{24, 32} {
		for _, height := range []int32{2, -2} {
			raw, err := decodeClipboardDIB(dibFixture(height, bpp))
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			want := color.NRGBA{R: 255, A: 255}
			if height < 0 {
				want = color.NRGBA{B: 255, A: 255}
			}
			if got := color.NRGBAModel.Convert(img.At(0, 0)); got != want {
				t.Fatalf("bpp=%d height=%d got=%v want=%v", bpp, height, got, want)
			}
		}
	}
}
func TestClipboardDIBRejectsInvalidInputAndMasks(t *testing.T) {
	valid := dibFixture(2, 32)
	for _, data := range [][]byte{nil, valid[:39], valid[:41]} {
		if _, err := decodeClipboardDIB(data); err == nil {
			t.Fatal("accepted truncated bitmap")
		}
	}
	bad := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(bad[4:], 0x7fffffff)
	if _, err := decodeClipboardDIB(bad); err == nil {
		t.Fatal("accepted huge dimensions")
	}
	data := make([]byte, 124+4)
	copy(data, valid[:40])
	binary.LittleEndian.PutUint32(data, 124)
	binary.LittleEndian.PutUint32(data[8:], 1)
	binary.LittleEndian.PutUint32(data[16:], 3)
	for i, mask := range []uint32{0xff0000, 0xff00, 0xff, 0xff000000} {
		binary.LittleEndian.PutUint32(data[40+4*i:], mask)
	}
	copy(data[124:], []byte{0, 0, 255, 128})
	raw, err := decodeClipboardDIB(data)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(bytes.NewReader(raw))
	if got := color.NRGBAModel.Convert(img.At(0, 0)); got != (color.NRGBA{R: 255, A: 128}) {
		t.Fatalf("alpha/color: %v", got)
	}
	binary.LittleEndian.PutUint32(data[44:], 0xff0000)
	if _, err := decodeClipboardDIB(data); err == nil {
		t.Fatal("accepted overlapping masks")
	}
}
func TestClipboardImagePortableAndDeduplicated(t *testing.T) {
	dir := t.TempDir()
	raw, err := decodeClipboardDIB(dibFixture(2, 24))
	if err != nil {
		t.Fatal(err)
	}
	first, err := SaveClipboardPNG(dir, raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SaveClipboardPNG(dir, raw)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || filepath.Dir(first) != filepath.Join(dir, "clipboard") {
		t.Fatal("not portable/deduplicated")
	}
	got, err := os.ReadFile(first)
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatal("image changed")
	}
	if _, err := SaveClipboardPNG("", raw); err == nil {
		t.Fatal("used implicit system directory")
	}
}
