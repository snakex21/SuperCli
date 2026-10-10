# Anthropic tool-schema compatibility regression

User report: Haiku 5.5 / Anthropic returns HTTP 400 tools.10.custom.input_schema because oneOf, allOf or anyOf appears at the root of input_schema.

The previous Anthropic request builder used the complete schema form. read_zip has a root oneOf for path/paths exclusivity, and ask_user has a root anyOf for single/multiple question forms. The complete local form is correct for validation but the reported endpoint rejects that wire root.

Anthropic Messages now uses its own checked, bounded-cache schema form. Root properties and ordinary nested constraints remain; branch-only MCP fields receive conservative property projections; references into removed root combinators are relocated under an unused $defs name. The full ToolDef.Schema and registry validator remain unchanged. Nested oneOf exclusivity is not invented from object-branch exclusivity.

Only Anthropic selects this new mode. Standard OpenAI portable schemas, the special Zen path, and model selection are unchanged. No probe/retry or model prompt instruction is added. Cache limits apply jointly to all mode payloads, and callers still own independent byte slices.

Before changing production, a mocked Messages HTTP endpoint reproduced the exact 400 with the real read_zip on tool index 10, plus one fixture for each root combinator. After the fix each case makes exactly one POST /v1/messages and returns successfully. This is a protocol regression fixture, not a live paid Anthropic inference.

Focused tests pass 26 top-level tests and 13 subcases: complete HTTP requests, full local read_zip/ask_user validation, nested constraints, permissive union alternatives, branch-only fields, local JSON references, request caching modes and cache ownership/residency. The final full llm/tools/factory and backend integration suite also passes.

The API tool shape is described in [Anthropic Define tools](https://platform.claude.com/docs/en/agents-and-tools/tool-use/define-tools). The exact unsupported-root rule here is independently evidenced by the user response and the regression fixture.

Files: internal/llm/anthropic_request.go, proto_tool_schema.go, proto_tool_schema_anthropic.go, anthropic_tool_schema_http_test.go and proto_tool_schema_anthropic_test.go.

Logs and hashes: .tmp/optimization-oct8-2026/closure-audit/anthropic-schema-o/receipt.json. Exact source validation, ten platform builds, installed EXE hashes and both --help checks: .tmp/optimization-oct8-2026/final-oct9-o.json.
