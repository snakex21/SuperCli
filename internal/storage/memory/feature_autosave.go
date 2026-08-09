package memory

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// AutoSaver is the code-level guarantee behind the "remember
// after each task" instruction in the system prompt: if the model
// finished a session without saving anything, the saver generates
// a one-call summary itself and writes it as a task-log entry,
// then refreshes the project's card in the global store.
type AutoSaver struct {
	Project     *Store
	Global      *Store
	ProjectPath string

	rememberCalls atomic.Int64
}

// NoteRemember is called by the remember tool every time the model
// saves something. A session with at least one save skips the
// synthetic summary (the model already did its job).
func (a *AutoSaver) NoteRemember() {
	if a == nil {
		return
	}
	a.rememberCalls.Add(1)
}

// Remembered reports whether the model called remember at least
// once this session (synthetic summaries are skipped then).
func (a *AutoSaver) Remembered() bool {
	return a != nil && a.rememberCalls.Load() > 0
}

// summaryPrompt is the auto-save summarization instruction. The
// USER-facts part is deliberately phrased as an unconditional,
// separate task: short personal declarations ("lubię komputery",
// "mam na imię Maks") must be extracted EVEN when the session
// contains no coding work and the summary itself is NOTHING.
const summaryPrompt = "Summarize this session in 2-4 short lines: WHAT was done, " +
	"WHY, and which files were touched. Plain text, no markdown, no preamble. " +
	"If no meaningful work happened, the summary is exactly: NOTHING.\n" +
	"SEPARATELY AND ALWAYS — even when the summary is NOTHING — scan the " +
	"user's messages for durable personal facts or preferences: their name, " +
	"what they like or dislike, preferred language, communication style, " +
	"standing preferences. Short casual declarations count (\"I like " +
	"computers\", \"my name is Anna\", in any language). Append one line per " +
	"fact, each starting with exactly \"USER: \" (example: USER: The user's " +
	"name is Anna. / USER: The user likes computers.). Write the facts in " +
	"English. Only durable facts about the user — no session details. " +
	"If there are no such facts, append no USER: lines."

// SummarizeFunc produces a short summary for the given prompt with
// a single LLM call. Wire it to the active provider.
type SummarizeFunc func(ctx context.Context, prompt string) (string, error)

// Finalize runs at session end (and may be called every N
// messages — it is idempotent per call site). transcript is a
// plain-text tail of the conversation; empty transcripts are
// skipped. The summarize call is bounded by the passed context.
func (a *AutoSaver) Finalize(ctx context.Context, transcript string, summarize SummarizeFunc) {
	if a == nil {
		return
	}
	// Always bump the card's last-session stamp.
	defer RefreshCard(a.Global, a.ProjectPath, "", "active")

	if a.rememberCalls.Load() > 0 {
		return // the model saved its own notes this session
	}
	a.StoreSummary(ctx, transcript, summarize)
}

// StoreSummary summarizes one transcript fragment with a single
// LLM call and stores the result as a task-log entry. It is the
// shared engine behind Finalize (session end) and the incremental
// background saver (after each agent turn). Returns true when the
// fragment is "consumed" — stored, judged NOTHING, or empty — and
// false when the summarize call failed (caller may retry with the
// same fragment later).
func (a *AutoSaver) StoreSummary(ctx context.Context, transcript string, summarize SummarizeFunc) bool {
	if a == nil {
		return true
	}
	// Never feed (or store) raw model reasoning: providers wrap
	// reasoning streams in <thinking>...</thinking> and those
	// blocks must not leak into summaries or saved entries.
	transcript = strings.TrimSpace(StripReasoning(transcript))
	if transcript == "" || summarize == nil || a.Project == nil {
		return true
	}
	prompt := summaryPrompt + "\n\n" + transcript
	summary, err := summarize(ctx, prompt)
	if err != nil {
		return false
	}
	summary = strings.TrimSpace(StripReasoning(summary))
	if summary == "" || len(summary) > 8000 {
		return true
	}
	summary, userFacts := splitUserFacts(summary)
	a.saveUserFacts(userFacts)
	if summary == "" || strings.EqualFold(summary, "NOTHING") || len(summary) > 4000 {
		return true
	}
	now := time.Now()
	// Automatic journals are a rolling working set, not an append-only audit
	// log. Keep their size bounded before and after the write so a long-running
	// project can never grow memory into a multi-gigabyte prompt/index file.
	_, _ = a.Project.Retain(ScopeTaskLog, MaxTaskLogEntries-1)
	if err := a.Project.Put(Entry{
		ID:      fmt.Sprintf("log-%x", now.UnixNano()),
		Scope:   ScopeTaskLog,
		Content: summary,
		Source:  SourceAgent,
	}); err != nil {
		return false
	}
	_, _ = a.Project.Retain(ScopeTaskLog, MaxTaskLogEntries)
	// Use the first line of the summary as the card description.
	first := summary
	if i := strings.IndexByte(first, '\n'); i > 0 {
		first = first[:i]
	}
	RefreshCard(a.Global, a.ProjectPath, first, "active")
	return true
}

