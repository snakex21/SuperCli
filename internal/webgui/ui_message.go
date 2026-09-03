package webgui

// uiWarning is a sentence the browser must render, expressed as a catalog key
// plus its parameters instead of finished prose. Go owns the condition; the
// wording and its translations live in assets/js/01-i18n.js, so a message can
// never be pinned to one language by the server.
//
// Name fills the catalog's {n} placeholder and Count its {c}, matching the
// interpolation convention the rest of the dictionary already uses.
type uiWarning struct {
	Code  string `json:"code"`
	Name  string `json:"name,omitempty"`
	Count string `json:"count,omitempty"`
}
