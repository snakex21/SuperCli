package headless

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

const webdriverSmallLimit = 64 << 10
const webdriverScreenshotLimit = 22 << 20
const webdriverElementKey = "element-6066-11e4-a52e-4f735466cecf"

// The fixed inspector returns semantics, not pixels or model-supplied JavaScript.
// Both traversal and output are bounded, including on very large documents.
const webdriverInspectScript = `return (() => {
 const root=document.body || document.documentElement;
 const result={url:String(location.href).slice(0,4096),title:String(document.title).slice(0,512),text:"",elements:[],truncated:false};
 if(!root) return result;
 let budget=JSON.stringify(result).length;
 const visibility=new WeakMap();
 const visible=(el)=>{
  let n=el,depth=0;
  const pending=[];
  let allowed=true;
  while(n&&depth++<32){
   if(visibility.has(n)){allowed=visibility.get(n);break;}
   pending.push(n);
   const style=getComputedStyle(n);
   if(n.hidden||n.getAttribute("aria-hidden")==="true"||style.display==="none"||style.visibility==="hidden"||style.visibility==="collapse"||style.opacity==="0"){allowed=false;break;}
   n=n.parentElement;
  }
  if(n&&depth>32)allowed=false;
  for(const item of pending)visibility.set(item,allowed);
  if(!allowed)return false;
  const r=el.getBoundingClientRect();
  return r.width>0&&r.height>0&&r.bottom>0&&r.right>0&&r.top<innerHeight&&r.left<innerWidth;
 };
 const unique=(path,el)=>{
  if(!path||path.length>512)return false;
  try{const matches=document.querySelectorAll(path);return matches.length===1&&matches[0]===el;}catch{return false;}
 };
 const selector=(el)=>{
  if(el.id&&el.id.length<=256){const id="#"+CSS.escape(el.id);if(unique(id,el))return id;}
  const parts=[];
  for(let n=el;n&&n.nodeType===1&&parts.length<32;n=n.parentElement){
   let part=n.localName;
   if(n.id&&n.id.length<=256){const path=["#"+CSS.escape(n.id),...parts].join(" > ");if(unique(path,el))return path;}
   if(n.parentElement){
    let i=1,scanned=0;
    for(let p=n.previousElementSibling;p;p=p.previousElementSibling){if(++scanned>2000)return "";if(p.localName===n.localName)i++;}
    part+=":nth-of-type("+i+")";
   }
   parts.unshift(part);
   const path=parts.join(" > ");
   if(path.length>512)return "";
   if(unique(path,el))return path;
  }
  return "";
 };
 const walker=document.createTreeWalker(root,NodeFilter.SHOW_ELEMENT|NodeFilter.SHOW_TEXT);
 let n,visited=0;
 while((n=walker.nextNode())&&visited++<2000){
  if(n.nodeType===3){
   const p=n.parentElement;
   if(p&&!p.closest("script,style,noscript,template")&&visible(p)&&result.text.length<12000){
    const raw=String(n.nodeValue||"");
    const text=raw.slice(0,800).replace(/\s+/g," ").trim();
    if(raw.length>800)result.truncated=true;
    const piece=(result.text&&text?" ":"")+text.slice(0,Math.min(800,12000-result.text.length));
    const size=JSON.stringify(piece).length-2;
    if(budget+size>16000){result.truncated=true;break;}
    result.text+=piece;budget+=size;
   }
   continue;
  }
  if(result.elements.length>=60||!n.matches("a[href],button,input,select,textarea,[role='button'],[role='link'],[contenteditable='true']")||!visible(n))continue;
  const target=selector(n);if(!target)continue;
  const label=n.getAttribute("aria-label")||n.getAttribute("title")||n.labels?.[0]?.textContent||n.textContent||n.getAttribute("placeholder")||"";
  const item={selector:target,tag:n.localName,label:String(label).slice(0,200).replace(/\s+/g," ").trim(),disabled:!!n.disabled};
  if(n.type)item.type=String(n.type).slice(0,32);
  if(n.href)item.href=String(n.href).slice(0,1024);
  if(n.value!==undefined&&n.type!=="password")item.value=String(n.value).slice(0,200);
  const size=JSON.stringify(item).length+(result.elements.length?1:0);
  if(budget+size>16000){result.truncated=true;break;}
  result.elements.push(item);budget+=size;
 }
 result.truncated=result.truncated||!!n||result.text.length>=12000||result.elements.length>=60;
 return result;
})();`

type webdriverClient struct {
	base      *url.URL
	client    *http.Client
	transport *http.Transport
}

