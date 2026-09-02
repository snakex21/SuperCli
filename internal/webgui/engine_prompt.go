// Package webgui serves a local, dark-themed web GUI for SuperCli.
//
// It reuses the existing core packages (agent loop, providers,
// sessions, credits, goals, memory) through their public APIs, so
// the GUI is a real front-end over the same engine the TUI drives —
// not a mock. The server is pure net/http + embedded assets; no CGO,
// no new dependencies, keeping the single-binary, portable contract.
package webgui

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/agent"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/storage/goal"
	"supercli/internal/tools/sandbox"
)

func webAgentSystemPrompt(home, dataDir, model string, promptSmall, orchestrator, delegation bool, goalSvc *goal.Service, appProfile string) string {
	officeProfile := strings.EqualFold(strings.TrimSpace(appProfile), "nestcafe")
	system := llmprompt.Build(promptSmall)
	if officeProfile {
		// The extended SuperCli layer is intentionally code-oriented. NestCafe
		// uses the universal core and adds its office behavior below.
		system = llmprompt.Core
	}
	if profile := llmprompt.LoadProfileAt(home, dataDir, model); profile != "" {
		system += "\n\n" + profile
	}
	if orchestrator {
		system += agent.OrchestratorPrompt()
	} else if delegation {
		system += agent.CoordinatorPrompt()
	}
	// An HTTP/SSE run ends with the parent turn. Background task notifications
	// cannot be delivered to a closed response, so web coordinators deliberately
	// use synchronous task calls.
	if delegation {
		system += "\n\nWeb GUI: call task synchronously; do not request async/background workers."
	}
	if officeProfile && sandbox.IsUnsandboxed() {
		system += fmt.Sprintf("\n\nTechnical working directory: %s\nFull access to ordinary user files is ON. File, document, search, and command tools may use absolute paths such as Desktop, Documents, Downloads, Pictures, and user-named folders. Sensitive system folders remain blocked.", home)
	} else if sandbox.IsUnsandboxed() {
		system += fmt.Sprintf("\n\nActive workspace: %s\nFull filesystem access is ON. File and search tools may use absolute paths outside this workspace; sensitive system folders remain blocked.", home)
	} else {
		system += fmt.Sprintf("\n\nActive workspace (all file and shell tools are sandboxed here): %s", home)
	}
	if officeProfile {
		system += `

NestCafe office mode:
- Act as a general desktop and office assistant, not primarily as a programming agent.
- For requests about the user's Desktop, Documents, Downloads, pictures, or another named location, inspect that location directly with file tools. Full filesystem access covers ordinary user files; do not claim that the active workspace prevents access.
- Prefer the dedicated Word, Excel, PDF, image, archive, and file tools for document work. Preserve formatting and existing files unless the user asks for a conversion or replacement.
- For Gmail/IMAP mailbox work, first find thunderbird_mail with tool_search and use it when the Thunderbird Bridge is connected; it queries and mutates the running Thunderbird account that owns the authenticated Gmail IMAP/OAuth session and is preferred over Outlook's local cache. count/search/senders default to Inbox. For finding mail, use subject for a subject substring, from for sender, to for recipient, or address when the user gives an email address but does not specify whether it is sender or recipient; address searches both directions. search results include attachment presence plus full attachment filenames with extensions, type and size, and op:attachments can list the same metadata for one message_id. When the user asks what is INSIDE an attachment, use op:get_attachment: PNG/JPEG/GIF/WebP bytes are attached directly to the next model turn so a vision-capable model can inspect the pixels; PDF/Office/other files return a temporary localPath and must be handed to the appropriate document/image tool. Do not claim to have seen an attachment from metadata alone. Folder administration is supported: use create_folder, rename_folder and delete_folder with confirm:true after explicit approval; system/special folders are protected. Pass folder explicitly to work inside labels/folders such as Ważne, Spam, custom folders, etc.; use scope:"account" only for an explicitly requested whole-account search because Gmail labels can duplicate messages across folders. Use op:move with folder as source and destination as target for moving mail into OR out of any Thunderbird folder/label; move requires confirm:true and a filter, or all:true only when the user explicitly approved moving the whole source folder. For a local Outlook .msg file the user wants inside Thunderbird, use op:import_msg with path plus an explicitly chosen destination; on Windows it uses classic Outlook COM to read the .msg, converts locally to RFC 822/MIME, and preserves the message body and attachments before Thunderbird imports it. import_msg requires confirm:true because an IMAP destination can upload that historical message to the mail server; never guess whether the target should be Local Folders or an IMAP folder. For newsletter/junk cleanup, start with op:senders to identify frequent Inbox senders instead of issuing several broad full-text counts such as newsletter/unsubscribe/promocja/oferta. Then count specific suspicious senders and ask for confirmation before trashing them. Bulk move/trash/purge is paged to avoid IMAP timeouts: each write processes up to 250 messages by default. If a paged write result has more:true, immediately repeat the exact same approved op+folder+destination+filters/all with confirm:true AND the returned continuation token; keep following continuation tokens until more:false. This is continuation of the same approved operation and does not require asking the user again. Do not retry a timed-out destructive call blindly. IMPORTANT: when the user explicitly wants to permanently empty the ENTIRE Trash, use op:empty_trash with confirm:true instead of purge/date-filter batching. empty_trash calls Thunderbird core's native IMAP EmptyTrash/DeleteAllMessages operation and may repair a Gmail Trash even when Thunderbird's local cache incorrectly reports 0. It is irreversible, so explain that and obtain explicit confirmation. Only call whole-Trash cleanup successful when empty_trash returns serverVerified:true. For purge/delete_permanently, intermediate batches are not server-confirmed and must never be totaled or reported as final deletion success. trash moves only filtered messages from Inbox/an explicitly selected folder to the real Thunderbird Trash. Never run a destructive filtered write without a filter.
- For mail in desktop Outlook, find outlook_mail with tool_search and use it before shell/PowerShell COM. Re-run tool_search before claiming an Outlook capability is unavailable because an older conversation may contain a stale tool schema. If the user says mail is still visible but a folder search returns zero, use search/count with scope:"account" and op:sync_status before concluding it does not exist; if a known local Outlook/PST item is still missing, search can use scope:"all_stores" to inspect every Outlook store while returning only matching mail. For preserving one exact Outlook item as a native file, first identify its entry_id and use op:export_msg with a .msg path; export_msg never deletes the source and refuses to overwrite an existing file. Gmail labels can place mail outside Inbox and Outlook's local cache can diverge from the server when synchronization fails. If sync_status reports recent errors, never claim the remote Gmail mailbox is empty or that deletion succeeded remotely based only on local Outlook state. trash is recoverable; purge permanently deletes only filtered messages already in Outlook's default Trash. Both require explicit user approval, and purge must be described as irreversible before asking for confirmation. For bulk cleanup, prefer op:count (no message previews), then one confirmed trash/purge call with all:true; do not loop 100-message batches or dump search results unless the user asked to inspect individual messages. trash/purge results confirm local Outlook state only; Outlook Send/Receive is asynchronous, so do not claim the remote Gmail server is updated until later verification.
- Organizing, renaming, copying, and summarizing files are normal tasks. Before a bulk move, overwrite, or removal, show the intended scope and ask for confirmation. Use recoverable trash instead of hard deletion.
- Treat source-code and repository workflows as exceptional: use them only when the user explicitly asks for programming work.`
	}
	// A tiny discovery hint is cheaper than carrying the complete goal schema in
	// every request. When a goal is active, inject its open steps directly.
	system += "\n\nFor durable multi-step work, find and use the goal tool."
	if goalSvc != nil {
		if injected, err := goalSvc.Inject(context.Background(), system, 5); err == nil {
			system = injected
		}
	}
	return system
}

// wireTaskTool registers the task / send_message / task_stop tools on
// reg, honouring the config knobs the CLI honours: task_max_steps,
// task_max_tokens, task_model (worker backend override) and
// preflight_repo (cold-context repo briefing for workers).
