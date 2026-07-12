// Wave 2 memory wiring: the /memory slash command, the global
// memory home resolution, and the end-of-session auto-save hook.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

// storeOrNil converts a possibly-nil *memory.Store into a clean
// nil interface so `keeper == nil` checks in the tools work
// (a typed nil pointer inside a non-nil interface would not).
func storeOrNil(s *memory.Store) tools.MemoryKeeper {
	if s == nil {
		return nil
	}
	return s
}

// memoryCommand implements /memory:
//
//	/memory             — overview (recent entries, DB sizes, embeddings)
//	/memory search <q>  — hybrid search across project + global stores
//	/memory forget <id> — delete an entry from whichever store has it
func memoryCommand(ctx context.Context, project, global *memory.Store, briefing, args string) (string, error) {
	if project == nil && global == nil {
		return "memory: no store available (open failed at startup — see logs)", nil
	}
	args = strings.TrimSpace(args)
	switch {
	case args == "":
		return memoryOverview(project, global, briefing), nil
	case strings.HasPrefix(args, "search "):
		q := strings.TrimSpace(strings.TrimPrefix(args, "search "))
		if q == "" {
			return "usage: /memory search <query>", nil
		}
		return memorySearch(ctx, project, global, q), nil
	case strings.HasPrefix(args, "forget "):
		id := strings.TrimSpace(strings.TrimPrefix(args, "forget "))
		if id == "" {
			return "usage: /memory forget <id>", nil
		}
		return memoryForget(project, global, id), nil
	default:
		return "usage: /memory | /memory search <query> | /memory forget <id>", nil
	}
}

func memoryOverview(project, global *memory.Store, briefing string) string {
	var b strings.Builder
	b.WriteString("Persistent memory\n")
	if briefing != "" {
		fmt.Fprintf(&b, "briefing: %d tokens, injected: yes\n", memory.EstimateTokens(briefing))
	} else {
		b.WriteString("briefing: 0 tokens, injected: no (nothing to inject yet)\n")
	}
	writeStore := func(label string, s *memory.Store) {
		if s == nil {
			fmt.Fprintf(&b, "\n%s: unavailable\n", label)
			return
		}
		dbPath := filepath.Join(s.Root(), "memory.db")
		size := "?"
		if fi, err := os.Stat(dbPath); err == nil {
			size = fmt.Sprintf("%.1f KB", float64(fi.Size())/1024)
		}
		emb := s.EmbedderName()
		if emb == "" {
			emb = "off (FTS5 only)"
		}
		fmt.Fprintf(&b, "\n%s: %s (%s, embeddings: %s)\n", label, dbPath, size, emb)
		entries, err := s.Recent("", 5)
		if err != nil || len(entries) == 0 {
			b.WriteString("  (no entries)\n")
			return
		}
		for _, e := range entries {
			content := strings.ReplaceAll(strings.TrimSpace(e.Content), "\n", " ")
			if len(content) > 90 {
				content = content[:90] + "…"
			}
			fmt.Fprintf(&b, "  [%s] %s %s: %s\n", e.ID, e.UpdatedAt.Format("2006-01-02"), e.Scope, content)
		}
	}
	writeStore("project", project)
	writeStore("global", global)
	if global != nil {
		if cards, err := global.ListProjectCards(0); err == nil && len(cards) > 0 {
			fmt.Fprintf(&b, "\nknown projects: %d\n", len(cards))
		}
	}
	b.WriteString("\n/memory search <query> · /memory forget <id>\n")
	return b.String()
}

func memorySearch(ctx context.Context, project, global *memory.Store, q string) string {
	var b strings.Builder
	total := 0
	search := func(label string, s *memory.Store) {
		if s == nil {
			return
		}
		entries, err := s.HybridSearch(ctx, q, 5)
		if err != nil || len(entries) == 0 {
			return
		}
		for _, e := range entries {
			content := strings.ReplaceAll(strings.TrimSpace(e.Content), "\n", " ")
			if len(content) > 100 {
				content = content[:100] + "…"
			}
			fmt.Fprintf(&b, "[%s] %s/%s %s: %s\n", e.ID, label, e.Scope, e.UpdatedAt.Format("2006-01-02"), content)
			total++
		}
	}
	search("project", project)
	search("global", global)
	if total == 0 {
		return fmt.Sprintf("memory: nothing matches %q", q)
	}
	return fmt.Sprintf("%d result(s) for %q:\n%s", total, q, b.String())
}

