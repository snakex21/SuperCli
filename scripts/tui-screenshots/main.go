// Command tui-screenshots renders exported production Model.View ANSI to HTML
// and PNG. It never launches the application or automates a terminal window.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"supercli/internal/testbrowser"
)

var colors = []string{"#101014", "#d95a5a", "#72ba72", "#e0c870", "#7aa2d7", "#ba8dd7", "#72bfc8", "#e8e8ea", "#68686f", "#ef7676", "#8bd48b", "#f0d782", "#91bafa", "#cca1e8", "#8dd2da", "#ffffff"}
var sgr = regexp.MustCompile("\x1b\\[([0-9;]*)m")
var escape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")

type options struct {
	Input, Output, Browser string
	HTMLOnly bool
	Timeout time.Duration
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var o options
	flags := flag.NewFlagSet("tui-screenshots", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&o.Browser, "browser", "", "development Chrome/Chromium/Edge executable (SUPERCLI_TEST_BROWSER)")
	flags.BoolVar(&o.HTMLOnly, "html-only", false, "write HTML only, without starting a screenshot browser")
	flags.DurationVar(&o.Timeout, "timeout", 2*time.Minute, "whole capture deadline")
	if err := flags.Parse(args); err != nil { return o, err }
	if flags.NArg() > 2 || o.Timeout <= 0 { return o, errors.New("usage: go run ./scripts/tui-screenshots [flags] [input-directory [output-directory]]") }
	o.Input = ".tmp/tui-screenshots"
	o.Output = ".tmp/tui-screenshots/html"
	if flags.NArg() > 0 { o.Input = flags.Arg(0) }
	if flags.NArg() > 1 { o.Output = flags.Arg(1) }
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

// Artifact paths stay in repository .tmp, with no symlink/reparse-point parents.
// OpenRoot additionally prevents path escapes if an entry changes while in use.
func artifactPath(repo, name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil { return "", err }
	if !filepath.IsAbs(name) { absolute = filepath.Join(repo, name) }
	scratch := filepath.Join(repo, ".tmp")
	relative, err := filepath.Rel(scratch, absolute)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("screenshot artifacts must remain in repository .tmp")
	}
	current := repo
	for _, part := range strings.Split(filepath.Join(".tmp", relative), string(filepath.Separator)) {
		if part == "." { continue }
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) { continue }
		if err != nil { return "", err }
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() { return "", fmt.Errorf("artifact parent is not a real directory: %s", current) }
	}
	return filepath.Join(".tmp", relative), nil
}

func indexed(n int) string {
	if n < 0 || n > 255 { return "" }
	if n < 16 { return colors[n] }
	if n >= 232 { x := 8+(n-232)*10; return fmt.Sprintf("rgb(%d,%d,%d)", x,x,x) }
	n -= 16
	level := func(v int) int { if v == 0 { return 0 }; return 55+v*40 }
	return fmt.Sprintf("rgb(%d,%d,%d)", level(n/36),level(n/6%6),level(n%6))
}

func htmlANSI(text string) string {
	var html strings.Builder
	fg, bg := "", ""
	bold, italic := false, false
	span := func() {
		html.WriteString(`<span style="`)
		if fg != "" { html.WriteString("color:"+fg+";") }
		if bg != "" { html.WriteString("background:"+bg+";") }
		if bold { html.WriteString("font-weight:700;") }
		if italic { html.WriteString("font-style:italic;") }
		html.WriteString(`">`)
	}
	span()
	last := 0
	for _, match := range sgr.FindAllStringSubmatchIndex(text, -1) {
		html.WriteString(escape.Replace(text[last:match[0]])); html.WriteString("</span>")
		last = match[1]
		codes := strings.Split(text[match[2]:match[3]], ";")
		values := make([]int, len(codes))
		for i, code := range codes { values[i], _ = strconv.Atoi(code) }
		for i := 0; i < len(values); i++ {
			n := values[i]
			switch {
			case n == 0: fg,bg,bold,italic = "","",false,false
			case n == 1: bold = true
			case n == 22: bold = false
			case n == 3: italic = true
			case n == 23: italic = false
			case n == 39: fg = ""
			case n == 49: bg = ""
			case n >= 30 && n <= 37: fg = colors[n-30]
			case n >= 90 && n <= 97: fg = colors[n-90+8]
			case n >= 40 && n <= 47: bg = colors[n-40]
			case n >= 100 && n <= 107: bg = colors[n-100+8]
			case (n == 38 || n == 48) && i+4 < len(values) && values[i+1] == 2:
				color := fmt.Sprintf("rgb(%d,%d,%d)", values[i+2],values[i+3],values[i+4])
				if n == 38 { fg = color } else { bg = color }; i += 4
			case (n == 38 || n == 48) && i+2 < len(values) && values[i+1] == 5:
				if n == 38 { fg = indexed(values[i+2]) } else { bg = indexed(values[i+2]) }; i += 2
			}
		}
		span()
	}
	html.WriteString(escape.Replace(text[last:])); html.WriteString("</span>")
	return html.String()
}

