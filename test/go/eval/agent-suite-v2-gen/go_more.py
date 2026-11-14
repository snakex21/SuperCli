"""Go tasks: extract interface, misleading trace, flaky tests, package split."""
from common import Task, d, cmd, go_filler, GO_MOD


# ------------------------------------------------------------ extract iface
def extract_interface():
    t = Task("go-extract-interface")
    mod = "alerts"
    t.fx("go.mod", GO_MOD.format(mod=mod))
    t.fx("internal/smtp/client.go", d('''
        // Package smtp is a tiny mail client used by the alerting daemon.
        package smtp

        import (
        	"context"
        	"errors"
        	"strings"
        	"time"
        )

        // Message is one outgoing mail.
        type Message struct {
        	To      string
        	Subject string
        	Body    string
        }

        // Stats counts client activity.
        type Stats struct {
        	Sent   int
        	Failed int
        }

        // Client talks to one relay host.
        type Client struct {
        	host    string
        	timeout time.Duration
        	stats   Stats
        	closed  bool
        }

        // Dial creates a client for host ("name:port").
        func Dial(host string) (*Client, error) {
        	if !strings.Contains(host, ":") {
        		return nil, errors.New("smtp: host needs a port")
        	}
        	return &Client{host: host, timeout: 10 * time.Second}, nil
        }

        // Send delivers m.
        func (c *Client) Send(ctx context.Context, m Message) error {
        	if c.closed {
        		return errors.New("smtp: client closed")
        	}
        	if err := ctx.Err(); err != nil {
        		c.stats.Failed++
        		return err
        	}
        	if m.To == "" {
        		c.stats.Failed++
        		return errors.New("smtp: empty recipient")
        	}
        	c.stats.Sent++
        	return nil
        }

        // Ping checks the relay is reachable.
        func (c *Client) Ping(ctx context.Context) error {
        	if c.closed {
        		return errors.New("smtp: client closed")
        	}
        	return ctx.Err()
        }

        // SetTimeout changes the per-command timeout.
        func (c *Client) SetTimeout(d time.Duration) { c.timeout = d }

        // Stats returns counters.
        func (c *Client) Stats() Stats { return c.stats }

        // Close releases the connection.
        func (c *Client) Close() error {
        	c.closed = true
        	return nil
        }
        '''))
    t.fx("internal/smtp/client_test.go", d('''
        package smtp

        import (
        	"context"
        	"testing"
        )

        func TestClient(t *testing.T) {
        	c, err := Dial("relay:25")
        	if err != nil {
        		t.Fatal(err)
        	}
        	if err := c.Send(context.Background(), Message{To: "a@b"}); err != nil {
        		t.Fatal(err)
        	}
        	if c.Stats().Sent != 1 {
        		t.Fatal("stats")
        	}
        }
        '''))
    alerter = d('''
        // Package service turns events into mails.
        package service

        import (
        	"context"
        	"fmt"
        	"strings"

        	"alerts/internal/smtp"
        )

        // Alerter mails events at or above a minimum level.
        type Alerter struct {
        	client   *smtp.Client
        	to       string
        	minLevel int
        }

        // NewAlerter builds an Alerter.
        func NewAlerter(c *smtp.Client, to string, minLevel int) *Alerter {
        	return &Alerter{client: c, to: to, minLevel: minLevel}
        }

        // Alert mails msg when level >= minLevel. The subject is "[L<level>] "
        // followed by the first line of msg.
        func (a *Alerter) Alert(ctx context.Context, level int, msg string) error {
        	if level < a.minLevel {
        		return nil
        	}
        	first, _, _ := strings.Cut(msg, "\\n")
        	return a.client.Send(ctx, smtp.Message{To: a.to, Subject: fmt.Sprintf("[L%d] %s", level, first), Body: msg})
        }

        // Healthy reports whether the relay answers.
        func (a *Alerter) Healthy(ctx context.Context) bool {
        	return a.client.Ping(ctx) == nil
        }
        ''')
    t.fx("internal/service/alerter.go", alerter)
    digest = d('''
        package service

        import (
        	"context"
        	"fmt"
        	"strings"

        	"alerts/internal/smtp"
        )

        // Digest batches low-priority notes into one mail.
        type Digest struct {
        	client *smtp.Client
        	to     string
        	items  []string
        }

        // NewDigest builds a Digest.
        func NewDigest(c *smtp.Client, to string) *Digest {
        	return &Digest{client: c, to: to}
        }

        // Add queues one note.
        func (d *Digest) Add(note string) { d.items = append(d.items, note) }

        // Flush mails all queued notes (if any) and clears the queue on success.
        func (d *Digest) Flush(ctx context.Context) error {
        	if len(d.items) == 0 {
        		return nil
        	}
        	body := strings.Join(d.items, "\\n")
        	err := d.client.Send(ctx, smtp.Message{To: d.to, Subject: fmt.Sprintf("Digest (%d)", len(d.items)), Body: body})
        	if err == nil {
        		d.items = nil
        	}
        	return err
        }
        ''')
    t.fx("internal/service/digest.go", digest)
    main_go = d('''
        // Command alertd wires the mail client into the alerting service.
        package main

        import (
        	"context"
        	"fmt"
        	"os"
        	"time"

        	"alerts/internal/service"
        	"alerts/internal/smtp"
        )

        func main() {
        	c, err := smtp.Dial("localhost:25")
        	if err != nil {
        		fmt.Fprintln(os.Stderr, err)
        		os.Exit(1)
        	}
        	defer c.Close()
        	c.SetTimeout(5 * time.Second)
        	a := service.NewAlerter(c, "ops@example.com", 2)
        	d := service.NewDigest(c, "ops@example.com")
        	ctx := context.Background()
        	_ = a.Alert(ctx, 3, "disk almost full")
        	d.Add("nightly job ok")
        	_ = d.Flush(ctx)
        	fmt.Println("sent:", c.Stats().Sent)
        }
        ''')
    t.fx("cmd/alertd/main.go", main_go)
    t.fx("README.md", "# alerts\n\nAlerting daemon. `go test ./...`.\n")
    for pkg in ["rules", "store", "metrics", "schedule", "config", "templates", "routing"]:
        go_filler(t, mod, "internal/" + pkg, count=4)

    # reference
    t.ref("internal/service/sender.go", d('''
        package service

        import (
        	"context"

        	"alerts/internal/smtp"
        )

        // Sender is the part of the mail client the service needs.
        type Sender interface {
        	Send(ctx context.Context, m smtp.Message) error
        	Ping(ctx context.Context) error
        }

        var _ Sender = (*smtp.Client)(nil)
        '''))
    t.ref("internal/service/alerter.go", alerter.replace("client   *smtp.Client", "client   Sender").replace("func NewAlerter(c *smtp.Client,", "func NewAlerter(c Sender,"))
    t.ref("internal/service/digest.go", digest.replace("client *smtp.Client", "client Sender").replace("func NewDigest(c *smtp.Client,", "func NewDigest(c Sender,"))
    t.ref("internal/service/alerter_test.go", d('''
        package service

        import (
        	"context"
        	"testing"

        	"alerts/internal/smtp"
        )

        type fakeSender struct{ sent []smtp.Message }

        func (f *fakeSender) Send(_ context.Context, m smtp.Message) error {
        	f.sent = append(f.sent, m)
        	return nil
        }

        func (f *fakeSender) Ping(context.Context) error { return nil }

        func TestAlerterUsesSender(t *testing.T) {
        	f := &fakeSender{}
        	a := NewAlerter(f, "ops", 2)
        	_ = a.Alert(context.Background(), 1, "ignored")
        	_ = a.Alert(context.Background(), 2, "kept")
        	if len(f.sent) != 1 || f.sent[0].Subject != "[L2] kept" {
        		t.Fatalf("sent = %+v", f.sent)
        	}
        }
        '''))
    t.gr("internal/service/zz_hidden_test.go", d('''
        package service

        import (
        	"context"
        	"errors"
        	"testing"

        	"alerts/internal/smtp"
        )

        // hiddenFake implements exactly the methods the service package uses.
        type hiddenFake struct {
        	sent    []smtp.Message
        	pingErr error
        	sendErr error
        }

        func (f *hiddenFake) Send(_ context.Context, m smtp.Message) error {
        	if f.sendErr != nil {
        		return f.sendErr
        	}
        	f.sent = append(f.sent, m)
        	return nil
        }

        func (f *hiddenFake) Ping(context.Context) error { return f.pingErr }

        var _ Sender = (*hiddenFake)(nil)
        var _ Sender = (*smtp.Client)(nil)

        func TestHiddenAlerterAndDigestWithFake(t *testing.T) {
        	ctx := context.Background()
        	f := &hiddenFake{}
        	a := NewAlerter(f, "ops@x", 2)
        	if err := a.Alert(ctx, 1, "low"); err != nil {
        		t.Fatal(err)
        	}
        	if err := a.Alert(ctx, 3, "disk full\\nsecond line"); err != nil {
        		t.Fatal(err)
        	}
        	if len(f.sent) != 1 || f.sent[0].Subject != "[L3] disk full" || f.sent[0].To != "ops@x" {
        		t.Fatalf("sent = %+v", f.sent)
        	}
        	if !a.Healthy(ctx) {
        		t.Fatal("healthy fake reported unhealthy")
        	}
        	f.pingErr = errors.New("down")
        	if a.Healthy(ctx) {
        		t.Fatal("ping error ignored")
        	}
        	dg := NewDigest(f, "ops@x")
        	dg.Add("a")
        	dg.Add("b")
        	f.sendErr = errors.New("boom")
        	if err := dg.Flush(ctx); err == nil {
        		t.Fatal("flush error swallowed")
        	}
        	f.sendErr = nil
        	if err := dg.Flush(ctx); err != nil || f.sent[len(f.sent)-1].Subject != "Digest (2)" {
        		t.Fatalf("flush = %v, %+v", err, f.sent)
        	}
        }
        '''))
    t.gr("graders/check_agent_test.py", d('''
        import glob, os, re, sys
        files = [f for f in glob.glob("internal/service/*_test.go") if not os.path.basename(f).startswith("zz_hidden")]
        ok = False
        for f in files:
            text = open(f, encoding="utf-8", errors="replace").read()
            if re.search(r"func Test\\w+\\(t \\*testing\\.T\\)", text) and re.search(r"\\)\\s*Send\\(", text):
                ok = True
        if not ok:
            print("no agent-written test with a fake Send implementation in internal/service")
            sys.exit(1)
        src = [f for f in glob.glob("internal/service/*.go") if not f.endswith("_test.go")]
        if not any(re.search(r"var\\s+_\\s+Sender\\s*=\\s*(\\(\\*smtp\\.Client\\)\\(nil\\)|&smtp\\.Client\\{\\}|new\\(smtp\\.Client\\))", open(f, encoding="utf-8").read()) for f in src):
            print("no compile-time assertion `var _ Sender = (*smtp.Client)(nil)` in package service")
            sys.exit(1)
        print("ok")
        '''))
    t.register(
        name="Extract a minimal consumer-side interface and test with a fake",
        category="refactor", tags=["go", "interface", "multi-file"], difficulty="hard",
        prompt=("The `service` package depends directly on `*smtp.Client`, so it cannot be unit-tested. Introduce an "
                "interface named `Sender` in package `service` that contains exactly the methods of `*smtp.Client` "
                "that the service package itself calls (no more), make `NewAlerter` and `NewDigest` (and the structs) "
                "depend on it, and add a compile-time assertion in package service that `*smtp.Client` implements it. "
                "Do not change the smtp package. Add a unit test in internal/service that uses a fake Sender. "
                "`go vet ./...` and `go test ./...` must pass."),
        strictDiff=True,
        allowedChangedFiles=["internal/service/**", "cmd/alertd/main.go"],
        forbiddenChangedFiles=["internal/smtp/**"],
        verificationCommands=[cmd("go-vet", "go", "vet", "./..."), cmd("go-test", "go", "test", "-count=1", "./..."),
                              cmd("agent-test", "python", "graders/check_agent_test.py")],
    )


