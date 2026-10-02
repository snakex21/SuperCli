package ctxexec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestEncodedPreviewParity(t *testing.T) {
	units := []string{"plain", "\n\r\t\b\f\x00\x01", "\\\"<>&", "é漢😀\u2028\u2029", string([]byte{0xff, 0xc0, 0x80})}
	rng := rand.New(rand.NewSource(5))
	for n := 0; n < 450; n++ {
		var a, b strings.Builder
		for i := 0; i < rng.Intn(1400); i++ {
			a.WriteString(units[rng.Intn(len(units))])
		}
		for i := 0; i < rng.Intn(450); i++ {
			b.WriteString(units[rng.Intn(len(units))])
		}
		r := &Result{Stdout: a.String(), Stderr: b.String(), DurationMS: 17, Command: "no command executed", Workdir: "fixture", OutputWarning: strings.Repeat("diagnostic ", n%5), OutputIncomplete: n%2 == 0, TruncatedStdout: n%3 == 0, TruncatedStderr: n%7 == 0}
		raw, e := json.Marshal(r)
		if e != nil {
			t.Fatal(e)
		}
		before, after := r.SuccessPreview(), r.SuccessPreviewFromJSON(raw)
		if before != after {
			t.Fatalf("parity case %d beforebytes=%d afterbytes=%d", n, len(before), len(after))
		}
		if len(after) > core.ModelOutputPreviewBytes || !json.Valid([]byte(after)) {
			t.Fatal("invalid bounded preview")
		}
	}
}
func TestEncodedPreviewFallbackAndOwnership(t *testing.T) {
	r := &Result{Stdout: strings.Repeat("ordinary\n", 1000), Stderr: "warning"}
	raw, _ := json.Marshal(r)
	original := append([]byte(nil), raw...)
	if r.SuccessPreviewFromJSON(raw) != r.SuccessPreview() {
		t.Fatal("mismatch")
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("mutated owned source")
	}
	for _, bad := range [][]byte{nil, []byte("{}"), []byte(`{"stdout":null,"stderr":"","exit_code":0}`), []byte(`{"stdout":"unfinished`), []byte(`{"stdout":"ok","other":"","exit_code":0}`)} {
		if r.SuccessPreviewFromJSON(bad) != r.SuccessPreview() {
			t.Fatal("guard fallback changed")
		}
	}
	r.ExitCode = 1
	if r.SuccessPreviewFromJSON(raw) != "" {
		t.Fatal("failure bypass")
	}
}

var encodedPreviewBenchmarkSink string

func BenchmarkAlreadyEncodedPreview(b *testing.B) {
	for _, n := range []int{4096, 16384, 65536} {
		r := &Result{Stdout: strings.Repeat("quoted=\"<unit>\"\n", n/16), Stderr: strings.Repeat("warning\n", n/32), Command: "fixture", Workdir: "workspace"}
		raw, _ := json.Marshal(r)
		for _, candidate := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes=%d/candidate=%t", len(raw), candidate), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if candidate {
						encodedPreviewBenchmarkSink = r.SuccessPreviewFromJSON(raw)
					} else {
						encodedPreviewBenchmarkSink = r.SuccessPreview()
					}
				}
			})
		}
	}
}
func BenchmarkEndToEndPreview(b *testing.B) {
	for _, n := range []int{4096, 16384, 65536} {
		r := &Result{Stdout: strings.Repeat("quoted=\"<unit>\"\n", n/16), Stderr: strings.Repeat("warning\n", n/32), Command: "fixture", Workdir: "workspace"}
		for _, candidate := range []bool{false, true} {
			b.Run(fmt.Sprintf("input=%d/candidate=%t", n, candidate), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					raw, _ := json.Marshal(r)
					public := string(raw)
					if candidate {
						encodedPreviewBenchmarkSink = r.SuccessPreviewFromJSON(raw)
					} else {
						encodedPreviewBenchmarkSink = r.SuccessPreview()
					}
					if public == "" {
						b.Fatal("empty")
					}
				}
			})
		}
	}
}
