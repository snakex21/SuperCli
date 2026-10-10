package webgui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
)

// webMemoryKeeper borrows the engine-owned store for the run's fixed workspace.
// The engine keeps connections across requests and closes them after HTTP drain.
type webMemoryKeeper struct {
	engine *Engine
	home   string
	global bool
}

func (k webMemoryKeeper) open() (*memory.Store, error) {
	return k.engine.webMemoryStore(k.home, k.global)
}

func (k webMemoryKeeper) Put(entry memory.Entry) error {
	store, err := k.open()
	if err != nil {
		return err
	}
	return store.Put(entry)
}

func (k webMemoryKeeper) Search(query string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	return store.Search(query, limit)
}

func (k webMemoryKeeper) Recent(scope string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	return store.Recent(scope, limit)
}

func (k webMemoryKeeper) HybridSearch(ctx context.Context, query string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	return store.HybridSearch(ctx, query, limit)
}

func (k webMemoryKeeper) RecallSearch(ctx context.Context, query string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	return store.RecallSearch(ctx, query, limit)
}

func (k webMemoryKeeper) RecallRecent(limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	return store.RecallRecent(limit)
}

// webMemoryStore opens each store lazily once. Failed opens are not cached, so a
// repaired directory can be retried; a closed engine must never reopen a store.
func (e *Engine) webMemoryStore(home string, global bool) (*memory.Store, error) {
	if e == nil {
		return nil, fmt.Errorf("memory store: engine unavailable")
	}
	e.memoryMu.Lock()
	defer e.memoryMu.Unlock()
	if e.memoryClosed {
		return nil, fmt.Errorf("memory store: engine closed")
	}
	if global {
		if e.globalMemory == nil {
			store, err := memory.OpenStore(e.dataDir)
			if err != nil {
				return nil, err
			}
			e.globalMemory = store
		}
		return e.globalMemory, nil
	}
	home = filepath.Clean(strings.TrimSpace(home))
	if e.projectMemory == nil {
		e.projectMemory = make(map[string]*memory.Store)
	}
	if store := e.projectMemory[home]; store != nil {
		return store, nil
	}
	// Keep the ordinary cache hit free of canonicalization and metadata I/O.
	// Windows spelling aliases also borrow an already observed home's identity,
	// including an authoritative relocated key absent under the new spelling.
	if runtime.GOOS == "windows" {
		for observed, store := range e.projectMemory {
			if strings.EqualFold(observed, home) {
				e.projectMemory[home] = store
				return store, nil
			}
		}
	}
	key := memory.ProjectStorageKey(e.dataDir, home)
	databaseKey := webMemoryDatabaseKey(filepath.Join(e.dataDir, "projects", key))
	if store := e.projectMemoryByDB[databaseKey]; store != nil {
		e.projectMemory[home] = store
		return store, nil
	}
	store, err := memory.OpenProjectStore(e.dataDir, home)
	if err != nil {
		return nil, err
	}
	// OpenProjectStore resolves projects.json itself. Another writer may have
	// changed it after the lookup above, so index the database actually opened.
	databaseKey = webMemoryDatabaseKey(store.Root())
	if existing := e.projectMemoryByDB[databaseKey]; existing != nil {
		// Only this unpublished handle is redundant; never close a live Store.
		_ = store.Close()
		e.projectMemory[home] = existing
		return existing, nil
	}
	if e.projectMemoryByDB == nil {
		e.projectMemoryByDB = make(map[string]*memory.Store)
	}
	e.projectMemoryByDB[databaseKey] = store
	e.projectMemory[home] = store
	return store, nil
}

// webMemoryDatabaseKey identifies the physical portable memory database. The
// resolver runs only on a new home; existing keepers retain their cached binding.
func webMemoryDatabaseKey(root string) string {
	path := filepath.Join(root, "memory.db")
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func (e *Engine) webMemoryStores(home string) (globalStore, projectStore *memory.Store) {
	globalStore, _ = e.webMemoryStore(home, true)
	projectStore, _ = e.webMemoryStore(home, false)
	return globalStore, projectStore
}

func (e *Engine) webMemoryBriefing(home string, tokenCap int) string {
	return e.webMemoryBriefingExcludingSession(home, tokenCap, "")
}

func (e *Engine) webMemoryBriefingExcludingSession(home string, tokenCap int, sessionID string) string {
	if tokenCap <= 0 {
		tokenCap = 700
	}
	globalStore, projectStore := e.webMemoryStores(home)
	if globalStore == nil && projectStore == nil {
		return ""
	}
	excludedID := ""
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		excludedID = "web-session-" + sessionID
	}
	return memory.BuildBriefingExcludingTaskLog(globalStore, projectStore, home, tokenCap, excludedID)
}

