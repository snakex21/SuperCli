// Command ui-bench runs model-free comparisons in an owned headless browser.
package main

import (
 "context"
 "embed"
 "encoding/json"
 "errors"
 "flag"
 "fmt"
 "io"
 "net/url"
 "os"
 "os/exec"
 "os/signal"
 "path/filepath"
 "strings"
 "time"

 "supercli/internal/testbrowser"
)

//go:embed *.js
var casesFS embed.FS

const defaultReference = "e83ca331b0a0892518a3083fad1fbe27aee21128"
var caseNames = []string{"tool-change-stats", "transcript-dom", "transcript-performance", "transcript-native"}

type options struct {
 Case, Reference, Output, Browser, URL string
 Timeout time.Duration
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
 var o options
 flags := flag.NewFlagSet("ui-bench", flag.ContinueOnError)
 flags.SetOutput(stderr)
 flags.StringVar(&o.Case, "case", "all", "all, tool-change-stats, transcript-dom, transcript-performance or transcript-native")
 reference := os.Getenv("TRANSCRIPT_BASE_REF")
 if reference == "" { reference = defaultReference }
 flags.StringVar(&o.Reference, "base-ref", reference, "git reference used by transcript baseline comparisons (TRANSCRIPT_BASE_REF)")
 flags.StringVar(&o.Output, "output", os.Getenv("TRANSCRIPT_BENCH_OUTPUT"), "optional JSON result file (TRANSCRIPT_BENCH_OUTPUT)")
 flags.StringVar(&o.Browser, "browser", "", "development Chrome/Chromium/Edge executable (SUPERCLI_TEST_BROWSER)")
 flags.StringVar(&o.URL, "url", "", "optional isolated loopback test page for transcript-performance/native only; never a live user session")
 flags.DurationVar(&o.Timeout, "timeout", 5*time.Minute, "whole benchmark deadline")
 if err := flags.Parse(args); err != nil { return o, err }
 if flags.NArg() != 0 || o.Timeout <= 0 { return o, errors.New("usage: go run ./scripts/ui-bench [-case name] [-base-ref ref] [-output file] [-browser executable] [-timeout 5m]") }
 if o.Case != "all" {
  found := false
  for _, name := range caseNames { found = found || name == o.Case }
  if !found { return o, fmt.Errorf("unknown case %q", o.Case) }
 }
 if o.Reference == "" || strings.HasPrefix(o.Reference, "-") || strings.ContainsAny(o.Reference, ":\r\n\x00") { return o, errors.New("base-ref must be a git revision, not an option or path") }
 if o.URL != "" {
  u, err := url.Parse(o.URL)
  if err != nil || u.User != nil || u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") { return o, errors.New("-url must name an isolated HTTP loopback fixture without credentials") }
  if o.Case != "transcript-performance" && o.Case != "transcript-native" { return o, errors.New("-url is only supported for transcript-performance or transcript-native") }
 }
 return o, nil
}

func repoRoot() (string, error) {
 current, err := os.Getwd()
 if err != nil { return "", err }
 for {
  if info, err := os.Stat(filepath.Join(current, "go.mod")); err == nil && info.Mode().IsRegular() { return current, nil }
  parent := filepath.Dir(current)
  if parent == current { return "", errors.New("run inside the SuperCli checkout") }
  current = parent
 }
}

func readBaseline(ctx context.Context, root, reference string) (map[string]string, error) {
 baseline := make(map[string]string)
 for _, name := range []string{"03-markdown.js", "04-transcript.js", "08-sessions.js"} {
  command := exec.CommandContext(ctx, "git", "show", reference+":internal/webgui/assets/js/"+name)
  command.Dir = root
  data, err := command.Output()
  if err != nil { return nil, fmt.Errorf("baseline %s/%s: %w (supply -base-ref or TRANSCRIPT_BASE_REF with the original renderer)", reference, name, err) }
  baseline[name] = string(data)
 }
 return baseline, nil
}

func run(ctx context.Context, root string, o options) (result map[string]any, err error) {
 selected := caseNames
 if o.Case != "all" { selected = []string{o.Case} }
 baseline := map[string]string{}
 if o.Case != "tool-change-stats" && o.Case != "transcript-native" {
  baseline, err = readBaseline(ctx, root, o.Reference)
  if err != nil { return nil, err }
 }
 sources, err := sourceSnapshot(root)
 if err != nil { return nil, err }
 server, err := serve(sources)
 if err != nil { return nil, err }
 defer server.Close()
 browser, err := testbrowser.Start(ctx, testbrowser.Options{Executable: o.Browser, Width: 1250, Height: 900})
 if err != nil { return nil, err }
 defer func() { err = errors.Join(err, browser.Close()) }()
 results := make(map[string]any)
 result = map[string]any{"kind": "Model-free native Chromium fixture measurements; not real FPS or WebView2 RAM", "baseline": o.Reference, "cases": results}
 encoded, err := json.Marshal(map[string]any{"baseline": baseline, "reference": o.Reference})
 if err != nil { return nil, err }
 for _, name := range selected {
  address := server.URL+"/"
  if name == "transcript-performance" || name == "transcript-native" {
   address = server.URL+"/app.html"
   if o.URL != "" { address = o.URL }
  }
  page, err := browser.NewPage(ctx, address)
  if err != nil { return result, err }
  module, _ := json.Marshal(server.URL+"/scripts/ui-bench/cases.js")
  caseJSON, _ := json.Marshal(name)
  expression := "import("+string(module)+").then(m => m.run("+string(caseJSON)+", "+string(encoded)+"))"
  var measured any
  if err := page.Eval(ctx, expression, &measured); err != nil { return result, fmt.Errorf("%s: %w", name, err) }
  results[name] = measured
 }
 return result, nil
}

func main() { os.Exit(cli()) }
func cli() int {
 o, err := parseOptions(os.Args[1:], os.Stderr)
 if errors.Is(err, flag.ErrHelp) { return 0 }
 if err != nil { fmt.Fprintln(os.Stderr, err); return 2 }
 root, err := repoRoot()
 if err != nil { fmt.Fprintln(os.Stderr, err); return 1 }
 ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
 defer stop()
 ctx, cancel := context.WithTimeout(ctx, o.Timeout)
 defer cancel()
 result, err := run(ctx, root, o)
 if err != nil { fmt.Fprintln(os.Stderr, err); return 1 }
 data, err := json.MarshalIndent(result, "", "  ")
 if err == nil && o.Output != "" {
  err = os.MkdirAll(filepath.Dir(o.Output), 0o700)
  if err == nil { err = os.WriteFile(o.Output, append(data, '\n'), 0o600) }
 }
 if err != nil { fmt.Fprintln(os.Stderr, err); return 1 }
 fmt.Println(string(data))
 return 0
}
