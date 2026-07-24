package files

import core "supercli/internal/tools/core"

type Tool = core.Tool
type Result = core.Result
type ImageContent = core.ImageContent
type Check = core.Check
type VerifyVerdict = core.VerifyVerdict
type DefaultVerifier = core.DefaultVerifier

func resolveSandboxed(baseDir, p string) (string, error) {
	return core.ResolveSandboxed(baseDir, p)
}
