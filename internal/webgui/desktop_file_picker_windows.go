//go:build windows

package webgui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	ofnHideReadOnly      = 0x00000004
	ofnNoChangeDir       = 0x00000008
	ofnAllowMultiSelect  = 0x00000200
	ofnPathMustExist     = 0x00000800
	ofnFileMustExist     = 0x00001000
	ofnExplorer          = 0x00080000
	ofnEnableSizing      = 0x00800000
	ofnDontAddToRecent   = 0x02000000
	pickerBufferUTF16Len = 65536
)

var (
	comdlg32                     = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpenFileNameW         = comdlg32.NewProc("GetOpenFileNameW")
	procCommDlgExtendedError     = comdlg32.NewProc("CommDlgExtendedError")
	user32Picker                 = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWindow      = user32Picker.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32Picker.NewProc("GetWindowThreadProcessId")
)

type openFileNameW struct {
	structSize      uint32
	owner           uintptr
	instance        uintptr
	filter          *uint16
	customFilter    *uint16
	maxCustomFilter uint32
	filterIndex     uint32
	file            *uint16
	maxFile         uint32
	fileTitle       *uint16
	maxFileTitle    uint32
	initialDir      *uint16
	title           *uint16
	flags           uint32
	fileOffset      uint16
	fileExtension   uint16
	defaultExt      *uint16
	customData      uintptr
	hook            uintptr
	templateName    *uint16
	reserved        uintptr
	reservedSize    uint32
	flagsEx         uint32
}

func pickDesktopFiles(initialDir string) ([]string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	fileBuffer := make([]uint16, pickerBufferUTF16Len)
	filter := pickerFilterUTF16()
	title, err := syscall.UTF16FromString("Wybierz pliki")
	if err != nil {
		return nil, err
	}

	var initialDirUTF16 []uint16
	if info, statErr := os.Stat(initialDir); statErr == nil && info.IsDir() {
		initialDirUTF16, err = syscall.UTF16FromString(initialDir)
		if err != nil {
			return nil, err
		}
	}

	of := openFileNameW{
		owner:       pickerOwnerWindow(),
		filter:      &filter[0],
		filterIndex: 1,
		file:        &fileBuffer[0],
		maxFile:     uint32(len(fileBuffer)),
		title:       &title[0],
		flags: ofnHideReadOnly |
			ofnNoChangeDir |
			ofnAllowMultiSelect |
			ofnPathMustExist |
			ofnFileMustExist |
			ofnExplorer |
			ofnEnableSizing |
			ofnDontAddToRecent,
	}
	of.structSize = uint32(unsafe.Sizeof(of))
	if len(initialDirUTF16) > 0 {
		of.initialDir = &initialDirUTF16[0]
	}

	ok, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&of)))
	runtime.KeepAlive(filter)
	runtime.KeepAlive(title)
	runtime.KeepAlive(initialDirUTF16)
	runtime.KeepAlive(fileBuffer)
	if ok == 0 {
		code, _, _ := procCommDlgExtendedError.Call()
		if code == 0 {
			return []string{}, nil
		}
		return nil, fmt.Errorf("Windows OpenFileDialog error 0x%X", code)
	}
	return parseOpenFileNameBuffer(fileBuffer), nil
}

func pickerOwnerWindow() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != uint32(os.Getpid()) {
		return 0
	}
	return hwnd
}

func pickerFilterUTF16() []uint16 {
	const allSupported = "*.png;*.jpg;*.jpeg;*.webp;*.gif;*.pdf;*.docx;*.xlsx;*.csv;*.zip;*.txt;*.md;*.json;*.yaml;*.yml;*.xml;*.html;*.css;*.js;*.ts;*.tsx;*.go;*.py"
	const images = "*.png;*.jpg;*.jpeg;*.webp;*.gif"
	const documents = "*.pdf;*.docx;*.xlsx;*.csv"
	const textCodeArchives = "*.zip;*.txt;*.md;*.json;*.yaml;*.yml;*.xml;*.html;*.css;*.js;*.ts;*.tsx;*.go;*.py"
	value := "Obsługiwane pliki\x00" + allSupported + "\x00" +
		"Obrazy\x00" + images + "\x00" +
		"Dokumenty i arkusze\x00" + documents + "\x00" +
		"Tekst, kod i archiwa\x00" + textCodeArchives + "\x00" +
		"Wszystkie pliki\x00*.*\x00\x00"
	return utf16.Encode([]rune(value))
}

func parseOpenFileNameBuffer(buffer []uint16) []string {
	parts := make([]string, 0, 8)
	start := 0
	for start < len(buffer) {
		end := start
		for end < len(buffer) && buffer[end] != 0 {
			end++
		}
		if end == start {
			break
		}
		parts = append(parts, string(utf16.Decode(buffer[start:end])))
		start = end + 1
	}
	if len(parts) <= 1 {
		return parts
	}
	root := parts[0]
	paths := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		paths = append(paths, filepath.Join(root, name))
	}
	return paths
}
