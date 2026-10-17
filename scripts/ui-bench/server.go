package main

import (
 "encoding/json"
 "errors"
 "fmt"
 "io/fs"
 "mime"
 "net"
 "net/http"
 "net/http/httptest"
 "os"
 "path"
 "path/filepath"
 "regexp"
 "strings"
 "unicode/utf8"
)

func sourceSnapshot(root string) (map[string]string, error) {
 result := make(map[string]string)
 trees := []string{"internal/webgui/assets", "internal/webgui/shared-ui"}
 for _, tree := range trees {
  directory := root
  for _, part := range strings.Split(tree, "/") {
   directory = filepath.Join(directory, part)
   info, err := os.Lstat(directory)
   if err != nil { return nil, err }
   if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 { return nil, fmt.Errorf("not a real source directory: %s", directory) }
  }
  dir, err := os.OpenRoot(directory)
  if err != nil { return nil, err }
  err = fs.WalkDir(dir.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
   if walkErr != nil { return walkErr }
   if strings.HasPrefix(entry.Name(), ".") && name != "." { if entry.IsDir() { return fs.SkipDir }; return nil }
   if entry.IsDir() || !entry.Type().IsRegular() { return nil }
   switch path.Ext(name) { case ".js", ".css", ".html", ".json", ".svg": default: return nil }
   data, err := dir.ReadFile(name)
   if err != nil { return err }
   if len(data) > 8<<20 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) { return fmt.Errorf("invalid text source %s/%s", tree, name) }
   result[tree+"/"+name] = string(data)
   return nil
  })
  err = errors.Join(err, dir.Close())
  if err != nil { return nil, err }
 }
 dir, err := os.OpenRoot(root)
 if err != nil { return nil, err }
 defer dir.Close()
 for _, name := range []string{"test/ui/harness.js", "test/ui/tool-change-stats-fixture.js", "scripts/transcript-dom-fixture.js", "scripts/transcript-native-fixture.js", "internal/system/uilang/language.go"} {
  info, err := dir.Lstat(name)
  if err != nil { return nil, err }
  if !info.Mode().IsRegular() { return nil, fmt.Errorf("not a regular source: %s", name) }
  data, err := dir.ReadFile(name)
  if err != nil { return nil, err }
  result[name] = string(data)
 }
 modules, err := fs.Glob(casesFS, "*.js")
 if err != nil { return nil, err }
 for _, name := range modules {
  data, err := casesFS.ReadFile(name)
  if err != nil { return nil, err }
  result["scripts/ui-bench/"+name] = string(data)
 }
 return result, nil
}

const errorRecorder = `<script>window.__uiBenchErrors=[];addEventListener('error',e=>__uiBenchErrors.push(e.message));addEventListener('unhandledrejection',e=>__uiBenchErrors.push(String(e.reason&&e.reason.stack||e.reason)));</script>`

func handler(sources map[string]string) (http.Handler, error) {
 snapshot, err := json.Marshal(sources)
 if err != nil { return nil, err }
 // Immutable allowlisted snapshot, never a FileServer over the checkout.
 files := make(map[string]string, len(sources))
 for name, content := range sources { files["/"+name] = content }
 english := sources["internal/webgui/assets/locales/en.json"]
 if !json.Valid([]byte(english)) { return nil, errors.New("production English locale missing or invalid") }
 languages := []map[string]string{}
 for _, m := range regexp.MustCompile(`\{"([\w-]+)", "([^"]+)", "(ltr|rtl)"\}`).FindAllStringSubmatch(sources["internal/system/uilang/language.go"], -1) {
  languages = append(languages, map[string]string{"code":m[1], "name":m[2], "dir":m[3]})
 }
 if len(languages) != 27 { return nil, fmt.Errorf("fixture language registry: got %d, want 27", len(languages)) }
 registry, _ := json.Marshal(languages)
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Cache-Control", "no-store")
  w.Header().Set("X-Content-Type-Options", "nosniff")
  if r.Method != http.MethodGet && r.Method != http.MethodHead { http.Error(w,"model-free fixture is read-only",http.StatusMethodNotAllowed); return }
  name := r.URL.Path
  if path.Clean(name) != name { http.NotFound(w,r); return }
  content, contentType := "", ""
  switch {
  case name == "/":
   contentType="text/html; charset=utf-8"; content="<!doctype html><html><head><meta charset=\"utf-8\"><title>SuperCli UI benchmarks</title></head><body>"+errorRecorder+"</body></html>"
  case name == "/__test/sources.json":
   contentType="application/json; charset=utf-8"; content=string(snapshot)
  case name == "/app.html":
   contentType="text/html; charset=utf-8"; content=strings.Replace(sources["internal/webgui/assets/index.html"], "<head>", "<head>"+errorRecorder, 1)
  case name == "/locales/en.js":
   contentType="text/javascript; charset=utf-8"; content="var I18N={en:"+english+"};var UI_LANGUAGES="+string(registry)+";"
  case strings.HasPrefix(name,"/api/"):
   contentType="application/json; charset=utf-8"
   data := any(map[string]any{"sessions":[]any{},"projects":[]any{},"providers":[]any{},"models":[]any{},"workers":[]any{},"tasks":[]any{},"settings":map[string]string{"ui.lang":"en"},"ui":map[string]string{"lang":"en"}})
   switch name { case "/api/sessions": data=[]any{}; case "/api/models":data=map[string]any{"active":"","models":[]any{}};case "/api/transcript":data=map[string]any{"messages":[]any{},"has_more":false} }
   encoded,_:=json.Marshal(data);content=string(encoded)
  default:
   key:=name
   if strings.HasPrefix(name,"/.__supercli/ui/") { key="/internal/webgui/shared-ui/"+path.Base(name) } else if !strings.HasPrefix(name,"/scripts/") && !strings.HasPrefix(name,"/test/") && !strings.HasPrefix(name,"/internal/") { key="/internal/webgui/assets"+name }
   var ok bool
   content,ok=files[key]
   if !ok { http.NotFound(w,r);return }
   contentType=mime.TypeByExtension(path.Ext(key))
   if path.Ext(key)==".js" { contentType="text/javascript; charset=utf-8" }
   if contentType=="" { contentType="text/plain; charset=utf-8" }
   // Only immutable benchmark modules are importable by an explicitly supplied test URL.
   if strings.HasPrefix(name,"/scripts/ui-bench/") { w.Header().Set("Access-Control-Allow-Origin","*") }
  }
  w.Header().Set("Content-Type",contentType)
  if r.Method != http.MethodHead { _,_=w.Write([]byte(content)) }
 }),nil
}

func serve(sources map[string]string) (*httptest.Server,error) {
 h,err:=handler(sources)
 if err!=nil{return nil,err}
 listener,err:=net.Listen("tcp4","127.0.0.1:0")
 if err!=nil{return nil,err}
 server:=&httptest.Server{Listener:listener,Config:&http.Server{Handler:h}}
 server.Start()
 return server,nil
}
