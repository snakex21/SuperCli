package session

import (
	"fmt"
	"testing"
)

func TestHumanToolDetailsDoNotEvictModelOutputs(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create("fixture", "fixture", "separate UI budget")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	if err = writer.SaveToolOutput(t.Context(), "out_model", "model evidence"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= maxSavedOutputs; i++ {
		if err = writer.SaveToolOutput(t.Context(), fmt.Sprintf("worker_ui_%032x", i), "human detail"); err != nil {
			t.Fatal(err)
		}
	}
	if text, err := writer.ReadToolOutput(t.Context(), "out_model"); err != nil || text != "model evidence" {
		t.Fatal("UI cache evicted model evidence")
	}
	if _, err = writer.ReadToolOutput(t.Context(), fmt.Sprintf("worker_ui_%032x", 0)); err == nil {
		t.Fatal("UI cache is unbounded")
	}
	for i := 0; i < maxSavedOutputs; i++ {
		if err = writer.SaveToolOutput(t.Context(), fmt.Sprintf("out_model_%d", i), "model evidence"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = writer.ReadToolOutput(t.Context(), "out_model"); err == nil {
		t.Fatal("model cache is unbounded")
	}
	if _, err = writer.ReadToolOutput(t.Context(), fmt.Sprintf("worker_ui_%032x", maxSavedOutputs)); err != nil {
		t.Fatal("model cache evicted human detail")
	}
}
