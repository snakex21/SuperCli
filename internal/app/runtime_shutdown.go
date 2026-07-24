package app

import (
	"context"
	"log"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

// afterTUIShutdown runs the post-TUI cleanup: warm slot-cache save,
// cancel idle memory, dump unsummarized tail, and stop the ask pump.
func afterTUIShutdown(
	slotCache *llm.SlotCache,
	loop *agent.Loop,
	sessionID string,
	memIdle *idleScheduler,
	memAutoSaver *memory.AutoSaver,
	memProg *memProgress,
	dataDir string,
	askCh chan tools.AskRequest,
	pumpDone <-chan struct{},
) {
	// Persist this session's slot KV state so the next launch can
	// /resume warm (llama.cpp re-prefills only from the divergence
	// point). Done FIRST after the TUI exits, before the memory
	// finalizer below fires its own summary request — that request
	// shares the session's prompt prefix and would otherwise advance
	// the slot past the state the resumed history will replay.
	if slotCache != nil && !slotCache.Disabled() && loop != nil && len(loop.AllMessages()) > 0 {
		sctx, scancel := context.WithTimeout(context.Background(), 30*time.Second)
		if n, serr := slotCache.Save(sctx, sessionID); serr != nil {
			log.Printf("slotcache: save %s: %v", sessionID, serr)
		} else if n > 0 {
			log.Printf("slotcache: saved %d cached token(s) for %s", n, sessionID)
		}
		scancel()
	}
	// B4 code guarantee, model-free: cancel any in-flight idle
	// save, then store the un-summarized conversation tail as a
	// raw-log entry (no inference — the exit is never held hostage
	// by a model call). The next startup summarizes it in ITS idle
	// window.
	if memIdle != nil {
		memIdle.Close()
	}
	finalizeMemorySession(memAutoSaver, loop, memProg)
	startPostTUIShutdownTimer(dataDir, 200*time.Millisecond)
	if askCh != nil {
		close(askCh)
	}
	if pumpDone != nil {
		<-pumpDone
	}
}
