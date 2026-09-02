package webgui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
)

func TestWebSessionCapsuleAndRelevantRecall(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(home, "echo-test", "Thunderbird cleanup")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "Napraw masowe usuwanie newsletterów przez Thunderbird Bridge."},
		{Role: llm.RoleAssistant, Content: "Dodałem natywne empty_trash dla Gmail IMAP i przebudowałem NestCafe."},
	} {
		if err := writer.AppendMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}

	eng.saveWebSessionCapsule(context.Background(), sess.ID)
	project, err := memory.OpenProjectStore(dataDir, home)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := project.Get("web-session-" + sess.ID)
	if err != nil {
		_ = project.Close()
		t.Fatal(err)
	}
	if !strings.Contains(entry.Content, "Thunderbird") || !strings.Contains(entry.Content, "empty_trash") {
		_ = project.Close()
		t.Fatalf("capsule lost task details: %q", entry.Content)
	}
	_ = project.Close()

	recall := eng.webRelevantSessionMemory(context.Background(), home, "co z usuwaniem newsletterów w Thunderbirdzie?", "different-session", 520)
	if !strings.Contains(recall, "Thunderbird") || !strings.Contains(recall, "empty_trash") {
		t.Fatalf("relevant recall missed prior session: %q", recall)
	}
	if current := eng.webRelevantSessionMemory(context.Background(), home, "Thunderbird newslettery", sess.ID, 520); strings.Contains(current, shortSessionID(sess.ID)) {
		t.Fatalf("current session leaked into cross-session recall: %q", current)
	}

	// Old installations have sessions that predate capsules. They must still
	// be recallable directly from sessions.db FTS without a foreground backfill.
	legacy, err := store.Create(home, "echo-test", "Old DSJ4 work")
	if err != nil {
		t.Fatal(err)
	}
	legacyWriter := session.NewWriter(store, legacy.ID)
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "Poszerz plastron DSJ4 do pełnej prostokątnej tekstury bez czarnych marginesów."},
		{Role: llm.RoleAssistant, Content: "Przygotowałem zasady pełnego wypełnienia tekstury 1153x1364 bez zmiany elementów."},
	} {
		if err := legacyWriter.AppendMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	legacyRecall := eng.webRelevantSessionMemory(context.Background(), home, "co ustaliliśmy z plastronem DSJ4?", "new-session", 520)
	if !strings.Contains(strings.ToLower(legacyRecall), "plastron") || !strings.Contains(strings.ToLower(legacyRecall), "dsj4") {
		t.Fatalf("legacy session FTS fallback missed old work: %q", legacyRecall)
	}
}

func BenchmarkWebRelevantSessionMemory(b *testing.B) {
	dataDir := b.TempDir()
	home := b.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = eng.Close() })
	_, project := eng.webMemoryStores(home)
	if project == nil {
		b.Fatal("project memory unavailable")
	}
	for i := 0; i < 200; i++ {
		content := "Conversation benchmark — User: popraw funkcję kolejki i interfejs NestCafe. Assistant result: kolejka działa sprawnie."
		if i%9 == 0 {
			content = "Conversation thunderbird — User: posprzątaj newslettery Gmail Thunderbird. Assistant result: poprawiono bridge i empty_trash."
		}
		if err := project.Put(memory.Entry{ID: fmt.Sprintf("web-session-bench-%03d", i), Scope: memory.ScopeTaskLog, Content: content, Source: memory.SourceAgent}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = eng.webRelevantSessionMemory(context.Background(), home, "co z Thunderbirdem i newsletterami?", "current", 520)
	}
}

func BenchmarkWebLegacySessionRecall(b *testing.B) {
	dataDir := b.TempDir()
	home := b.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = eng.Close() })
	store, err := eng.sessionStore()
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		sess, err := store.Create(home, "echo-test", fmt.Sprintf("session %d", i))
		if err != nil {
			b.Fatal(err)
		}
		writer := session.NewWriter(store, sess.ID)
		text := "Zwykła praca nad interfejsem i kolejką NestCafe."
		answer := "Poprawiono interfejs kolejki i sprawdzono testy."
		if i%11 == 0 {
			text = "Napraw Gmail Thunderbird Bridge i masowe newslettery."
			answer = "Dodano serwerowe empty_trash i weryfikację Gmail IMAP."
		}
		if err := writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: text}); err != nil {
			b.Fatal(err)
		}
		if err := writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleAssistant, Content: answer}); err != nil {
			b.Fatal(err)
		}
	}
	// Open/cache the empty memory store before timing; the benchmark measures
	// sessions.db fallback rather than first-use setup.
	_, _ = eng.webMemoryStores(home)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = eng.webRelevantSessionMemory(context.Background(), home, "co z Thunderbird Bridge i newsletterami?", "current", 520)
	}
}

func TestSessionRecallQueryIsSmallAndFallbackRecognizesPastIntent(t *testing.T) {
	query := sessionRecallFTSQuery("Pamiętasz co robiliśmy wcześniej z Thunderbirdem i newsletterami w Gmailu?")
	if query == "" || !strings.Contains(query, "thunderbirdem") || !strings.Contains(query, "newsletterami") {
		t.Fatalf("unexpected FTS query: %q", query)
	}
	if strings.Count(query, " OR ") > 7 {
		t.Fatalf("query is too wide: %q", query)
	}
	if !explicitPastRecall("co robiliśmy wcześniej?") {
		t.Fatal("past-recall intent was not detected")
	}
}
