package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/storage"
	"supercli/internal/storage/goal"
)

// Compare the observed three independent additions with one explicit batch.
// Reset rows outside timing: both routes append the same tasks to the same
// empty active goal, including registry validation and the real cache refresh.
func BenchmarkGoalTool_AddThreeTasks(b *testing.B) {
	for _, tc := range []struct {
		name  string
		calls []json.RawMessage
	}{
		{"three-single-calls", []json.RawMessage{
			json.RawMessage(`{"action":"add_task","title":"Inspect"}`),
			json.RawMessage(`{"action":"add_task","title":"Implement"}`),
			json.RawMessage(`{"action":"add_task","title":"Verify"}`),
		}},
		{"one-explicit-batch", []json.RawMessage{json.RawMessage(`{"action":"add_task","titles":["Inspect","Implement","Verify"]}`)}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			db, err := storage.Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = db.Close() })
			store := goal.NewStorage(db)
			if err := store.Migrate(ctx); err != nil {
				b.Fatal(err)
			}
			svc := goal.NewService(store)
			g, err := svc.Set(ctx, "Synthetic objective", "", "", "")
			if err != nil {
				b.Fatal(err)
			}
			reg := NewRegistry()
			reg.MustRegister(NewGoalTool(svc).Spec())
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if _, err := db.ExecContext(ctx, `DELETE FROM goal_tasks WHERE goal_id=?`, g.ID); err != nil {
					b.Fatal(err)
				}
				if _, err := svc.Refresh(ctx); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				for _, raw := range tc.calls {
					res, err := reg.Execute(ctx, "goal", raw)
					if err != nil || res.Err != nil {
						b.Fatalf("add tasks: %v %v", err, res.Err)
					}
				}
				b.StopTimer()
				if p := svc.Progress(); p.Total != 3 || p.Done != 0 {
					b.Fatalf("wrong result: %+v", p)
				}
				b.StartTimer()
			}
			b.ReportMetric(float64(len(tc.calls)), "toolcalls/op")
		})
	}
}
