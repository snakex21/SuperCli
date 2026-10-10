package memorytools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage/memory"
	"supercli/internal/tools/core"
)

type rememberProjectionOutputSpy struct {
	saves int
	reads int
}

func (s *rememberProjectionOutputSpy) SaveToolOutput(context.Context, string, string) error {
	s.saves++
	return errors.New("unexpected output retention")
}

func (s *rememberProjectionOutputSpy) ReadToolOutput(context.Context, string) (string, error) {
	s.reads++
	return "", errors.New("unexpected output read")
}

func TestRememberSuccessProjectionPreservesRealStoredNoteAndFullText(t *testing.T) {
	for _, note := range []string{
		"\u2003 \tZachowaj żółć, \"cudzysłowy\" i ścieżkę C:\\fixture.\n\tDruga linia: 日本語 😀.\r\n ",
		strings.Repeat("x", maxRememberTextBytes),
	} {
		t.Run(fmt.Sprintf("bytes_%d", len(note)), func(t *testing.T) {
			store := openMemStore(t)
			remember := NewRemember(store)
			now := time.Unix(1730000000, 123)
			remember.Now = func() time.Time { return now }
			saves := 0
			remember.OnSave = func() { saves++ }
			args, err := json.Marshal(rememberArgs{Text: note, Type: memory.ScopeDecision, Topic: "fixture-topic"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := remember.Spec().Fn(context.Background(), args)
			if err != nil || result.Err != nil || saves != 1 {
				t.Fatalf("save err=%v result.Err=%v OnSave=%d", err, result.Err, saves)
			}
			id := fmt.Sprintf("mem-%x", now.UnixNano())
			trimmed := strings.TrimSpace(note)
			wantReceipt := fmt.Sprintf("remembered [%s] (decision, project)", id)
			wantFull := fmt.Sprintf("remembered [%s] (decision, project): %s", id, trimmed)
			if result.Text != wantFull || result.ModelText != wantReceipt {
				t.Fatal("full text changed or projection lost ID/type/target")
			}
			stored, err := store.Get(id)
			if err != nil || stored.Content != trimmed || stored.Scope != memory.ScopeDecision || stored.Source != memory.SourceAgent || len(stored.Tags) != 1 || stored.Tags[0] != "fixture-topic" {
				t.Fatalf("saved note/routing changed: err=%v", err)
			}
			if result.RetainedText != "" || result.ModelPreview != "" {
				t.Fatal("complete receipt unnecessarily retained evidence")
			}
			outputSpy := &rememberProjectionOutputSpy{}
			ctx := core.WithOutputPersistence(context.Background(), outputSpy)
			for _, outputs := range []*core.OutputStore{nil, core.NewOutputStore()} {
				content := outputs.ModelContentContext(ctx, "remember", result)
				if content != wantReceipt || core.StoredOutputHandle(content) != "" {
					t.Fatal("model receipt changed or created a read_output handle")
				}
			}
			if outputSpy.saves != 0 || outputSpy.reads != 0 {
				t.Fatal("inline receipt performed output persistence I/O")
			}
			serialized, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(serialized, &payload); err != nil {
				t.Fatal(err)
			}
			var text string
			if err := json.Unmarshal(payload["Text"], &text); err != nil || text != wantFull {
				t.Fatal("Result serialization no longer carries the exact full echo")
			}
			if _, exposed := payload["ModelText"]; exposed {
				t.Fatal("model-only receipt leaked into Result serialization")
			}
		})
	}
}

func TestRememberSuccessProjectionReportsActualRouting(t *testing.T) {
	for _, test := range []struct {
		name             string
		args             rememberArgs
		projectAvailable bool
		globalAvailable  bool
		wantType         string
		wantTarget       string
	}{
		{"explicit_global", rememberArgs{Text: "synthetic note", Scope: "global", Type: "fact"}, true, true, "fact", "global"},
		{"profile_default", rememberArgs{Text: "synthetic note", Topic: "user_profile"}, true, true, "preference", "global"},
		{"project_missing", rememberArgs{Text: "synthetic note", Scope: "project"}, false, true, "fact", "global"},
		{"global_missing", rememberArgs{Text: "synthetic note", Scope: "global"}, true, false, "fact", "project"},
	} {
		t.Run(test.name, func(t *testing.T) {
			project, global := openMemStore(t), openMemStore(t)
			var projectKeeper, globalKeeper MemoryKeeper
			if test.projectAvailable {
				projectKeeper = project
			}
			if test.globalAvailable {
				globalKeeper = global
			}
			remember := NewRememberDual(projectKeeper, globalKeeper)
			now := time.Unix(1730000000, 456)
			remember.Now = func() time.Time { return now }
			args, err := json.Marshal(test.args)
			if err != nil {
				t.Fatal(err)
			}
			result, err := remember.Spec().Fn(context.Background(), args)
			if err != nil || result.Err != nil {
				t.Fatalf("save err=%v result.Err=%v", err, result.Err)
			}
			id := fmt.Sprintf("mem-%x", now.UnixNano())
			want := fmt.Sprintf("remembered [%s] (%s, %s)", id, test.wantType, test.wantTarget)
			if result.ModelText != want || result.Text != want+": "+test.args.Text {
				t.Fatal("receipt lost actual normalized type or target")
			}
			target, other := project, global
			if test.wantTarget == "global" {
				target, other = global, project
			}
			entry, err := target.Get(id)
			if err != nil || entry.Content != test.args.Text || entry.Scope != test.wantType {
				t.Fatal("receipt does not describe persisted note")
			}
			if _, err := other.Get(id); err == nil {
				t.Fatal("note also saved to the other target")
			}
		})
	}
}

type rememberProjectionFailingKeeper struct {
	err  error
	puts int
}

func (s *rememberProjectionFailingKeeper) Put(memory.Entry) error {
	s.puts++
	return s.err
}

func (*rememberProjectionFailingKeeper) Search(string, int) ([]memory.Entry, error) {
	return nil, nil
}

func TestRememberFailureNeverClaimsProjectedSuccess(t *testing.T) {
	putError := errors.New("synthetic keeper unavailable")
	for _, test := range []struct {
		name       string
		args       json.RawMessage
		putFailure bool
	}{
		{"bad_json", json.RawMessage(`{`), false},
		{"wrong_text_type", json.RawMessage(`{"text":123}`), false},
		{"empty_note", json.RawMessage(`{"text":" \t "}`), false},
		{"oversized_note", json.RawMessage(`{"text":"` + strings.Repeat("x", maxRememberTextBytes+1) + `"}`), false},
		{"invalid_scope", json.RawMessage(`{"text":"synthetic note","scope":"workspace"}`), false},
		{"put_failure", json.RawMessage(`{"text":"synthetic note"}`), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			keeper := &rememberProjectionFailingKeeper{err: putError}
			remember := NewRemember(keeper)
			saved := 0
			remember.OnSave = func() { saved++ }
			result, err := remember.Spec().Fn(context.Background(), test.args)
			if err != nil || result.Err == nil || result.Text != "" || result.ModelText != "" || result.RetainedText != "" || result.ModelPreview != "" || saved != 0 {
				t.Fatalf("failure claimed success or changed result contract: err=%v result.Err=%v", err, result.Err)
			}
			wantPuts := 0
			if test.putFailure {
				wantPuts = 1
				if !errors.Is(result.Err, putError) {
					t.Fatal("keeper failure lost its cause")
				}
			}
			if keeper.puts != wantPuts {
				t.Fatalf("Put calls=%d want=%d", keeper.puts, wantPuts)
			}
			if content := core.NewOutputStore().ModelContent("remember", result); content != result.ModelContent() || core.StoredOutputHandle(content) != "" {
				t.Fatal("projection changed the existing model error")
			}
		})
	}
}

func TestRememberUnavailableKeeperKeepsOriginalResult(t *testing.T) {
	for _, args := range []json.RawMessage{json.RawMessage(`{"text":"synthetic note"}`), json.RawMessage(`{`)} {
		remember := NewRememberDual(nil, nil)
		saved := 0
		remember.OnSave = func() { saved++ }
		result, err := remember.Spec().Fn(context.Background(), args)
		if err != nil || result.Err != nil || result.Text != "remember: memory store not available" || result.ModelText != "" || result.RetainedText != "" || result.ModelPreview != "" || saved != 0 {
			t.Fatal("unavailable keeper changed its original early-return contract")
		}
		if content := core.NewOutputStore().ModelContent("remember", result); content != result.Text || core.StoredOutputHandle(content) != "" {
			t.Fatal("unavailable keeper created a success receipt or handle")
		}
	}
}