func (e *Engine) saveWebUserFacts(prompt string) {
	if len(memory.ExtractUserFacts([]string{prompt})) == 0 {
		return
	}
	globalStore, err := e.webMemoryStore("", true)
	if err != nil {
		return
	}
	saver := &memory.AutoSaver{Global: globalStore}
	saver.SaveDeterministicUserFacts([]string{prompt})
}

const webSessionRecallTokens = 420

// saveWebSessionCapsule keeps one compact, deterministic task-log record per
// conversation. It deliberately makes no LLM call: all text already exists in
// the session DB, so cross-session memory adds only a small local SQLite read
// and one upsert after a turn completes.
func (e *Engine) saveWebSessionCapsule(ctx context.Context, sessionID string, runHome ...string) {
	if e == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if ctx.Err() != nil {
		return
	}
	// The caller may pass the workspace captured when this run started. Never
	// resolve its SID against a newly switched project during the terminal tail.
	home := ""
	if len(runHome) > 0 {
		home = runHome[0]
	} else {
		if !e.mu.TryRLock() {
			return
		}
		home = e.home
		e.mu.RUnlock()
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return
	}
	home = filepath.Clean(home)
	project := e.cachedWebCapsuleStore(home)
	if project == nil {
		return
	}
	// A real run already owns an open session store. Do not wait behind an
	// unrelated session open, media recovery or shutdown while closing this run.
	if !e.sessionMu.TryLock() {
		return
	}
	sessions := e.sessions
	closed := e.closed
	e.sessionMu.Unlock()
	if closed || sessions == nil {
		return
	}
	messages, err := sessions.ReadDialogueExcerpt(ctx, sessionID, 8, func(message session.Encoded) bool {
		return webCapsuleText(message) != ""
	})
	if err != nil || len(messages) == 0 {
		return
	}

	content := buildWebSessionCapsule(sessionID, messages)
	if content == "" {
		return
	}

	id := "web-session-" + sessionID
	entry := memory.Entry{
		ID:      id,
		Scope:   memory.ScopeTaskLog,
		Content: content,
		Tags:    []string{"session", "cross-session"},
		Source:  memory.SourceAgent,
	}
	_ = project.SaveTaskLogCapsule(ctx, entry, memory.MaxTaskLogEntries)
}

// cachedWebCapsuleStore never waits behind an open/migration or rebinds a late
// capsule to another workspace. Skipping this best-effort update is safe: the
// complete transcript is already durable and can rebuild the capsule later.
func (e *Engine) cachedWebCapsuleStore(home string) *memory.Store {
	if !e.memoryMu.TryLock() {
		return nil
	}
	defer e.memoryMu.Unlock()
	if e.memoryClosed {
		return nil
	}
	if store := e.projectMemory[home]; store != nil {
		return store
	}
	if runtime.GOOS == "windows" {
		for observed, store := range e.projectMemory {
			if strings.EqualFold(observed, home) {
				return store
			}
		}
	}
	return nil
}

// webCapsuleText accepts the same text and parts representations as the archive.
// The excerpt reader uses it to skip unusable rows before choosing its tail.
func webCapsuleText(message session.Encoded) string {
	if message.Role != "user" && message.Role != "assistant" {
		return ""
	}
	decoded, err := message.ToMessage()
	if err != nil {
		return ""
	}
	return compactMemoryText(memory.StripReasoning(decoded.TextOnly().Content), 900)
}

