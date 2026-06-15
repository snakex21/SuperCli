package codexauth

import (
	"fmt"
	"strings"
)

// callback_page.go holds the HTML served on the OAuth loopback
// callback — the page the user sees in their browser after signing
// in. It is fully self-contained (inline CSS, no external assets)
// because the loopback server is short-lived and offline-friendly.

// callbackSuccessHTML is the page shown after a successful sign-in.
// It confirms success, tells the user they can close the tab, and
// attempts to auto-close after a few seconds (browsers only honour
// window.close() for script-opened tabs, so the manual hint stays).
func callbackSuccessHTML() string {
	return callbackPage(callbackContent{
		accent:  "#34d399", // emerald
		icon:    `<svg width="56" height="56" viewBox="0 0 24 24" fill="none" stroke="#34d399" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10" opacity="0.25"/><path d="M8 12.5l2.5 2.5L16 9"/></svg>`,
		title:   "Signed in",
		message: "Your ChatGPT account is connected to SuperCli.",
		hint:    "You can close this tab and return to the terminal.",
		close:   true,
	})
}

// callbackErrorHTML is the page shown when the OAuth provider
// returns an error on the callback.
func callbackErrorHTML(errCode, desc string) string {
	msg := "Sign-in did not complete."
	if desc != "" {
		msg = desc
	} else if errCode != "" {
		msg = errCode
	}
	return callbackPage(callbackContent{
		accent:  "#f87171", // red
		icon:    `<svg width="56" height="56" viewBox="0 0 24 24" fill="none" stroke="#f87171" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10" opacity="0.25"/><path d="M15 9l-6 6M9 9l6 6"/></svg>`,
		title:   "Sign-in failed",
		message: htmlEscape(msg),
		hint:    "You can close this tab and try again in the terminal.",
		close:   false,
	})
}

type callbackContent struct {
	accent  string
	icon    string
	title   string
	message string
	hint    string
	close   bool
}

// callbackPage renders the shared card layout. Dark, centered,
// single inline-styled card — no external fonts or assets so it
// renders identically offline.
func callbackPage(c callbackContent) string {
	autoClose := ""
	if c.close {
		autoClose = `<script>setTimeout(function(){window.close();},4000);</script>`
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>SuperCli — %s</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh;
    display: flex; align-items: center; justify-content: center;
    background: radial-gradient(1200px 600px at 50%% -10%%, #1b2130 0%%, #0d1017 60%%);
    font-family: ui-sans-serif, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    color: #e6e8ee;
  }
  .card {
    width: min(92vw, 420px);
    background: #141925;
    border: 1px solid #232a39;
    border-radius: 16px;
    padding: 40px 36px;
    text-align: center;
    box-shadow: 0 20px 60px rgba(0,0,0,.45);
  }
  .icon { margin-bottom: 18px; line-height: 0; }
  h1 { margin: 0 0 8px; font-size: 22px; font-weight: 650; letter-spacing: -0.01em; }
  p { margin: 0 0 6px; color: #aab2c5; font-size: 15px; line-height: 1.5; }
  .hint { margin-top: 18px; color: #6b7488; font-size: 13px; }
  .brand {
    margin-top: 26px; padding-top: 18px; border-top: 1px solid #232a39;
    color: %s; font-size: 12px; font-weight: 600; letter-spacing: 0.08em;
    text-transform: uppercase;
  }
</style>
</head>
<body>
  <div class="card">
    <div class="icon">%s</div>
    <h1>%s</h1>
    <p>%s</p>
    <p class="hint">%s</p>
    <div class="brand">SuperCli</div>
  </div>
  %s
</body>
</html>`, c.title, c.accent, c.icon, c.title, c.message, c.hint, autoClose)
}

// htmlEscape minimally escapes a string for safe inclusion in HTML
// text (the OAuth error_description is attacker-influenceable).
func htmlEscape(s string) string {
	repl := []struct{ from, to string }{
		{"&", "&amp;"}, {"<", "&lt;"}, {">", "&gt;"}, {`"`, "&quot;"}, {"'", "&#39;"},
	}
	for _, r := range repl {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	return s
}
