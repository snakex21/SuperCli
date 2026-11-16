package llm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// streamArgs feeds args through the streaming guard in uneven deltas, like a
// provider does, and returns the first guard error.
func streamArgs(name, args string, seed int64) error {
	rng := rand.New(rand.NewSource(seed))
	var b strings.Builder
	for i := 0; i < len(args); {
		step := 1 + rng.Intn(120)
		if rng.Intn(50) == 0 {
			step = 4096 + rng.Intn(9000) // an occasional large chunk
		}
		end := min(i+step, len(args))
		prev := b.Len()
		b.WriteString(args[i:end])
		if err := checkRunawayToolArguments(name, &b, prev); err != nil {
			return err
		}
		i = end
	}
	return nil
}

func writeFileArgs(t testing.TB, content string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": "data/out.txt", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fill(target int, line func(i int) string) string {
	var b strings.Builder
	for i := 0; b.Len() < target; i++ {
		b.WriteString(line(i))
	}
	return b.String()[:target]
}

// Realistic large arguments that repeat a lot but are not degenerate. Each is
// ~200 KiB (below the 256 KiB cap) and must pass both the streaming and the
// one-piece check.
func legitLargeArgs(t testing.TB) map[string]string {
	t.Helper()
	const size = 200 << 10
	rng := rand.New(rand.NewSource(42))
	random := make([]byte, size)
	rng.Read(random)
	cities := []string{"Warsaw", "Krakow", "Gdansk", "Poznan", "Wroclaw", "Lodz"}
	statuses := []string{"ok", "pending", "failed", "skipped"}
	out := map[string]string{
		"csv-rows-different-values": fill(size, func(i int) string {
			return fmt.Sprintf("%d,2024-%02d-%02d,%s,%d.%02d,%s\n", 100000+i, 1+i%12, 1+i%28, cities[i%len(cities)], (i*7919)%10000, i%100, statuses[(i/3)%len(statuses)])
		}),
		"markdown-table": "| id | name | qty | status |\n|---:|------|----:|--------|\n" + fill(size, func(i int) string {
			return fmt.Sprintf("| %d | item-%04d | %d | %s |\n", i, i, (i*37)%500, statuses[i%len(statuses)])
		}),
		"base64": base64.StdEncoding.EncodeToString(random)[:size],
		"minified-js": fill(size, func(i int) string {
			return fmt.Sprintf("function f%d(a,b){var c=a[%d]||0;return c+b*%d}var v%d=f%d(o,%d);", i, i%17, i%97, i, i, (i*31)%1000)
		}),
		"sql-inserts": fill(size, func(i int) string {
			return fmt.Sprintf("INSERT INTO orders (id, customer, amount, created) VALUES (%d, 'cust_%d', %d.%02d, '2024-01-%02d');\n", i, (i*13)%2000, (i*7)%900, i%100, 1+i%28)
		}),
		"json-array-of-objects": func() string {
			var items []map[string]any
			for i := 0; i < 5000; i++ {
				items = append(items, map[string]any{"id": i, "sku": fmt.Sprintf("SKU-%05d", i*3), "price": float64(i%500) + 0.99, "tags": []string{"a", statuses[i%4]}})
			}
			b, _ := json.Marshal(items)
			return string(b)[:size]
		}(),
		"go-test-table": fill(size, func(i int) string {
			return fmt.Sprintf("\t\t{name: \"case %d\", in: %d, want: %d, err: false},\n", i, i*3, i*i%10007)
		}),
		"log-lines": fill(size, func(i int) string {
			return fmt.Sprintf("2024-05-01T10:%02d:%02d.%03dZ INFO request id=%08x path=/api/v1/items/%d status=200 dur=%dms\n", (i/60)%60, i%60, (i*7)%1000, i*2654435761, i%5000, 3+i%250)
		}),
	}
	// This repository's own Go source as realistic code.
	var src strings.Builder
	files, _ := filepath.Glob("*.go")
	sort.Strings(files)
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			src.Write(b)
		}
		if src.Len() >= size {
			break
		}
	}
	if src.Len() >= size {
		out["go-source"] = src.String()[:size]
	}
	return out
}

