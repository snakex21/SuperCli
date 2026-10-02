package headless

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

const webdriverScreenshotBytes = 16 << 20

// decodeWebdriverScreenshot avoids a second full base64 string allocation for
// the usual unescaped JSON value. Escaped values still use the standard JSON
// decoder; both paths allocate only a bounded decoded destination.
func decodeWebdriverScreenshot(value json.RawMessage) ([]byte, error) {
	quoted := bytes.TrimSpace(value)
	if len(quoted) >= 2 && quoted[0] == '"' && quoted[len(quoted)-1] == '"' && bytes.IndexByte(quoted[1:len(quoted)-1], '\\') < 0 && json.Valid(value) {
		return decodeWebdriverScreenshotBytes(quoted[1 : len(quoted)-1])
	}
	var encoded string
	if err := json.Unmarshal(value, &encoded); err != nil {
		return nil, errors.New("webdriver screenshot is not a base64 string")
	}
	return decodeWebdriverScreenshotBytes([]byte(encoded))
}

func decodeWebdriverScreenshotBytes(encoded []byte) ([]byte, error) {
	length := len(encoded)
	// Standard base64 accepts CR/LF. JSON can contain them only as escapes, so
	// the common raw-string path does not need to copy or remove newlines.
	if bytes.IndexByte(encoded, '\r') >= 0 || bytes.IndexByte(encoded, '\n') >= 0 {
		length -= bytes.Count(encoded, []byte{'\r'}) + bytes.Count(encoded, []byte{'\n'})
	}
	decodedLength := base64.StdEncoding.DecodedLen(length)
	if length > 0 && length%4 == 0 {
		last := len(encoded) - 1
		for last >= 0 && (encoded[last] == '\r' || encoded[last] == '\n') {
			last--
		}
		if last >= 0 && encoded[last] == '=' {
			decodedLength--
			last--
			for last >= 0 && (encoded[last] == '\r' || encoded[last] == '\n') {
				last--
			}
			if last >= 0 && encoded[last] == '=' {
				decodedLength--
			}
		}
	}
	if decodedLength > webdriverScreenshotBytes {
		return nil, errors.New("webdriver screenshot exceeds 16 MiB")
	}
	data := make([]byte, decodedLength)
	n, err := base64.StdEncoding.Decode(data, encoded)
	if err != nil {
		return nil, fmt.Errorf("webdriver screenshot base64: %w", err)
	}
	return data[:n], nil
}