// StoreRawTail stores the un-summarized transcript tail verbatim
// as a raw-log entry. It makes NO LLM call — it is the emergency
// path for abrupt termination (console window closed via the X,
// CTRL_CLOSE_EVENT gives ~5s). The entry is summarized into a
// normal task-log entry at the next startup.
func (a *AutoSaver) StoreRawTail(transcript string) {
	if a == nil || a.Project == nil {
		return
	}
	transcript = strings.TrimSpace(StripReasoning(transcript))
	if transcript == "" {
		return
	}
	const maxRaw = 8000
	if len(transcript) > maxRaw {
		transcript = transcript[len(transcript)-maxRaw:]
	}
	_, _ = a.Project.Retain(ScopeRawLog, MaxRawLogEntries-1)
	if err := a.Project.Put(Entry{
		ID:      fmt.Sprintf("raw-%x", time.Now().UnixNano()),
		Scope:   ScopeRawLog,
		Content: transcript,
		Source:  SourceAgent,
	}); err == nil {
		_, _ = a.Project.Retain(ScopeRawLog, MaxRawLogEntries)
	}
}

// SummarizePendingRaw summarizes raw-log entries left behind by an
// abrupt shutdown into normal task-log entries (extracting USER:
// facts on the way) and deletes the raw entries. Call it in the
// background at startup — it makes one LLM call per pending entry.
func (a *AutoSaver) SummarizePendingRaw(ctx context.Context, summarize SummarizeFunc) {
	if a == nil || a.Project == nil || summarize == nil {
		return
	}
	entries, err := a.Project.Recent(ScopeRawLog, 10)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !a.StoreSummary(ctx, e.Content, summarize) {
			continue // summarize failed — keep the raw entry for next time
		}
		_ = a.Project.Delete(e.ID)
	}
}

// splitUserFacts separates trailing "USER: ..." lines from the
// task-log summary. Returns the cleaned summary and the facts.
func splitUserFacts(summary string) (string, []string) {
	var rest, facts []string
	for _, line := range strings.Split(summary, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "USER:") {
			if f := strings.TrimSpace(strings.TrimPrefix(t, "USER:")); f != "" {
				facts = append(facts, f)
			}
			continue
		}
		rest = append(rest, line)
	}
	return strings.TrimSpace(strings.Join(rest, "\n")), facts
}

// saveUserFacts writes durable user facts to the GLOBAL store as
// preference entries (the briefing always shows global
// preferences), skipping junk facts and facts that already have
// an identical or semantically near-identical entry.
func (a *AutoSaver) saveUserFacts(facts []string) {
	if a == nil || a.Global == nil || len(facts) == 0 {
		return
	}
	existing := map[string]struct{}{}
	var existingRaw []string
	for _, scope := range []string{ScopePreference, ScopeFact} {
		if entries, err := a.Global.Recent(scope, 100); err == nil {
			for _, e := range entries {
				existing[normalizeFact(e.Content)] = struct{}{}
				existingRaw = append(existingRaw, e.Content)
			}
		}
	}
	for _, f := range facts {
		f = strings.TrimSpace(f)
		if f == "" || len(f) > 500 {
			continue
		}
		if junkFact(f) {
			continue // "not explicitly stated", gratitude, guesses — never durable
		}
		norm := normalizeFact(f)
		if _, dup := existing[norm]; dup {
			continue
		}
		if containsSimilar(existingRaw, f) {
			continue
		}
		_ = a.Global.Put(Entry{
			ID:      fmt.Sprintf("pref-%x", time.Now().UnixNano()),
			Scope:   ScopePreference,
			Content: f,
			Source:  SourceAgent,
		})
		existing[norm] = struct{}{}
		existingRaw = append(existingRaw, f)
	}
}

