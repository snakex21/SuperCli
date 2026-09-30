package goal

import (
	"context"
	"fmt"
	"supercli/internal/storage"
	"testing"
	"time"
)

func BenchmarkGoalContextOpenTasks(b *testing.B) {
	db, err := storage.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	store := NewStorage(db)
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	svc := NewProjectService(store, "project")
	g, err := svc.Set(ctx, "Large plan", "", "", "")
	if err != nil {
		b.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO goal_tasks(id,goal_id,seq,title,status,created_at) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	for i := 1; i <= 10000; i++ {
		status := "done"
		if i > 9900 {
			status = "pending"
		}
		if _, err := stmt.ExecContext(ctx, fmt.Sprint(i), g.ID, i, fmt.Sprintf("Task %d", i), status, time.Now().UnixNano()); err != nil {
			b.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		text, err := svc.Inject(ctx, "base", 5)
		if err != nil || text == "base" {
			b.Fatal(err)
		}
	}
}
