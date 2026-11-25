package assembly

// Surface toolsets. Registration order is part of the contract: the provider
// receives tools in this order, so changing it invalidates prompt caches and
// the golden snapshots. Read across the tables to see every deliberate
// difference between surfaces in one place.
//
// Summary of the intentional differences:
//   - TUI registers everything (office editors, mail, library finder, user
//     tools, consult, hide_messages) and chooses its always-on core per
//     execution profile (AlwaysOnProfile) rather than per row.
//   - Batch is a small CI/scripting set: thin file tools always-on, no office,
//     mail, memory, goal or interactive tools, lexical tool_search.
//   - Web has a fixed always-on core per row, ask_user/host_power only on the
//     human-facing stream, a shared browser, memory through the engine, and
//     MCP/mail registered for discovery.

// TUIEarly is registered first, before the approval gate and the first
// tool_search index build (registerMediaAndOfficeTools in the past).
var TUIEarly = []Entry{
	{Tool: "read_image"},
	{Tool: "ask_user"},
	{Tool: "search_code"},
	{Tool: "code_intel"}, // dormant: exposed through tool_search only
	{Tool: "process_session"},
	{Tool: "headless_control"},
	{Tool: "read_zip", Wrap: WrapMutation},
	{Tool: "read_docx"},
	{Tool: "read_xlsx"},
	{Tool: "edit_docx", Wrap: WrapMutation},
	{Tool: "edit_xlsx", Wrap: WrapMutation},
	{Tool: "list_dir"},
	{Tool: "read_pdf"},
	{Tool: "show_media"},
	{Tool: "media_generation"},
	{Tool: "send_screenshot"},
	{Tool: "tool_search"}, // FTS-backed searcher owned by the TUI
	{Tool: "apply_skill"},
	{Tool: "user_tools"},
}

// TUICore follows the always-on profile and the memory stack: memory tools,
// the context-mode sandbox, goal, host power, darwin and session search.
var TUICore = []Entry{
	{Tool: "remember", AlwaysOn: true},
	{Tool: "recall", AlwaysOn: true},
	// ctx_execute gets undo snapshots but (unlike batch/web) no code-intel
	// invalidation: historical TUI wiring, kept identical.
	{Tool: "ctx_execute", Wrap: WrapCheckpoint},
	{Tool: "goal"},
	{Tool: "host_power"},
	{Tool: "darwin"}, // provided only when config darwin != false
	{Tool: "search_history"},
}

// MCPBridge is the one thin bridge to every configured MCP server (servers
// start lazily on first use). The TUI registers it after the agent loop.
var MCPBridge = []Entry{{Tool: "mcp_bridge", AlwaysOn: true}}

// TUILate holds the file, web and line tools registered last, after the loop
// and slash wiring; tool_search is re-indexed afterwards. Mutating rows get
// the full mutation wrapper (undo snapshot + code-intel invalidation), the
// same as web and batch. (Before 1.0.26 the TUI passed its wrappers by value
// and these tools never invalidated code-intel after an edit.)
var TUILate = []Entry{
	{Tool: "check_library_alternatives"},
	{Tool: "read_lines"},
	{Tool: "read_context"},
	{Tool: "read_many"},
	{Tool: "scratchpad"},
	{Tool: "patch_file", Wrap: WrapMutation},
	{Tool: "create_file", Wrap: WrapMutation},
	{Tool: "edit_files", Wrap: WrapMutation},
	{Tool: "write_file", Wrap: WrapMutation},
	{Tool: "make_dir", Wrap: WrapMutation},
	{Tool: "move", Wrap: WrapMutation},
	{Tool: "copy", Wrap: WrapMutation},
	{Tool: "trash", Wrap: WrapMutation},
	{Tool: "web_fetch"},
	{Tool: "browser"},
	{Tool: "web_download", Wrap: WrapMutation},
	{Tool: "web_search"},
	{Tool: "web_lookup", AlwaysOn: true},
	{Tool: "invoke_tool"},
	{Tool: "outlook_mail"},
	{Tool: "thunderbird_mail"},
}

// Batch is the CI/scripting toolset: the thin file family always-on with the
// same mutation wrapper rows as the other surfaces. Batch passes no
// checkpoint (a one-shot run has no undo UI), so only code-intel
// invalidation applies.
var Batch = []Entry{
	{Tool: "read_lines", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "read_context", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "read_many", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "scratchpad", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "list_dir", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "patch_file", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "create_file", Wrap: WrapMutation, AlwaysOn: true},
	// One edit across many files (rename, header); see edit_files.
	{Tool: "edit_files", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "write_file", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "make_dir", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "move", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "copy", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "trash", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "search_code", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "ctx_execute", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "code_intel"},
	{Tool: "process_session"},
	{Tool: "headless_control"},
	{Tool: "invoke_tool", AlwaysOn: true},
	// A compact current-facts tool is always available; the advanced
	// web_search/web_fetch contracts stay discoverable on demand.
	{Tool: "web_fetch"},
	{Tool: "browser"},
	{Tool: "web_download", Wrap: WrapMutation},
	{Tool: "web_search"},
	{Tool: "web_lookup", AlwaysOn: true},
	{Tool: "tool_search", AlwaysOn: true},
}