// normalizeFact lower-cases and strips punctuation/whitespace so
// "The user's name is Anna." == "the users name is anna".
func normalizeFact(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// containsSimilar reports whether any existing raw fact text is
// semantically near-identical to the candidate — a cheap
// token-overlap check that needs no embeddings. Delegates the
// actual similarity rule to similarFact (feature_tidy.go) so the
// autosave and the on-demand tidy sweep never drift apart.
func containsSimilar(existingRaw []string, raw string) bool {
	for _, e := range existingRaw {
		if similarFact(e, raw) {
			return true
		}
	}
	return false
}

// junkFact reports whether a candidate fact is not durable enough
// to store: the model explicitly said it does not know, it is a
// guess, or it is conversational filler (gratitude) rather than a
// standing fact about the user.
func junkFact(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	for _, frag := range []string{
		"not explicitly stated", "not stated", "not mentioned",
		"not provided", "not specified", "not known", "not clear",
		"unknown", "unable to", "could not determine",
		"cannot determine", "cannot be determined", "no information",
		"no mention", "appears to", "seems to", "likely", "probably",
		"maybe", "perhaps", "possibly", "gratitude", "thanks",
		"thank you", "dziękuję", "dziekuje", "thx", "welcome",
	} {
		if strings.Contains(t, frag) {
			return true
		}
	}
	// Pure filler like "The user expressed gratitude with ..." or
	// trailing sign-off phrases are never durable facts.
	if len(factTokens(t)) == 0 {
		return true
	}
	return false
}

// factStopwords are the words that carry no identifying meaning
// for a short fact ("the user prefers Polish" == "user prefers
// Polish"). Kept deliberately small: over-filtering would merge
// genuinely distinct facts ("works on SuperCli" vs "works on Go").
var factStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"has": true, "have": true, "had": true, "does": true, "do": true,
	"to": true, "in": true, "on": true, "at": true, "of": true,
	"for": true, "and": true, "or": true, "but": true, "with": true,
	"from": true, "by": true, "as": true, "it": true, "this": true,
	"that": true, "their": true, "his": true, "her": true, "they": true,
	"he": true, "she": true, "we": true, "you": true, "i": true,
	"my": true, "me": true, "our": true, "us": true, "user": true,
	"users": true, "name": true, "communicates": true,
	// Modal / hedge words carry no identifying meaning either:
	// "prefers to communicate in Polish" == "communicates primarily
	// in Polish" — only the topic word "polish" identifies the fact.
	"prefers": true, "preferred": true, "prefer": true,
	"primarily": true, "mainly": true, "mostly": true,
	"generally": true, "usually": true,
	// Polish equivalents of the common-filler set.
	"użytkownik": true, "uzytkownik": true, "ma": true, "na": true,
	"imię": true, "imie": true, "w": true, "z": true,
	"że": true, "ze": true, "się": true, "sie": true,
	"jest": true, "dla": true, "po": true, "jak": true,
}

// factTokens splits a normalized fact into its identifying words:
// lower-cased, non-alphanumeric stripped, stopwords removed,
// tokens shorter than 3 chars dropped. "theusersnameismaks"
// becomes [maks].
func factTokens(norm string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		t := strings.TrimSpace(cur.String())
		cur.Reset()
		if len(t) < 3 || factStopwords[t] {
			return
		}
		out = append(out, t)
	}
	for _, r := range strings.ToLower(norm) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}