func TestDegenerateGuardAllowsRealisticLargeArguments(t *testing.T) {
	for name, content := range legitLargeArgs(t) {
		args := writeFileArgs(t, content)
		if len(args) > maxToolCallArgumentBytes {
			args = args[:maxToolCallArgumentBytes]
		}
		if err := streamArgs("write_file", args, 7); err != nil {
			t.Errorf("%s (%d KB): streaming guard tripped: %v", name, len(args)>>10, err)
		}
		if err := checkFullToolArguments("write_file", args); err != nil {
			t.Errorf("%s: one-piece guard tripped: %v", name, err)
		}
		if DegenerateToolArguments(args) {
			t.Errorf("%s: DegenerateToolArguments = true", name)
		}
	}
}

func TestDegenerateGuardStopsRepeatingBlocks(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	randomUnit := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + rng.Intn(26))
		}
		return string(b)
	}
	cases := map[string]struct {
		unit   string
		maxKiB int // the guard must fire before this much was accepted (window + one large delta)
	}{
		"incident-block":    {" RENDERING_ARGS####json 1/1 (1)####{\"\\t\\t:\\t\" } \ufffc RENDERING_ARGS####json 1/1 (1)####{\"\\t\\t,\\t\" } \ufffc", 32},
		"short-period":      {"abcdefgh", 32},
		"period-900":        {randomUnit(900), 32},
		"period-1500":       {randomUnit(1500), 32}, // > 1024 distinct grams: caught by the exact-repetition rule
		"period-2048":       {randomUnit(2048), 32},
		"json-escape-loop":  {`{\"\t\t:\t\" } `, 32},
		"repeated-csv-line": {"0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0\n", 32},
	}
	for name, tc := range cases {
		prefix := `{"path":"main.go","content":"package main\nfunc main() {}\n`
		args := prefix + strings.Repeat(tc.unit, (300<<10)/len(tc.unit)+1)
		err := streamArgs("invoke_tool", args, 11)
		var runaway *RunawayToolCallError
		if !errors.As(err, &runaway) {
			t.Errorf("%s: not stopped: %v", name, err)
			continue
		}
		if runaway.Reason != RunawayRepetition || runaway.KB > tc.maxKiB+len(prefix)>>10 {
			t.Errorf("%s: reason=%s at %d KB", name, runaway.Reason, runaway.KB)
		}
		if !DegenerateToolArguments(args[:200<<10]) {
			t.Errorf("%s: one-piece check missed it", name)
		}
	}
	// A unit longer than 2 KiB is not a "short loop"; the size cap ends it.
	long := strings.Repeat(randomUnit(3000), 100)
	err := streamArgs("write_file", long, 5)
	var runaway *RunawayToolCallError
	if !errors.As(err, &runaway) || runaway.Reason != RunawayTooLarge || runaway.KB < maxToolCallArgumentBytes>>10 || runaway.KB > 270 {
		t.Fatalf("3000-byte period: %v", err)
	}
}

func TestDegenerateGuardRepetitionInTheMiddleOfOneChunk(t *testing.T) {
	// One delta carrying legit text, then a degenerate run, then legit text
	// again: the one-chunk scan checks every 4 KiB boundary, not just the end.
	legit := fill(40<<10, func(i int) string { return fmt.Sprintf("line %d of real text, value %d\n", i, i*i) })
	args := legit + strings.Repeat("loop!", 8<<10) + legit
	var b strings.Builder
	b.WriteString(args)
	if err := checkRunawayToolArguments("write_file", &b, 0); err == nil {
		t.Fatal("degenerate middle of a single chunk not detected")
	}
}

