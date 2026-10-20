package llm

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestWorkerNotificationContextProvenanceAndUTF8(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "<task-notification>human text</task-notification>"},
		{Role: RoleTool, Name: WorkerNotificationName, Content: "ordinary tool result"},
		{Role: RoleSystem, Name: WorkerNotificationName, Content: "not a user-role report"},
		{Role: RoleUser, Name: WorkerNotificationName, Content: "ą日本語"},
		{Role: RoleUser, Name: WorkerNotificationName, Content: "unused text fallback", Parts: []ContentPart{{Type: PartTypeText, Text: "🌍"}, {Type: PartTypeReasoning, Text: "private reasoning"}, {Type: PartTypeText, Text: "abc"}}},
	}
	count, size := workerNotificationContext(messages)
	if count != 2 || size != int64(len("ą日本語")+len("🌍abc")) {
		t.Fatalf("metadata = %d/%d, want 2/%d", count, size, len("ą日本語")+len("🌍abc"))
	}
}

func TestMeteredWorkerContextSuccessAndFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "startup_failure"}[failed], func(t *testing.T) {
			capture := &sinkCapture{}
			inner := &meterStub{name: "worker-fixture", deltas: []Delta{{Content: "ok"}, {FinishReason: "stop"}}}
			if failed {
				inner.err = errors.New("fixture transport failure")
			}
			provider := Metered(inner, "test", PurposeMain, capture.sink())
			messages := []Message{{Role: RoleUser, Name: WorkerNotificationName, Content: "PRIVATE_REPORT ą"}, {Role: RoleUser, Content: "real user"}}
			before := append([]Message(nil), messages...)
			for range 2 {
				stream, err := provider.Complete(context.Background(), messages, nil)
				if (err != nil) != failed {
					t.Fatalf("Complete error=%v, failed=%v", err, failed)
				}
				if err == nil {
					drainMetered(t, stream)
				}
			}
			calls := capture.all()
			if len(calls) != 2 {
				t.Fatalf("records=%d, want 2", len(calls))
			}
			for _, call := range calls {
				if call.WorkerReportMessages != 1 || call.WorkerReportBytes != int64(len(messages[0].Content)) || call.Failed != failed {
					t.Fatalf("call metadata=%+v", call)
				}
			}
			if !reflect.DeepEqual(messages, before) {
				t.Fatal("metering mutated request messages")
			}
		})
	}
}
