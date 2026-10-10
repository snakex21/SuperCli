package webgui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage/memory"
)

func userFactsColdEngine(t testing.TB) *Engine {
	t.Helper()
	eng := &Engine{home: t.TempDir(), dataDir: filepath.Join(t.TempDir(), "data")}
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

func assertUserFactsStoreCold(t *testing.T, eng *Engine) {
	t.Helper()
	if eng.globalMemory != nil || len(eng.projectMemory) != 0 {
		t.Fatal("no-facts save opened a memory store")
	}
	if _, err := os.Stat(eng.dataDir); !os.IsNotExist(err) {
		t.Fatalf("no-facts save touched its missing data root: %v", err)
	}
}

func TestWebUserFactsNoFactsKeepsStoreCold(t *testing.T) {
	for _, tc := range []struct{ name, prompt string }{
		{"greeting", "hello"},
		{"empty", ""},
		{"unrelated", "Please fix the build."},
		{"pl-transient", "Jestem zmęczony."},
		{"en-transient", "I'm done."},
		{"question", "I like coffee?"},
		{"rejected-value", "I like " + strings.Repeat("x", 200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng := userFactsColdEngine(t)
			eng.saveWebUserFacts(tc.prompt)
			assertUserFactsStoreCold(t, eng)
		})
	}
}

func TestWebUserFactsNoFactsDoesNotWaitForStoreMutex(t *testing.T) {
	eng := userFactsColdEngine(t)
	eng.memoryMu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			eng.memoryMu.Unlock()
		}
	}()
	done := make(chan struct{})
	go func() {
		eng.saveWebUserFacts("hello")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("no-facts save waited for the unrelated store lock")
	}
	eng.memoryMu.Unlock()
	unlocked = true
	assertUserFactsStoreCold(t, eng)
}

type userFactsHeaderRecorder struct {
	*httptest.ResponseRecorder
	beforeHeader func(int)
}

func (w *userFactsHeaderRecorder) WriteHeader(status int) {
	w.beforeHeader(status)
	w.ResponseRecorder.WriteHeader(status)
}