func document(name, text string) string {
	page := "background:#101014;color:#e8e8ea"
	if strings.Contains(name,"-light") { page = "background:#fbfaf8;color:#1f1f24" }
	return `<!doctype html><html><head><meta charset="utf-8"><title>SuperCli TUI render</title><style>html,body{margin:0;`+page+`}pre{box-sizing:border-box;display:inline-block;margin:0;padding:22px;font:15px/1.35 Consolas,"Cascadia Mono",monospace;white-space:pre;font-variant-ligatures:none}</style></head><body><pre>`+htmlANSI(text)+`</pre></body></html>`
}

func capture(ctx context.Context, pages map[string]string, browserPath string) (images map[string][]byte, err error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil { return nil, err }
	server := &httptest.Server{Listener:listener, Config:&http.Server{Handler:http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		content, ok := pages[strings.TrimPrefix(r.URL.Path,"/")]
		if !ok || (r.Method != http.MethodGet && r.Method != http.MethodHead) { http.NotFound(w,r);return }
		w.Header().Set("Content-Type","text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy","default-src 'none'; style-src 'unsafe-inline'")
		if r.Method != http.MethodHead { _,_ = io.WriteString(w,content) }
	})}}
	server.Start(); defer server.Close()
	browser, err := testbrowser.Start(ctx,testbrowser.Options{Executable:browserPath})
	if err != nil { return nil, err }
	defer func(){ err = errors.Join(err,browser.Close()) }()
	page, err := browser.NewPage(ctx, "about:blank")
	if err != nil { return nil, err }
	images = make(map[string][]byte)
	for name := range pages {
		if err := page.Navigate(ctx, server.URL+"/"+url.PathEscape(name)); err != nil { return nil, err }
		var bounds struct{ Width, Height float64 }
		if err := page.Eval(ctx, `document.fonts.ready.then(()=>{const b=document.querySelector('pre').getBoundingClientRect();return {Width:b.width,Height:b.height};})`, &bounds); err != nil { return nil, err }
		width,height := int(math.Ceil(bounds.Width)),int(math.Ceil(bounds.Height))
		if err := page.SetViewport(ctx,width,height); err != nil { return nil, fmt.Errorf("%s dimensions: %w",name,err) }
		data, err := page.Screenshot(ctx)
		if err != nil { return nil, err }
		images[strings.TrimSuffix(name,".html")+".png"] = data
	}
	return images,nil
}

func run(ctx context.Context, repo string, o options) (count int, err error) {
	input, err := artifactPath(repo,o.Input)
	if err != nil { return 0, err }
	output, err := artifactPath(repo,o.Output)
	if err != nil { return 0, err }
	dir, err := os.OpenRoot(filepath.Join(repo,".tmp"))
	if err != nil { return 0, err }
	defer dir.Close()
	input, err = filepath.Rel(".tmp",input)
	if err != nil { return 0, err }
	output, err = filepath.Rel(".tmp",output)
	if err != nil { return 0, err }
	entries, err := fs.ReadDir(dir.FS(),filepath.ToSlash(input))
	if err != nil { return 0, err }
	pages := make(map[string]string)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(),".ansi") { continue }
		if !entry.Type().IsRegular() { return 0, fmt.Errorf("ANSI input is not a regular file: %s",entry.Name()) }
		data, err := dir.ReadFile(filepath.Join(input,entry.Name()))
		if err != nil { return 0, err }
		pages[strings.TrimSuffix(entry.Name(),".ansi")+".html"] = document(entry.Name(),string(data))
	}
	if len(pages) == 0 { return 0, errors.New("no .ansi exports found; export model-free Model.View fixtures first") }
	if err := dir.MkdirAll(output,0o700); err != nil { return 0, err }
	for name, content := range pages {
		if err := writeArtifact(dir,filepath.Join(output,name),[]byte(content)); err != nil { return 0, err }
	}
	if !o.HTMLOnly {
		images, err := capture(ctx,pages,o.Browser)
		if err != nil { return 0, err }
		for name,data := range images {
			if err := writeArtifact(dir,filepath.Join(output,name),data); err != nil { return 0, err }
		}
	}
	return len(pages),nil
}

func writeArtifact(dir *os.Root, name string, data []byte) error {
	info,err:=dir.Lstat(name)
	if err!=nil && !errors.Is(err,os.ErrNotExist) { return err }
	if err==nil && !info.Mode().IsRegular() { return fmt.Errorf("artifact is not a regular file: %s",name) }
	return dir.WriteFile(name,data,0o600)
}

func main() { os.Exit(cli()) }
func cli() int {
	o, err := parseOptions(os.Args[1:],os.Stderr)
	if errors.Is(err,flag.ErrHelp) { return 0 }
	if err != nil { fmt.Fprintln(os.Stderr,err); return 2 }
	repo, err := repoRoot()
	if err != nil { fmt.Fprintln(os.Stderr,err); return 1 }
	ctx,stop := signal.NotifyContext(context.Background(),os.Interrupt); defer stop()
	ctx,cancel := context.WithTimeout(ctx,o.Timeout); defer cancel()
	count,err := run(ctx,repo,o)
	if err != nil { fmt.Fprintln(os.Stderr,err); return 1 }
	formats := "HTML + PNG"
	if o.HTMLOnly { formats = "HTML" }
	fmt.Printf("Rendered %d production ANSI exports as %s in %s (not terminal-window automation)\n",count,formats,o.Output)
	return 0
}
