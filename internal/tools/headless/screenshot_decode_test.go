package headless

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestWebdriverScreenshotDecodePreservesJSONAndBase64Behavior(t *testing.T) {
	for _, test := range []struct {
		name      string
		value     string
		want      []byte
		errPrefix string
	}{
		{"plain", `"TWFu"`, []byte("Man"), ""},
		{"one padding", `"TWE="`, []byte("Ma"), ""},
		{"two padding", `"TQ=="`, []byte("M"), ""},
		{"JSON whitespace", " \t\n\"TQ==\"\r ", []byte("M"), ""},
		{"escaped slash", `"\/w=="`, []byte{255}, ""},
		{"unicode slash and padding", `"\u002fw\u003d\u003d"`, []byte{255}, ""},
		{"escaped newlines", `"T\rW\nFu\r\n"`, []byte("Man"), ""},
		{"newline split padding", `"TQ=\n=\r\n"`, []byte("M"), ""},
		{"empty", `""`, nil, ""},
		{"null legacy", "null", nil, ""},
		{"missing padding", `"TQ"`, nil, "webdriver screenshot base64:"},
		{"extra padding", `"TQ==="`, nil, "webdriver screenshot base64:"},
		{"padding in middle", `"TW=u"`, nil, "webdriver screenshot base64:"},
		{"invalid bytes", `"!!!!"`, nil, "webdriver screenshot base64:"},
		{"non-ASCII", `"TQ==é"`, nil, "webdriver screenshot base64:"},
		{"number", "123", nil, "webdriver screenshot is not a base64 string"},
		{"object", `{"base64":"TQ=="}`, nil, "webdriver screenshot is not a base64 string"},
		{"unescaped control", "\"TW\nFu\"", nil, "webdriver screenshot is not a base64 string"},
		{"trailing value", `"TQ==" true`, nil, "webdriver screenshot is not a base64 string"},
		{"bad escape", `"TQ\x3d="`, nil, "webdriver screenshot is not a base64 string"},
		{"invalid JSON whitespace", "\v\"TQ==\"", nil, "webdriver screenshot is not a base64 string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := []byte(test.value)
			before := bytes.Clone(value)
			got, err := decodeWebdriverScreenshot(value)
			if test.errPrefix != "" {
				if err == nil || !strings.HasPrefix(err.Error(), test.errPrefix) {
					t.Fatalf("error=%v, want prefix %q", err, test.errPrefix)
				}
			} else if err != nil || !bytes.Equal(got, test.want) || cap(got) != len(got) {
				t.Fatalf("decoded=%x cap=%d error=%v, want %x", got, cap(got), err, test.want)
			}
			if !bytes.Equal(value, before) {
				t.Fatal("raw protocol value was mutated")
			}
		})
	}
}

func TestWebdriverScreenshotDecodeMatchesStandardDecoderOnMalformedInput(t *testing.T) {
	rng := rand.New(rand.NewSource(31415))
	alphabet := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=!? \r\n")
	for i := 0; i < 2000; i++ {
		encoded := make([]byte, rng.Intn(80))
		for j := range encoded {
			encoded[j] = alphabet[rng.Intn(len(alphabet))]
		}
		value, _ := json.Marshal(string(encoded))
		want, stdErr := base64.StdEncoding.DecodeString(string(encoded))
		got, err := decodeWebdriverScreenshot(value)
		if (err == nil) != (stdErr == nil) || (err == nil && !bytes.Equal(got, want)) {
			t.Fatalf("input=%q decoded=%x err=%v standard=%x/%v", encoded, got, err, want, stdErr)
		}
	}
}

func TestWebdriverScreenshotDecodeUsesExactBoundAndRejectsLarger(t *testing.T) {
	encodedLength := base64.StdEncoding.EncodedLen(webdriverScreenshotBytes)
	value := make([]byte, encodedLength+2)
	value[0], value[len(value)-1] = '"', '"'
	for i := 1; i < len(value)-1; i++ {
		value[i] = 'A'
	}
	// 16 MiB is 1 modulo 3: the exact boundary needs two padding bytes.
	value[len(value)-3], value[len(value)-2] = '=', '='
	got, err := decodeWebdriverScreenshot(value)
	if err != nil || len(got) != webdriverScreenshotBytes || cap(got) != webdriverScreenshotBytes {
		t.Fatalf("exact bound: len=%d cap=%d err=%v", len(got), cap(got), err)
	}
	value[len(value)-3] = 'A' // same encoded length, one extra decoded byte
	if got, err := decodeWebdriverScreenshot(value); got != nil || err == nil || err.Error() != "webdriver screenshot exceeds 16 MiB" {
		t.Fatalf("oversized payload not rejected before decode: len=%d err=%v", len(got), err)
	}
}

func BenchmarkWebdriverScreenshotDecode(b *testing.B) {
	pixels := bytes.Repeat([]byte{137, 80, 78, 71, 13, 10, 26, 10}, 4<<20/8)
	value, _ := json.Marshal(base64.StdEncoding.EncodeToString(pixels))
	for _, variant := range []string{"raw_json", "string_baseline"} {
		b.Run(variant, func(b *testing.B) {
			b.SetBytes(int64(len(pixels)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var decoded []byte
				var err error
				if variant == "raw_json" {
					decoded, err = decodeWebdriverScreenshot(value)
				} else {
					var encoded string
					if err = json.Unmarshal(value, &encoded); err == nil {
						decoded, err = base64.StdEncoding.DecodeString(encoded)
					}
				}
				if err != nil || len(decoded) != len(pixels) || decoded[len(decoded)-1] != pixels[len(pixels)-1] {
					b.Fatal(fmt.Sprintf("bad decode: len=%d err=%v", len(decoded), err))
				}
			}
		})
	}
}
