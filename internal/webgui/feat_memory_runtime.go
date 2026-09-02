package webgui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
)

// webMemoryKeeper opens the appropriate SQLite memory store for one operation.
// Web runs are short-lived and created per request, so keeping a raw *Store on
// the tool would either leak handles or close it before the model can call it.
type webMemoryKeeper struct {
	dataDir string
	home    string
	global  bool
}

func (k webMemoryKeeper) open() (*memory.Store, error) {
	if k.global {
		return memory.OpenStore(k.dataDir)
	}
	return memory.OpenProjectStore(k.dataDir, k.home)
}

func (k webMemoryKeeper) Put(entry memory.Entry) error {
	store, err := k.open()
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Put(entry)
}

func (k webMemoryKeeper) Search(query string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.Search(query, limit)
}

func (k webMemoryKeeper) Recent(scope string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.Recent(scope, limit)
}

func (k webMemoryKeeper) HybridSearch(ctx context.Context, query string, limit int) ([]memory.Entry, error) {
	store, err := k.open()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.HybridSearch(ctx, query, limit)
}

func (e *Engine) webMemoryStores(home string) (globalStore, projectStore *memory.Store) {
	if e == nil {
		return nil, nil
	}
	home = filepath.Clean(strings.TrimSpace(home))
	e.memoryMu.Lock()
	defer e.memoryMu.Unlock()
	if e.memoryClosed {
		return nil, nil
	}
	if e.globalMemory == nil {
		if store, err := memory.OpenStore(e.dataDir); err == nil {
			e.globalMemory = store
		}
	}
	if e.projectMemory == nil {
		e.projectMemory = make(map[string]*memory.Store)
	}
	if home != "" && e.projectMemory[home] == nil {
		if store, err := memory.OpenProjectStore(e.dataDir, home); err == nil {
			e.projectMemory[home] = store
		}
	}
	return e.globalMemory, e.projectMemory[home]
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

func saveWebUserFacts(dataDir, prompt string) {
	globalStore, err := memory.OpenStore(dataDir)
	if err != nil {
		return
	}
	defer globalStore.Close()
	saver := &memory.AutoSaver{Global: globalStore}
	saver.SaveDeterministicUserFacts([]string{prompt})
}

const webSessionRecallTokens = 420

// saveWebSessionCapsule keeps one compact, deterministic task-log record per
// conversation. It deliberately makes no LLM call: all text already exists in
// the session DB, so cross-session memory adds only a small local SQLite read
// and one upsert after a turn completes.
func (e *Engine) saveWebSessionCapsule(ctx context.Context, sessionID string) {
	if e == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	sessions, err := e.sessionStore()
	if err != nil {
		return
	}
	messages, err := sessions.ReadMessages(ctx, sessionID)
	if err != nil || len(messages) == 0 {
		return
	}

	content := buildWebSessionCapsule(sessionID, messages)
	if content == "" {
		return
	}

	_, project := e.webMemoryStores(e.Home())
	if project == nil {
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
	if old, getErr := project.Get(id); getErr == nil {
		entry.CreatedAt = old.CreatedAt
	}
	_, _ = project.Retain(memory.ScopeTaskLog, memory.MaxTaskLogEntries-1)
	if project.Put(entry) == nil {
		_, _ = project.Retain(memory.ScopeTaskLog, memory.MaxTaskLogEntries)
	}
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
		// Assistant replies produced by the live agent are stored losslessly in
		// PartsJSON (so text and image parts can coexist); older/manual rows often
		// use Content. Decode both representations before building the capsule.
		// Reading only Content made normal WebGUI conversations look like a
		// one-message session and silently skipped cross-session memory.
		decoded, err := message.ToMessage()
		if err != nil {
			continue
		}
		text := compactMemoryText(memory.StripReasoning(decoded.TextOnly().Content), 900)
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
		hits, searchErr := sessions.SearchHistory(ctx, query, "", "", time.Time{}, time.Time{}, 32)
		if searchErr == nil {
			for _, hit := range hits {
				if hit.SessionID == currentSessionID || seen[hit.SessionID] {
					continue
				}
				meta, getErr := sessions.Get(hit.SessionID)
				if getErr != nil || !sameSessionWorkspace(meta.Cwd, home) {
					continue
				}
				seen[hit.SessionID] = true
				candidates = append(candidates, candidate{id: hit.SessionID, match: stripFTSMarks(hit.Snippet)})
				if len(candidates) == 4 {
					break
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
		messages, _, readErr := sessions.ReadMessagesBefore(ctx, item.id, 0, 16)
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
		line := "- " + compactMemoryText(text, 720) + "\n"
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
