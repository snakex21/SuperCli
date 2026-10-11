package webgui

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

type goalStartupQueryCounts struct {
	Active, Progress, OpenTasks, Other int
}

type goalStartupQueries struct {
	mu     sync.Mutex
	counts goalStartupQueryCounts
}

func (q *goalStartupQueries) record(query string) {
	query = strings.ToLower(strings.Join(strings.Fields(query), " "))
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case strings.Contains(query, "from goals where status='active'"):
		q.counts.Active++
	case strings.HasPrefix(query, "select count(*)") && strings.Contains(query, "from goal_tasks"):
		q.counts.Progress++
	case strings.Contains(query, "from goal_tasks") && strings.Contains(query, "order by seq limit ?"):
		q.counts.OpenTasks++
	default:
		q.counts.Other++
	}
}

func (q *goalStartupQueries) take() goalStartupQueryCounts {
	q.mu.Lock()
	defer q.mu.Unlock()
	counts := q.counts
	q.counts = goalStartupQueryCounts{}
	return counts
}

// The connector counts queries forwarded to the real registered SQLite driver.
// It does not replace rows, SQL failures, transactions or persisted data. A
// separate ordinary connection represents another SuperCli process.
type goalStartupConnector struct {
	backend driver.Driver
	dsn     string
	queries *goalStartupQueries
}

func (c *goalStartupConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.backend.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &goalStartupConn{Conn: conn, queries: c.queries}, nil
}

func (c *goalStartupConnector) Driver() driver.Driver { return c.backend }

type goalStartupConn struct {
	driver.Conn
	queries *goalStartupQueries
}

func (c *goalStartupConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &goalStartupStmt{Stmt: stmt, query: query, queries: c.queries}, nil
}

func (c *goalStartupConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	inner, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := inner.QueryContext(ctx, query, args)
	if err != driver.ErrSkip {
		c.queries.record(query)
	}
	return rows, err
}

type goalStartupStmt struct {
	driver.Stmt
	query   string
	queries *goalStartupQueries
}

func (s *goalStartupStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.queries.record(s.query)
	return s.Stmt.Query(args)
}

func (s *goalStartupStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if inner, ok := s.Stmt.(driver.StmtQueryContext); ok {
		rows, err := inner.QueryContext(ctx, args)
		if err != driver.ErrSkip {
			s.queries.record(s.query)
		}
		return rows, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		if arg.Name != "" {
			return nil, fmt.Errorf("goal query fixture requires positional arguments")
		}
		values[i] = arg.Value
	}
	return s.Query(values)
}

type goalStartupFixture struct {
	engine   *Engine
	external *sql.DB
	queries  *goalStartupQueries
	home     string
}

func newGoalStartupFixture(tb testing.TB, counted bool) *goalStartupFixture {
	tb.Helper()
	data, home := tb.TempDir(), tb.TempDir()
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte("orchestrator = false\npreflight_repo = false\ncontext_window = 100000\n"), 0o600); err != nil {
		tb.Fatal(err)
	}
	external, err := storage.OpenAt(data)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = external.Close() })
	if err := goal.NewStorage(external).Migrate(context.Background()); err != nil {
		tb.Fatal(err)
	}
	e, err := NewEngine(echoConfig(), home, data)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = e.Close() })
	var db *sql.DB
	queries := &goalStartupQueries{}
	if counted {
		dsn := filepath.Join(data, storage.DBFileName) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
		db = sql.OpenDB(&goalStartupConnector{backend: external.Driver(), dsn: dsn, queries: queries})
	} else {
		db, err = storage.OpenAt(data)
		if err != nil {
			tb.Fatal(err)
		}
	}
	// Only the shared DB is ready. The real project/global service factory has
	// not created any scope, and the full loop-building consumer is unchanged.
	e.goalDB, e.goals = db, goal.NewService(goal.NewStorage(db))
	e.projectGoals = make(map[string]*goal.Service)
	return &goalStartupFixture{engine: e, external: external, queries: queries, home: home}
}