func memoryForget(project, global *memory.Store, id string) string {
	for _, s := range []*memory.Store{project, global} {
		if s == nil {
			continue
		}
		if _, err := s.Get(id); err == nil {
			if err := s.Delete(id); err != nil {
				return fmt.Sprintf("memory forget: %v", err)
			}
			return fmt.Sprintf("forgot [%s]", id)
		}
	}
	return fmt.Sprintf("memory forget: no entry with id %q", id)
}

// memProgress tracks how much of the conversation the incremental
// background saver has already summarized into task-log entries.
// The mutex also serializes the background saver against the
// end-of-session finalizer so no fragment is summarized twice.
type memProgress struct {
	mu      sync.Mutex
	covered int // number of loop messages already summarized
	// factsCovered tracks the deterministic user-fact extractor
	// separately: facts are saved IMMEDIATELY after each turn (no
	// model call), while the summary waits for the idle window.
	factsCovered int
}

// lockWithin acquires the mutex, giving up after d (so the exit
// path never waits long behind an in-flight background save).
func (p *memProgress) lockWithin(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if p.mu.TryLock() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// providerSummarizer adapts an llm.Provider to memory.SummarizeFunc.
// Every call is labeled as background "memory" work so the metered
// stats attribute it correctly (autosave used to be invisible).
func providerSummarizer(provider llm.Provider) memory.SummarizeFunc {
	return func(ctx context.Context, prompt string) (string, error) {
		ctx = llm.WithBackground(llm.WithPurpose(ctx, llm.PurposeMemory))
		ch, err := provider.Complete(ctx, []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		}, nil)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		for d := range ch {
			if d.Err != nil {
				return "", d.Err
			}
			out.WriteString(d.Content)
		}
		return out.String(), nil
	}
}

// usableSummaryProvider reports whether p can produce a real
// summary (echo would store its own prompt as a "summary").
func usableSummaryProvider(p llm.Provider) bool {
	return p != nil && !strings.Contains(strings.ToLower(p.Name()), "echo")
}

// hasUserTurn reports whether msgs contain a non-empty user message.
func hasUserTurn(msgs []llm.Message) bool {
	for _, m := range msgs {
		if m.Role == llm.RoleUser && strings.TrimSpace(m.Content) != "" {
			return true
		}
	}
	return false
}

// rawUserTexts returns the plain-text content of every user message
// in msgs (multimodal parts flattened via TextOnly). It feeds the
// deterministic user-fact extractor, which must see the user's exact
// words — never the assistant's.
func rawUserTexts(msgs []llm.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role != llm.RoleUser {
			continue
		}
		text := strings.TrimSpace(m.TextOnly().Content)
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

// compactFragment renders msgs as a transcript capped at maxChars
// and at most 40 messages (the most recent ones win).
func compactFragment(msgs []llm.Message) string {
	if len(msgs) > 40 {
		msgs = msgs[len(msgs)-40:]
	}
	transcript := renderCompactTranscript(msgs)
	const maxChars = 24000
	if len(transcript) > maxChars {
		transcript = transcript[len(transcript)-maxChars:]
	}
	return transcript
}

// saveDeterministicMemoryFacts persists simple personal
// declarations from the user's raw words right after each turn —
// pure string matching, NO model call, dedup'd in the store. It
// runs immediately (not in the idle window) so short declarations
// ("nazywam się Maks") survive even a kill -9 seconds later.
func saveDeterministicMemoryFacts(saver *memory.AutoSaver, loop *agent.Loop, prog *memProgress) {
	if saver == nil || loop == nil || prog == nil {
		return
	}
	prog.mu.Lock()
	defer prog.mu.Unlock()
	msgs := loop.AllMessages()
	if len(msgs) > prog.factsCovered {
		saver.SaveDeterministicUserFacts(rawUserTexts(msgs[prog.factsCovered:]))
		prog.factsCovered = len(msgs)
	}
}

// incrementalMemorySave summarizes ONLY the not-yet-covered slice
// of the conversation into a task-log entry, so the exit path has
// nothing left to do and the program quits instantly. It makes one
// model call, so it runs ONLY in the idle window (see the
// idleScheduler wiring in main.go): ctx is canceled when the user
// sends a new prompt, and the uncovered fragment — several turns
// batched into one summary if the user kept typing — is retried at
// the next idle. summarizer should be the small/cheap tier
// provider when one is configured.
func incrementalMemorySave(ctx context.Context, saver *memory.AutoSaver, loop *agent.Loop, prog *memProgress, summarizer llm.Provider) {
	if saver == nil || loop == nil || prog == nil {
		return
	}
	prog.mu.Lock()
	defer prog.mu.Unlock()
	msgs := loop.AllMessages()
	// Deterministic safety net: normally already done right after
	// the turn (saveDeterministicMemoryFacts), repeated here for
	// call sites that reach this path first. Dedup makes it free.
	if len(msgs) > prog.factsCovered {
		saver.SaveDeterministicUserFacts(rawUserTexts(msgs[prog.factsCovered:]))
		prog.factsCovered = len(msgs)
	}
	if saver.Remembered() {
		// The model saves its own notes; nothing synthetic needed.
		prog.covered = len(msgs)
		return
	}
	if len(msgs) <= prog.covered {
		return
	}
	fragment := msgs[prog.covered:]
	if !hasUserTurn(fragment) {
		return
	}
	// No minimum-size threshold: a single short user message
	// ("cześć, lubię komputery") is exactly the kind of content
	// that must survive the session. hasUserTurn above is the
	// only gate — it skips empty/command-only turns.
	transcript := compactFragment(fragment)
	if !usableSummaryProvider(summarizer) {
		return
	}
	snapshot := len(msgs)
	// ctx comes from the idle scheduler: canceled the moment the
	// user sends a new prompt. A canceled/failed summary leaves
	// `covered` untouched, so nothing is lost — the fragment is
	// retried (batched with newer turns) at the next idle window.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if saver.StoreSummary(ctx, transcript, providerSummarizer(summarizer)) {
		prog.covered = snapshot
	}
}