func TestToolArgumentsHardCap(t *testing.T) {
	content := legitLargeArgs(t)["base64"] + strings.Repeat("Q", 0)
	args := writeFileArgs(t, content+base64.StdEncoding.EncodeToString([]byte(content))[:80<<10])
	if len(args) <= maxToolCallArgumentBytes {
		t.Fatalf("fixture too small: %d", len(args))
	}
	err := streamArgs("write_file", args, 1)
	var runaway *RunawayToolCallError
	if !errors.As(err, &runaway) || runaway.Reason != RunawayTooLarge {
		t.Fatalf("hard cap: %v", err)
	}
	if !strings.Contains(err.Error(), "exceeded 256 KB") {
		t.Fatalf("message: %q", err)
	}
	if err := checkFullToolArguments("write_file", args); err == nil {
		t.Fatal("one-piece hard cap missing")
	}
}

func TestToolStreamWatchNoticesAndLimit(t *testing.T) {
	now := time.Unix(1000, 0)
	saved := toolStreamClock
	toolStreamClock = func() time.Time { return now }
	t.Cleanup(func() { toolStreamClock = saved; SetToolCallStreamLimit(DefaultToolCallStreamLimit) })
	SetToolCallStreamLimit(2 * time.Minute)
	var w toolStreamWatch
	if n, err := w.observe("write_file", 10); n != "" || err != nil {
		t.Fatal(n, err)
	}
	now = now.Add(10 * time.Second)
	if n, _ := w.observe("write_file", 2048); n != "" {
		t.Fatalf("early notice %q", n)
	}
	now = now.Add(25 * time.Second)
	n, err := w.observe("write_file", 40<<10)
	if err != nil || !strings.Contains(n, "still receiving the write_file call") || !strings.Contains(n, "40 KB") || !strings.Contains(n, "35s") {
		t.Fatalf("notice=%q err=%v", n, err)
	}
	now = now.Add(2 * time.Minute)
	_, err = w.observe("write_file", 50<<10)
	var runaway *RunawayToolCallError
	if !errors.As(err, &runaway) || runaway.Reason != RunawayTooSlow || runaway.Elapsed != 155*time.Second {
		t.Fatalf("limit: %v", err)
	}
	SetToolCallStreamLimit(0)
	if _, err := w.observe("write_file", 60<<10); err != nil {
		t.Fatalf("disabled limit still fires: %v", err)
	}
}

// Highly regular content that IS legitimate in rare cases (a sparse 0/1
// matrix, a zero-filled array, a hex dump of zeros). These are documented
// trade-offs: they repeat with a short period over 16 KiB, so the guard
// rejects them and the agent asks the model to split/regenerate the call.
func TestDegenerateGuardKnownTradeOffs(t *testing.T) {
	identity := fill(64<<10, func(i int) string {
		row := make([]string, 64)
		for j := range row {
			row[j] = "0"
		}
		row[i%64] = "1"
		return strings.Join(row, ",") + "\n"
	})
	cases := map[string]string{
		"identity-matrix-csv": identity,
		"zero-array":          "[" + strings.Repeat("0,", 32<<10) + "0]",
		"hexdump-zeros": fill(64<<10, func(i int) string {
			return fmt.Sprintf("%08x  00 00 00 00 00 00 00 00  00 00 00 00 00 00 00 00  |................|\n", i*16)
		}),
	}
	for name, content := range cases {
		args := writeFileArgs(t, content)
		t.Logf("%s: rejected=%v", name, DegenerateToolArguments(args))
	}
}

func BenchmarkDegenerateGuardLegit200KiB(b *testing.B) {
	args := writeFileArgs(b, legitLargeArgs(b)["csv-rows-different-values"])
	b.SetBytes(int64(len(args)))
	for i := 0; i < b.N; i++ {
		if err := streamArgs("write_file", args, 7); err != nil {
			b.Fatal(err)
		}
	}
}
