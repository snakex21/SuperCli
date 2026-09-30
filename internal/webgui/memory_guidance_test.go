package webgui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	llmprompt "supercli/internal/llm/prompt"
)

func TestWebLoopMemoryGuidanceRespectsSettings(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		auto           bool
	}{
		{"default", `{}`, true},
		{"enabled", `{"supercli.autoMemory":true}`, true},
		{"disabled", `{"supercli.autoMemory":false}`, false},
		{"legacy-disabled", `{"nestcafe.autoMemory":false}`, false},
		{"shared-overrides-legacy", `{"supercli.autoMemory":true,"nestcafe.autoMemory":false}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := t.TempDir()
			if err := os.WriteFile(filepath.Join(data, uiSettingsFile), []byte(tc.settings), 0600); err != nil {
				t.Fatal(err)
			}
			eng, err := NewEngine(echoConfig(), t.TempDir(), data)
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			loop, err := eng.newLoopWithSession(nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var system strings.Builder
			for _, m := range loop.Messages {
				system.WriteString(m.Content)
			}
			got := system.String()
			if !strings.Contains(got, llmprompt.MemoryGuidance(tc.auto)) {
				t.Error("composed prompt lost configured memory policy")
			}
			if strings.Contains(got, "After completing a useful task") {
				t.Error("foreground task-log ritual returned")
			}
		})
	}
}
