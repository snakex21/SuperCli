package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"supercli/internal/storage/session"
)

func TestWorkerToolDetailsKeepArgumentsOutputAndErrors(t *testing.T) {
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create("fixture", "fixture", "human detail")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	call := ToolCallEvent{ID: "call", Args: `{"content":"` + strings.Repeat("żółć ", 400) + `"}`}
	result := ToolResultEvent{ID: "call", Output: strings.Repeat("diagnostic output\n", 200), Err: errors.New(strings.Repeat("failure żółć\n", 100))}
	handle, partial := retainWorkerToolDetail(t.Context(), writer, call, result)
	if handle == "" || !partial {
		t.Fatal("long detail was lost")
	}
	saved, err := writer.ReadToolOutput(t.Context(), handle)
	var got WorkerToolDetail
	if err != nil || json.Unmarshal([]byte(saved), &got) != nil || got.Args != call.Args || got.Output != result.Output || got.Error != result.Err.Error() {
		t.Fatal("detail changed")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if ref, partial := retainWorkerToolDetail(canceled, writer, call, result); ref != "" || !partial {
		t.Fatal("failed save advertised a complete detail")
	}
	if ref, partial := retainWorkerToolDetail(t.Context(), nil, call, result); ref != "" || !partial {
		t.Fatal("missing store concealed truncation")
	}
	if ref, partial := retainWorkerToolDetail(t.Context(), writer, ToolCallEvent{Args: "{}"}, ToolResultEvent{Output: "ok"}); ref != "" || partial {
		t.Fatal("small detail needs no extra cache entry")
	}
}
