package main

import (
 "context"
 "encoding/json"
 "io"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)

func TestOptions(t *testing.T) {
 t.Setenv("TRANSCRIPT_BASE_REF", "fixture-baseline")
 t.Setenv("TRANSCRIPT_BENCH_OUTPUT", ".tmp/result.json")
 o,err:=parseOptions([]string{"-case","tool-change-stats"},io.Discard)
 if err!=nil || o.Case!="tool-change-stats" || o.Reference!="fixture-baseline" || o.Output!=".tmp/result.json" { t.Fatalf("options=%+v %v",o,err) }
 for _,args:=range [][]string{{"-case","missing"},{"-timeout","0s"},{"-base-ref","--help"},{"-base-ref","main:path"},{"-url","https://example.com"},{"-url","http://localhost:1234","-case","tool-change-stats"},{"unexpected"}} {
  if _,err:=parseOptions(args,io.Discard);err==nil { t.Errorf("accepted invalid options %v",args) }
 }
 if _,err:=parseOptions([]string{"-case","transcript-native","-url","http://127.0.0.1:1234/app.html"},io.Discard);err!=nil { t.Fatal(err) }
}

func TestFixtureServerIsImmutableReadOnlyAndAllowlisted(t *testing.T) {
 languages:=strings.Repeat(`{"en", "English", "ltr"}`+"\n",27)
 sources:=map[string]string{"internal/system/uilang/language.go":languages,"internal/webgui/assets/locales/en.json":`{"common.save":"Save"}`,"internal/webgui/assets/index.html":"<html><head></head><body>fixture</body></html>","scripts/ui-bench/cases.js":"export const fixture=1;"}
 h,err:=handler(sources);if err!=nil { t.Fatal(err) }
 sources["scripts/ui-bench/cases.js"]="mutated"
 for _,target:=range []string{"/config.json","/.tmp/private","/api/../config","/../go.mod","/auth.json"} {
  r:=httptest.NewRecorder();h.ServeHTTP(r,httptest.NewRequest(http.MethodGet,target,nil))
  if r.Code!=http.StatusNotFound { t.Errorf("%s status=%d",target,r.Code) }
 }
 for _,target:=range []string{"/api/chat","/api/settings","/scripts/ui-bench/cases.js"} {
  r:=httptest.NewRecorder();h.ServeHTTP(r,httptest.NewRequest(http.MethodPost,target,strings.NewReader(`{"prompt":"never submit"}`)))
  if r.Code!=http.StatusMethodNotAllowed { t.Errorf("write %s status=%d",target,r.Code) }
 }
 for _,target:=range []string{"/","/app.html","/__test/sources.json","/locales/en.js","/scripts/ui-bench/cases.js"} {
  r:=httptest.NewRecorder();h.ServeHTTP(r,httptest.NewRequest(http.MethodGet,target,nil))
  if r.Code!=http.StatusOK { t.Fatalf("%s status=%d",target,r.Code) }
  if target=="/scripts/ui-bench/cases.js" && !strings.Contains(r.Body.String(),"fixture=1") { t.Fatal("snapshot changed after creation") }
  if target=="/app.html" && !strings.Contains(r.Body.String(),"__uiBenchErrors") { t.Fatal("browser error recorder missing") }
  if target=="/__test/sources.json" && !json.Valid(r.Body.Bytes()) { t.Fatal("bad source snapshot") }
 }
}

func TestMissingBaselineFailsWithoutBrowser(t *testing.T) {
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 if _,err:=readBaseline(ctx,t.TempDir(),"missing-reference");err==nil { t.Fatal("missing baseline silently accepted") }
}
