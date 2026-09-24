package tui

import (
	"os"
	"runtime"
	"strings"
)

// Classic conhost cannot reliably draw emoji/supplementary glyphs. Substitute
// only in the presentation layer; copying, persistence and model input retain
// the original Unicode text. Modern terminals keep emoji unchanged.
func legacyTerminalSymbols() bool {
	return legacyTerminalSymbolsFor(runtime.GOOS, os.Getenv("WT_SESSION"), os.Getenv("TERM_PROGRAM"), os.Getenv("SUPERCLI_EMOJI"))
}
func legacyTerminalSymbolsFor(platform, wt, program, override string) bool {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "1", "on", "true":
		return false
	case "0", "off", "false":
		return true
	}
	return platform == "windows" && wt == "" && program == ""
}
func terminalText(text string, legacy bool) string {
	if !legacy {
		return text
	}
	var b strings.Builder
	for _, r := range text {
		switch r {
		case '😀', '😃', '😄', '😁', '😊', '🙂', '😎', '🤗':
			b.WriteString(":)")
		case '😉':
			b.WriteString(";)")
		case '😂', '🤣':
			b.WriteString(":D")
		case '😔', '😢', '😭', '🙁', '😞':
			b.WriteString(":(")
		case '🤔', '🧐':
			b.WriteString("(?)")
		case '👋':
			b.WriteString("o/")
		case '👍', '👌', '✅', '✔', '✓':
			b.WriteString("[OK]")
		case '👎', '❌', '✗':
			b.WriteString("[X]")
		case '⚠', '🚨':
			b.WriteString("[!]")
		case '📎', '📄', '📁', '📂', '🗂':
			b.WriteString("[file]")
		case '💡':
			b.WriteString("[idea]")
		case '🚀', '⚡':
			b.WriteString(">>")
		case '❤', '💙', '💚', '💜', '🧡', '💛':
			b.WriteString("<3")
		case '🔧', '🛠', '⚙':
			b.WriteString("[tool]")
		case '🔍', '🔎':
			b.WriteString("[search]")
		case '🎉', '🥳':
			b.WriteString(":D")
		case '⏳', '⌛', '⏱':
			b.WriteString("[time]")
		case '\ufe0f', '\ufe0e', '\u200d': // emoji presentation / composition controls
		default:
			if r >= 0x1f3fb && r <= 0x1f3ff {
				continue
			}
			if (r >= 0x1f300 && r <= 0x1faff) || (r >= 0x1f1e6 && r <= 0x1f1ff) {
				b.WriteString("[*]")
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