func TestHandleChatNoUserFactsBeforeHeaders(t *testing.T) {
	home, dataDir := t.TempDir(), t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	headerSeen := false
	w := &userFactsHeaderRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		beforeHeader: func(status int) {
			headerSeen = true
			if status != http.StatusOK {
				t.Fatalf("headers status=%d, want 200", status)
			}
			if eng.globalMemory != nil {
				t.Fatal("greeting opened global memory before SSE headers")
			}
			for _, path := range []string{"memory.db", "memory"} {
				if _, err := os.Stat(filepath.Join(dataDir, path)); !os.IsNotExist(err) {
					t.Fatalf("greeting touched %s before headers: %v", path, err)
				}
			}
		},
	}
	NewServer(eng, false).handleChat(w, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"hello"}`)))
	if !headerSeen || !strings.Contains(w.Body.String(), `"type":"done"`) {
		t.Fatalf("fake chat did not complete normally: headers=%v status=%d", headerSeen, w.Code)
	}
	if eng.globalMemory == nil || len(eng.projectMemory) == 0 {
		t.Fatal("later loop briefing did not open the memory stores")
	}
}

type userFactProjection struct {
	Scope, Source, Content string
}

func userFactContents(t *testing.T, store *memory.Store) []userFactProjection {
	t.Helper()
	entries, err := store.Recent(memory.ScopePreference, 100)
	if err != nil {
		t.Fatal(err)
	}
	projection := make([]userFactProjection, len(entries))
	for i, entry := range entries {
		projection[i] = userFactProjection{entry.Scope, entry.Source, entry.Content}
	}
	sort.Slice(projection, func(i, j int) bool { return projection[i].Content < projection[j].Content })
	return projection
}

func TestWebUserFactsNonemptyMatchesExistingSaver(t *testing.T) {
	for _, tc := range []struct {
		name, prompt string
		facts        []string
	}{
		{"pl-unicode", "Cześć, mam na imię Łukasz. Lubię kawę.", []string{"The user's name is Łukasz.", "The user likes kawę."}},
		{"en-ascii", "My name is Anna. I prefer coffee.", []string{"The user's name is Anna.", "The user prefers coffee."}},
		{"en-unicode", "My name is José. I prefer café output.", []string{"The user's name is José.", "The user prefers café output."}},
		{"existing-filtered-unicode", "My name is Zoë. I prefer crème brûlée.", nil},
		{"mixed-clauses", "Nazywam się Élodie; I love żółte filiżanki!", []string{"The user's name is Élodie.", "The user likes żółte filiżanki."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(memory.ExtractUserFacts([]string{tc.prompt})) == 0 {
				t.Fatal("nonempty fixture is not recognized by the existing extractor")
			}
			eng := userFactsColdEngine(t)
			eng.saveWebUserFacts(tc.prompt)
			if eng.globalMemory == nil {
				t.Fatal("real facts did not open global memory")
			}
			baseline, err := memory.OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = baseline.Close() })
			(&memory.AutoSaver{Global: baseline}).SaveDeterministicUserFacts([]string{tc.prompt})
			want := make([]userFactProjection, len(tc.facts))
			for i, fact := range tc.facts {
				want[i] = userFactProjection{memory.ScopePreference, memory.SourceAgent, fact}
			}
			sort.Slice(want, func(i, j int) bool { return want[i].Content < want[j].Content })
			got := userFactContents(t, eng.globalMemory)
			if len(got) != len(tc.facts) {
				t.Fatalf("persisted fact count=%d, want %d", len(got), len(tc.facts))
			}
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(got, userFactContents(t, baseline)) {
				t.Fatalf("saved facts differ from existing saver/Unicode contract: got=%#v want=%#v", got, want)
			}
			eng.saveWebUserFacts(tc.prompt)
			if again := userFactContents(t, eng.globalMemory); !reflect.DeepEqual(again, got) {
				t.Fatalf("repeated declaration changed saved facts: %#v", again)
			}
			reader, err := memory.OpenStore(eng.dataDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close() })
			if persisted := userFactContents(t, reader); !reflect.DeepEqual(persisted, got) {
				t.Fatal("independent store did not read the same persisted facts")
			}
		})
	}
}

func TestWebUserFactsBriefingStillReadsPersistedHistory(t *testing.T) {
	eng := userFactsColdEngine(t)
	global, err := memory.OpenStore(eng.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := global.Put(memory.Entry{ID: "fixture-pref", Scope: memory.ScopePreference, Content: "Fixture user prefers Unicode output.", Source: memory.SourceAgent}); err != nil {
		_ = global.Close()
		t.Fatal(err)
	}
	if err := global.Close(); err != nil {
		t.Fatal(err)
	}
	project, err := memory.OpenProjectStore(eng.dataDir, eng.home)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []memory.Entry{
		{ID: "fixture-fact", Scope: memory.ScopeFact, Content: "Fixture parser preserves Unicode identifiers.", Source: memory.SourceAgent},
		{ID: "fixture-log", Scope: memory.ScopeTaskLog, Content: "Fixture parser repair retained escaped delimiters.", Source: memory.SourceAgent},
	} {
		if err := project.Put(entry); err != nil {
			_ = project.Close()
			t.Fatal(err)
		}
	}
	if err := project.Close(); err != nil {
		t.Fatal(err)
	}
	eng.saveWebUserFacts("hello")
	if eng.globalMemory != nil || len(eng.projectMemory) != 0 {
		t.Fatal("no-facts save opened existing memory stores")
	}
	briefing := eng.webMemoryBriefing(eng.home, 1200)
	for _, want := range []string{"Fixture user prefers Unicode output.", "Fixture parser preserves Unicode identifiers.", "Fixture parser repair retained escaped delimiters."} {
		if !strings.Contains(briefing, want) {
			t.Fatalf("later briefing lost persisted fixture: %q", want)
		}
	}
	if eng.globalMemory == nil || len(eng.projectMemory) == 0 {
		t.Fatal("briefing did not open the persisted stores")
	}
}

func TestWebUserFactsRealFactRetriesFailedOpen(t *testing.T) {
	eng := userFactsColdEngine(t)
	if err := os.WriteFile(eng.dataDir, []byte("fixture blocks directory creation"), 0600); err != nil {
		t.Fatal(err)
	}
	eng.saveWebUserFacts("My name is Anna.")
	if eng.globalMemory != nil {
		t.Fatal("failed open cached a memory store")
	}
	if err := os.Remove(eng.dataDir); err != nil {
		t.Fatal(err)
	}
	eng.saveWebUserFacts("My name is Anna.")
	if eng.globalMemory == nil || len(userFactContents(t, eng.globalMemory)) != 1 {
		t.Fatal("real fact was not saved after repairing the directory")
	}
}

// Run this benchmark on the original through the source preimage overlay and
// on the changed helper. Setup/close are outside timing; cold means a fresh
// Engine cache, not an uncached disk. It measures helper latency, not model TTFT.
func BenchmarkWebUserFactsPreheaders(b *testing.B) {
	for _, cache := range []string{"cold-fresh", "cold-existing", "warm"} {
		for _, prompt := range []struct{ name, text string }{{"no-facts", "hello"}, {"real-facts", "Mam na imię Łukasz. I prefer café output."}} {
			b.Run(cache+"/"+prompt.name, func(b *testing.B) {
				root, home := b.TempDir(), b.TempDir()
				dataDir := filepath.Join(root, "data")
				if cache == "cold-existing" {
					store, err := memory.OpenStore(dataDir)
					if err != nil {
						b.Fatal(err)
					}
					if err := store.Put(memory.Entry{ID: "fixture-existing", Scope: memory.ScopeFact, Content: "Synthetic existing global history.", Source: memory.SourceAgent}); err != nil {
						_ = store.Close()
						b.Fatal(err)
					}
					if err := store.Close(); err != nil {
						b.Fatal(err)
					}
				}
				var warm *Engine
				if cache == "warm" {
					warm = &Engine{home: home, dataDir: dataDir}
					if _, err := warm.webMemoryStore("", true); err != nil {
						b.Fatal(err)
					}
					warm.saveWebUserFacts(prompt.text)
					b.Cleanup(func() { _ = warm.Close() })
				}
				newStores := 0
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					eng := warm
					if eng == nil {
						eng = &Engine{home: home, dataDir: dataDir}
					}
					wasOpen := eng.globalMemory != nil
					b.StartTimer()
					eng.saveWebUserFacts(prompt.text)
					b.StopTimer()
					if i == 0 && prompt.name == "real-facts" {
						if eng.globalMemory == nil {
							b.Fatal("real-facts benchmark did not open global memory")
						}
						entries, err := eng.globalMemory.Recent(memory.ScopePreference, 10)
						if err != nil || len(entries) != 2 {
							b.Fatalf("real-facts benchmark lost its two facts: count=%d err=%v", len(entries), err)
						}
					}
					if !wasOpen && eng.globalMemory != nil {
						newStores++
					}
					if warm == nil {
						if err := eng.Close(); err != nil {
							b.Fatal(err)
						}
						if cache == "cold-fresh" {
							if err := os.RemoveAll(dataDir); err != nil {
								b.Fatal(err)
							}
						}
					}
				}
				b.ReportMetric(float64(newStores)/float64(b.N), "store-opens/op")
			})
		}
	}
}
