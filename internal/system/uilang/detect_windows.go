//go:build windows

package uilang

import "golang.org/x/sys/windows"

var getUserDefaultUILanguage = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

func detectSystem() string {
	langID, _, callErr := getUserDefaultUILanguage.Call()
	if langID == 0 || callErr != windows.ERROR_SUCCESS {
		return ""
	}
	// Windows LANGID: the low ten bits are the primary language identifier.
	switch uint16(langID) & 0x03ff {
	case 0x15: // LANG_POLISH
		return Polish
	case 0x09: // LANG_ENGLISH
		return English
	default:
		return ""
	}
}
