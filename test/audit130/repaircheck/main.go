//go:build audit130

// Command repaircheck measures, for the degenerate argument stream the
// audit130 mock emits, (1) whether the agent's history copy
// (historySafeToolArguments -> RepairToolArguments) keeps the garbage when the
// stream is cut at an arbitrary byte, and (2) simple repetition statistics a
// runaway guard could use (compression ratio and the shortest repeating period
// of the trailing window).
//
// Build/run: go run -buildvcs=false -tags audit130 ./test/audit130/repaircheck
package main

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/agent"
)

const (
	repeatPrefix = `{"tool":"edit_files","arg.include":"**/*.go","arg.path":"main.go","arg.old":"Foo(","arg.new":"Bar(","`
	repeatA      = "} \uFFFC RENDERING_ARGS####json 1/1 (1)####{\"\t\t:\t\" } HIDDEN 1/1 (1)####.json 1/1 (1)####\\n\\nchannel: commentary \uFFFC to=functions.invoke_tool \uFFFCcommentary \uFFFCjson\\n{\"\t\t,\t\" "
	repeatB      = "} \uFFFC RENDERING_ARGS####json 1/1 (1)####{\"\t\t:\t\" } \uFFFC RENDERING_ARGS####json 1/1 (1)####{\"\t\t,\t\" "
)

func stream(n int) string {
	var b strings.Builder
	b.WriteString(repeatPrefix)
	for i := 0; b.Len() < n; i++ {
		if i%3 == 2 {
			b.WriteString(repeatB)
		} else {
			b.WriteString(repeatA)
		}
	}
	return b.String()[:n]
}

func ratio(s string) float64 {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestSpeed)
	w.Write([]byte(s))
	w.Close()
	return float64(len(s)) / float64(buf.Len())
}

// period returns the smallest p such that tail[i]==tail[i+p] for all i.
func period(tail string, maxP int) int {
	for p := 1; p <= maxP && p < len(tail); p++ {
		ok := true
		for i := 0; i+p < len(tail); i++ {
			if tail[i] != tail[i+p] {
				ok = false
				break
			}
		}
		if ok {
			return p
		}
	}
	return 0
}

// realisticJSON is a legitimate large edit argument (Go source in a JSON string).
func realisticJSON(n int) string {
	src := "func (s *Server) handle%d(w http.ResponseWriter, r *http.Request) {\n\tif err := s.check(r); err != nil {\n\t\thttp.Error(w, err.Error(), http.StatusBadRequest)\n\t\treturn\n\t}\n\tfmt.Fprintf(w, \"ok %%d\", %d)\n}\n\n"
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		b.WriteString(fmt.Sprintf(src, i, i*7))
	}
	enc, _ := json.Marshal(map[string]string{"path": "server.go", "content": b.String()})
	return string(enc)
}

// historySafe mirrors internal/agent/tool_repair.go historySafeToolArguments
// (unexported) using the exported RepairToolArguments.
func historySafe(raw string) string {
	args := strings.TrimSpace(raw)
	if args == "" {
		return "{}"
	}
	if json.Valid([]byte(args)) {
		return args
	}
	if fixed, ok := agent.RepairToolArguments(args); ok {
		return fixed
	}
	return "{}"
}

// distinctShingles counts distinct k-byte substrings in s.
func distinctShingles(s string, k int) int {
	seen := map[string]struct{}{}
	for i := 0; i+k <= len(s); i++ {
		seen[s[i:i+k]] = struct{}{}
	}
	return len(seen)
}

// repoSourceJSON JSON-encodes real Go files from the repo as one write_file
// style argument: a legitimate large argument.
func repoSourceJSON(n int) string {
	var b strings.Builder
	files, _ := filepath.Glob("internal/agent/*.go")
	for _, f := range files {
		if b.Len() >= n {
			break
		}
		data, _ := os.ReadFile(f)
		b.Write(data)
	}
	enc, _ := json.Marshal(map[string]string{"path": "x.go", "content": b.String()})
	return string(enc)
}

func main() {
	fmt.Println("== history repair of a cut degenerate stream (historySafeToolArguments)")
	kept, total := 0, 0
	for _, n := range []int{4096, 8192, 16384, 65536, 131072, 262144, 512000, 512002} {
		for off := 0; off < 170; off += 17 {
			s := stream(n + off)
			total++
			out := historySafe(s)
			if len(out) > 2 {
				kept++
			}
			if off == 0 {
				fmt.Printf("cut=%7d history_len=%7d\n", n+off, len(out))
			}
		}
	}
	fmt.Printf("garbage kept in history for %d/%d cut points\n", kept, total)

	fmt.Println("== repetition statistics of the trailing window")
	s := stream(600000)
	for _, w := range []int{1024, 2048, 4096, 8192} {
		tail := s[len(s)-w:]
		fmt.Printf("degenerate window=%5d deflate_ratio=%.1f period=%d\n", w, ratio(tail), period(tail, 2048))
	}
	r := realisticJSON(600000)
	for _, w := range []int{1024, 2048, 4096, 8192} {
		tail := r[len(r)-w:]
		fmt.Printf("realistic  window=%5d deflate_ratio=%.1f period=%d\n", w, ratio(tail), period(tail, 2048))
	}
	src := repoSourceJSON(700000)
	for _, w := range []int{4096, 8192, 16384} {
		tail := src[len(src)-w:]
		fmt.Printf("repo-src   window=%5d deflate_ratio=%.1f period=%d distinct16=%d\n", w, ratio(tail), period(tail, 2048), distinctShingles(tail, 16))
		dt := s[len(s)-w:]
		fmt.Printf("degenerate window=%5d distinct16=%d\n", w, distinctShingles(dt, 16))
		rt := r[len(r)-w:]
		fmt.Printf("templated  window=%5d distinct16=%d\n", w, distinctShingles(rt, 16))
	}
	fmt.Printf("repo-src full 512KB deflate ratio=%.1f\n", ratio(src[:512000]))
	fmt.Printf("realistic full 512KB deflate ratio=%.1f\n", ratio(r[:512000]))
	fmt.Printf("degenerate full 512KB deflate ratio=%.1f\n", ratio(s[:512000]))
	fmt.Printf("block A len=%d B len=%d cycle(A,A,B)=%d bytes\n", len(repeatA), len(repeatB), 2*len(repeatA)+len(repeatB))
}