# ------------------------------------------------------------ misleading trace
def misleading_trace():
    t = Task("go-misleading-trace")
    mod = "tmplsvc"
    t.fx("go.mod", GO_MOD.format(mod=mod))
    t.fx("config/config.go", d('''
        // Package config holds template settings.
        package config

        // Config configures rendering.
        type Config struct {
        	Name   string
        	Vars   map[string]string
        	Strict bool
        }

        // Base returns the built-in defaults.
        func Base() Config {
        	return Config{Name: "site", Vars: map[string]string{"brand": "Acme", "year": "2026"}}
        }
        '''))
    merge = d('''
        package config

        // Merge overlays o on top of base.
        //
        // Contract: the result's Vars is never nil and is always a fresh map
        // holding base's keys plus o's keys (o wins on conflicts). Neither
        // base nor o is modified, and later writes to the result never leak
        // back into base or o.
        func Merge(base, o Config) Config {
        	out := Config{Name: base.Name, Strict: base.Strict || o.Strict}
        	if o.Name != "" {
        		out.Name = o.Name
        	}
        	if len(o.Vars) > 0 {
        		out.Vars = make(map[string]string, len(base.Vars)+len(o.Vars))
        		for k, v := range base.Vars {
        			out.Vars[k] = v
        		}
        		for k, v := range o.Vars {
        			out.Vars[k] = v
        		}
        	}
        	return out
        }
        ''')
    t.fx("config/merge.go", merge)
    t.fx("config/merge_test.go", d('''
        package config

        import "testing"

        func TestMergeOverride(t *testing.T) {
        	m := Merge(Base(), Config{Name: "blog", Vars: map[string]string{"year": "2027"}})
        	if m.Name != "blog" || m.Vars["year"] != "2027" || m.Vars["brand"] != "Acme" {
        		t.Fatalf("Merge = %+v", m)
        	}
        }
        '''))
    defaults = d('''
        package render

        import "tmplsvc/config"

        // applyDefaults fills per-page variables before substitution.
        func applyDefaults(c config.Config, page string) {
        	c.Vars["page"] = page
        	if _, ok := c.Vars["lang"]; !ok {
        		c.Vars["lang"] = "en"
        	}
        }
        ''')
    t.fx("render/defaults.go", defaults)
    t.fx("render/render.go", d('''
        // Package render substitutes {{name}} placeholders.
        package render

        import (
        	"strings"

        	"tmplsvc/config"
        )

        // Render fills tmpl for page using c.Vars.
        // TODO(templates): unbalanced braces are passed through verbatim.
        func Render(c config.Config, page, tmpl string) string {
        	applyDefaults(c, page)
        	var b strings.Builder
        	for {
        		i := strings.Index(tmpl, "{{")
        		if i < 0 {
        			b.WriteString(tmpl)
        			return b.String()
        		}
        		j := strings.Index(tmpl[i:], "}}")
        		if j < 0 {
        			b.WriteString(tmpl)
        			return b.String()
        		}
        		b.WriteString(tmpl[:i])
        		key := strings.TrimSpace(tmpl[i+2 : i+j])
        		if v, ok := c.Vars[key]; ok {
        			b.WriteString(v)
        		} else if !c.Strict {
        			b.WriteString(tmpl[i : i+j+2])
        		}
        		tmpl = tmpl[i+j+2:]
        	}
        }
        '''))
    t.fx("render/render_test.go", d('''
        package render

        import (
        	"testing"

        	"tmplsvc/config"
        )

        func TestRenderWithOverrides(t *testing.T) {
        	c := config.Merge(config.Base(), config.Config{Vars: map[string]string{"lang": "pl"}})
        	got := Render(c, "home", "{{brand}}/{{page}}/{{lang}}/{{missing}}")
        	if got != "Acme/home/pl/{{missing}}" {
        		t.Fatalf("got %q", got)
        	}
        }

        func TestRenderWithBaseOnly(t *testing.T) {
        	c := config.Merge(config.Base(), config.Config{Name: "docs"})
        	got := Render(c, "about", "{{brand}} {{year}} {{page}} {{lang}}")
        	if got != "Acme 2026 about en" {
        		t.Fatalf("got %q", got)
        	}
        }
        '''))
    t.fx("cmd/tmpl/main.go", d('''
        // Command tmpl renders one template from stdin.
        package main

        import (
        	"fmt"
        	"io"
        	"os"

        	"tmplsvc/config"
        	"tmplsvc/render"
        )

        func main() {
        	data, _ := io.ReadAll(os.Stdin)
        	page := "index"
        	if len(os.Args) > 1 {
        		page = os.Args[1]
        	}
        	fmt.Print(render.Render(config.Merge(config.Base(), config.Config{}), page, string(data)))
        }
        '''))
    t.fx("README.md", d('''
        # tmplsvc

        Template rendering service.

        Troubleshooting: panics inside `render` are almost always caused by
        malformed templates (unbalanced `{{`/`}}`); check the template first.
        '''))
    for pkg in ["assets", "cache", "feeds", "sitemap", "i18n", "images", "search"]:
        go_filler(t, mod, "internal/" + pkg, count=4)
    render_line = [i + 1 for i, l in enumerate(t.read_fx("render/render.go").splitlines()) if "applyDefaults(c, page)" in l][0]
    test_line = [i + 1 for i, l in enumerate(t.read_fx("render/render_test.go").splitlines()) if 'Render(c, "about"' in l][0]
    defaults_line = [i + 1 for i, l in enumerate(defaults.splitlines()) if 'c.Vars["page"] = page' in l][0]
    trace = d('''
        --- FAIL: TestRenderWithBaseOnly (0.00s)
        panic: assignment to entry in nil map [recovered]
        	panic: assignment to entry in nil map

        goroutine 7 [running]:
        testing.tRunner.func1.2({0x5a1f20, 0x5f3c18})
        	/usr/local/go/src/testing/testing.go:1734 +0x21c
        panic({0x5a1f20?, 0x5f3c18?})
        	/usr/local/go/src/runtime/panic.go:792 +0x132
        tmplsvc/render.applyDefaults(...)
        	/src/tmplsvc/render/defaults.go:%d
        tmplsvc/render.Render({{0x5c2a11, 0x4}, 0x0, 0x0}, {0x5c29f3, 0x5}, {0x5c8d40, 0x22})
        	/src/tmplsvc/render/render.go:%d +0x6e
        tmplsvc/render.TestRenderWithBaseOnly(0xc000003a40)
        	/src/tmplsvc/render/render_test.go:%d +0x105
        FAIL	tmplsvc/render	0.004s
        ''') % (defaults_line, render_line, test_line)
    old_block = ("\tif len(o.Vars) > 0 {\n\t\tout.Vars = make(map[string]string, len(base.Vars)+len(o.Vars))\n"
                 "\t\tfor k, v := range base.Vars {\n\t\t\tout.Vars[k] = v\n\t\t}\n"
                 "\t\tfor k, v := range o.Vars {\n\t\t\tout.Vars[k] = v\n\t\t}\n\t}\n")
    new_block = ("\tout.Vars = make(map[string]string, len(base.Vars)+len(o.Vars))\n"
                 "\tfor k, v := range base.Vars {\n\t\tout.Vars[k] = v\n\t}\n"
                 "\tfor k, v := range o.Vars {\n\t\tout.Vars[k] = v\n\t}\n")
    if old_block not in merge:
        raise SystemExit("go-misleading-trace: merge block not found")
    t.ref("config/merge.go", merge.replace(old_block, new_block))
    t.gr("config/zz_hidden_test.go", d('''
        package config

        import "testing"

        func TestHiddenMergeContract(t *testing.T) {
        	base := Base()
        	m := Merge(base, Config{})
        	if m.Vars == nil || m.Vars["brand"] != "Acme" || m.Name != "site" {
        		t.Fatalf("Merge(base, empty) = %+v", m)
        	}
        	m.Vars["page"] = "x"
        	if _, leaked := base.Vars["page"]; leaked {
        		t.Fatal("write to merged Vars leaked into base")
        	}
        	o := Config{Vars: map[string]string{"k": "v"}}
        	m2 := Merge(Config{}, o)
        	m2.Vars["z"] = "1"
        	if _, leaked := o.Vars["z"]; leaked || m2.Vars["k"] != "v" {
        		t.Fatal("override map aliased or lost")
        	}
        	if e := Merge(Config{}, Config{}); e.Vars == nil {
        		t.Fatal("nil Vars for empty inputs")
        	}
        }
        '''))
    t.register(
        name="Debug a nil-map panic whose stack trace points at the wrong package",
        category="debugging", tags=["go", "debugging", "root-cause"], difficulty="hard",
        prompt=("`go test ./...` fails in this repository:\n\n```\n" + trace + "```\n\n"
                "Find and fix the root cause (not just the symptom) with a minimal change. Do not modify any _test.go "
                "file. Make sure `go test ./...` passes."),
        strictDiff=True,
        allowedChangedFiles=["config/*.go", "render/*.go"],
        forbiddenChangedFiles=["**/*_test.go"],
        verificationCommands=[cmd("go-test", "go", "test", "-count=1", "./...")],
        fileChecks=[{"id": "merge-small-change", "path": "config/merge.go", "maxChangedLines": 24}],
    )


