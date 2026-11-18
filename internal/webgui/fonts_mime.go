package webgui

import "mime"

// The embedded GUI fonts (assets/fonts, 1.0.33) are served with their font
// media types; the platform MIME table does not always know them.
func init() {
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".ttf", "font/ttf")
}