func buildWebSessionCapsule(sessionID string, messages []session.Encoded) string {
	type line struct {
		role string
		text string
	}
	useful := make([]line, 0, 8)
	for _, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		text := webCapsuleText(message)
		if text == "" {
			continue
		}
		useful = append(useful, line{role: message.Role, text: text})
	}
	if len(useful) < 2 {
		return ""
	}
	firstUser := ""
	for _, item := range useful {
		if item.role == "user" {
			firstUser = item.text
			break
		}
	}
	if len(useful) > 8 {
		useful = useful[len(useful)-8:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Conversation %s", shortSessionID(sessionID))
	if firstUser != "" {
		b.WriteString(" — started: ")
		b.WriteString(compactMemoryText(firstUser, 500))
	}
	b.WriteString("\n")
	for _, item := range useful {
		label := "User"
		if item.role == "assistant" {
			label = "Assistant result"
		}
		fmt.Fprintf(&b, "%s: %s\n", label, item.text)
	}
	content := strings.TrimSpace(b.String())
	if len(content) > 4200 {
		content = content[len(content)-4200:]
		content = "Conversation " + shortSessionID(sessionID) + " — recent work:\n" + content
	}
	return content
}

// webRelevantSessionMemory retrieves only a few prior-session capsules using
// local FTS5. No embedding or model call is required on the foreground path.
// The current conversation is excluded so this block is genuinely
// cross-session context rather than a duplicate of the live transcript.
func (e *Engine) webRelevantSessionMemory(ctx context.Context, home, prompt, currentSessionID string, tokenCap int) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if tokenCap <= 0 {
		tokenCap = webSessionRecallTokens
	}
	_, project := e.webMemoryStores(home)
	if project != nil {
		if recalled := relevantSessionMemoryFromStore(project, prompt, currentSessionID, tokenCap); recalled != "" {
			return recalled
		}
	}
	// Existing installations may have years of sessions but no capsules yet.
	// Fall back to the already-open sessions.db FTS instead of running a
	// foreground backfill. New/updated conversations become capsules
	// automatically, so this path naturally gets colder over time.
	return e.relevantLegacySessions(ctx, home, prompt, currentSessionID, tokenCap)
}

func relevantSessionMemoryFromStore(project *memory.Store, prompt, currentSessionID string, tokenCap int) string {
	if project == nil {
		return ""
	}
	query := sessionRecallFTSQuery(prompt)
	var candidates []memory.Entry
	if query != "" {
		candidates, _ = project.Search(query, 16)
	}
	if len(candidates) == 0 && explicitPastRecall(prompt) {
		candidates, _ = project.Recent(memory.ScopeTaskLog, 8)
	}
	if len(candidates) == 0 {
		return ""
	}

	currentID := "web-session-" + strings.TrimSpace(currentSessionID)
	seen := map[string]bool{}
	var picked []memory.Entry
	for _, entry := range candidates {
		if entry.Scope != memory.ScopeTaskLog || entry.ID == currentID || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		picked = append(picked, entry)
		if len(picked) == 4 {
			break
		}
	}
	if len(picked) == 0 {
		return ""
	}

	texts := make([]string, 0, len(picked))
	for _, entry := range picked {
		texts = append(texts, entry.Content)
	}
	return renderSessionRecallTexts(texts, tokenCap)
}