// Web is the web GUI / NestCafe toolset for one chat run.
var Web = []Entry{
	// Only the human-facing stream (AskCh set) gets host power and ask_user.
	{Tool: "host_power"},
	// Completed tool envelopes leave future prompts; their persisted rows
	// stay retrievable through this discoverable FTS tool.
	{Tool: "search_history"},
	// The current conversation's full transcript behind the compaction
	// state note: always available, so the model recalls instead of guessing.
	{Tool: "session_recall", AlwaysOn: true},
	{Tool: "code_intel"},
	{Tool: "process_session"},
	{Tool: "headless_control"},
	{Tool: "apply_skill", AlwaysOn: true},
	{Tool: "remember", AlwaysOn: true},
	{Tool: "recall", AlwaysOn: true},
	// One edit path (patch_file/create_file), matching the TUI thin core;
	// unlike the TUI the web core is fixed per row, not per profile.
	{Tool: "read_lines", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "read_context", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "read_many", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "read_image", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "list_dir", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "patch_file", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "create_file", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "edit_files", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "make_dir", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "move", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "copy", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "trash", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "search_code", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "ctx_execute", Wrap: WrapMutation, AlwaysOn: true},
	{Tool: "scratchpad", Wrap: WrapMutation, AlwaysOn: true},
	// write_file rewrites a whole file: discoverable, not core.
	{Tool: "write_file", Wrap: WrapMutation},
	// Rich attachment readers stay discoverable through tool_search.
	{Tool: "read_pdf"},
	{Tool: "read_docx"},
	{Tool: "read_xlsx"},
	{Tool: "send_screenshot"},
	{Tool: "show_media"},
	{Tool: "read_zip", Wrap: WrapCheckpoint},
	{Tool: "media_generation"},
	{Tool: "edit_docx", Wrap: WrapCheckpoint},
	{Tool: "edit_xlsx", Wrap: WrapCheckpoint},
	{Tool: "invoke_tool", Wrap: WrapCheckpoint, AlwaysOn: true},
	// goal becomes always-on only once a goal is active (engine_loop).
	{Tool: "goal"},
	{Tool: "ask_user", AlwaysOn: true},
	{Tool: "web_fetch"},
	{Tool: "browser"}, // shared engine browser
	{Tool: "web_download", Wrap: WrapMutation},
	{Tool: "web_search"},
	{Tool: "web_lookup", AlwaysOn: true},
	{Tool: "mcp_bridge", AlwaysOn: true},
	{Tool: "outlook_mail"},
	{Tool: "thunderbird_mail"},
	{Tool: "tool_search", AlwaysOn: true},
	{Tool: "darwin"}, // provided unless NestCafe or darwin = false
}

// Delegation returns the task/send_message/task_stop rows. The web GUI
// checkpoints all three and keeps them always-on; the CLI checkpoints the
// two that start or resume work and promotes them to always-on only in
// coordinator mode (adaptive CLI keeps them discoverable).
func Delegation(s Surface, coordinator bool) []Entry {
	if s == SurfaceWeb {
		return []Entry{
			{Tool: "task", Wrap: WrapCheckpoint, AlwaysOn: true},
			{Tool: "send_message", Wrap: WrapCheckpoint, AlwaysOn: true},
			{Tool: "task_stop", Wrap: WrapCheckpoint, AlwaysOn: true},
		}
	}
	return []Entry{
		{Tool: "task", Wrap: WrapCheckpoint, AlwaysOn: coordinator},
		{Tool: "send_message", Wrap: WrapCheckpoint, AlwaysOn: coordinator},
		{Tool: "task_stop", AlwaysOn: coordinator},
	}
}

// AlwaysOnProfile is the TUI's execution-profile-aware core (marked before
// most tools exist; marking is by name). ThinTools decides which of these
// carry a full schema vs a catalog entry; small_full_tools is the escape
// hatch. darwin is deliberately never core: delegation overlaps it.
func AlwaysOnProfile(coordinator, thinTools bool) []string {
	// Skill discovery stays visible in every profile.
	out := []string{"apply_skill"}
	switch {
	case coordinator:
		return append(out, "ask_user", "tool_search", "goal")
	case thinTools:
		// Prefer patch_file/create_file for mutations; read_image stays
		// core for attachment/vision turns.
		return append(out,
			"read_lines", "read_many", "read_image", "search_code",
			"ctx_execute", "ask_user", "patch_file", "create_file", "edit_files",
			"list_dir", "goal", "tool_search", "invoke_tool")
	default:
		return append(out, "read_image", "ask_user", "tool_search", "invoke_tool", "goal", "ctx_execute")
	}
}
