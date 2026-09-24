package desktopfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"math/bits"
	"os"
	"path/filepath"
)

const maxClipboardBytes = 128 << 20
const maxClipboardPixels = 32 << 20

// decodeClipboardDIB supports the uncompressed 24/32-bit DIBs produced by
// Windows screenshots. Bounds are checked before allocation or pixel access.
func decodeClipboardDIB(data []byte) ([]byte, error) {
	if len(data) < 40 {
		return nil, fmt.Errorf("truncated clipboard bitmap")
	}
	u32 := func(i int) uint32 { return binary.LittleEndian.Uint32(data[i:]) }
	header := int(u32(0))
	if (header != 40 && header != 108 && header != 124) || header > len(data) {
		return nil, fmt.Errorf("unsupported clipboard bitmap header")
	}
	width, height := int64(int32(u32(4))), int64(int32(u32(8)))
	topDown := height < 0
	if topDown {
		height = -height
	}
	if width <= 0 || height <= 0 || width > 32768 || height > 32768 || width*height > maxClipboardPixels {
		return nil, fmt.Errorf("clipboard image dimensions are too large")
	}
	bpp := int(binary.LittleEndian.Uint16(data[14:]))
	compression := u32(16)
	if binary.LittleEndian.Uint16(data[12:]) != 1 || (bpp != 24 && bpp != 32) || (compression != 0 && compression != 3) || (compression == 3 && bpp != 32) {
		return nil, fmt.Errorf("unsupported clipboard bitmap; save it and use Ctrl+O")
	}
	if header == 124 && u32(116) != 0 {
		return nil, fmt.Errorf("clipboard color profile is unsupported")
	}
	offset := int64(header) + int64(u32(32))*4
	masks := [4]uint32{0x00ff0000, 0x0000ff00, 0x000000ff, 0}
	if compression == 3 {
		if len(data) < 52 {
			return nil, fmt.Errorf("truncated clipboard color masks")
		}
		for i := 0; i < 3; i++ {
			masks[i] = u32(40 + 4*i)
		}
		if header == 40 {
			offset += 12
		} else {
			masks[3] = u32(52)
		}
		var used uint32
		for i, mask := range masks {
			if mask == 0 {
				if i < 3 {
					return nil, fmt.Errorf("invalid clipboard color mask")
				}
				continue
			}
			shifted := mask >> bits.TrailingZeros32(mask)
			if used&mask != 0 || shifted&(shifted+1) != 0 {
				return nil, fmt.Errorf("invalid clipboard color mask")
			}
			used |= mask
		}
	}
	stride := ((width*int64(bpp) + 31) / 32) * 4
	if offset > int64(len(data)) || stride*height > int64(len(data))-offset {
		return nil, fmt.Errorf("truncated clipboard pixels")
	}
	img := image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
	channel := func(value, mask uint32) byte {
		if mask == 0 {
			return 255
		}
		shift := bits.TrailingZeros32(mask)
		return byte(uint64((value&mask)>>shift) * 255 / uint64(mask>>shift))
	}
	for y := int64(0); y < height; y++ {
		srcY := y
		if !topDown {
			srcY = height - 1 - y
		}
		row := data[offset+srcY*stride:]
		for x := int64(0); x < width; x++ {
			src := x * int64(bpp/8)
			dst := int(y)*img.Stride + int(x)*4
			if bpp == 24 || compression == 0 {
				img.Pix[dst], img.Pix[dst+1], img.Pix[dst+2], img.Pix[dst+3] = row[src+2], row[src+1], row[src], 255
			} else {
				value := binary.LittleEndian.Uint32(row[src:])
				for c, mask := range masks {
					img.Pix[dst+c] = channel(value, mask)
				}
			}
		}
	}
	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func validateClipboardPNG(data []byte) error {
	if len(data) > 32<<20 {
		return fmt.Errorf("clipboard PNG exceeds 32 MiB")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxClipboardPixels {
		return fmt.Errorf("clipboard image dimensions are too large")
	}
	return nil
}

// SaveClipboardPNG writes only into the application's supplied portable data
// directory. Identical pastes reuse one file; no temporary OS/profile directory.
func SaveClipboardPNG(dataDir string, data []byte) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("portable data directory unavailable")
	}
	if err := validateClipboardPNG(data); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	dir := filepath.Join(dataDir, "clipboard")
	path := filepath.Join(dir, "screenshot-"+hex.EncodeToString(sum[:16])+".png")
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(data)) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".image-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}