# ------------------------------------------------------------ flaky
def flaky():
    t = Task("go-flaky-tests")
    mod = "scoreboard"
    t.fx("go.mod", GO_MOD.format(mod=mod))
    rank = d('''
        // Package board ranks players.
        package board

        import "sort"

        // Entry is one ranked player.
        type Entry struct {
        	Name  string
        	Score int
        }

        // Rank returns all players ordered by score, highest first. Players
        // with equal scores are ordered by name (ascending).
        func Rank(scores map[string]int) []Entry {
        	out := make([]Entry, 0, len(scores))
        	for name, s := range scores {
        		out = append(out, Entry{Name: name, Score: s})
        	}
        	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
        	return out
        }

        // Top returns the first n entries of Rank.
        func Top(scores map[string]int, n int) []Entry {
        	r := Rank(scores)
        	if n < len(r) {
        		r = r[:n]
        	}
        	return r
        }
        ''')
    t.fx("board/rank.go", rank)
    collect = d('''
        package board

        import (
        	"context"
        	"sync"
        )

        // Source produces one player's score.
        type Source interface {
        	Fetch(ctx context.Context) (Entry, error)
        }

        // CollectAll fetches every source concurrently. Results are returned in
        // the same order as srcs. If any fetch fails, the error returned is
        // the one from the earliest failing source in srcs order.
        func CollectAll(ctx context.Context, srcs []Source) ([]Entry, error) {
        	var (
        		mu       sync.Mutex
        		wg       sync.WaitGroup
        		out      []Entry
        		firstErr error
        	)
        	for _, s := range srcs {
        		wg.Add(1)
        		go func(s Source) {
        			defer wg.Done()
        			e, err := s.Fetch(ctx)
        			mu.Lock()
        			defer mu.Unlock()
        			if err != nil {
        				if firstErr == nil {
        					firstErr = err
        				}
        				return
        			}
        			out = append(out, e)
        		}(s)
        	}
        	wg.Wait()
        	if firstErr != nil {
        		return nil, firstErr
        	}
        	return out, nil
        }
        ''')
    t.fx("board/collect.go", collect)
    t.fx("board/board_test.go", d('''
        package board

        import (
        	"context"
        	"fmt"
        	"testing"
        )

        func TestRank(t *testing.T) {
        	got := Rank(map[string]int{"dora": 5, "ann": 7, "cid": 5, "bob": 7, "eve": 1, "fay": 5})
        	want := []Entry{{"ann", 7}, {"bob", 7}, {"cid", 5}, {"dora", 5}, {"fay", 5}, {"eve", 1}}
        	if fmt.Sprint(got) != fmt.Sprint(want) {
        		t.Fatalf("Rank = %v, want %v", got, want)
        	}
        }

        type staticSource Entry

        func (s staticSource) Fetch(context.Context) (Entry, error) { return Entry(s), nil }

        func TestCollect(t *testing.T) {
        	var srcs []Source
        	for i := 0; i < 8; i++ {
        		srcs = append(srcs, staticSource{Name: fmt.Sprintf("p%d", i), Score: i})
        	}
        	got, err := CollectAll(context.Background(), srcs)
        	if err != nil {
        		t.Fatal(err)
        	}
        	for i, e := range got {
        		if e.Score != i {
        			t.Fatalf("CollectAll order = %v", got)
        		}
        	}
        }
        '''))
    t.fx("README.md", d('''
        # scoreboard

        `TestRank` and `TestCollect` fail intermittently in CI (roughly one run
        in five). Locally they usually pass.
        '''))
    t.fx(".ci/last-failure.txt", d('''
        === RUN   TestRank
            board_test.go:12: Rank = [{bob 7} {ann 7} {fay 5} {cid 5} {dora 5} {eve 1}], want [{ann 7} {bob 7} {cid 5} {dora 5} {fay 5} {eve 1}]
        --- FAIL: TestRank (0.00s)
        === RUN   TestCollect
        --- PASS: TestCollect (0.00s)
        FAIL
        '''))
    for pkg in ["season", "players", "matches", "elo", "export", "audit"]:
        go_filler(t, mod, "internal/" + pkg, count=4)
    t.ref("board/rank.go", rank.replace(
        "sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })",
        d('''
        sort.Slice(out, func(i, j int) bool {
        		if out[i].Score != out[j].Score {
        			return out[i].Score > out[j].Score
        		}
        		return out[i].Name < out[j].Name
        	})''').rstrip("\n")))
    t.ref("board/collect.go", d('''
        package board

        import (
        	"context"
        	"sync"
        )

        // Source produces one player's score.
        type Source interface {
        	Fetch(ctx context.Context) (Entry, error)
        }

        // CollectAll fetches every source concurrently. Results are returned in
        // the same order as srcs. If any fetch fails, the error returned is
        // the one from the earliest failing source in srcs order.
        func CollectAll(ctx context.Context, srcs []Source) ([]Entry, error) {
        	out := make([]Entry, len(srcs))
        	errs := make([]error, len(srcs))
        	var wg sync.WaitGroup
        	for i, s := range srcs {
        		wg.Add(1)
        		go func(i int, s Source) {
        			defer wg.Done()
        			out[i], errs[i] = s.Fetch(ctx)
        		}(i, s)
        	}
        	wg.Wait()
        	for _, err := range errs {
        		if err != nil {
        			return nil, err
        		}
        	}
        	return out, nil
        }
        '''))
    t.answer("Two root causes: Rank built its slice from map iteration (random order in Go) and sort.Slice only "
             "compared scores, so ties kept the random map order; fixed by breaking ties by name. CollectAll appended "
             "results from goroutines in completion order (and kept whichever error finished first); fixed by "
             "writing each result/error into its own index and picking the earliest failing source.\n")
    t.gr("board/zz_hidden_test.go", d('''
        package board

        import (
        	"context"
        	"errors"
        	"fmt"
        	"runtime"
        	"testing"
        	"time"
        )

        type slowSource struct {
        	e     Entry
        	delay time.Duration
        	err   error
        }

        func (s slowSource) Fetch(context.Context) (Entry, error) {
        	time.Sleep(s.delay)
        	runtime.Gosched()
        	return s.e, s.err
        }

        func TestHiddenCollectOrderAndFirstError(t *testing.T) {
        	for round := 0; round < 30; round++ {
        		var srcs []Source
        		for i := 0; i < 6; i++ {
        			srcs = append(srcs, slowSource{e: Entry{Name: fmt.Sprint(i), Score: i}, delay: time.Duration(6-i) * 200 * time.Microsecond})
        		}
        		got, err := CollectAll(context.Background(), srcs)
        		if err != nil {
        			t.Fatal(err)
        		}
        		for i, e := range got {
        			if e.Score != i {
        				t.Fatalf("order = %v", got)
        			}
        		}
        		early, late := errors.New("early"), errors.New("late")
        		srcs = []Source{
        			slowSource{delay: 0},
        			slowSource{err: early, delay: 2 * time.Millisecond},
        			slowSource{err: late, delay: 0},
        		}
        		if _, err := CollectAll(context.Background(), srcs); err != early {
        			t.Fatalf("error = %v, want the earliest failing source's error", err)
        		}
        	}
        }

        func TestHiddenRankTiesAndTop(t *testing.T) {
        	for round := 0; round < 50; round++ {
        		got := Top(map[string]int{"z": 3, "y": 3, "x": 3, "w": 9, "v": 3, "u": 3, "t": 3, "s": 3, "r": 3}, 4)
        		if fmt.Sprint(got) != "[{w 9} {r 3} {s 3} {t 3}]" {
        			t.Fatalf("Top = %v", got)
        		}
        	}
        }
        '''))
    t.register(
        name="Diagnose and fix two flaky tests (map order, goroutine completion order)",
        category="debugging", tags=["go", "flaky", "concurrency"], difficulty="hard",
        prompt=("`TestRank` and `TestCollect` in board/ are flaky in CI (see README.md and .ci/last-failure.txt). "
                "Find the root causes and fix them in the production code so the documented behaviour holds every "
                "time. Do not modify any test, and do not add retries, sleeps or skips. In your final answer explain "
                "each root cause in one or two sentences."),
        strictDiff=True,
        allowedChangedFiles=["board/rank.go", "board/collect.go"],
        forbiddenChangedFiles=["**/*_test.go"],
        verificationCommands=[cmd("stress", "go", "test", "-count=300", "-run", "TestRank|TestCollect", "./board/"),
                              cmd("hidden", "go", "test", "-count=3", "./...")],
        answerChecks=[{"id": "explains-map", "anyOf": [r"\bmap\b.*(order|iterat|random)", r"(order|iterat|random).*\bmap\b"], "regex": True},
                      {"id": "explains-goroutines", "anyOf": [r"goroutine", r"concurren", r"completion order", r"finish(es|ed)? first", r"współbie", r"równoleg"], "regex": True}],
    )


