package webgui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	llmprompt "supercli/internal/llm/prompt"
)

func TestWebLoopMemoryPolicyFollowsFreshSettings(t *testing.T) {
	data, home := t.TempDir(), t.TempDir()
	eng, err := NewEngine(echoConfig(), home, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	cases := []struct {
		name, raw string
		want      bool
	}{
		{"missing", "", true},
		{"empty", "{}", true},
		{"legacy-false", "{\"nestcafe.autoMemory\":false}", false},
		{"shared-false", "{\"supercli.autoMemory\":false}", false},
		{"shared-wins-true", "{\"supercli.autoMemory\":true,\"nestcafe.autoMemory\":false}", true},
		{"shared-wins-false", "{\"supercli.autoMemory\":false,\"nestcafe.autoMemory\":true}", false},
		{"shared-null", "{\"supercli.autoMemory\":null,\"nestcafe.autoMemory\":false}", false},
		{"shared-string", "{\"supercli.autoMemory\":\"true\",\"nestcafe.autoMemory\":false}", false},
		{"legacy-wrong-type", "{\"nestcafe.autoMemory\":[]}", true},
		{"number", "{\"supercli.autoMemory\":0,\"nestcafe.autoMemory\":false}", false},
		{"malformed", "{\"supercli.autoMemory\":false", true},
		{"null-object", "null", true},
		{"array-root", "[]", true},
		{"fresh-again", "{\"supercli.autoMemory\":true}", true},
	}
	p := filepath.Join(data, uiSettingsFile)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.raw == "" {
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(p, []byte(c.raw), 0600); err != nil {
				t.Fatal(err)
			}
			loop, err := eng.newLoopWithSession(nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var system strings.Builder
			for _, m := range loop.Messages {
				system.WriteString(m.Content)
			}
			got := system.String()
			if !strings.Contains(got, llmprompt.MemoryGuidance(c.want)) {
				t.Fatalf("fresh policy=%v lost in loop prompt", c.want)
			}
			if strings.Contains(got, llmprompt.MemoryGuidance(!c.want)) {
				t.Fatal("opposite policy also present")
			}
		})
	}
}

func memorySettingsFixture(size int) []byte {
	values := map[string]any{"supercli.autoMemory": true, "nestcafe.autoMemory": false, "synthetic_style": ""}
	for i := 0; i < 14; i++ {
		values["meta"+string(rune('a'+i))] = "synthetic"
	}
	base, _ := json.Marshal(values)
	values["synthetic_style"] = strings.Repeat("x", size-len(base))
	raw, _ := json.Marshal(values)
	return raw
}

func BenchmarkWebLoopMemorySettings(b *testing.B) {
	for _, c := range []struct {
		name string
		size int
	}{{"missing", 0}, {"4134B", 4134}, {"27513B", 27513}} {
		b.Run(c.name, func(b *testing.B) {
			data, home := b.TempDir(), b.TempDir()
			if c.size > 0 {
				raw := memorySettingsFixture(c.size)
				if len(raw) != c.size {
					b.Fatal(len(raw))
				}
				if err := os.WriteFile(filepath.Join(data, uiSettingsFile), raw, 0600); err != nil {
					b.Fatal(err)
				}
			}
			eng, err := NewEngine(echoConfig(), home, data)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = eng.Close() })
			if _, err := eng.newLoopWithSession(nil, nil); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				loop, err := eng.newLoopWithSession(nil, nil)
				if err != nil {
					b.Fatal(err)
				}
				if len(loop.Messages) == 0 {
					b.Fatal("empty prompt")
				}
			}
		})
	}
}

func BenchmarkWebMemorySettingLookup(b *testing.B) {
	for _, size := range []int{4134, 27513} {
		b.Run(strconv.Itoa(size)+"B", func(b *testing.B) {
			data := b.TempDir()
			raw := memorySettingsFixture(size)
			if err := os.WriteFile(filepath.Join(data, uiSettingsFile), raw, 0600); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				got := uiSettingBool(data, "supercli.autoMemory", true, "nestcafe.autoMemory")
				if !got {
					b.Fatal("shared policy lost")
				}
			}
		})
	}
}
