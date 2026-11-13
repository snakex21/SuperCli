package claudeauth

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"

	"supercli/internal/system/uilang"
)

func callbackReturnURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := u.Hostname()
	if host != "localhost" {
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return ""
		}
	}
	u.RawQuery, u.Fragment, u.Path = "", "", "/"
	return u.String()
}

func callbackPage(opts Options, success bool) string {
	t := func(key string) string { return html.EscapeString(uilang.Text(opts.Language, key)) }
	title, message, badge, state := t("claude.auth.title"), t("claude.auth.body"), "✓", "success"
	if !success {
		title, message, badge, state = t("claude.auth.error"), t("claude.auth.retry"), "!", "error"
	}
	action, connection := "", ""
	if back := callbackReturnURL(opts.ReturnURL); back != "" {
		action = `<a class="primary" href="` + html.EscapeString(back) + `">` + t("claude.auth.back") + ` <span aria-hidden="true">↗</span></a>`
	}
	if success {
		connection = `<div class="connection"><span class="dot"></span>Claude <span aria-hidden="true">→</span> SuperCli</div>`
	}
	return fmt.Sprintf(`<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>SuperCli · Claude</title><style>
:root{color-scheme:dark;font-family:Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;color:#e8e8ea;background:#101014}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:radial-gradient(ellipse at 50%% 0,rgba(232,118,61,.1),transparent 62%%)}main{width:min(100%%,460px);padding:36px;background:#17171c;border:1px solid #303036;border-radius:18px;box-shadow:0 24px 90px #0005}.brand{display:flex;align-items:center;gap:10px;font-size:13px;font-weight:650;color:#9a9aa2;letter-spacing:.04em}.mark{display:grid;place-items:center;width:30px;height:30px;border-radius:8px;background:#e8763d;color:#17171c;font-weight:800}.status{margin:34px 0 20px;display:grid;place-items:center;width:58px;height:58px;border:1px solid #58a56f55;border-radius:50%%;background:#58a56f15;color:#89d9a0;font-size:29px}.error .status{background:#d05c5c15;border-color:#d05c5c55;color:#f19999}h1{margin:0 0 12px;font-size:28px;letter-spacing:-.035em;font-weight:650}p{color:#9a9aa2;line-height:1.65;font-size:14px;margin:0}.connection{margin:26px 0;display:flex;align-items:center;gap:10px;padding:13px 15px;border:1px solid #303036;border-radius:9px;font-size:14px}.dot{width:7px;height:7px;border-radius:50%%;background:#89d9a0}.primary{display:flex;justify-content:space-between;align-items:center;padding:14px 17px;background:#e8763d;border:1px solid #f08c59;border-radius:9px;color:#17171c;text-decoration:none;font-size:14px;font-weight:700}.primary:hover{background:#f18a53}.primary:focus-visible{outline:3px solid #e8e8ea;outline-offset:4px}.hint{margin-top:20px;font-size:12px;color:#74747e}@media(max-width:480px){main{padding:28px}h1{font-size:25px}}
</style></head><body><main class="%s"><div class="brand"><span class="mark">S</span>SuperCli <span aria-hidden="true">/</span> Claude</div><div class="status" aria-hidden="true">%s</div><h1>%s</h1><p>%s</p>%s%s<p class="hint">%s</p></main></body></html>`, html.EscapeString(uilang.Normalize(opts.Language)), state, badge, title, message, connection, action, t("claude.auth.hint"))
}

func writeCallbackPage(w http.ResponseWriter, opts Options, success bool, status int) {
	page := callbackPage(opts, success)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Length", fmt.Sprint(len(page)))
	w.WriteHeader(status)
	_, _ = strings.NewReader(page).WriteTo(w)
	_ = http.NewResponseController(w).Flush()
}