# ------------------------------------------------------------ split util
def split_util():
    t = Task("go-split-util")
    mod = "gateway"
    t.fx("go.mod", GO_MOD.format(mod=mod))
    t.fx("internal/util/strings.go", d('''
        // Package util is a grab bag of helpers (scheduled for removal).
        package util

        import "strings"

        // Slugify lowercases s and joins words with '-'.
        func Slugify(s string) string {
        	var parts []string
        	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
        		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
        	}) {
        		parts = append(parts, f)
        	}
        	return strings.Join(parts, "-")
        }

        // Truncate cuts s to at most n runes, adding "..." when cut.
        func Truncate(s string, n int) string {
        	r := []rune(s)
        	if len(r) <= n {
        		return s
        	}
        	if n <= 3 {
        		return string(r[:n])
        	}
        	return string(r[:n-3]) + "..."
        }
        '''))
    t.fx("internal/util/time.go", d('''
        package util

        import (
        	"fmt"
        	"strconv"
        	"strings"
        	"time"
        )

        // ParseDurationLoose accepts Go durations, bare seconds ("90") and days ("2d").
        func ParseDurationLoose(s string) (time.Duration, error) {
        	s = strings.TrimSpace(s)
        	if n, err := strconv.Atoi(s); err == nil {
        		return time.Duration(n) * time.Second, nil
        	}
        	if strings.HasSuffix(s, "d") {
        		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
        		if err != nil {
        			return 0, fmt.Errorf("bad duration %q", s)
        		}
        		return time.Duration(n) * 24 * time.Hour, nil
        	}
        	return time.ParseDuration(s)
        }

        // FormatAgo renders d as "5m ago", "3h ago" or "2d ago".
        func FormatAgo(d time.Duration) string {
        	switch {
        	case d < time.Hour:
        		return fmt.Sprintf("%dm ago", int(d.Minutes()))
        	case d < 24*time.Hour:
        		return fmt.Sprintf("%dh ago", int(d.Hours()))
        	}
        	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
        }
        '''))
    t.fx("internal/util/ids.go", d('''
        package util

        import (
        	"fmt"
        	"sync/atomic"
        )

        var seq atomic.Uint64

        // NewID returns prefix_000001, prefix_000002, ...
        func NewID(prefix string) string {
        	return fmt.Sprintf("%s_%06d", prefix, seq.Add(1))
        }
        '''))
    t.fx("internal/util/util_test.go", d('''
        package util

        import (
        	"strings"
        	"testing"
        	"time"
        )

        func TestStrings(t *testing.T) {
        	if got := Slugify("Hello, World 2"); got != "hello-world-2" {
        		t.Fatal(got)
        	}
        	if got := Truncate("abcdefgh", 6); got != "abc..." {
        		t.Fatal(got)
        	}
        }

        func TestTime(t *testing.T) {
        	if d, _ := ParseDurationLoose("2d"); d != 48*time.Hour {
        		t.Fatal(d)
        	}
        	if got := FormatAgo(90 * time.Minute); got != "1h ago" {
        		t.Fatal(got)
        	}
        }

        func TestIDs(t *testing.T) {
        	if id := NewID("req"); !strings.HasPrefix(id, "req_") {
        		t.Fatal(id)
        	}
        }
        '''))
    users = {
        "internal/routes/routes.go": ("routes", '''
            // Package routes maps paths to backends.
            package routes

            import "gateway/internal/util"

            // Route is one mapping.
            type Route struct {
            	ID      string
            	Path    string
            	Backend string
            }

            // New builds a route with a generated id and a slug path.
            func New(title, backend string) Route {
            	return Route{ID: util.NewID("rt"), Path: "/" + util.Slugify(title), Backend: backend}
            }
            '''),
        "internal/routes/table.go": ("routes", '''
            package routes

            import (
            	"sort"

            	"gateway/internal/util"
            )

            // Table renders routes as "path -> backend" lines, sorted by path.
            func Table(rs []Route, width int) []string {
            	var lines []string
            	for _, r := range rs {
            		lines = append(lines, util.Truncate(r.Path+" -> "+r.Backend, width))
            	}
            	sort.Strings(lines)
            	return lines
            }
            '''),
        "internal/routes/routes_test.go": ("routes", '''
            package routes

            import (
            	"strings"
            	"testing"
            )

            func TestNew(t *testing.T) {
            	r := New("User Profile", "users:8080")
            	if r.Path != "/user-profile" || !strings.HasPrefix(r.ID, "rt_") {
            		t.Fatalf("%+v", r)
            	}
            	if got := Table([]Route{r}, 12); got[0] != "/user-pro..." {
            		t.Fatalf("%q", got)
            	}
            }
            '''),
        "internal/proxy/timeouts.go": ("proxy", '''
            // Package proxy forwards requests.
            package proxy

            import (
            	"time"

            	"gateway/internal/util"
            )

            // Timeouts for upstream calls.
            type Timeouts struct {
            	Dial, Read time.Duration
            }

            // ParseTimeouts reads loose duration strings.
            func ParseTimeouts(dial, read string) (Timeouts, error) {
            	d, err := util.ParseDurationLoose(dial)
            	if err != nil {
            		return Timeouts{}, err
            	}
            	r, err := util.ParseDurationLoose(read)
            	if err != nil {
            		return Timeouts{}, err
            	}
            	return Timeouts{Dial: d, Read: r}, nil
            }
            '''),
        "internal/proxy/requests.go": ("proxy", '''
            package proxy

            import (
            	u "gateway/internal/util"
            )

            // Request is a proxied request.
            type Request struct {
            	ID   string
            	Path string
            }

            // NewRequest tags a request with an id.
            func NewRequest(path string) Request {
            	return Request{ID: u.NewID("req"), Path: path}
            }
            '''),
        "internal/proxy/batch.go": ("proxy", '''
            package proxy

            import "gateway/internal/util"

            // Batch assigns ids to a batch of paths and returns them in order.
            func Batch(paths []string) []string {
            	ids := make([]string, 0, len(paths))
            	for range paths {
            		ids = append(ids, util.NewID("b"))
            	}
            	return ids
            }
            '''),
        "internal/proxy/proxy_test.go": ("proxy", '''
            package proxy

            import (
            	"strings"
            	"testing"
            	"time"
            )

            func TestProxyHelpers(t *testing.T) {
            	to, err := ParseTimeouts("5", "1m")
            	if err != nil || to.Dial != 5*time.Second || to.Read != time.Minute {
            		t.Fatalf("%+v %v", to, err)
            	}
            	if r := NewRequest("/x"); !strings.HasPrefix(r.ID, "req_") {
            		t.Fatal(r.ID)
            	}
            	if ids := Batch([]string{"a", "b"}); len(ids) != 2 || ids[0] == ids[1] {
            		t.Fatal(ids)
            	}
            }
            '''),
        "internal/auth/session.go": ("auth", '''
            // Package auth issues sessions.
            package auth

            import (
            	"time"

            	"gateway/internal/util"
            )

            // Session is a login session.
            type Session struct {
            	ID      string
            	User    string
            	Created time.Time
            }

            // Start creates a session.
            func Start(user string, now time.Time) Session {
            	return Session{ID: util.NewID("sess"), User: util.Slugify(user), Created: now}
            }

            // Age describes how old s is.
            func Age(s Session, now time.Time) string {
            	return util.FormatAgo(now.Sub(s.Created))
            }
            '''),
        "internal/auth/policy.go": ("auth", '''
            package auth

            import (
            	"time"

            	"gateway/internal/util"
            )

            // MaxAge parses the configured session lifetime (default 7d).
            func MaxAge(cfg string) time.Duration {
            	if cfg == "" {
            		cfg = "7d"
            	}
            	d, err := util.ParseDurationLoose(cfg)
            	if err != nil {
            		return 7 * 24 * time.Hour
            	}
            	return d
            }
            '''),
        "internal/auth/auth_test.go": ("auth", '''
            package auth

            import (
            	"testing"
            	"time"
            )

            func TestSession(t *testing.T) {
            	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
            	s := Start("Jane Doe", now.Add(-3*time.Hour))
            	if s.User != "jane-doe" || Age(s, now) != "3h ago" {
            		t.Fatalf("%+v %s", s, Age(s, now))
            	}
            	if MaxAge("") != 7*24*time.Hour || MaxAge("90") != 90*time.Second {
            		t.Fatal("MaxAge")
            	}
            }
            '''),
        "internal/admin/audit.go": ("admin", '''
            // Package admin implements the admin console backend.
            package admin

            import (
            	"fmt"
            	"time"

            	"gateway/internal/util"
            )

            // Event is an audit event.
            type Event struct {
            	ID   string
            	What string
            	At   time.Time
            }

            // Describe renders an event line.
            func Describe(e Event, now time.Time, width int) string {
            	return util.Truncate(fmt.Sprintf("%s %s (%s)", e.ID, e.What, util.FormatAgo(now.Sub(e.At))), width)
            }

            // Record creates an event.
            func Record(what string, at time.Time) Event {
            	return Event{ID: util.NewID("evt"), What: what, At: at}
            }
            '''),
        "internal/admin/admin_test.go": ("admin", '''
            package admin

            import (
            	"strings"
            	"testing"
            	"time"
            )

            func TestDescribe(t *testing.T) {
            	now := time.Now()
            	e := Record("login", now.Add(-10*time.Minute))
            	if got := Describe(e, now, 80); !strings.Contains(got, "login (10m ago)") {
            		t.Fatal(got)
            	}
            }
            '''),
        "cmd/gatewayd/main.go": ("main", '''
            // Command gatewayd runs the gateway.
            package main

            import (
            	"fmt"
            	"os"

            	"gateway/internal/proxy"
            	"gateway/internal/routes"
            	"gateway/internal/util"
            )

            func main() {
            	to, err := proxy.ParseTimeouts("5", "30s")
            	if err != nil {
            		fmt.Fprintln(os.Stderr, err)
            		os.Exit(1)
            	}
            	r := routes.New("Health Check", "self:9000")
            	fmt.Println(util.Truncate(r.Path, 40), to.Dial)
            }
            '''),
    }
    for rel, (_, body) in users.items():
        t.fx(rel, d(body))
    t.fx("docs/ARCHITECTURE.md", d('''
        # gateway architecture

        - `internal/routes`: path table.
        - `internal/proxy`: upstream forwarding.
        - `internal/auth`: sessions.
        - `internal/util`: misc helpers (to be split up, see issue #41).
        '''))
    for pkg in ["ratelimit", "metrics", "tls", "health", "config", "cache"]:
        go_filler(t, mod, "internal/" + pkg, count=4)

    # reference: new packages + rewritten users
    t.delete("internal/util")
    util_strings = t.read_fx("internal/util/strings.go").replace("// Package util is a grab bag of helpers (scheduled for removal).\npackage util", "// Package textutil holds string helpers.\npackage textutil")
    t.ref("internal/textutil/textutil.go", util_strings)
    t.ref("internal/clock/clock.go", t.read_fx("internal/util/time.go").replace("package util", "// Package clock holds duration helpers.\npackage clock"))
    t.ref("internal/ids/ids.go", t.read_fx("internal/util/ids.go").replace("package util", "// Package ids generates identifiers.\npackage ids").replace("// NewID returns", "// New returns").replace("func NewID(", "func New("))
    t.ref("internal/textutil/textutil_test.go", d('''
        package textutil

        import "testing"

        func TestStrings(t *testing.T) {
        	if got := Slugify("Hello, World 2"); got != "hello-world-2" {
        		t.Fatal(got)
        	}
        	if got := Truncate("abcdefgh", 6); got != "abc..." {
        		t.Fatal(got)
        	}
        }
        '''))
    t.ref("internal/clock/clock_test.go", d('''
        package clock

        import (
        	"testing"
        	"time"
        )

        func TestTime(t *testing.T) {
        	if d, _ := ParseDurationLoose("2d"); d != 48*time.Hour {
        		t.Fatal(d)
        	}
        	if got := FormatAgo(90 * time.Minute); got != "1h ago" {
        		t.Fatal(got)
        	}
        }
        '''))
    t.ref("internal/ids/ids_test.go", d('''
        package ids

        import (
        	"strings"
        	"testing"
        )

        func TestIDs(t *testing.T) {
        	if id := New("req"); !strings.HasPrefix(id, "req_") {
        		t.Fatal(id)
        	}
        }
        '''))

    def rewrite(body):
        body = d(body)
        need = []
        if "util.Slugify" in body or "util.Truncate" in body:
            need.append('"gateway/internal/textutil"')
        if "util.ParseDurationLoose" in body or "util.FormatAgo" in body:
            need.append('"gateway/internal/clock"')
        if "util.NewID" in body or "u.NewID" in body:
            need.append('"gateway/internal/ids"')
        body = (body.replace("util.Slugify", "textutil.Slugify").replace("util.Truncate", "textutil.Truncate")
                .replace("util.ParseDurationLoose", "clock.ParseDurationLoose").replace("util.FormatAgo", "clock.FormatAgo")
                .replace("u.NewID", "ids.New").replace("util.NewID", "ids.New"))
        body = body.replace('\t"gateway/internal/util"\n', "".join("\t%s\n" % n for n in need))
        body = body.replace('\tu "gateway/internal/util"\n', "".join("\t%s\n" % n for n in need))
        if 'import "gateway/internal/util"' in body:
            if len(need) == 1:
                body = body.replace('import "gateway/internal/util"', "import " + need[0])
            else:
                body = body.replace('import "gateway/internal/util"', "import (\n" + "".join("\t%s\n" % n for n in need) + ")")
        body = body.replace("ids := make([]string, 0, len(paths))", "out := make([]string, 0, len(paths))").replace("ids = append(ids, ids.New(\"b\"))", "out = append(out, ids.New(\"b\"))").replace("\treturn ids\n", "\treturn out\n")
        return body
    for rel, (_, body) in users.items():
        if rel.endswith("_test.go"):
            continue
        t.ref(rel, rewrite(body))
    t.gr("internal/textutil/zz_hidden_test.go", d('''
        package textutil

        import "testing"

        func TestHiddenTextutil(t *testing.T) {
        	if Slugify("  Go -- Rocks!! ") != "go-rocks" || Truncate("żółwik", 4) != "ż..." {
        		t.Fatal("textutil behaviour changed")
        	}
        }
        '''))
    t.gr("internal/clock/zz_hidden_test.go", d('''
        package clock

        import (
        	"testing"
        	"time"
        )

        func TestHiddenClock(t *testing.T) {
        	if d, err := ParseDurationLoose("1h30m"); err != nil || d != 90*time.Minute {
        		t.Fatal(d, err)
        	}
        	if FormatAgo(49*time.Hour) != "2d ago" {
        		t.Fatal("FormatAgo")
        	}
        }
        '''))
    t.gr("internal/ids/zz_hidden_test.go", d('''
        package ids

        import "testing"

        func TestHiddenIDs(t *testing.T) {
        	a, b := New("x"), New("x")
        	if a == b || len(a) != len("x_000001") {
        		t.Fatal(a, b)
        	}
        }
        '''))
    t.register(
        name="Split a grab-bag package into three packages across ~15 call sites",
        category="refactor", tags=["go", "move-package", "multi-file"], difficulty="hard",
        prompt=("Split the package `gateway/internal/util` into three packages and delete it:\n"
                "- `internal/textutil` (package textutil): `Slugify`, `Truncate`;\n"
                "- `internal/clock` (package clock): `ParseDurationLoose`, `FormatAgo`;\n"
                "- `internal/ids` (package ids): `NewID` renamed to `New`.\n"
                "Behaviour must not change. Move the existing tests along with the code, update every caller and "
                "remove internal/util completely. `go vet ./...` and `go test ./...` must pass."),
        strictDiff=True,
        allowedChangedFiles=["internal/**/*.go", "cmd/**/*.go", "docs/ARCHITECTURE.md"],
        verificationCommands=[cmd("go-vet", "go", "vet", "./..."), cmd("go-test", "go", "test", "-count=1", "./...")],
        fileChecks=[
            {"id": "util-removed", "path": "internal/util/**", "exists": False, "critical": True},
            {"id": "no-util-imports", "path": "**/*.go", "notContains": ['"gateway/internal/util"'], "critical": True},
        ],
    )


def build():
    extract_interface()
    misleading_trace()
    flaky()
    split_util()
