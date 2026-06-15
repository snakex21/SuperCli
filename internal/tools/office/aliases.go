package office

import core "supercli/internal/tools/core"

type Tool = core.Tool
type Result = core.Result
type ImageContent = core.ImageContent
type Registry = core.Registry

var NewRegistry = core.NewRegistry

func resolveSandboxed(baseDir, p string) (string, error) {
	return core.ResolveSandboxed(baseDir, p)
}

func readZipEntry(path, entryName string, maxBytes int64) ([]byte, error) {
	return core.ReadZipEntry(path, entryName, maxBytes)
}

func backupAndReplace(target, tmpPath string) (string, error) {
	return core.BackupAndReplace(target, tmpPath)
}

func editZipEntryInPlace(target, entryName string, newData []byte) (string, error) {
	return core.EditZipEntryInPlace(target, entryName, newData)
}

func xmlEscapeText(s string) string { return core.XMLEscapeText(s) }
