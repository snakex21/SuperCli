package app

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestMainLiveContextMemoryRefreshIsOutsidePrefixAndSourceLabelled(t *testing.T) {
	prior := memoryBriefing
	memoryBriefing = "GLOBAL_STARTUP_STALE_MEMORY"
	t.Cleanup(func() { memoryBriefing = prior })
	version := "CURRENT_MEMORY_A"
	cfg := buildMainLoopConfig(loopAssembly{
		memoryBriefing:       "LEGACY_MEMORY",
		memoryBriefingForRun: func() string { return version },
	})
	if cfg.LiveContextBlocks == nil || cfg.Briefing != "" || strings.Contains(cfg.System, "GLOBAL_STARTUP_STALE_MEMORY") {
		t.Fatal("main loop duplicated or froze startup memory in its prefix")
	}
	blocks, err := cfg.LiveContextBlocks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) == 0 || blocks[0].Source != llm.SourceMemory || blocks[0].Text != version {
		t.Fatal("main live context lost the producer's memory source")
	}
	version = "CURRENT_MEMORY_B"
	blocks, err = cfg.LiveContextBlocks(context.Background())
	if err != nil || !strings.Contains(liveBlocksText(blocks), version) || strings.Contains(liveBlocksText(blocks), "CURRENT_MEMORY_A") {
		t.Fatalf("main live context did not refresh memory: %v", err)
	}
	// Other legacy callers still receive the historical startup briefing.
	if !strings.Contains(buildSystemPromptWithInstructions(nil, ""), "GLOBAL_STARTUP_STALE_MEMORY") {
		t.Fatal("legacy prompt contract changed")
	}
}