func (f *goalStartupFixture) seed(tb testing.TB, key, id, title string, tasks int) *goal.Goal {
	tb.Helper()
	store := goal.NewStorage(f.external)
	g := &goal.Goal{
		ID: id, Title: title, Description: "Preserve the user's chosen thinking level.",
		SuccessCriteria: "Existing tests pass without losing earlier requirements.",
		ProjectKey:      key, Status: goal.StatusActive, CreatedAt: time.Unix(1700000000, 0),
	}
	if err := store.CreateGoal(context.Background(), g); err != nil {
		tb.Fatal(err)
	}
	for first := 0; first < tasks; {
		titles := make([]string, min(goal.MaxTaskBatch, tasks-first))
		for i := range titles {
			titles[i] = fmt.Sprintf("step %04d", first+i+1)
		}
		if _, err := store.AddTasks(context.Background(), id, titles); err != nil {
			tb.Fatal(err)
		}
		first += len(titles)
	}
	return g
}

func (f *goalStartupFixture) build(t *testing.T, home string, want goalStartupQueryCounts) (*agent.Loop, *tools.Registry, *prefixCaptureProvider) {
	t.Helper()
	p := &prefixCaptureProvider{}
	f.engine.prov = p
	f.queries.take()
	loop, reg, err := f.engine.buildLoopWithSession(nil, nil, home, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.queries.take(); got != want {
		t.Fatalf("real SQLite goal queries=%+v, want %+v", got, want)
	}
	return loop, reg, p
}

func goalStartupRequest(t *testing.T, loop *agent.Loop, p *prefixCaptureProvider) string {
	t.Helper()
	events, err := loop.Run(context.Background(), "Continue the current implementation and preserve the existing requirements.")
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if failure, ok := event.(agent.ErrorEvent); ok {
			t.Fatal(failure.Err)
		}
	}
	if len(p.requests) != 1 {
		t.Fatalf("deterministic provider calls=%d, want 1", len(p.requests))
	}
	var text strings.Builder
	for _, message := range p.requests[0] {
		text.WriteString(message.TextOnly().Content)
		text.WriteByte('\n')
	}
	return text.String()
}

func goalStartupWantBlock(g *goal.Goal, taskLines string) string {
	block := "[current_goal]\ntitle: " + g.Title + "\ngoal_id: " + g.ID + "\n"
	if g.ProjectKey == goal.GlobalProjectKey {
		block += "scope: global\n"
	}
	if g.Description != "" {
		block += "description: " + g.Description + "\n"
	}
	if g.SuccessCriteria != "" {
		block += "success_criteria: " + g.SuccessCriteria + "\n"
	}
	return block + "open_tasks:\n" + taskLines + "state_updates: use the goal tool; start_task, complete_task, verify, and mark_done are action values, not tool names. Omit goal_id to use this active goal.\n[end current_goal]\n"
}

func goalStartupAssertBlock(t *testing.T, request, block string, forbidden ...string) {
	t.Helper()
	if strings.Count(request, "[current_goal]") != 1 || strings.Count(request, block) != 1 {
		t.Fatalf("current goal block missing, duplicated or changed; want %q", block)
	}
	for _, text := range forbidden {
		if strings.Contains(request, text) {
			t.Fatalf("request contains stale or out-of-scope goal evidence %q", text)
		}
	}
}

func TestWebGoalStartupReadsOnceColdAndWarm(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "empty"
		if active {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			f := newGoalStartupFixture(t, true)
			want := goalStartupQueryCounts{Active: 1}
			var block string
			if active {
				g := f.seed(t, memory.ProjectStorageKey(f.engine.dataDir, f.home), "goal-first", "First implementation goal", 2)
				block = goalStartupWantBlock(g, "  - [pending] 1. step 0001\n  - [pending] 2. step 0002\n")
				want.Progress, want.OpenTasks = 1, 1
			}
			var first *goal.Service
			for _, phase := range []string{"cold", "warm"} {
				loop, reg, p := f.build(t, f.home, want)
				if reg.IsVisible("goal") != active {
					t.Fatalf("%s: active goal visibility=%v, want %v", phase, reg.IsVisible("goal"), active)
				}
				request := goalStartupRequest(t, loop, p)
				if active {
					goalStartupAssertBlock(t, request, block)
				} else if strings.Contains(request, "[current_goal]") {
					t.Fatalf("%s: empty goal unexpectedly entered the model request", phase)
				}
				svc, err := f.engine.goalService(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if first != nil && first != svc {
					t.Fatal("warm request replaced its project goal service")
				}
				first = svc
				if got := f.queries.take(); got != (goalStartupQueryCounts{}) {
					t.Fatalf("%s: returning the cached service added SQL reads %+v", phase, got)
				}
			}
		})
	}
}

