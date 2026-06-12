package agent

import (
	"context"
	"testing"
)

func TestWorkerRegistry_ListOrder(t *testing.T) {
	r := NewWorkerRegistry()
	for i := 0; i < 12; i++ {
		r.Add("explore", "task", nil)
	}
	list := r.List()
	if len(list) != 12 {
		t.Fatalf("len = %d, want 12", len(list))
	}
	for i, w := range list {
		want := i + 1
		if workerSeq(w.ID) != want {
			t.Errorf("list[%d] = %s, want worker-%d", i, w.ID, want)
		}
	}
}

func TestWorkerRegistry_Stop(t *testing.T) {
	r := NewWorkerRegistry()
	if err := r.Stop("worker-99"); err == nil {
		t.Error("Stop(unknown) should error")
	}
	w := r.Add("code", "task", nil)
	if err := r.Stop(w.ID); err == nil {
		t.Error("Stop(not running) should error")
	}
	_, cancel := context.WithCancel(context.Background())
	w.setCancel(cancel)
	if err := r.Stop(w.ID); err != nil {
		t.Errorf("Stop(running) = %v, want nil", err)
	}
	if !w.clearCancel() {
		t.Error("clearCancel after Stop should report stopped=true")
	}
}

func TestTaskStopTool_Spec(t *testing.T) {
	tool := NewTaskStopTool(nil).Spec()
	if err := tool.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	res, err := tool.Fn(context.Background(), []byte(`{"task_id":"worker-1"}`))
	if err != nil {
		t.Fatalf("Fn: %v", err)
	}
	if res.Err == nil {
		t.Error("stopping unknown worker should set Result.Err")
	}
}