func (e *Engine) relevantLegacySessions(ctx context.Context, home, prompt, currentSessionID string, tokenCap int) string {
	if e == nil {
		return ""
	}
	sessions, err := e.sessionStore()
	if err != nil {
		return ""
	}
	type candidate struct {
		id    string
		match string
	}
	candidates := make([]candidate, 0, 4)
	seen := map[string]bool{}
	query := sessionRecallFTSQuery(prompt)
	if query != "" {
		workspaces, workspaceErr := sessions.HistoryWorkspaces(ctx)
		if workspaceErr == nil {
			allowed := make([]string, 0, 1)
			for _, workspace := range workspaces {
				if sameSessionWorkspace(workspace, home) {
					allowed = append(allowed, workspace)
				}
			}
			hits, searchErr := sessions.SearchSessionMatches(ctx, query, allowed, currentSessionID, 4)
			if searchErr == nil {
				for _, hit := range hits {
					seen[hit.SessionID] = true
					candidates = append(candidates, candidate{id: hit.SessionID, match: stripFTSMarks(hit.Snippet)})
				}
			}
		}
	}
	if len(candidates) == 0 && explicitPastRecall(prompt) {
		recent, recentErr := sessions.ListRecentByCwd(ctx, home, 8)
		if recentErr == nil {
			for _, item := range recent {
				if item.ID == currentSessionID || seen[item.ID] {
					continue
				}
				seen[item.ID] = true
				candidates = append(candidates, candidate{id: item.ID, match: compactMemoryText(item.FirstUserMsg, 500)})
				if len(candidates) == 4 {
					break
				}
			}
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	texts := make([]string, 0, len(candidates))
	for _, item := range candidates {
		messages, readErr := sessions.ReadDialogueExcerpt(ctx, item.id, 8, func(message session.Encoded) bool {
			return webCapsuleText(message) != ""
		})
		if readErr != nil {
			continue
		}
		capsule := buildWebSessionCapsule(item.id, messages)
		if item.match != "" {
			if capsule == "" {
				capsule = "Conversation " + shortSessionID(item.id)
			}
			capsule = "Matched earlier context: " + compactMemoryText(item.match, 600) + "\n" + capsule
		}
		if capsule != "" {
			texts = append(texts, capsule)
		}
	}
	return renderSessionRecallTexts(texts, tokenCap)
}

func renderSessionRecallTexts(texts []string, tokenCap int) string {
	if len(texts) == 0 || tokenCap <= 0 {
		return ""
	}
	const header = "[relevant_previous_sessions]\nLocal recall from other conversations. Use it only when relevant; current-session messages take precedence.\n"
	var b strings.Builder
	b.WriteString(header)
	used := memory.EstimateTokens(header)
	added := 0
	for _, text := range texts {
		line := "- " + compactSessionRecallText(text) + "\n"
		cost := memory.EstimateTokens(line)
		if used+cost > tokenCap {
			continue
		}
		b.WriteString(line)
		used += cost
		added++
		if added == 4 {
			break
		}
	}
	if added == 0 {
		return ""
	}
	b.WriteString("[/relevant_previous_sessions]")
	return b.String()
}

// Keep both the task/match at the beginning and the latest dialogue at the end.
// A long opening message must not consume the entire prior-session preview.
func compactSessionRecallText(text string) string {
	const maxBytes = 720
	const omission = " … "
	text = compactMemoryText(text, 0)
	if len(text) <= maxBytes {
		return text
	}
	head := (maxBytes - len(omission)) / 2
	tail := len(text) - (maxBytes - len(omission) - head)
	for head > 0 && !utf8.RuneStart(text[head]) {
		head--
	}
	for tail < len(text) && !utf8.RuneStart(text[tail]) {
		tail++
	}
	return strings.TrimSpace(text[:head]) + omission + strings.TrimSpace(text[tail:])
}

func stripFTSMarks(s string) string {
	s = strings.ReplaceAll(s, "<mark>", "")
	s = strings.ReplaceAll(s, "</mark>", "")
	return compactMemoryText(s, 800)
}

func compactMemoryText(text string, max int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" || max <= 0 || len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut]) + "…"
}

func shortSessionID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 10 {
		return id
	}
	return id[:10]
}

var sessionRecallStopWords = map[string]bool{
	"jest": true, "jako": true, "tego": true, "tam": true, "tutaj": true,
	"czy": true, "się": true, "sie": true, "mam": true, "masz": true,
	"może": true, "mozesz": true, "możesz": true, "zrobić": true, "zrobic": true,
	"teraz": true, "jeszcze": true, "który": true, "ktory": true, "które": true,
	"było": true, "bylo": true, "będzie": true, "bedzie": true, "tak": true,
	"nie": true, "dla": true, "ale": true, "jak": true, "oraz": true,
	"the": true, "and": true, "with": true, "this": true, "that": true,
	"have": true, "what": true, "from": true, "about": true,
}

func sessionRecallFTSQuery(prompt string) string {
	parts := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != '-'
	})
	seen := map[string]bool{}
	terms := make([]string, 0, 8)
	for _, part := range parts {
		part = strings.Trim(part, "-_ ")
		if utf8.RuneCountInString(part) < 4 || sessionRecallStopWords[part] || seen[part] {
			continue
		}
		seen[part] = true
		terms = append(terms, `"`+strings.ReplaceAll(part, `"`, `""`)+`"`)
		if len(terms) == 8 {
			break
		}
	}
	return strings.Join(terms, " OR ")
}

func explicitPastRecall(prompt string) bool {
	p := strings.ToLower(prompt)
	for _, needle := range []string{
		"wcześniej", "wczesniej", "poprzednio", "ostatnio", "inna sesj", "innej sesj",
		"innych sesj", "pamiętasz", "pamietasz", "robiliśmy", "robilismy", "robiłeś", "robiles",
		"previous session", "other session", "remember when", "last time",
	} {
		if strings.Contains(p, needle) {
			return true
		}
	}
	return false
}
