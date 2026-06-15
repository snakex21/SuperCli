package search

import core "supercli/internal/tools/core"

type Tool = core.Tool
type Result = core.Result
type Registry = core.Registry

var NewRegistry = core.NewRegistry

func toolSignature(name, schema string) string { return core.ToolSignature(name, schema) }
