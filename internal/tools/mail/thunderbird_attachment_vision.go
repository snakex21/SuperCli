package mail

import (
	"io"
	"os"
)

// Documents keep only their portable local path. Read the signature before
// allocating an image payload, and keep one handle if the path is replaced.
func readThunderbirdVision(path string) *ImageContent {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return readThunderbirdVisionData(file, info.Size())
}

func readThunderbirdVisionData(reader io.Reader, size int64) *ImageContent {
	const signatureBytes = 12
	if size < signatureBytes || size > maxThunderbirdVisionBytes {
		return nil
	}
	var signature [signatureBytes]byte
	if _, err := io.ReadFull(reader, signature[:]); err != nil {
		return nil
	}
	mediaType := thunderbirdVisionMIME(signature[:])
	if mediaType == "" {
		return nil
	}
	// The extra byte detects growth beyond the vision cap. Size is a capacity
	// hint, not permission for an unbounded read if the file changes afterward.
	raw := make([]byte, signatureBytes, int(size)+1)
	copy(raw, signature[:])
	noProgress := 0
	for {
		if len(raw) == cap(raw) {
			next := min(int(maxThunderbirdVisionBytes)+1, max(512, 2*cap(raw)))
			if next <= cap(raw) {
				return nil
			}
			grown := make([]byte, len(raw), next)
			copy(grown, raw)
			raw = grown
		}
		n, err := reader.Read(raw[len(raw):cap(raw)])
		raw = raw[:len(raw)+n]
		if int64(len(raw)) > maxThunderbirdVisionBytes {
			return nil
		}
		if err == io.EOF {
			return &ImageContent{MediaType: mediaType, Data: raw}
		}
		if err != nil {
			return nil
		}
		if n == 0 {
			noProgress++
			if noProgress >= 100 {
				return nil
			}
		} else {
			noProgress = 0
		}
	}
}
