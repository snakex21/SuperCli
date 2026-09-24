package webgui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/storage/session"
)

func TestSessionCapsuleExcerptMatchesFullHistory(t *testing.T) {
	user := func(s string) session.Encoded { return session.Encoded{Role: "user", Content: s} }
	assistant := func(s string) session.Encoded { return session.Encoded{Role: "assistant", Content: s} }
	samples := [][]session.Encoded{
		nil,
		{user("one")},
		{assistant("one"), assistant("two")},
		{user("Polski żółw 😀"), {Role: "assistant", PartsJSON: `[{"type":"text","text":"Odpowiedź 中文"}]`}},
	}
	sparse := []session.Encoded{user("  "), {Role: "user", PartsJSON: "invalid"}, user("<think>hidden</think>"), user("original task")}
	for i := 0; i < 45; i++ {
		sparse = append(sparse, session.Encoded{Role: "tool", ToolCallID: "test", Content: strings.Repeat("ignored tool payload ", 100)},
			session.Encoded{Role: "assistant", ToolCallsJSON: `[{"ID":"test","Name":"read_lines","Arguments":"{}"}]`},
			assistant("<thinking>hidden reasoning</thinking>"))
		if i%4 == 0 {
			sparse = append(sparse, assistant(fmt.Sprintf("result-%d Unicode 中文 😀", i)))
		}
		if i%7 == 0 {
			sparse = append(sparse, user(fmt.Sprintf("followup-%d", i)))
		}
	}
	sparse = append(sparse, session.Encoded{Role: "assistant", PartsJSON: `[{"type":"reasoning","reasoning":{"text":"private scratch work"}}]`},
		session.Encoded{Role: "user", PartsJSON: `[{"type":"image","image":{"media_type":"image/png","data":"AA=="}}]`},
		session.Encoded{Role: "assistant", PartsJSON: "{"},
		session.Encoded{Role: "assistant", Content: "invalid call envelope", ToolCallsJSON: "["})
	samples = append(samples, sparse)
	long := []session.Encoded{user("initial task")}
	for i := 0; i < 20; i++ {
		long = append(long, assistant(strings.Repeat(fmt.Sprintf("answer-%d ", i), 200)))
	}
	samples = append(samples, long)
	for i, sample := range samples {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			store, err := session.OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			sess, err := store.Create(t.TempDir(), "echo-test", "excerpt")
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range sample {
				if err := store.AppendMessage(ctx, sess.ID, row); err != nil {
					t.Fatal(err)
				}
			}
			all, err := store.ReadMessages(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			excerpt, err := store.ReadDialogueExcerpt(ctx, sess.ID, 8, func(row session.Encoded) bool { return webCapsuleText(row) != "" })
			if err != nil {
				t.Fatal(err)
			}
			want, got := buildWebSessionCapsule("fixture", all), buildWebSessionCapsule("fixture", excerpt)
			if got != want {
				t.Fatalf("capsule differs\ngot=%q\nwant=%q", got, want)
			}
			if strings.Contains(got, "hidden reasoning") || strings.Contains(got, "ignored tool payload") {
				t.Fatal("irrelevant content leaked")
			}
			if len(excerpt) > 9 {
				t.Fatalf("retained %d rows", len(excerpt))
			}
		})
	}
}

func TestSavedSessionCapsuleTracksLaterEdits(t *testing.T) {
	eng, sid := capsuleCostFixture(t, 10)
	ctx := context.Background()
	store, _ := eng.sessionStore()
	check := func() {
		t.Helper()
		all, err := store.ReadMessages(ctx, sid)
		if err != nil {
			t.Fatal(err)
		}
		want := buildWebSessionCapsule(sid, all)
		eng.saveWebSessionCapsule(ctx, sid)
		archived, err := store.ReadMessages(ctx, sid)
		if err != nil || !reflect.DeepEqual(archived, all) {
			t.Fatalf("archive changed: %v", err)
		}
		_, mem := eng.webMemoryStores(eng.Home())
		entry, err := mem.Get("web-session-" + sid)
		if err != nil || entry.Content != want {
			t.Fatalf("saved capsule differs: %v", err)
		}
	}
	check()
	if err := store.AppendMessage(ctx, sid, session.Encoded{Role: "assistant", Content: "NEWEST FIX verified"}); err != nil {
		t.Fatal(err)
	}
	check()
	if _, err := store.TruncateFrom(ctx, sid, 49); err != nil {
		t.Fatal(err)
	}
	check()
}