func TestWebGoalStartupFreshOtherConnectionAndScopes(t *testing.T) {
	f := newGoalStartupFixture(t, true)
	homeB, homeC := t.TempDir(), t.TempDir()
	keyA := memory.ProjectStorageKey(f.engine.dataDir, f.home)
	gA := f.seed(t, keyA, "goal-a", "Project A only", 2)
	gB := f.seed(t, memory.ProjectStorageKey(f.engine.dataDir, homeB), "goal-b", "Project B only", 1)
	gGlobal := f.seed(t, goal.GlobalProjectKey, "goal-global", "Explicit shared fallback", 1)
	f.seed(t, "", "legacy-unassigned", "Unassigned legacy must remain private", 1)
	want := goalStartupQueryCounts{Active: 1, Progress: 1, OpenTasks: 1}
	for _, scope := range []struct {
		home string
		g    *goal.Goal
		line string
	}{
		{f.home, gA, "  - [pending] 1. step 0001\n  - [pending] 2. step 0002\n"},
		{homeB, gB, "  - [pending] 1. step 0001\n"},
		{homeC, gGlobal, "  - [pending] 1. step 0001\n"},
	} {
		loop, _, p := f.build(t, scope.home, want)
		request := goalStartupRequest(t, loop, p)
		goalStartupAssertBlock(t, request, goalStartupWantBlock(scope.g, scope.line), "Unassigned legacy must remain private")
		for _, other := range []*goal.Goal{gA, gB, gGlobal} {
			if other.ID != scope.g.ID && strings.Contains(request, "title: "+other.Title+"\n") {
				t.Fatalf("scope %q exposed %q", scope.home, other.Title)
			}
		}
	}

	// Mutations use an independently opened SQLite connection, not the Engine's
	// cached service. The next warm build must refresh both goal and task state.
	outsideA := goal.NewProjectService(goal.NewStorage(f.external), keyA)
	gNew, err := outsideA.Set(context.Background(), "Project A edited in another process", "Żółć and spaces remain exact.", "Keep xhigh and earlier user prohibitions.", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outsideA.AddTasks(context.Background(), gNew.ID, []string{"already completed elsewhere", "resume modified source", "run existing regression test"}); err != nil {
		t.Fatal(err)
	}
	if err := outsideA.SetTaskStatus(context.Background(), gNew.ID, 1, goal.TaskDone); err != nil {
		t.Fatal(err)
	}
	if err := outsideA.SetTaskStatus(context.Background(), gNew.ID, 2, goal.TaskInProgress); err != nil {
		t.Fatal(err)
	}
	loop, reg, p := f.build(t, f.home, want)
	if !reg.IsVisible("goal") {
		t.Fatal("fresh goal lost its always-on tool")
	}
	request := goalStartupRequest(t, loop, p)
	goalStartupAssertBlock(t, request, goalStartupWantBlock(gNew, "  - [in_progress] 2. resume modified source\n  - [pending] 3. run existing regression test\n"), gA.Title, gB.Title, gGlobal.Title, "already completed elsewhere")
	svc, err := f.engine.goalService(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.Progress(); got.Title != gNew.Title || got.Done != 1 || got.Total != 3 {
		t.Fatalf("cached progress failed to observe another connection: %+v", got)
	}
	global, err := f.engine.activeGoalScope(context.Background(), "global")
	if err != nil || global == nil || global.ID != gGlobal.ID {
		t.Fatalf("global consumer selected a project goal: view=%+v err=%v", global, err)
	}
	loop, _, p = f.build(t, homeB, want)
	goalStartupAssertBlock(t, goalStartupRequest(t, loop, p), goalStartupWantBlock(gB, "  - [pending] 1. step 0001\n"), gNew.Title)
}

func TestWebGoalStartupCancellationAndSQLRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	t.Run("canceled_unopened_database", func(t *testing.T) {
		e, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.Close() })
		if _, err := e.goalService(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("unopened factory cancellation=%v, want context.Canceled", err)
		}
		if e.goalDB != nil || e.goals != nil || len(e.projectGoals) != 0 {
			t.Fatal("already canceled factory opened or published goal state")
		}
	})
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled_cached_%v", cached), func(t *testing.T) {
			f := newGoalStartupFixture(t, true)
			if cached {
				if _, err := f.engine.goalService(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			f.queries.take()
			if _, err := f.engine.goalService(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("factory cancellation=%v, want context.Canceled", err)
			}
			if _, err := f.engine.activeGoal(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("consumer cancellation=%v, want context.Canceled", err)
			}
			if got := f.queries.take(); got != (goalStartupQueryCounts{}) {
				t.Fatalf("already canceled request issued SQL: %+v", got)
			}
			if !cached && len(f.engine.projectGoals) != 0 {
				t.Fatal("canceled cold factory published a service")
			}
			svc, err := f.engine.goalService(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Refresh(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation after factory return was lost: %v", err)
			}
		})
	}

	t.Run("real_sql_error_then_retry", func(t *testing.T) {
		f := newGoalStartupFixture(t, true)
		g := f.seed(t, memory.ProjectStorageKey(f.engine.dataDir, f.home), "retry-goal", "Recovered goal", 1)
		if _, err := f.external.ExecContext(context.Background(), "ALTER TABLE goals RENAME TO unavailable_goals"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.engine.buildLoopWithSession(nil, nil, f.home, nil, nil); err == nil {
			t.Fatal("the remaining consumer Refresh concealed a real SQLite failure")
		}
		if _, err := f.external.ExecContext(context.Background(), "ALTER TABLE unavailable_goals RENAME TO goals"); err != nil {
			t.Fatal(err)
		}
		loop, _, p := f.build(t, f.home, goalStartupQueryCounts{Active: 1, Progress: 1, OpenTasks: 1})
		goalStartupAssertBlock(t, goalStartupRequest(t, loop, p), goalStartupWantBlock(g, "  - [pending] 1. step 0001\n"))
	})
}

