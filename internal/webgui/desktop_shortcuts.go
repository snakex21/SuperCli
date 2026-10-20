package webgui

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// Kept outside assets: normal HTTP GUI pages never receive this bootstrap.
//
//go:embed desktop_shortcuts.js
var desktopShortcutGuard string

func desktopBrowserShortcutScript(address string) string {
	owned, err := url.Parse(address)
	if err != nil || owned.Host == "" || (owned.Scheme != "http" && owned.Scheme != "https") {
		return ""
	}
	encoded, _ := json.Marshal(address)
	return strings.Replace(desktopShortcutGuard, "__SUPERCLI_OWNED_URL__", string(encoded), 1)
}

// Init is the cross-platform document-start contract, independent of native
// extensions. This also supports WebKit's DOM path if a native Mac/Linux host
// is added; the current non-Windows launcher uses an ordinary external browser.
func installDesktopShortcutProtection(view interface{ Init(string) }, address string) error {
	script := desktopBrowserShortcutScript(address)
	if script == "" {
		return errors.New("desktop shortcuts: invalid owned URL")
	}
	view.Init(script)
	if native, ok := view.(interface{ EnableSelectiveBrowserShortcuts() error }); ok {
		return native.EnableSelectiveBrowserShortcuts()
	}
	return nil
}
