package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestANSIStylesAndEscaping(t *testing.T) {
	text := "<&\"日本語🧪\x1b[1;3;31;44mstyled\x1b[22;23;39;49mplain\x1b[38;2;1;2;3;48;2;4;5;6mtrue\x1b[0mreset"
	html := htmlANSI(text)
	for _, expected := range []string{"&lt;&amp;&quot;日本語🧪", `style="color:#d95a5a;background:#7aa2d7;font-weight:700;font-style:italic;">styled`, `style="">plain`, `style="color:rgb(1,2,3);background:rgb(4,5,6);">true`, `style="">reset`} {
		if !strings.Contains(html,expected) { t.Errorf("missing %q in %s",expected,html) }
	}
	if strings.ContainsRune(html,'\x1b') { t.Fatal("SGR escapes leaked into HTML") }
}

func TestIndexedPaletteAndReset(t *testing.T) {
	for n,want := range map[int]string{0:"#101014",15:"#ffffff",16:"rgb(0,0,0)",21:"rgb(0,0,255)",231:"rgb(255,255,255)",232:"rgb(8,8,8)",255:"rgb(238,238,238)"} {
		if got:=indexed(n);got!=want { t.Errorf("indexed(%d)=%s want %s",n,got,want) }
	}
	html:=htmlANSI("\x1b[38;5;21;48;5;232m256\x1b[mreset\x1b[91;107mbright")
	for _,want:=range []string{`color:rgb(0,0,255);background:rgb(8,8,8);`, `style="">reset`, `color:#ef7676;background:#ffffff;`} {
		if !strings.Contains(html,want) { t.Errorf("missing %q in %s",want,html) }
	}
}

func TestArtifactContainment(t *testing.T) {
	repo:=t.TempDir()
	if err:=os.MkdirAll(filepath.Join(repo,".tmp","in"),0o700);err!=nil { t.Fatal(err) }
	for _,bad:=range []string{"../outside", ".tmp/../../outside", "internal", filepath.Join(t.TempDir(),"outside")} {
		if _,err:=artifactPath(repo,bad);err==nil { t.Errorf("accepted outside artifact path %q",bad) }
	}
	if got,err:=artifactPath(repo,".tmp/in");err!=nil || got!=filepath.Join(".tmp","in") { t.Fatalf("contained=%q %v",got,err) }
}

func TestRenderHTMLPreservesNamesThemesAndSource(t *testing.T) {
	repo:=t.TempDir()
	input:=filepath.Join(repo,".tmp","input")
	if err:=os.MkdirAll(input,0o700);err!=nil { t.Fatal(err) }
	for _,name:=range []string{"chat-dark.ansi","chat-light.ansi"} {
		if err:=os.WriteFile(filepath.Join(input,name),[]byte("\x1b[32mfixture\x1b[0m\n<&"),0o600);err!=nil { t.Fatal(err) }
	}
	if err:=os.WriteFile(filepath.Join(input,"ignored.txt"),[]byte("not an export"),0o600);err!=nil { t.Fatal(err) }
	count,err:=run(context.Background(),repo,options{Input:".tmp/input",Output:".tmp/output",HTMLOnly:true})
	if err!=nil || count!=2 { t.Fatalf("render=%d %v",count,err) }
	for _,name:=range []string{"chat-dark.html","chat-light.html"} {
		data,err:=os.ReadFile(filepath.Join(repo,".tmp","output",name));if err!=nil { t.Fatal(err) }
		text:=string(data)
		for _,want:=range []string{"font:15px/1.35 Consolas",`color:#72ba72;">fixture`,`&lt;&amp;`} { if !strings.Contains(text,want) { t.Errorf("%s lost %q",name,want) } }
		light:=strings.Contains(text,"background:#fbfaf8;color:#1f1f24")
		if light!=strings.Contains(name,"-light") { t.Errorf("wrong theme for %s",name) }
	}
}

func TestCLIOptionsAndEmptyInput(t *testing.T) {
	o,err:=parseOptions([]string{"-html-only",".tmp/in",".tmp/out"},io.Discard)
	if err!=nil || !o.HTMLOnly || o.Input!=".tmp/in" || o.Output!=".tmp/out" { t.Fatalf("options=%+v %v",o,err) }
	for _,args:=range [][]string{{"-timeout","0s"},{"a","b","c"}} { if _,err:=parseOptions(args,io.Discard);err==nil { t.Fatalf("accepted %v",args) } }
	repo:=t.TempDir();if err:=os.MkdirAll(filepath.Join(repo,".tmp","empty"),0o700);err!=nil { t.Fatal(err) }
	if _,err:=run(context.Background(),repo,options{Input:".tmp/empty",Output:".tmp/out",HTMLOnly:true});err==nil { t.Fatal("empty export silently succeeded") }
}
