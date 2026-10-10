package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"supercli/internal/tools"
)

func TestForgetFromDetachesAcceptedAsyncWorkerBeforeLateCheckpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "source.bin")
	before := []byte{0xff, 0xfe, 0, '\r', '\n', '\n'}
	after := []byte("worker after\r\n\x00")
	untouched := filepath.Join(home, "untouched.txt")
	if err := os.WriteFile(target, before, 0600); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, untouched, "unrelated original\r\n")
	turn := m.NewTurn("synthetic-rewound-session", "accepted async write")
	turn.SetUserSeq(7)
	borrow, err := BorrowInvocation(WithTurn(ctx, turn, &turn.barrier), true)
	if err != nil {
		t.Fatal(err)
	}
	workReady, allowWork, workerExited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	workerResult := make(chan error, 1)
	var allowOnce sync.Once
	allow := func() { allowOnce.Do(func() { close(allowWork) }) }
	type completed struct {
		record *Record
		err    error
	}
	completion := make(chan completed, 1)
	completionScheduled, completionReceived := false, false
	t.Cleanup(func() {
		allow()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		select {
		case <-workerExited:
		case <-cleanupCtx.Done():
			t.Error("synthetic async worker did not exit during cleanup")
			return
		}
		if completionScheduled && !completionReceived {
			select {
			case <-completion:
			case <-cleanupCtx.Done():
				t.Error("synthetic deferred completion did not finish during cleanup")
				return
			}
		} else if !completionScheduled {
			if _, err := turn.Complete(cleanupCtx); err != nil {
				t.Errorf("synthetic checkpoint cleanup: %v", err)
			}
		}
	})
	write := turn.Wrap(tools.NewWriteFile(home).Spec())
	args, err := json.Marshal(map[string]string{"path": "source.bin", "content": string(after)})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(workerExited)
		defer borrow.Close()
		close(workReady)
		select {
		case <-allowWork:
		case <-ctx.Done():
			workerResult <- ctx.Err()
			return
		}
		result, err := write.Fn(borrow.Bind(ctx), args)
		workerResult <- errors.Join(err, result.Err)
	}()
	select {
	case <-workReady:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if pendingOwnerCount(t, m) != 1 || m.Latest("synthetic-rewound-session") != nil {
		t.Fatal("fixture did not accept the worker before its first checkpoint")
	}
	foreground, err := turn.CompleteDeferred(ctx, func(record *Record, err error) {
		completion <- completed{record, err}
	})
	if err != nil || foreground != nil {
		t.Fatalf("foreground did not defer accepted async work: %+v, %v", foreground, err)
	}
	completionScheduled = true
	if err := m.ForgetFrom("synthetic-rewound-session", 7); err != nil {
		t.Fatal(err)
	}
	turn.SetUserSeq(7) // A stale binder cannot relink the reused user sequence.
	turn.SetUserSeq(42)
	assertFileBytes(t, target, before)
	if m.Latest("synthetic-rewound-session") != nil {
		t.Fatal("ForgetFrom manufactured an early checkpoint")
	}
	allow()
	select {
	case result := <-completion:
		completionReceived = true
		if result.err != nil || result.record == nil {
			t.Fatalf("late checkpoint lost actual worker changes: %+v", result)
		}
		if result.record.SessionID != "synthetic-rewound-session" || result.record.UserSeq != 0 || !result.record.RawBytes || !reflect.DeepEqual(result.record.Files, []string{"source.bin"}) || !reflect.DeepEqual(result.record.Changes, []FileChange{{Path: "source.bin", Kind: "modified"}}) {
			t.Fatalf("detached checkpoint identity/scope changed: %+v", result.record)
		}
		select {
		case err := <-workerResult:
			if err != nil {
				t.Fatalf("detaching dropped the accepted worker: %v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		assertFileBytes(t, target, after)
		for _, cutoff := range []int{1, 7} {
			if preview := m.PreviewFrom("synthetic-rewound-session", cutoff); len(preview.Records) != 0 || len(preview.Files) != 0 {
				t.Fatalf("late checkpoint attached to a reused user message: %+v", preview)
			}
		}
		reopened, err := Open(home, data)
		if err != nil {
			t.Fatal(err)
		}
		stored := reopened.Latest("synthetic-rewound-session")
		if stored == nil || stored.ID != result.record.ID || stored.UserSeq != 0 {
			t.Fatalf("detachment was not persisted: %+v", stored)
		}
		writeCheckpointFixture(t, untouched, "manual outside scope\r\n")
		if _, err := reopened.Undo(ctx, result.record.ID); err != nil {
			t.Fatal(err)
		}
		assertFileBytes(t, target, before)
		assertFileBytes(t, untouched, []byte("manual outside scope\r\n"))
		if _, err := reopened.Redo(ctx, result.record.ID); err != nil {
			t.Fatal(err)
		}
		assertFileBytes(t, target, after)
		assertFileBytes(t, untouched, []byte("manual outside scope\r\n"))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if pendingOwnerCount(t, m) != 0 {
		t.Fatal("detached successful completion retained its pending owner")
	}
}

func TestPendingUserSequenceDetachmentMatchesTailEvenWhenStoreFails(t *testing.T) {
	for _, broken := range []bool{false, true} {
		name := "healthy metadata"
		if broken {
			name = "failed metadata"
		}
		t.Run(name, func(t *testing.T) {
			m, _, _ := bindingFixture(t)
			turns := []*Turn{
				m.NewTurn("rewound", "older retained user"),
				m.NewTurn("rewound", "discarded tail user"),
				m.NewTurn("other-session", "unrelated session"),
			}
			for i, seq := range []int{3, 7, 9} {
				turns[i].SetUserSeq(seq)
				borrow, err := BorrowInvocation(WithTurn(context.Background(), turns[i], &turns[i].barrier), true)
				if err != nil {
					t.Fatal(err)
				}
				borrow.Close()
				turn := turns[i]
				t.Cleanup(func() {
					if _, err := turn.Complete(context.Background()); err != nil {
						t.Errorf("synthetic pending cleanup: %v", err)
					}
				})
			}
			if broken {
				if err := os.WriteFile(m.meta, []byte("[broken"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.WriteFile(m.meta, []byte("[]"), 0600); err != nil {
						t.Error(err)
					}
				})
			}
			err := m.ForgetFrom("rewound", 7)
			if (err != nil) != broken {
				t.Fatalf("unexpected ForgetFrom result: %v", err)
			}
			turns[1].SetUserSeq(7)
			if turns[0].userSeq != 3 || turns[0].detachedUserSeq || turns[1].userSeq != 0 || !turns[1].detachedUserSeq || turns[2].userSeq != 9 || turns[2].detachedUserSeq {
				t.Fatal("detachment relinked a removed user or changed an unrelated owner")
			}
		})
	}
}
