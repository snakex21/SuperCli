# Native tool image provenance and restored transcript presentation

Date: 2026-10-03. Baseline: dev17 / 9a930aa.

A native image read previously resumed as an ordinary user attachment because its synthetic multimodal carrier did not retain the originating call ID. The backend now records explicit host provenance on the image reference and exposes it through a presentation-only transcript DTO. The frontend owns folding/reparenting; the stored model transcript remains intact.

## Host provenance and DTO

ImageRef adds optional SourceToolCallID (json source_tool_call_id) and ToolOutputCarrier (json tool_output_carrier). Only the existing native tool-result carrier construction assigns them, using the actual tool call ID. It does not set Message.ToolCallID or change the carrier role, text, image bytes, Active state or name.

The public attachment admission path is SetNextUserImages followed by Run. prepareSessionImages clears both host-only fields after normalization/externalization, including Path, inline Data and URL representations. Caller-owned refs remain unchanged. Genuine input cannot become a host-authored carrier merely by carrying these metadata fields.

The transcript DTO adds optional:

- tool_images: [{source_call_id, path}], where path is the existing safe session preview token.
- tool_image_carrier: true only for a complete explicitly marked synthetic carrier.

A carrier is eligible only when it is RoleUser, has no independent Content/Name/ToolCallID/tool calls, has no real attachment records, and all image parts have both explicit origin fields and a safe preview token. Surrounding text parts are covered by the host-authorship marker; their words are never matched. Any unmarked/mixed image, unsupported part, unsafe token, genuine attachment or missing source ID keeps the whole message's existing attachment/bubble fallback.

Source IDs and sequence numbers are exact. Backend projection does not search across pages, infer intent from prose, rewrite history or create a persistent association table. The UI uses carrier seq plus the nearest preceding real tool-result seq/ID, including repeated IDs and page boundaries. Missing/pruned origins can retain a safe folded tool-media boundary card.

Explicit show_media/capture/generation output retains its existing canonical presentation metadata. Native attached analysis pixels carry origin separately; the frontend keeps canonical-preview deduplication.

## Protocol and persistence proof

No provider builder consumes either new field. Focused differential tests compare exact request bytes with and without provenance for Chat Completions, Anthropic, Codex, standard Responses and the unchanged OpenCode Zen Responses projection, with vision enabled/disabled and file/data/URL references. Echo output is identical.

The request fixtures include native chat state, signed Anthropic thinking and encrypted Responses state. Builders preserve these blocks and the original input. Healthy call/result/carrier histories still bypass tool-call repair; using the image field avoids incorrectly treating a RoleUser carrier as a tool result.

DormantImages retains origin while clearing only Active on its owned copy. Existing omitted-image markers and active/dormant provider projection are unchanged. Codec, actual portable storage close/reopen and saved model-context projection retain exact origin, role and text without a database migration.

Transcript tests preserve stored rows byte-for-byte, ordinary attachment fallback and exact pagination cursors. An image-only last page retains its carrier seq/ID while the actual tool result remains on the preceding page. Reused call IDs do not change backend evidence.

## Completed validation

The first focused run passed llm (0.422 s) and webgui (0.213 s). Two new fixtures needed correction to existing contracts: nil image entries are already rejected by verification, and the agent explicitly supplies DormantImages to Writer.AppendMessage rather than the writer forcing dormancy. Only those fixture expectations changed.

The corrected session/agent scope passed (session 0.533 s; agent 0.132 s). Final vet passed for llm, session, agent and webgui. Earlier llm/webgui passing tests were not repeated. Existing image externalization, error, preview safety, reasoning and repair controls were included.

No real provider call, model/GPU workload, screenshot, user application, benchmark or progress polling was used. The companion UI owner validates live folded loading, restored nearest-result association, page-boundary transfer, reused IDs and compatible genuine/legacy attachment behavior.

## Cost and limits

On 64-bit, ImageRef grows from 104 to 120 bytes for the additional string header. ToolOutputCarrier sits next to Active and uses existing alignment padding: unsafe.Sizeof matches an origin-only layout (120 bytes). There is no additional bool-related growth, cache or standing per-message field. The source ID shares the existing immutable call ID string bytes on initial assignment; restored metadata necessarily decodes its own stored string.

Both metadata fields are omitted for ordinary refs. Native refs add small serialized metadata to existing PartsJSON. Provider request bytes and therefore provider input tokens remain unchanged. The carrier DTO adds only safe path/ID metadata to native presentation rows; ordinary rows omit the new fields.

This is a provenance and disclosure fix. It does not claim a reduction in generation TPS, inference latency or process RSS. Legacy unmarked carriers remain compatible rather than being heuristically reassigned.

Portable receipts and baseline snapshots: .tmp/goal-tool-media-presentation-2026-10-03/backend-tests.txt, backend-corrected-tests.txt, backend-vet.txt, backend-baseline and backend-frozen-manifest.json. Production ownership is proto_message.go, loop_tools.go, loop_media.go, data.go and data_transcript.go plus four focused test files; provider/Zen implementation files and R14 worker-mutation sources remain unchanged.