func newWebdriverClient(endpoint string) (*webdriverClient, error) {
	base, err := localEndpoint(endpoint, "http")
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, MaxResponseHeaderBytes: 16 << 10}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("webdriver redirects are not allowed") }}
	return &webdriverClient{base: base, client: client, transport: transport}, nil
}

func (c *webdriverClient) call(ctx context.Context, method, path string, body any, limit int) (json.RawMessage, error) {
	target := *c.base
	target.Path = strings.TrimRight(target.Path, "/") + path
	target.RawPath = ""
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webdriver request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("webdriver response: %w", err)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("webdriver response exceeds %d-byte limit", limit)
	}
	var envelope struct {
		Value json.RawMessage `json:"value"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("webdriver invalid JSON response (HTTP %d)", response.StatusCode)
	}
	var failure struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(envelope.Value, &failure)
	if response.StatusCode < 200 || response.StatusCode >= 300 || failure.Error != "" {
		message := failure.Message
		if len(message) > 1024 {
			message = message[:1024]
		}
		return nil, fmt.Errorf("webdriver HTTP %d %s: %s", response.StatusCode, failure.Error, message)
	}
	if len(envelope.Value) == 0 {
		return nil, errors.New("webdriver response has no value")
	}
	return envelope.Value, nil
}

func webdriverID(id string) error {
	if len(id) == 0 || len(id) > 128 {
		return errors.New("webdriver session/element id must contain 1–128 characters")
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return errors.New("webdriver session/element id contains unsafe path characters")
		}
	}
	if id == "." || id == ".." {
		return errors.New("webdriver session/element id is invalid")
	}
	return nil
}

func webdriverURL(raw string) error {
	if len(raw) > 8192 {
		return errors.New("webdriver URL exceeds 8192 bytes")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return errors.New("webdriver URL must be an absolute HTTP(S) address without credentials")
	}
	return nil
}

func webdriverParams(p params) error {
	switch p.Action {
	case "open":
		if p.SessionID != "" {
			return errors.New("webdriver open cannot attach a session_id")
		}
		if p.Browser != "" && p.Browser != "chrome" && p.Browser != "firefox" {
			return errors.New("webdriver browser must be chrome or firefox")
		}
		if p.URL != "" {
			if err := webdriverURL(p.URL); err != nil {
				return err
			}
		}
	case "status":
		if p.SessionID != "" {
			return webdriverID(p.SessionID)
		}
	case "inspect", "screenshot", "close":
		return webdriverID(p.SessionID)
	case "navigate":
		if err := webdriverID(p.SessionID); err != nil {
			return err
		}
		return webdriverURL(p.URL)
	case "click", "type":
		if err := webdriverID(p.SessionID); err != nil {
			return err
		}
		if strings.TrimSpace(p.Selector) == "" || len(p.Selector) > 2048 {
			return errors.New("webdriver click/type requires a CSS selector of at most 2048 bytes")
		}
		if p.Action == "type" && (!utf8.ValidString(p.Text) || utf8.RuneCountInString(p.Text) > 4096 || len(p.Text) > 16<<10) {
			return errors.New("webdriver text must be valid UTF-8, at most 4096 characters and 16 KiB")
		}
	default:
		return fmt.Errorf("webdriver unsupported action %q", p.Action)
	}
	return nil
}

type webdriverOwnership struct {
	Endpoint  string `json:"endpoint"`
	SessionID string `json:"session_id"`
	Profile   string `json:"profile"`
}

func webdriverOwnershipPath(root, endpoint, session string) string {
	hash := sha256.Sum256([]byte(endpoint + "\x00" + session))
	return filepath.Join(root, "session-"+hex.EncodeToString(hash[:])+".json")
}

// Existing/attached driver sessions remain externally owned. Only open creates
// a portable profile; explicit close removes its record after DELETE succeeds.
// The driver process and unrelated sessions are never stopped by this adapter.
func (t *Tool) runWebDriver(ctx context.Context, p params) (core.Result, error) {
	if err := webdriverParams(p); err != nil {
		return core.Result{}, err
	}
	if p.ImageDetail != "" && p.ImageDetail != "auto" && p.ImageDetail != "original" {
		return core.Result{}, errors.New("webdriver image_detail must be auto or original")
	}
	var binary string
	if p.Binary != "" {
		if p.Action != "open" {
			return core.Result{}, errors.New("webdriver binary is only valid for open")
		}
		binary = p.Binary
		if !filepath.IsAbs(binary) {
			binary = filepath.Join(t.BaseDir, binary)
		}
		info, err := os.Stat(binary)
		if err != nil || !info.Mode().IsRegular() {
			return core.Result{}, errors.New("webdriver binary must be an existing regular file")
		}
		binary, err = filepath.Abs(binary)
		if err != nil {
			return core.Result{}, err
		}
	}
	client, err := newWebdriverClient(p.Endpoint)
	if err != nil {
		return core.Result{}, err
	}
	defer client.transport.CloseIdleConnections()
	base := "/session/" + url.PathEscape(p.SessionID)
	root := filepath.Join(t.DataDir, ".supercli", "headless")
	if (p.Action == "open" || p.Action == "close") && strings.TrimSpace(t.DataDir) != "" {
		dataRoot, rootErr := filepath.Abs(t.DataDir)
		if rootErr != nil {
			return core.Result{}, rootErr
		}
		root, rootErr = sandbox.ResolveWithin(dataRoot, filepath.Join(".supercli", "headless"))
		if rootErr != nil {
			return core.Result{}, rootErr
		}
	}
	switch p.Action {
	case "open":
		if strings.TrimSpace(t.DataDir) == "" {
			return core.Result{}, errors.New("webdriver open requires a portable data directory")
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			return core.Result{}, err
		}
		profile, err := os.MkdirTemp(root, "profile-")
		if err != nil {
			return core.Result{}, err
		}
		profile, err = filepath.Abs(profile)
		if err != nil {
			return core.Result{}, err
		}
		browser := p.Browser
		if browser == "" {
			browser = "chrome"
		}
		capabilities := map[string]any{"browserName": browser, "pageLoadStrategy": "eager", "timeouts": map[string]int{"implicit": 0, "pageLoad": 30000, "script": 10000}}
		var options map[string]any
		if browser == "firefox" {
			options = map[string]any{"args": []string{"-headless", "-profile", profile}, "prefs": map[string]any{"browser.shell.checkDefaultBrowser": false, "browser.cache.disk.enable": false, "browser.download.folderList": 2, "browser.download.dir": filepath.Join(profile, "downloads")}}
			capabilities["moz:firefoxOptions"] = options
		} else {
			options = map[string]any{"args": []string{"--headless=new", "--no-first-run", "--no-default-browser-check", "--user-data-dir=" + profile, "--disk-cache-dir=" + filepath.Join(profile, "cache"), "--crash-dumps-dir=" + filepath.Join(profile, "crashes")}, "prefs": map[string]any{"download.default_directory": filepath.Join(profile, "downloads"), "download.prompt_for_download": false}}
			capabilities["goog:chromeOptions"] = options
		}
		if binary != "" {
			options["binary"] = binary
		}
		value, err := client.call(ctx, http.MethodPost, "/session", map[string]any{"capabilities": map[string]any{"alwaysMatch": capabilities}}, webdriverSmallLimit)
		if err != nil {
			_ = os.RemoveAll(profile)
			return core.Result{}, err
		}
		var opened struct {
			SessionID    string `json:"sessionId"`
			Capabilities struct {
				BrowserName string `json:"browserName"`
			} `json:"capabilities"`
		}
		if err = json.Unmarshal(value, &opened); err != nil || webdriverID(opened.SessionID) != nil {
			return core.Result{}, fmt.Errorf("webdriver created session response is invalid; portable profile retained at %s", profile)
		}
		record := webdriverOwnership{Endpoint: client.base.String(), SessionID: opened.SessionID, Profile: filepath.Base(profile)}
		encoded, _ := json.Marshal(record)
		if err = os.WriteFile(webdriverOwnershipPath(root, record.Endpoint, record.SessionID), encoded, 0600); err != nil {
			return core.Result{}, fmt.Errorf("webdriver session %s created but ownership record failed: %w; profile %s", opened.SessionID, err, profile)
		}
		if p.URL != "" {
			if _, err = client.call(ctx, http.MethodPost, "/session/"+url.PathEscape(opened.SessionID)+"/url", map[string]string{"url": p.URL}, webdriverSmallLimit); err != nil {
				return core.Result{}, fmt.Errorf("webdriver session %s created, navigation failed: %w", opened.SessionID, err)
			}
		}
		return jsonResult(map[string]any{"backend": "webdriver", "session_id": opened.SessionID, "browser": browser, "headless": true, "owned": true, "profile": profile, "url": p.URL})
	case "status":
		if p.SessionID == "" {
			value, err := client.call(ctx, http.MethodGet, "/status", nil, webdriverSmallLimit)
			if err != nil {
				return core.Result{}, err
			}
			return jsonResult(map[string]any{"backend": "webdriver", "status": value})
		}
		return webdriverSessionStatus(ctx, client, base, p.SessionID)
	case "inspect":
		value, err := client.call(ctx, http.MethodPost, base+"/execute/sync", map[string]any{"script": webdriverInspectScript, "args": []any{}}, webdriverSmallLimit)
		if err != nil {
			return core.Result{}, err
		}
		return jsonResult(map[string]any{"backend": "webdriver", "session_id": p.SessionID, "page": value})
	case "navigate":
		if _, err := client.call(ctx, http.MethodPost, base+"/url", map[string]string{"url": p.URL}, webdriverSmallLimit); err != nil {
			return core.Result{}, err
		}
		return jsonResult(map[string]any{"backend": "webdriver", "session_id": p.SessionID, "url": p.URL, "navigated": true})
	case "click", "type":
		value, err := client.call(ctx, http.MethodPost, base+"/element", map[string]string{"using": "css selector", "value": p.Selector}, webdriverSmallLimit)
		if err != nil {
			return core.Result{}, err
		}
		var element map[string]string
		if err = json.Unmarshal(value, &element); err != nil {
			return core.Result{}, errors.New("webdriver invalid element response")
		}
		id := element[webdriverElementKey]
		if err := webdriverID(id); err != nil {
			return core.Result{}, err
		}
		suffix := "/click"
		body := map[string]any{}
		if p.Action == "type" {
			suffix = "/value"
			body = map[string]any{"text": p.Text}
		}
		if _, err = client.call(ctx, http.MethodPost, base+"/element/"+url.PathEscape(id)+suffix, body, webdriverSmallLimit); err != nil {
			return core.Result{}, err
		}
		return jsonResult(map[string]any{"backend": "webdriver", "session_id": p.SessionID, "action": p.Action, "selector": p.Selector, "completed": true})
	case "screenshot":
		value, err := client.call(ctx, http.MethodGet, base+"/screenshot", nil, webdriverScreenshotLimit)
		if err != nil {
			return core.Result{}, err
		}
		data, err := decodeWebdriverScreenshot(value)
		if err != nil {
			return core.Result{}, err
		}
		if !bytes.HasPrefix(data, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
			return core.Result{}, errors.New("webdriver screenshot is not PNG")
		}
		return capturedResult(ctx, t.DataDir, "browser", data, p.Attach, p.ImageDetail)
	case "close":
		if _, err := client.call(ctx, http.MethodDelete, base, nil, webdriverSmallLimit); err != nil {
			return core.Result{}, err
		}
		cleanup := webdriverCleanupProfile(root, client.base.String(), p.SessionID)
		result := map[string]any{"backend": "webdriver", "session_id": p.SessionID, "closed": true}
		if cleanup != nil {
			result["profile_cleanup_error"] = cleanup.Error()
		}
		return jsonResult(result)
	}
	return core.Result{}, errors.New("webdriver unsupported action")
}

func webdriverSessionStatus(ctx context.Context, client *webdriverClient, base, session string) (core.Result, error) {
	title, err := client.call(ctx, http.MethodGet, base+"/title", nil, webdriverSmallLimit)
	if err != nil {
		return core.Result{}, err
	}
	location, err := client.call(ctx, http.MethodGet, base+"/url", nil, webdriverSmallLimit)
	if err != nil {
		return core.Result{}, err
	}
	return jsonResult(map[string]any{"backend": "webdriver", "session_id": session, "title": title, "url": location})
}

func webdriverCleanupProfile(root, endpoint, session string) error {
	recordPath := webdriverOwnershipPath(root, endpoint, session)
	file, err := os.Open(recordPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	_ = file.Close()
	if err != nil {
		return err
	}
	if len(data) > 4096 {
		return errors.New("webdriver ownership record exceeds limit")
	}
	var record webdriverOwnership
	if err = json.Unmarshal(data, &record); err != nil {
		return err
	}
	if record.Endpoint != endpoint || record.SessionID != session {
		return errors.New("webdriver ownership identity mismatch")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if record.Profile != filepath.Base(record.Profile) || !strings.HasPrefix(record.Profile, "profile-") {
		return errors.New("webdriver ownership profile is not a portable basename")
	}
	profile, err := filepath.Abs(filepath.Join(absRoot, record.Profile))
	if err != nil {
		return err
	}
	if filepath.Dir(profile) != absRoot || !strings.HasPrefix(filepath.Base(profile), "profile-") {
		return errors.New("webdriver ownership profile is outside portable directory")
	}
	info, err := os.Lstat(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("webdriver ownership profile is a symbolic link")
	}
	if err == nil {
		resolvedRoot, resolveErr := filepath.EvalSymlinks(absRoot)
		if resolveErr != nil {
			return resolveErr
		}
		resolvedProfile, resolveErr := filepath.EvalSymlinks(profile)
		if resolveErr != nil {
			return resolveErr
		}
		if filepath.Dir(resolvedProfile) != resolvedRoot {
			return errors.New("webdriver resolved profile is outside portable directory")
		}
	}
	if err = os.RemoveAll(profile); err != nil {
		return err
	}
	return os.Remove(recordPath)
}
