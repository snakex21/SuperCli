package preflight

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Independent original implementation: it retains all porcelain lines before
// counting areas. Keep this oracle even though production needs only the first
// sixteen path samples; malformed/quoted paths stay opaque in both versions.
func originalCompactStatus(status string) []string {
	var lines []string
	for _, line := range strings.Split(status, "\n") {
		if line = strings.TrimRight(line, "\r"); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) <= defaultMaxStatusFiles {
		return lines
	}
	types, areas := make(map[string]int), make(map[string]int)
	sample := make([]string, 0, defaultMaxStatusFiles)
	for _, line := range lines {
		code, path := porcelainParts(line)
		types[statusKind(code)]++
		areas[statusArea(path)]++
		if len(sample) < defaultMaxStatusFiles {
			sample = append(sample, path)
		}
	}
	type orderCount struct {
		name  string
		count int
	}
	var orderedTypes []orderCount
	for _, name := range []string{"modified", "untracked", "added", "deleted", "renamed", "conflicted", "other"} {
		if n := types[name]; n > 0 {
			orderedTypes = append(orderedTypes, orderCount{name, n})
		}
	}
	typeParts := make([]string, 0, len(orderedTypes))
	for _, item := range orderedTypes {
		typeParts = append(typeParts, item.name+" "+itoa(item.count))
	}
	orderedAreas := make([]orderCount, 0, len(areas))
	for name, count := range areas {
		orderedAreas = append(orderedAreas, orderCount{name, count})
	}
	sort.Slice(orderedAreas, func(i, j int) bool {
		if orderedAreas[i].count != orderedAreas[j].count {
			return orderedAreas[i].count > orderedAreas[j].count
		}
		return orderedAreas[i].name < orderedAreas[j].name
	})
	if len(orderedAreas) > defaultMaxStatusAreas {
		orderedAreas = orderedAreas[:defaultMaxStatusAreas]
	}
	areaParts := make([]string, 0, len(orderedAreas))
	for _, item := range orderedAreas {
		areaParts = append(areaParts, item.name+" "+itoa(item.count))
	}
	return []string{"total: " + itoa(len(lines)) + " (" + strings.Join(typeParts, ", ") + ")", "areas: " + strings.Join(areaParts, ", "), "sample: " + strings.Join(sample, ", ") + " (and " + itoa(len(lines)-len(sample)) + " more)"}
}

func TestCompactStatusStreamIsByteIdentical(t *testing.T) {
	inputs := []string{"", "\n \t\r\n", " M first.go\r\n?? źródło.go\r\n", strings.Repeat(" M repeated.go\n", defaultMaxStatusFiles), strings.Repeat(" M repeated.go\n", defaultMaxStatusFiles+1)}
	rng := rand.New(rand.NewSource(731246))
	codes := []string{" M", "??", "A ", " D", "R ", "UU", "AA", "DD", " T", "!!", "xx", "", "x"}
	paths := []string{"internal/agent/loop.go", "internal/tools/handler.go", "cmd/tool.go", "docs/README.md", "root.go", `"internal\agent\żółć.go"`, `"name with spaces/file.go"`, `old.go -> internal/agent/new.go`, "\x00\xff/ę世", "/root.go", "internal//odd.go"}
	counts := []int{1, 15, 16, 17, 80, 500, 5000}
	for n := 0; n < 2000; n++ {
		counts = append(counts, rng.Intn(80))
	}
	for _, count := range counts {
		var b strings.Builder
		for n := 0; n < count; n++ {
			if rng.Intn(6) == 0 {
				b.WriteString(" \t\r\n")
			}
			b.WriteString(codes[rng.Intn(len(codes))])
			b.WriteByte(' ')
			b.WriteString(paths[rng.Intn(len(paths))])
			if rng.Intn(2) == 0 {
				b.WriteByte('\r')
			}
			b.WriteByte('\n')
		}
		inputs = append(inputs, b.String())
	}
	for _, input := range inputs {
		if got, want := compactStatus(input), originalCompactStatus(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("status bytes=%d changed:\ngot %#v\nwant %#v", len(input), got, want)
		}
	}
}

func TestStreamingPreflightHardBudgetsAndFreshTaskState(t *testing.T) {
	for _, budget := range []int{1, 24, 30, 60, 150, 300, 1000} {
		for _, task := range []string{"first-task", "next-task"} {
			status := strings.ReplaceAll(streamingStatusFixture(50000), "internal/", task+"/")
			block := Build(".", Options{Budget: budget, LookPath: hasGit, RunGit: func(_ string, args ...string) (string, error) {
				if args[0] == "status" {
					return "## " + task + "\n" + status, nil
				}
				return "abc123 " + task, nil
			}})
			if tokens := EstimateTokens(block); tokens > budget {
				t.Fatalf("task=%s budget=%d block uses %d tokens", task, budget, tokens)
			}
			if budget >= 150 && (!strings.Contains(block, "branch: "+task) || !strings.Contains(block, "areas: "+task+" 50000")) {
				t.Fatalf("current task state was lost or reused at budget=%d: %q", budget, block)
			}
			if task == "next-task" && strings.Contains(block, "first-task") {
				t.Fatalf("reused preceding task state: %q", block)
			}
		}
	}
}

func streamingStatusFixture(count int) string {
	var out strings.Builder
	for n := 0; n < count; n++ {
		fmt.Fprintf(&out, " M internal/area-%02d/nested/file-%06d.go\n", n%24, n)
	}
	return out.String()
}

var statusStreamSink []string

func BenchmarkCompactStatusStream(b *testing.B) {
	for _, count := range []int{6, 500, 50000} {
		status := streamingStatusFixture(count)
		for _, implementation := range []struct {
			name string
			fn   func(string) []string
		}{{"original", originalCompactStatus}, {"production", compactStatus}} {
			b.Run(fmt.Sprintf("status_%d/%s", count, implementation.name), func(b *testing.B) {
				b.SetBytes(int64(len(status)))
				b.ReportAllocs()
				for b.Loop() {
					statusStreamSink = implementation.fn(status)
				}
			})
		}
	}
}

func BenchmarkPreflightStreamingBuild(b *testing.B) {
	for _, count := range []int{6, 500, 50000} {
		b.Run(fmt.Sprintf("status_%d", count), func(b *testing.B) {
			streamingStatus := streamingStatusFixture(count)
			options := Options{LookPath: hasGit, RunGit: func(_ string, args ...string) (string, error) {
				if args[0] == "status" {
					return "## fixture-branch\n" + streamingStatus, nil
				}
				return "abc123 newest\ndef456 older", nil
			}}
			b.ReportAllocs()
			for b.Loop() {
				preflightBriefingSink = Build(".", options)
			}
		})
	}
}