// dumpRawMemoryTail is the emergency path for abrupt termination
// (console window closed → CTRL_CLOSE_EVENT, ~5s budget): it
// writes the not-yet-summarized transcript tail VERBATIM to the
// project store — no LLM call — as a raw-log entry that the next
// startup summarizes (AutoSaver.SummarizePendingRaw) and the next
// briefing shows raw until then.
func dumpRawMemoryTail(saver *memory.AutoSaver, loop *agent.Loop, prog *memProgress) {
	if saver == nil || loop == nil {
		return
	}
	if prog != nil {
		// An in-flight incremental save is already covering the
		// tail; give it a moment, then dump whatever is left.
		if !prog.lockWithin(2 * time.Second) {
			return
		}
		defer prog.mu.Unlock()
	}
	msgs := loop.AllMessages()
	uncovered := msgs
	if prog != nil && prog.covered > 0 && prog.covered <= len(msgs) {
		uncovered = msgs[prog.covered:]
	}
	if !hasUserTurn(uncovered) {
		return
	}
	if saver.Remembered() {
		return // the model saved its own notes this session
	}
	saver.StoreRawTail(compactFragment(uncovered))
}

// finalizeMemorySession is the end-of-session auto-save. It makes
// NO model call — blocking the exit on an inference is exactly the
// foreground-vs-background priority inversion the idle saver
// exists to avoid. Any conversation tail the idle saver has not
// summarized yet is stored VERBATIM as a raw-log entry (the same
// mechanism as the abrupt-close handler); the NEXT startup
// summarizes it in its idle window. Nothing is lost, the exit is
// instant.
func finalizeMemorySession(saver *memory.AutoSaver, loop *agent.Loop, prog *memProgress) {
	if saver == nil || loop == nil {
		return
	}
	// Always bump the project card's last-session stamp.
	defer saver.Finalize(context.Background(), "", nil)
	if prog == nil {
		prog = &memProgress{}
	}
	// The idle scheduler is Close()d before this runs, so any
	// in-flight background save is already canceled; the short
	// bounded wait is just for its goroutine to release the lock.
	if !prog.lockWithin(3 * time.Second) {
		return
	}
	defer prog.mu.Unlock()
	msgs := loop.AllMessages()
	uncovered := msgs
	if prog.covered > 0 && prog.covered <= len(msgs) {
		uncovered = msgs[prog.covered:]
	}
	// Deterministic safety net on the exit path too: a final short
	// declaration ("nazywam się Maks") must persist even if it was
	// never covered by an incremental save. No LLM call.
	if len(msgs) > prog.factsCovered {
		saver.SaveDeterministicUserFacts(rawUserTexts(msgs[prog.factsCovered:]))
		prog.factsCovered = len(msgs)
	}
	if !hasUserTurn(uncovered) {
		return
	}
	if saver.Remembered() {
		return // the model saved its own notes this session
	}
	saver.StoreRawTail(compactFragment(uncovered))
}
