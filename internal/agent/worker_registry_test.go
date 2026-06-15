package agent

import (
	"context"
	"strings"
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

func TestWorkerRegistry_Counts(t *testing.T) {
	r := NewWorkerRegistry()
	if tile := r.Counts().StatusTile(); tile != "" {
		t.Errorf("empty registry tile = %q, want \"\"", tile)
	}
	r.Add("explore", "a", nil) // created -> counts as running
	r.Add("code", "b", nil).Status = "running"
	r.Add("code", "c", nil).Status = "done"
	r.Add("code", "d", nil).Status = "failed"
	r.Add("code", "e", nil).Status = "stopped"

	c := r.Counts()
	if c.Total != 5 {
		t.Errorf("Total = %d, want 5", c.Total)
	}
	if c.Running != 2 {
		t.Errorf("Running = %d, want 2 (created+running)", c.Running)
	}
	if c.Done != 1 || c.Failed != 1 || c.Stopped != 1 {
		t.Errorf("Done/Failed/Stopped = %d/%d/%d, want 1/1/1", c.Done, c.Failed, c.Stopped)
	}
	tile := c.StatusTile()
	for _, want := range []string{"2 running", "1 done", "1 failed", "1 stopped"} {
		if !strings.Contains(tile, want) {
			t.Errorf("tile %q missing %q", tile, want)
		}
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