// This boundary starts with the genuine portable DB open and migrated. Cold
// means a new scoped Service, not cold filesystem/SQLite caches or a new model.
// Resets and fixture creation are outside the timer; no query-count wrapper is
// used in timed code. The full-build cases keep unrelated Engine caches warm.
func BenchmarkWebGoalStartup(b *testing.B) {
	for _, tasks := range []int{-1, 5, 1000} {
		name := "no_goal"
		if tasks >= 0 {
			name = fmt.Sprintf("active_%d_tasks", tasks)
		}
		b.Run(name, func(b *testing.B) {
			for _, path := range []string{"scope_cold_ready_refresh", "build_scope_cold", "build_warm_control"} {
				b.Run(path, func(b *testing.B) {
					f := newGoalStartupFixture(b, false)
					if tasks >= 0 {
						f.seed(b, memory.ProjectStorageKey(f.engine.dataDir, f.home), "bench-goal", "Benchmark persistent goal", tasks)
					}
					if _, _, err := f.engine.buildLoopWithSession(nil, nil, f.home, nil, nil); err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for n := 0; n < b.N; n++ {
						if path != "build_warm_control" {
							b.StopTimer()
							f.engine.goalMu.Lock()
							f.engine.projectGoals = make(map[string]*goal.Service)
							f.engine.goalMu.Unlock()
							b.StartTimer()
						}
						if path == "scope_cold_ready_refresh" {
							svc, err := f.engine.goalService(context.Background())
							if err != nil {
								b.Fatal(err)
							}
							if _, err := svc.Refresh(context.Background()); err != nil {
								b.Fatal(err)
							}
							text, err := svc.Inject(context.Background(), "Existing live context.", 5)
							if err != nil || (tasks >= 0) != strings.Contains(text, "[current_goal]") {
								b.Fatalf("fresh goal context invalid: err=%v", err)
							}
						} else {
							loop, reg, err := f.engine.buildLoopWithSession(nil, nil, f.home, nil, nil)
							if err != nil {
								b.Fatal(err)
							}
							if len(loop.Messages) == 0 || reg.IsVisible("goal") != (tasks >= 0) {
								b.Fatal("actual loop lost its prompt or current goal visibility")
							}
						}
					}
				})
			}
		})
	}
}
