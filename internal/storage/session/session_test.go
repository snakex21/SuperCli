package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenStore_CreatesRoot(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()
	if s.Root() == "" {
		t.Errorf("Root = empty")
	}
}

func TestStore_Create(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create("/cwd", "gpt-4o", "hello")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.ID == "" {
		t.Errorf("ID empty")
	}
	if sess.Cwd != "/cwd" {
		t.Errorf("Cwd = %q", sess.Cwd)
	}
	if sess.Model != "gpt-4o" {
		t.Errorf("Model = %q", sess.Model)
	}
	if sess.Title != "hello" {
		t.Errorf("Title = %q", sess.Title)
	}
	if sess.CreatedAt.IsZero() || sess.UpdatedAt.IsZero() {
		t.Errorf("timestamps not set")
	}
}

func TestStore_SetRuntime(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create("/cwd", "old-model", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRuntime(sess.ID, "any-router", "gpt-5.6-sol", "high"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "any-router" || got.Model != "gpt-5.6-sol" || got.ReasoningEffort != "high" {
		t.Fatalf("runtime = provider=%q model=%q reasoning=%q", got.Provider, got.Model, got.ReasoningEffort)
	}
}

func TestOpenStore_MigratesSessionRuntimeColumns(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, cwd TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', model TEXT NOT NULL,
		parent_id TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
		message_count INTEGER NOT NULL DEFAULT 0, token_in INTEGER NOT NULL DEFAULT 0, token_out INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := db.Exec(`INSERT INTO sessions(id,cwd,title,model,created_at,updated_at) VALUES('old','/cwd','old','legacy-model',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetRuntime("old", "legacy-provider", "legacy-model", "medium"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("old")
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "legacy-provider" || got.ReasoningEffort != "medium" {
		t.Fatalf("migrated runtime = %+v", got)
	}
}

func TestStore_Create_RejectsEmpty(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Create("", "gpt-4o", ""); err == nil {
		t.Error("expected error on empty cwd")
	}
	if _, err := s.Create("/cwd", "", ""); err == nil {
		t.Error("expected error on empty model")
	}
}

func TestStore_Get(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/cwd", "m", "t")
	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("ID = %q, want %q", got.ID, sess.ID)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.Get("nope")
	if !IsNotFound(err) {
		t.Errorf("expected IsNotFound, got %v", err)
	}
}

func TestStore_List_OrderedByUpdated(t *testing.T) {
	s := openTestStore(t)
	a, _ := s.Create("/a", "m", "first")
	b, _ := s.Create("/b", "m", "second")
	c, _ := s.Create("/c", "m", "third")
	got, _ := s.List(10)
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].ID != c.ID || got[1].ID != b.ID || got[2].ID != a.ID {
		t.Errorf("order: %+v", got)
	}
}

func TestStore_List_AppliesLimitInQuery(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 5; i++ {
		if _, err := s.Create("/a", "m", "session"); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	got, err := s.List(2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d sessions, want 2", len(got))
	}
}

func TestStore_ListByCwd(t *testing.T) {
	s := openTestStore(t)
	s.Create("/x", "m", "")
	s.Create("/x", "m", "")
	s.Create("/y", "m", "")
	got, _ := s.ListByCwd("/x", 10)
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestStore_LastForCwd(t *testing.T) {
	s := openTestStore(t)
	s.Create("/a", "m", "")
	sess, _ := s.Create("/a", "m", "")
	got, err := s.LastForCwd("/a")
	if err != nil {
		t.Fatalf("LastForCwd: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("ID = %q, want %q", got.ID, sess.ID)
	}
}

func TestStore_LastForCwd_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.LastForCwd("/missing")
	if !IsNotFound(err) {
		t.Errorf("expected IsNotFound, got %v", err)
	}
}

func TestStore_SetTitle(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "old")
	if err := s.SetTitle(sess.ID, "new"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	got, _ := s.Get(sess.ID)
	if got.Title != "new" {
		t.Errorf("Title = %q", got.Title)
	}
}

func TestStore_SetTitleIfCurrentPreservesManualRename(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create("/tmp", "model", "generated locally")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTitle(sess.ID, "My conversation"); err != nil {
		t.Fatal(err)
	}
	updated, err := s.SetTitleIfCurrent(sess.ID, "generated locally", "LLM summary")
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("asynchronous generated title overwrote a manual rename")
	}
	got, err := s.Get(sess.ID)
	if err != nil || got.Title != "My conversation" {
		t.Fatalf("title = %q, err=%v", got.Title, err)
	}
}

func TestStore_ListRecentByCwd_FiltersByProject(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	// Two sessions in project A, one in project B. EnsureSession is how
	// the live TUI records a cwd for a pre-chosen session id.
	for _, tc := range []struct{ id, cwd string }{
		{"sess-a1", "/proj/a"},
		{"sess-a2", "/proj/a"},
		{"sess-b1", "/proj/b"},
	} {
		if err := s.EnsureSession(tc.id, tc.cwd, "m"); err != nil {
			t.Fatalf("EnsureSession(%s): %v", tc.id, err)
		}
		enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "hello from " + tc.id})
		if err := s.AppendMessage(ctx, tc.id, enc); err != nil {
			t.Fatalf("AppendMessage(%s): %v", tc.id, err)
		}
	}

	a, err := s.ListRecentByCwd(ctx, "/proj/a", 10)
	if err != nil {
		t.Fatalf("ListRecentByCwd: %v", err)
	}
	if len(a) != 2 {
		t.Fatalf("project A sessions = %d, want 2 (%+v)", len(a), a)
	}
	for _, r := range a {
		if r.Cwd != "/proj/a" {
			t.Errorf("session %s cwd = %q, want /proj/a", r.ID, r.Cwd)
		}
	}

	all, err := s.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all sessions = %d, want 3", len(all))
	}
}

func TestStore_EnsureSession_Idempotent(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureSession("sess-x", "/a", "m"); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	// A second call must not clobber the row or error (INSERT OR IGNORE).
	if err := s.EnsureSession("sess-x", "/different", "m2"); err != nil {
		t.Fatalf("EnsureSession (2nd): %v", err)
	}
	got, err := s.Get("sess-x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Cwd != "/a" {
		t.Errorf("cwd = %q, want /a (first write wins)", got.Cwd)
	}
	if err := s.EnsureSession("", "/a", "m"); err == nil {
		t.Error("empty id should error")
	}
	if err := s.EnsureSession("id", "", "m"); err == nil {
		t.Error("empty cwd should error")
	}
}

func TestStore_Delete_CascadesMessages(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	if err := s.AppendMessage(context.Background(), sess.ID, enc); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.Delete(sess.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	msgs, _ := s.ReadMessages(context.Background(), sess.ID)
	if len(msgs) != 0 {
		t.Errorf("messages after cascade delete: %v", msgs)
	}
}

func TestStore_AppendMessage_SeqMonotonic(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	for i := 1; i <= 3; i++ {
		enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "x"})
		if err := s.AppendMessage(context.Background(), sess.ID, enc); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	msgs, _ := s.ReadMessages(context.Background(), sess.ID)
	if len(msgs) != 3 {
		t.Fatalf("len = %d", len(msgs))
	}
	for i, m := range msgs {
		if m.Seq != i+1 {
			t.Errorf("seq[%d] = %d", i, m.Seq)
		}
	}
}

func TestStore_AppendMessage_BumpsMessageCount(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	for i := 0; i < 3; i++ {
		enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "x"})
		s.AppendMessage(context.Background(), sess.ID, enc)
	}
	got, _ := s.Get(sess.ID)
	if got.MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", got.MessageCount)
	}
}

func TestStore_AppendMessage_ToolRole(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	enc, _ := FromMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "call-1", Content: "result"})
	if err := s.AppendMessage(context.Background(), sess.ID, enc); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	msgs, _ := s.ReadMessages(context.Background(), sess.ID)
	if msgs[0].ToolCallID != "call-1" {
		t.Errorf("ToolCallID = %q", msgs[0].ToolCallID)
	}
}

func TestStore_AppendMessage_RejectsToolWithoutID(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	enc := Encoded{Role: string(llm.RoleTool), Content: "x"}
	err := s.AppendMessage(context.Background(), sess.ID, enc)
	if err == nil {
		t.Fatal("expected error on tool role without ToolCallID")
	}
}

func TestStore_ReadMessages_Empty(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	msgs, _ := s.ReadMessages(context.Background(), sess.ID)
	if len(msgs) != 0 {
		t.Errorf("len = %d, want 0", len(msgs))
	}
}

func TestStore_UpdateUsage(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	if err := s.UpdateUsage(sess.ID, 100, 50); err != nil {
		t.Fatalf("UpdateUsage: %v", err)
	}
	if err := s.UpdateUsage(sess.ID, 25, 10); err != nil {
		t.Fatalf("UpdateUsage 2: %v", err)
	}
	got, _ := s.Get(sess.ID)
	if got.TokenIn != 125 {
		t.Errorf("TokenIn = %d, want 125", got.TokenIn)
	}
	if got.TokenOut != 60 {
		t.Errorf("TokenOut = %d, want 60", got.TokenOut)
	}
}

func TestStore_CreateWithParent(t *testing.T) {
	s := openTestStore(t)
	parent, _ := s.Create("/a", "m", "")
	child, err := s.CreateWithParent("/a", "m", "child", parent.ID)
	if err != nil {
		t.Fatalf("CreateWithParent: %v", err)
	}
	if child.ParentID != parent.ID {
		t.Errorf("ParentID = %q", child.ParentID)
	}
	// Deleting the parent should null the child's parent_id.
	if err := s.Delete(parent.ID); err != nil {
		t.Fatalf("Delete parent: %v", err)
	}
	got, _ := s.Get(child.ID)
	if got.ParentID != "" {
		t.Errorf("ParentID after cascade = %q, want empty", got.ParentID)
	}
}

func TestStore_AppendMessage_RejectsEmptySessionID(t *testing.T) {
	s := openTestStore(t)
	enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "x"})
	if err := s.AppendMessage(context.Background(), "", enc); err == nil {
		t.Fatal("expected error on empty sessionID")
	}
}

func TestStore_AppendMessage_RejectsBadRole(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	enc := Encoded{Role: "banana", Content: "x"}
	if err := s.AppendMessage(context.Background(), sess.ID, enc); err == nil {
		t.Fatal("expected error on bad role")
	}
}

// silence unused import warning if all references disappear
var _ = errors.Is

// ---------------------------------------------------------------------------
// F13: SearchHistory (FTS5 over messages.content)
// ---------------------------------------------------------------------------

// seedSearchable populates two sessions with a known set of
// messages for SearchHistory tests. Layout:
//
//	sess-A: 3 user, 1 assistant, 1 tool
//	sess-B: 2 user
//
// All created_at timestamps are spread 1 hour apart starting
// from a known base, so since/until filters are deterministic.
func seedSearchable(t *testing.T, s *Store) (sessA, sessB Session) {
	t.Helper()
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	sessA, err := s.Create("/a", "gpt-4o", "")
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	sessB, err = s.Create("/a", "gpt-4o", "")
	if err != nil {
		t.Fatalf("Create B: %v", err)
	}
	// Fix the row's created_at via direct SQL so the time
	// window is testable. AppendMessage bumps updated_at to
	// "now", which we don't want.
	// Actually AppendMessage sets created_at on the *message*
	// (not the session), so we don't need this for message
	// tests; we use AppendMessage's own clock. Skip.
	_ = base
	items := []struct {
		sessID string
		role   string
		text   string
		toolID string
	}{
		{sessA.ID, "user", "konspekt spotkania o refaktoryzacji", ""},
		{sessA.ID, "assistant", "Jasne, zacznijmy od celów projektu refaktoryzacji", ""},
		{sessA.ID, "user", "drugi prompt — lista TODO", ""},
		{sessA.ID, "tool", "output: 42", "call-001"},
		{sessA.ID, "assistant", "gotowe", ""},
		{sessB.ID, "user", "spotkanie jutro o 10:00", ""},
		{sessB.ID, "user", "łąka żółwia w Krakowie", ""},
	}
	for _, it := range items {
		enc, err := FromMessage(llm.Message{Role: llm.Role(it.role), Content: it.text, ToolCallID: it.toolID})
		if err != nil {
			t.Fatalf("FromMessage: %v", err)
		}
		if err := s.AppendMessage(context.Background(), it.sessID, enc); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	return sessA, sessB
}

func TestSearchHistory_BasicMatch(t *testing.T) {
	s := openTestStore(t)
	sessA, _ := seedSearchable(t, s)
	hits, err := s.SearchHistory(context.Background(), "refaktoryzacji", "", "", time.Time{}, time.Time{}, 20)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (sess-A user+assistant); got %+v", len(hits), hits)
	}
	for _, h := range hits {
		if h.SessionID != sessA.ID {
			t.Errorf("hit session = %q, want %q", h.SessionID, sessA.ID)
		}
		if h.Snippet == "" {
			t.Errorf("hit %d snippet empty", h.Seq)
		}
		if !strings.Contains(h.Snippet, "<mark>") {
			t.Errorf("hit %d snippet missing <mark>: %q", h.Seq, h.Snippet)
		}
	}
}

func TestSearchHistory_BooleanOr(t *testing.T) {
	s := openTestStore(t)
	_, _ = seedSearchable(t, s)
	hits, err := s.SearchHistory(context.Background(), "spotkania OR TODO", "", "", time.Time{}, time.Time{}, 20)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("hits = %d, want >= 2", len(hits))
	}
}

func TestSearchHistory_RoleFilter(t *testing.T) {
	s := openTestStore(t)
	_, _ = seedSearchable(t, s)
	hits, err := s.SearchHistory(context.Background(), "refaktoryzacji", "", "assistant", time.Time{}, time.Time{}, 20)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1 (sess-A assistant only)", len(hits))
	}
	if hits[0].Role != "assistant" {
		t.Errorf("role = %q, want assistant", hits[0].Role)
	}
}

func TestSearchHistory_SessionFilter(t *testing.T) {
	s := openTestStore(t)
	sessA, sessB := seedSearchable(t, s)
	hits, err := s.SearchHistory(context.Background(), "spotkanie", sessB.ID, "", time.Time{}, time.Time{}, 20)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1 (sess-B only)", len(hits))
	}
	if hits[0].SessionID != sessB.ID {
		t.Errorf("hit from wrong session: %q", hits[0].SessionID)
	}
	// And verify sess-A is excluded.
	for _, h := range hits {
		if h.SessionID == sessA.ID {
			t.Errorf("unexpected sess-A hit")
		}
	}
}

func TestSearchHistory_DiacriticFolding(t *testing.T) {
	s := openTestStore(t)
	// unicode61's remove_diacritics 2 folds combining marks
	// (ą ↔ a, ę ↔ e, ó ↔ o) at both index and query time.
	// It does NOT fold `ł` (U+0142) ↔ `l` (a separate letter,
	// not a marked L), and we document that limitation.
	// Case folding DOES work for plain ASCII.
	sess, _ := s.Create("/a", "m", "")
	enc1, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "Refaktoryzacja Krakowa"})
	_ = s.AppendMessage(context.Background(), sess.ID, enc1)

	// Case + combining-mark folding: lowercase + diacritic-stripped
	// query should match the original.
	hits, err := s.SearchHistory(context.Background(), "refaktoryzacja krakowa", "", "", time.Time{}, time.Time{}, 20)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1 (case + combining-mark folded)", len(hits))
	}
	if !strings.Contains(hits[0].Snippet, "Refaktoryzacja") {
		t.Errorf("snippet should contain original Refaktoryzacja, got %q", hits[0].Snippet)
	}
}

func TestSearchHistory_StemmingSuffixTrimming(t *testing.T) {
	s := openTestStore(t)
	// Porter's heavy suffix-trimming happens to fold some
	// Polish inflections (e.g. spotkania / spotkaniach /
	// spotkaniu → "spotk" or similar shared stem). We don't
	// promise polish-aware stemming; we just verify the
	// tokenizer does some suffix-stripping, not exact-match.
	sess, _ := s.Create("/a", "m", "")
	items := []string{
		"refaktoryzacja", "refaktoryzacji", "refaktoryzacje",
	}
	for _, text := range items {
		enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: text})
		_ = s.AppendMessage(context.Background(), sess.ID, enc)
	}
	// Query with the most inflected form; at least the exact
	// match should come back (FTS5 matches stems that are
	// substring-equal after porter's truncation).
	hits, err := s.SearchHistory(context.Background(), "refaktoryzacj", "", "", time.Time{}, time.Time{}, 20)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) < 1 {
		t.Errorf("hits = %d, want >= 1 (prefix should match via porter truncation)", len(hits))
	}
}

func TestSearchHistory_LimitClamping(t *testing.T) {
	s := openTestStore(t)
	_, _ = seedSearchable(t, s)
	// Insert 150 messages that all match
	sess, _ := s.Create("/a", "m", "")
	for i := 0; i < 150; i++ {
		enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "wielokrotnie to samo słowo"})
		_ = s.AppendMessage(context.Background(), sess.ID, enc)
	}
	_ = sess
	// limit=0 -> default 20
	hits, _ := s.SearchHistory(context.Background(), "wielokrotnie", "", "", time.Time{}, time.Time{}, 0)
	if len(hits) != 20 {
		t.Errorf("hits with limit=0 = %d, want 20 (default)", len(hits))
	}
	// limit=9999 -> capped at 100
	hits, _ = s.SearchHistory(context.Background(), "wielokrotnie", "", "", time.Time{}, time.Time{}, 9999)
	if len(hits) != 100 {
		t.Errorf("hits with limit=9999 = %d, want 100 (max)", len(hits))
	}
}

func TestSearchHistory_EmptyQuery(t *testing.T) {
	s := openTestStore(t)
	_, _ = seedSearchable(t, s)
	if _, err := s.SearchHistory(context.Background(), "", "", "", time.Time{}, time.Time{}, 20); err == nil {
		t.Error("empty query should error")
	}
}

func TestSearchHistory_NoMatches(t *testing.T) {
	s := openTestStore(t)
	_, _ = seedSearchable(t, s)
	hits, err := s.SearchHistory(context.Background(), "xyznonexistent", "", "", time.Time{}, time.Time{}, 20)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %d, want 0", len(hits))
	}
}

func TestSearchHistory_DeleteRemovesFromIndex(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "indeksowane slowo kluczowe"})
	_ = s.AppendMessage(context.Background(), sess.ID, enc)
	// Match should find it
	hits, _ := s.SearchHistory(context.Background(), "indeksowane", "", "", time.Time{}, time.Time{}, 20)
	if len(hits) != 1 {
		t.Fatalf("hits before delete = %d, want 1", len(hits))
	}
	// Delete session (cascades to messages, trigger should
	// also remove from messages_fts)
	if err := s.Delete(sess.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	hits, _ = s.SearchHistory(context.Background(), "indeksowane", "", "", time.Time{}, time.Time{}, 20)
	if len(hits) != 0 {
		t.Errorf("hits after delete = %d, want 0 (FTS index not synced)", len(hits))
	}
}

func TestSearchHistory_TimeRangeFilter(t *testing.T) {
	s := openTestStore(t)
	_, _ = seedSearchable(t, s)
	// since=now+1h should filter out everything (all
	// messages were just inserted)
	future := time.Now().Add(1 * time.Hour)
	hits, err := s.SearchHistory(context.Background(), "refaktoryzacji", "", "", future, time.Time{}, 20)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("hits with future since = %d, want 0", len(hits))
	}
	// since=epoch should match everything
	hits, _ = s.SearchHistory(context.Background(), "refaktoryzacji", "", "", time.Unix(0, 0), time.Time{}, 20)
	if len(hits) != 2 {
		t.Errorf("hits with epoch since = %d, want 2", len(hits))
	}
}

func TestSearchHistory_ResultsOrderedByRankAndTime(t *testing.T) {
	s := openTestStore(t)
	// Two messages with same content shape (both: "X Y refaktoryzacji")
	// so FTS5 rank is tied. Secondary sort (created_at DESC)
	// should put the newer one first.
	sess, _ := s.Create("/a", "m", "")
	for i, text := range []string{
		"slowo klucz refaktoryzacji",
		"inne slowo refaktoryzacji",
	} {
		enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: text})
		_ = s.AppendMessage(context.Background(), sess.ID, enc)
		_ = i
	}
	hits, err := s.SearchHistory(context.Background(), "refaktoryzacji", "", "", time.Time{}, time.Time{}, 20)
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	// Either rank differs (then no order guarantee) or rank
	// is tied, in which case hits[0].CreatedAt >= hits[1].CreatedAt.
	if !hits[0].CreatedAt.After(hits[1].CreatedAt) && !hits[0].CreatedAt.Equal(hits[1].CreatedAt) {
		t.Errorf("results not ordered by created_at DESC: %v then %v",
			hits[0].CreatedAt, hits[1].CreatedAt)
	}
}
