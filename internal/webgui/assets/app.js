// SuperCli Web GUI — front-end
// Vanilla JS, no build step, served from Go embedded assets.
// Design notes: docs/webgui.md. Talks JSON + SSE to the local server.
"use strict";

/* ═══ helpers ═══ */

function $(sel) { return document.querySelector(sel); }
function $$(sel) { return Array.prototype.slice.call(document.querySelectorAll(sel)); }
function el(tag, cls, text) {
  var e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}
function escHtml(s) {
  return String(s == null ? "" : s)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}
function escAttr(s) { return escHtml(s); }
function clip(s, n) {
  s = String(s || "");
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}
function fmtDuration(ms) {
  if (ms < 60000) return (ms / 1000).toFixed(1) + "s";
  var m = Math.floor(ms / 60000), s = Math.round((ms % 60000) / 1000);
  return m + "m" + (s < 10 ? "0" : "") + s + "s";
}
function fmtTok(n) {
  n = n || 0;
  if (n >= 100000) return Math.round(n / 1000) + "k";
  if (n >= 10000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}
function fmtSize(b) {
  if (b >= 1048576) return (b / 1048576).toFixed(1) + " MB";
  if (b >= 1024) return (b / 1024).toFixed(1) + " KB";
  return b + " B";
}
function fmtWhen(iso) {
  var d = new Date(iso);
  if (isNaN(d)) return "";
  var now = new Date(), diff = now - d;
  if (diff < 3600000) return Math.max(1, Math.round(diff / 60000)) + "m";
  if (diff < 86400000) return Math.round(diff / 3600000) + "h";
  return d.toLocaleDateString();
}
function statsLocale() {
  return typeof ui !== "undefined" && ui.lang === "pl" ? "pl-PL" : "en-US";
}
function fmtInteger(n) {
  n = Number(n);
  if (!Number.isFinite(n)) n = 0;
  try { return new Intl.NumberFormat(statsLocale(), { maximumFractionDigits: 0 }).format(Math.round(n)); }
  catch (e) { return String(Math.round(n)); }
}
function fmtCompactNumber(n) {
  n = Number(n);
  if (!Number.isFinite(n)) n = 0;
  try {
    return new Intl.NumberFormat(statsLocale(), {
      notation: "compact", maximumFractionDigits: Math.abs(n) >= 10000 ? 1 : 0,
    }).format(n);
  } catch (e) { return fmtTok(n); }
}
function fmtMoney(n, currency, rate) {
  n = Number(n);
  if (!Number.isFinite(n)) return "—";
  var abs = Math.abs(n);
  var digits = rate ? 6 : (abs > 0 && abs < 0.0001 ? 6 : (abs > 0 && abs < 0.01 ? 4 : 2));
  try {
    return new Intl.NumberFormat(statsLocale(), {
      style: "currency", currency: currency || "USD",
      minimumFractionDigits: rate ? 2 : Math.min(2, digits), maximumFractionDigits: digits,
    }).format(n);
  } catch (e) { return (currency || "USD") + " " + n.toFixed(digits); }
}
function fmtDateTime(iso) {
  if (!iso) return "—";
  var d = new Date(iso);
  if (isNaN(d)) return "—";
  try {
    return new Intl.DateTimeFormat(statsLocale(), {
      dateStyle: "medium", timeStyle: "short",
    }).format(d);
  } catch (e) { return d.toLocaleString(); }
}
async function j(url, opts) {
  var resp = await fetch(url, opts);
  if (!resp.ok) throw new Error((await resp.text() || resp.status).toString().trim());
  return resp.json();
}
function jpost(url, body) {
  return j(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}

var toastTimer = null;
function toast(msg) {
  var t = $("#toast");
  t.textContent = msg;
  t.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(function () { t.classList.remove("show"); }, 2600);
}

/* ═══ i18n ═══ */

var I18N = {
  en: {
    "side.stats": "Stats", "side.sessions": "Sessions", "side.projects": "Projects", "side.tasks": "Tasks", "side.goal": "Goal",
    "side.add": "add", "side.noSessions": "No sessions yet.", "side.noProjects": "No projects registered.",
    "session.rename": "Rename", "session.delete": "Delete", "session.namePrompt": "Conversation name",
    "session.deleteConfirm": "Delete this conversation permanently?", "session.renamed": "Conversation renamed.",
    "session.deleted": "Conversation deleted.", "session.stopRun": "Stop the current run before deleting this conversation.",
    "session.runtime": "Remember chat model", "session.runtimeHint": "Restore this session's provider, model and reasoning level when it is opened.",
    "session.runtimeFailed": "Could not restore the session model; keeping the current selection.",
    "session.older": "Show older messages", "session.loadingOlder": "Loading older messages…",
    "session.today": "Today", "session.yesterday": "Yesterday",
    "project.stopRun": "Stop the current run before switching projects.",
    "common.refresh": "refresh", "common.scan": "scan", "common.back": "Back", "common.save": "Save", "common.openFolder": "Open folder",
    "common.openProjectFolder": "Open project folder", "common.closePanel": "Close side panel",
    "common.cancel": "Cancel", "common.edit": "edit", "common.remove": "remove", "common.add": "Add",
    "common.loading": "Loading…", "common.error": "error",
    "model.none": "no model", "model.search": "Search models…", "model.noMatches": "No models match this search.", "model.reasoning": "Reasoning effort",
    "model.think": "think", "model.auto": "auto", "model.default": "default",
    "model.hide": "hide", "model.show": "show", "model.setDefault": "CLI default",
    "model.allProviders": "All", "model.hideAll": "Hide all", "model.showAll": "Show all",
    "welcome.title": "What are we building?",
    "welcome.sub": "The agent reads, edits and runs code in the active workspace.",
    "welcome.h1": "Summarize this project", "welcome.h2": "Check configuration status", "welcome.h3": "How do I run the tests?",
    "composer.ph": "Message SuperCli…", "composer.send": "Send", "composer.stop": "Stop",
	"composer.queue": "Queue", "composer.queued": "Queued", "composer.interrupt": "Interrupt & send",
	"composer.sendNow": "Send now", "composer.resume": "Resume queue", "composer.remove": "Remove",
	"composer.ready": "Ready", "composer.working": "Working…", "composer.waiting": "Waiting for provider…", "composer.stopped": "Stopped.",
	"question.title": "Decision needed", "question.custom": "Your own answer…", "question.submit": "Continue",
	"question.cancel": "Cancel question", "question.pick": "Select an option or enter your own answer.",
	"question.prompt": "Image prompt", "question.copy": "Copy prompt", "question.copied": "Prompt copied.",
	"question.waiting": "Waiting for your decision…", "question.zoom": "Open full preview",
	"undo.undo": "Undo changes", "undo.redo": "Restore changes", "undo.done": "Changes undone.",
	"undo.redone": "Changes restored.", "undo.conflict": "Undo stopped: files changed after the agent turn.",
    "run.done": "Done", "run.tools": "tools", "run.think": "think", "run.cached": "cached", "tool.running": "running",
    "tool.input": "Input", "tool.output": "Output", "tool.error": "Error", "tool.stdout": "Output", "tool.stderr": "Error output",
    "tool.ctx_execute": "Command", "tool.process_session": "Process", "tool.list_dir": "Folder contents",
    "tool.read_lines": "Read file", "tool.read_many": "Read files", "tool.read_context": "Read context",
    "tool.search_code": "Search code", "tool.edit_line": "Edit file", "tool.edit_lines": "Edit file",
    "tool.insert_after": "Insert line", "tool.delete_lines": "Delete lines", "tool.write_file": "Write file",
    "task.delegation": "Delegation", "task.done": "done", "task.failed": "failed", "task.stopped": "stopped",
    "task.brief": "Task", "task.activity": "Activity", "task.report": "Worker report", "task.step": "step", "task.steps": "steps",
    "task.input": "input", "task.output": "output",
    "role.thinking": "Thinking", "chat.stopped": "stopped by user", "chat.connError": "connection error",
    "chat.streamEnded": "The response stream ended unexpectedly. The answer may be incomplete.",
    "stats.model": "model", "stats.provider": "provider", "stats.ctx": "context (last turn)", "stats.session": "session tokens",
    "stats.daily": "tokens today", "stats.workers": "Workers", "stats.noWorkers": "No delegations this session.",
    "stats.turn": "Last turn", "stats.orch": "Orchestrator", "stats.orchDesc": "delegation",
    "stats.orchAuto": "auto", "stats.orchOn": "always", "stats.orchOff": "never",
    "stats.noTurn": "No turns yet.", "stats.sessionSection": "Session", "stats.totalTokens": "total tokens",
    "stats.inputTokens": "input tokens", "stats.evaluatedInput": "evaluated input", "stats.cachedInput": "cached input",
    "stats.outputTokens": "output tokens", "stats.reasoningTokens": "reasoning", "stats.totalCost": "total cost",
    "stats.messages": "messages", "stats.userMessages": "user", "stats.assistantMessages": "assistant",
    "stats.toolMessages": "tool", "stats.toolCalls": "tool calls", "stats.calls": "model calls",
    "stats.created": "created", "stats.updated": "last activity", "stats.contextWindow": "context window",
    "stats.currentContext": "Current context", "stats.contextShare": "estimated request composition",
    "panel.title": "Control panel", "panel.settings": "Settings", "panel.appearance": "Appearance",
    "panel.models": "Models", "panel.providers": "Providers", "panel.runtime": "Model servers", "panel.accounts": "Accounts",
    "panel.mcp": "MCP", "panel.memory": "Memory", "panel.data": "Data", "panel.goal": "Goal", "panel.usage": "Usage",
    "panel.files": "Files", "panel.about": "About", "panel.workflow": "Workflow",
	"data.hint": "Back up or remove SuperCli's local conversations and memory. Project files and API keys are never deleted here.",
	"data.sessions": "Conversations", "data.memories": "Memory entries", "data.goals": "Goals",
	"data.backup": "Backup", "data.export": "Export data", "data.exportHint": "Downloads conversations, memory, goals and UI preferences. API keys, accounts, caches and checkpoints are excluded.",
	"data.import": "Import data", "data.importHint": "Validated data replaces the current conversations and memory after restart. A rescue copy is made automatically.",
	"data.importPending": "Import prepared — restart SuperCli to apply it.", "data.importReady": "Import prepared. Restart SuperCli to apply it.",
	"data.danger": "Delete local data", "data.clearSessions": "Delete all conversations", "data.clearMemory": "Clear AI memory",
	"data.confirmSessions": "Permanently delete all conversations? Project files, settings and API keys remain untouched.",
	"data.confirmMemory": "Permanently clear global and project AI memory? Conversations, project files and API keys remain untouched.",
	"data.cleared": "Local data was cleared.",
	"workflow.queue": "Persistent task queue", "workflow.branches": "Session branches", "workflow.profile": "Model prompt profile",
	"workflow.scratch": "Worker scratchpad", "workflow.hard": "Hard verification", "workflow.runHard": "Run /test hard",
	"workflow.noBranches": "No branches yet.", "workflow.fork": "Fork", "workflow.compare": "Compare selected", "workflow.open": "Open",
	"workflow.rewind": "Rewind here", "workflow.rewindDone": "Message ready to edit or send again.",
	"workflow.rewindHint": "Return to just before this message and place it in the composer. The original conversation stays available.",
	"workflow.rewindWhy": "Why are you rewinding? (optional)", "workflow.rewindWhyHint": "A short reason helps the model avoid repeating the rejected approach.",
	"workflow.rewindReason": "What should the model do differently?", "workflow.rewindContinue": "Rewind",
	"workflow.rewindFiles": "Also restore file changes", "workflow.rewindFilesHint": "checkpoint(s), file(s). Conflicts stop the entire rewind.",
	"workflow.rewindConflict": "File rewind stopped because these files changed later",
	"workflow.restoreFiles": "Restore the agent's version", "workflow.restoreFilesHint": "File changes were rewound. You can restore the complete version produced by the agent.",
	"workflow.rewindFileCount": "Rewound files: {n}", "workflow.filesRestored": "The agent's file version was restored.",
	"workflow.runBoth": "Queue same prompt in both",
	"workflow.profileHint": "Only this bounded local file is appended for the active model family. No extra model call.",
	"undo.learn": "Why did you undo this? (optional)", "undo.learnHint": "A short reason helps the agent avoid repeating the same mistake.",
	"undo.sessionScope": "This session", "undo.globalScope": "All projects", "undo.saveLesson": "Save lesson",
	"sandbox.allowConfirm": "Allow the agent to read, search and modify files outside the active project? Sensitive Windows system folders remain blocked.",
    "set.nextSession": "next session", "set.resetAll": "Reset all to defaults",
    "set.state.default": "default", "set.state.auto": "auto", "set.state.on": "on", "set.state.off": "off",
    "set.source.default": "default", "set.source.manual": "custom",
    "set.resetAllHint": "Removes every managed key above; providers and API keys stay untouched.",
    "set.hint": "value · source — same knobs as the TUI /settings panel, stored in config.toml.",
    "app.theme": "Theme", "app.dark": "Dark", "app.light": "Light", "app.midnight": "Midnight", "app.lang": "Language",
	"app.scale": "Interface size", "app.auto": "Auto", "app.badge": "App icon counter",
    "app.uiFont": "Interface font", "app.codeFont": "Code font", "app.notify": "Notifications",
    "app.sound": "Play a sound when a run finishes", "app.desktop": "Desktop notification when tab is hidden",
    "prov.configured": "Configured providers", "prov.addNew": "Add provider", "prov.templates": "Templates",
    "prov.name": "Name", "prov.type": "Type", "prov.baseUrl": "Base URL", "prov.apiKey": "API key",
    "prov.apiKeyHint": "optional for local endpoints", "prov.model": "Default model",
    "prov.addScan": "Add & scan models", "prov.keepKey": "saved key is loaded for editing",
    "prov.clearKey": "Clear stored API key", "prov.save": "Save changes", "prov.none": "No providers configured yet.",
    "prov.showKey": "Show", "prov.hideKey": "Hide", "prov.loadingKey": "Loading saved key…",
    "prov.keyLoaded": "Saved key loaded locally.", "prov.keyLoadFailed": "Saved key could not be shown; leaving this field empty will keep it.",
    "prov.key": "key", "prov.noKey": "no key", "prov.models": "models",
    "prov.added": "Provider added.", "prov.addFailed": "Error — provider was not added",
    "runtime.title": "Model servers", "runtime.hint": "Passive status check. Refresh never starts an inference or loads a model.",
    "runtime.empty": "No providers configured.", "runtime.checking": "checking", "runtime.online": "online",
    "runtime.offline": "offline", "runtime.disabled": "disabled", "runtime.active": "active",
    "runtime.endpoint": "endpoint", "runtime.model": "selected model", "runtime.models": "reported models",
    "runtime.latency": "endpoint latency", "runtime.ttft": "first token", "runtime.speed": "generation",
    "runtime.duration": "last call", "runtime.context": "context window", "runtime.tools": "tool calling",
    "runtime.available": "available", "runtime.unknown": "not reported", "runtime.lastRun": "this app run",
    "runtime.measured": "measured", "runtime.reported": "reported", "runtime.catalog": "catalog",
    "runtime.limits": "Not exposed by this endpoint", "runtime.hardware": "VRAM / RAM / GPU",
    "runtime.backendQueue": "backend queue", "runtime.details": "models and limitations",
    "runtime.enable": "enable", "runtime.disable": "disable", "runtime.switchFirst": "Switch model before disabling the active provider.",
    "acct.title": "Codex accounts", "acct.login": "Log in", "acct.logout": "Log out",
    "acct.refreshTok": "Refresh token", "acct.loggingIn": "logging in…", "acct.loggedOut": "logged out",
    "mcp.servers": "MCP servers", "mcp.none": "No MCP servers configured.", "mcp.addServer": "Add MCP server",
    "mcp.command": "Command", "mcp.args": "Args (comma-separated)", "mcp.env": "Env (KEY=VALUE, one per line)",
    "mcp.editJson": "edit as JSON", "mcp.saveJson": "Save JSON", "mcp.backToList": "back to list",
    "mcp.portable": "Portable MCP packages", "mcp.portableHint": "Drop one self-contained package folder with manifest.toml into supercli-data/mcp. It moves with SuperCli and starts only when the agent needs it.",
    "mcp.openPackages": "Open package folder", "mcp.noPackages": "No portable packages installed.",
    "mcp.ready": "ready", "mcp.running": "running", "mcp.disabled": "disabled", "mcp.unavailable": "unavailable", "mcp.lazy": "lazy start",
    "mem.empty": "No memory entries.", "goal.none": "No active goal.",
    "goal.noneHint": "Create one for work that spans several steps or sessions.",
    "goal.new": "New goal", "goal.title": "What should be accomplished?", "goal.description": "Context (optional)",
    "goal.criteria": "Definition of done (optional)", "goal.create": "Create goal", "goal.tasks": "Steps",
    "goal.noTasks": "No steps yet.", "goal.addTask": "Add step", "goal.taskPlaceholder": "Next concrete step",
    "goal.start": "Start", "goal.complete": "Complete", "goal.skip": "Skip", "goal.reopen": "Reopen",
    "goal.finish": "Finish goal", "goal.abandon": "Abandon", "goal.finishConfirm": "Mark this goal as completed?",
    "goal.abandonConfirm": "Abandon this goal? Its history will remain saved.", "goal.progress": "Progress",
    "goal.notes": "Progress note", "goal.notePlaceholder": "Decision, result or blocker", "goal.addNote": "Add note",
    "goal.descriptionLabel": "Context", "goal.criteriaLabel": "Done when", "goal.updated": "Goal updated.",
    "goal.verification": "Final verification", "goal.verificationWaiting": "Complete or skip every open step before verification.",
    "goal.verificationReady": "Check the finished work against the definition of done and record concrete evidence.",
    "goal.verificationPassed": "Verified — ready to finish", "goal.verificationFailed": "Verification failed — more work is required",
    "goal.verificationEvidence": "Evidence", "goal.verifyPlaceholder": "Checks run and their results", "goal.verifyPass": "Verification passed",
    "goal.verifyFail": "Needs more work", "goal.finishBlocked": "Finish every step and record a successful verification first.",
    "goal.awaitingVerification": "Awaiting verification", "goal.readyToFinish": "Verified — ready to finish",
    "goal.status.active": "active", "goal.status.pending": "pending", "goal.status.in_progress": "in progress",
    "goal.status.done": "done", "goal.status.skipped": "skipped",
    "goal.manage": "manage", "goal.sideHelpLabel": "How does Goal work?",
    "goal.sideHelpTitle": "How Goal works",
    "goal.sideHelpPurpose": "It is a persistent plan for work spanning multiple messages or sessions.",
    "goal.sideHelpAgent": "The agent sees the active goal and open steps in every turn, and can update steps and notes as it works.",
    "goal.sideHelpFinish": "After every step is finished, the result must pass a concrete verification. Only then can you or the agent explicitly close the goal.",
    "goal.sideHelpNoBackground": "It does not start background work or extra model calls by itself.",
    "goal.sideEmpty": "No active goal.", "goal.sideEmptyHint": "Use it for work that should survive more than one message.",
    "goal.openSteps": "open steps", "goal.currentStep": "Current step", "goal.nextSteps": "Next steps",
    "usage.model": "Model", "usage.session": "Session tokens", "usage.daily": "Tokens today",
    "usage.context": "Context report", "usage.summary": "Session summary", "usage.details": "Session details",
    "usage.noSession": "Start or select a session to collect persistent usage statistics.",
    "usage.contextEmpty": "No context snapshot has been recorded yet.",
    "telemetry.title": "Performance telemetry", "telemetry.empty": "Use this build for a few turns to collect timing samples.",
    "telemetry.scope.7d": "last 7 days",
    "telemetry.samples": "measured replies", "telemetry.average": "Average reply", "telemetry.steps": "Agent steps",
    "telemetry.model": "Model backend", "telemetry.tools": "Tools", "telemetry.cli": "CLI overhead", "telemetry.persist": "Session write",
    "telemetry.topTools": "Slowest tools", "telemetry.bottleneck.model": "The model backend is the limit",
    "telemetry.bottleneck.tools": "Tool execution is the limit", "telemetry.bottleneck.cli": "CLI overhead is the limit",
    "telemetry.signal.collect_more": "A few more replies will make this diagnosis more reliable.",
    "telemetry.signal.model_bound": "The CLI is out of the hot path; backend or hardware changes will have the largest effect.",
    "telemetry.signal.cli_overhead": "Client-side preparation is measurable and worth profiling further.",
    "telemetry.signal.many_steps": "The agent uses many model rounds per reply; batching or clearer tool results may help.",
    "telemetry.signal.tool_failures": "Some tool calls fail; removing those retries can save complete model rounds.",
    "telemetry.signal.model_failures": "Provider calls failed or were canceled; inspect endpoint stability and timeouts.",
    "telemetry.signal.low_cache": "Prompt-cache reuse is low; keep the system/tool prefix byte-stable.",
    "telemetry.signal.slow_persist": "Session persistence is slower than expected, although it does not block inference.",
    "cost.estimated": "Estimated", "cost.manual": "Manual rate", "cost.subscription": "Subscription",
    "cost.local": "Local", "cost.free": "Free", "cost.unknown": "Unknown", "cost.partial": "Partial estimate",
    "cost.subscriptionValue": "Included in plan", "cost.localValue": "Runs locally", "cost.freeValue": "Free",
    "cost.unknownValue": "Price unavailable", "cost.partialValue": "Partial coverage",
    "cost.source": "Source", "cost.source.manual": "manual rate", "cost.source.official": "official pricing",
    "cost.source.provider": "provider pricing", "cost.source.catalog": "price catalog",
    "cost.source.mixed": "mixed rates", "cost.source.free": "free model",
    "cost.source.subscription": "subscription", "cost.source.local": "local runtime",
    "cost.unknownCalls.one": "call without a known price", "cost.unknownCalls.other": "calls without a known price",
    "cost.includedCalls.one": "included or local call", "cost.includedCalls.other": "included or local calls",
    "cost.cacheUnknown": "No separate cache rate is known; cached input uses the regular input rate in this estimate.",
    "price.title": "Rates & manual override", "price.hint": "USD per 1M tokens for this exact provider/model pair.",
    "price.input": "Input", "price.cache": "Cached input", "price.output": "Output", "price.unit": "USD / 1M tokens",
    "price.cacheHint": "Use 0 when the provider does not publish a separate cache rate.",
    "price.save": "Save manual rate", "price.remove": "Remove manual rate",
    "price.saved": "Manual rate saved.", "price.removed": "Manual rate removed.",
    "price.invalid": "Enter finite values from 0 to 1,000,000.",
    "price.identityMissing": "A provider and model are required before a manual rate can be saved.",
    "price.removeConfirm": "Remove the manual rate for this provider and model?",
    "context.user": "User", "context.assistant": "Assistant", "context.tools": "Tools", "context.other": "Other",
    "files.up": "up", "files.save": "Save", "files.saved": "Saved.",
    "about.desc": "Single-binary AI coding agent with portable data. This GUI is a thin front-end over the same engine the TUI drives.",
    "about.workspace": "Workspace", "about.model": "Model", "about.shortcuts": "Keyboard shortcuts",
    "about.rebindHint": "Click a key to rebind it.",
    "kb.send": "Send prompt", "kb.newline": "New line", "kb.close": "Close overlay / stop palette",
    "kb.focus": "Focus composer", "kb.panel": "Control panel", "kb.sidebar": "Toggle side panel",
    "kb.thinking": "Expand/collapse thinking", "kb.tools": "Expand/collapse tool rows",
    "status.connected": "connected", "status.connecting": "connecting…", "status.offline": "server unreachable",
  },
  pl: {
    "side.stats": "Statystyki", "side.sessions": "Sesje", "side.projects": "Projekty", "side.tasks": "Zadania", "side.goal": "Cel",
    "side.add": "dodaj", "side.noSessions": "Brak sesji.", "side.noProjects": "Brak projektów.",
    "session.rename": "Zmień nazwę", "session.delete": "Usuń", "session.namePrompt": "Nazwa rozmowy",
    "session.deleteConfirm": "Usunąć tę rozmowę na stałe?", "session.renamed": "Zmieniono nazwę rozmowy.",
    "session.deleted": "Usunięto rozmowę.", "session.stopRun": "Zatrzymaj trwającą odpowiedź przed usunięciem tej rozmowy.",
    "session.runtime": "Pamiętaj model czatu", "session.runtimeHint": "Po otwarciu sesji przywróć jej provider, model i poziom myślenia.",
    "session.runtimeFailed": "Nie udało się przywrócić modelu sesji; pozostawiono bieżący wybór.",
    "session.older": "Pokaż starsze wiadomości", "session.loadingOlder": "Wczytuję starsze wiadomości…",
    "session.today": "Dzisiaj", "session.yesterday": "Wczoraj",
    "project.stopRun": "Zatrzymaj bieżące zadanie przed zmianą projektu.",
    "common.refresh": "odśwież", "common.scan": "skanuj", "common.back": "Wróć", "common.save": "Zapisz", "common.openFolder": "Otwórz folder",
    "common.openProjectFolder": "Otwórz folder projektu", "common.closePanel": "Zamknij panel boczny",
    "common.cancel": "Anuluj", "common.edit": "edytuj", "common.remove": "usuń", "common.add": "Dodaj",
    "common.loading": "Wczytywanie…", "common.error": "błąd",
    "model.none": "brak modelu", "model.search": "Szukaj modeli…", "model.noMatches": "Żaden model nie pasuje do wyszukiwania.", "model.reasoning": "Wysiłek rozumowania",
    "model.think": "think", "model.auto": "auto", "model.default": "domyślny",
    "model.hide": "ukryj", "model.show": "pokaż", "model.setDefault": "domyślny CLI",
    "model.allProviders": "Wszystkie", "model.hideAll": "Ukryj wszystkie", "model.showAll": "Pokaż wszystkie",
    "welcome.title": "Co dziś budujemy?",
    "welcome.sub": "Agent czyta, edytuje i uruchamia kod w aktywnym projekcie.",
    "welcome.h1": "Podsumuj ten projekt", "welcome.h2": "Sprawdź stan konfiguracji", "welcome.h3": "Jak uruchomić testy?",
    "composer.ph": "Napisz do SuperCli…", "composer.send": "Wyślij", "composer.stop": "Stop",
	"composer.queue": "Dodaj do kolejki", "composer.queued": "W kolejce", "composer.interrupt": "Przerwij i wyślij",
	"composer.sendNow": "Wyślij teraz", "composer.resume": "Wznów kolejkę", "composer.remove": "Usuń",
	"composer.ready": "Gotowy", "composer.working": "Pracuję…", "composer.waiting": "Czekam na odpowiedź providera…", "composer.stopped": "Zatrzymano.",
	"question.title": "Potrzebna decyzja", "question.custom": "Własna odpowiedź…", "question.submit": "Kontynuuj",
	"question.cancel": "Anuluj pytanie", "question.pick": "Wybierz opcję albo wpisz własną odpowiedź.",
	"question.prompt": "Prompt do obrazu", "question.copy": "Kopiuj prompt", "question.copied": "Skopiowano prompt.",
	"question.waiting": "Czekam na Twoją decyzję…", "question.zoom": "Otwórz pełny podgląd",
	"undo.undo": "Cofnij zmiany", "undo.redo": "Przywróć zmiany", "undo.done": "Cofnięto zmiany.",
	"undo.redone": "Przywrócono zmiany.", "undo.conflict": "Cofanie zatrzymane: pliki zmieniono po turze agenta.",
    "run.done": "Gotowe", "run.tools": "narzędzia", "run.think": "myślenie", "run.cached": "z cache", "tool.running": "pracuje",
    "tool.input": "Dane wejściowe", "tool.output": "Wynik", "tool.error": "Błąd", "tool.stdout": "Wynik", "tool.stderr": "Wyjście błędu",
    "tool.ctx_execute": "Komenda", "tool.process_session": "Proces", "tool.list_dir": "Zawartość folderu",
    "tool.read_lines": "Odczyt pliku", "tool.read_many": "Odczyt plików", "tool.read_context": "Kontekst pliku",
    "tool.search_code": "Szukanie w kodzie", "tool.edit_line": "Edycja pliku", "tool.edit_lines": "Edycja pliku",
    "tool.insert_after": "Dodanie linii", "tool.delete_lines": "Usunięcie linii", "tool.write_file": "Zapis pliku",
    "task.delegation": "Delegacja", "task.done": "gotowe", "task.failed": "błąd", "task.stopped": "zatrzymano",
    "task.brief": "Zadanie", "task.activity": "Aktywność", "task.report": "Raport agenta", "task.step": "krok", "task.steps": "kroków",
    "task.input": "wej.", "task.output": "wyj.",
    "role.thinking": "Myślenie", "chat.stopped": "zatrzymane przez użytkownika", "chat.connError": "błąd połączenia",
    "chat.streamEnded": "Strumień odpowiedzi zakończył się nieoczekiwanie. Odpowiedź może być niepełna.",
    "stats.model": "model", "stats.provider": "dostawca", "stats.ctx": "kontekst (ostatnia tura)", "stats.session": "tokeny sesji",
    "stats.daily": "tokeny dziś", "stats.workers": "Workerzy", "stats.noWorkers": "Brak delegacji w tej sesji.",
    "stats.turn": "Ostatnia tura", "stats.orch": "Orkiestrator", "stats.orchDesc": "delegowanie",
    "stats.orchAuto": "auto", "stats.orchOn": "zawsze", "stats.orchOff": "nigdy",
    "stats.noTurn": "Jeszcze bez tur.", "stats.sessionSection": "Sesja", "stats.totalTokens": "łącznie tokenów",
    "stats.inputTokens": "tokeny wejściowe", "stats.evaluatedInput": "wejście bez cache", "stats.cachedInput": "wejście z cache",
    "stats.outputTokens": "tokeny wyjściowe", "stats.reasoningTokens": "rozumowanie", "stats.totalCost": "łączny koszt",
    "stats.messages": "wiadomości", "stats.userMessages": "użytkownik", "stats.assistantMessages": "asystent",
    "stats.toolMessages": "narzędzia", "stats.toolCalls": "wywołania narzędzi", "stats.calls": "wywołania modelu",
    "stats.created": "utworzono", "stats.updated": "ostatnia aktywność", "stats.contextWindow": "limit kontekstu",
    "stats.currentContext": "Bieżący kontekst", "stats.contextShare": "szacowany podział zapytania",
    "panel.title": "Panel sterowania", "panel.settings": "Ustawienia", "panel.appearance": "Wygląd",
    "panel.models": "Modele", "panel.providers": "Dostawcy", "panel.runtime": "Serwery modeli", "panel.accounts": "Konta",
    "panel.mcp": "MCP", "panel.memory": "Pamięć", "panel.data": "Dane", "panel.goal": "Cel", "panel.usage": "Zużycie",
    "panel.files": "Pliki", "panel.about": "O programie", "panel.workflow": "Przepływ pracy",
	"data.hint": "Twórz kopie lub usuwaj lokalne rozmowy i pamięć SuperCli. Pliki projektów ani klucze API nie są tutaj usuwane.",
	"data.sessions": "Rozmowy", "data.memories": "Wpisy pamięci", "data.goals": "Cele",
	"data.backup": "Kopia zapasowa", "data.export": "Eksportuj dane", "data.exportHint": "Pobiera rozmowy, pamięć, cele i preferencje wyglądu. Klucze API, konta, cache i checkpointy są pomijane.",
	"data.import": "Importuj dane", "data.importHint": "Po ponownym uruchomieniu sprawdzone dane zastąpią obecne rozmowy i pamięć. Program automatycznie utworzy kopię ratunkową.",
	"data.importPending": "Import przygotowany — uruchom SuperCli ponownie, aby go zastosować.", "data.importReady": "Import przygotowany. Uruchom SuperCli ponownie, aby go zastosować.",
	"data.danger": "Usuwanie danych lokalnych", "data.clearSessions": "Usuń wszystkie rozmowy", "data.clearMemory": "Wyczyść pamięć AI",
	"data.confirmSessions": "Trwale usunąć wszystkie rozmowy? Pliki projektów, ustawienia i klucze API pozostaną nietknięte.",
	"data.confirmMemory": "Trwale wyczyścić globalną i projektową pamięć AI? Rozmowy, pliki projektów i klucze API pozostaną nietknięte.",
	"data.cleared": "Wyczyszczono dane lokalne.",
	"workflow.queue": "Trwała kolejka zadań", "workflow.branches": "Gałęzie sesji", "workflow.profile": "Profil promptu modelu",
	"workflow.scratch": "Notatnik workerów", "workflow.hard": "Twarda weryfikacja", "workflow.runHard": "Uruchom /test hard",
	"workflow.noBranches": "Brak gałęzi.", "workflow.fork": "Rozgałęź", "workflow.compare": "Porównaj wybrane", "workflow.open": "Otwórz",
	"workflow.rewind": "Wróć tutaj", "workflow.rewindDone": "Wiadomość jest gotowa do poprawienia lub ponownego wysłania.",
	"workflow.rewindHint": "Wróć tuż przed tę wiadomość i wstaw ją do pola edycji. Oryginalna rozmowa pozostanie zachowana.",
	"workflow.rewindWhy": "Dlaczego cofasz? (opcjonalnie)", "workflow.rewindWhyHint": "Krótki powód pomoże modelowi nie powtórzyć odrzuconego podejścia.",
	"workflow.rewindReason": "Co model powinien zrobić inaczej?", "workflow.rewindContinue": "Cofnij",
	"workflow.rewindFiles": "Cofnij również zmiany w plikach", "workflow.rewindFilesHint": "checkpointów, plików. Konflikt zatrzyma całe cofanie.",
	"workflow.rewindConflict": "Cofanie plików zatrzymane — te pliki zmieniono później",
	"workflow.restoreFiles": "Przywróć wersję agenta", "workflow.restoreFilesHint": "Cofnięto zmiany w plikach. Możesz przywrócić całą wersję wykonaną przez agenta.",
	"workflow.rewindFileCount": "Cofnięte pliki: {n}", "workflow.filesRestored": "Przywrócono wersję plików wykonaną przez agenta.",
	"workflow.runBoth": "Dodaj ten sam prompt do obu",
	"workflow.profileHint": "Do promptu trafia tylko ten ograniczony plik rodziny aktywnego modelu. Bez dodatkowej inferencji.",
	"undo.learn": "Dlaczego cofnąłeś tę zmianę? (opcjonalnie)", "undo.learnHint": "Krótki powód pomoże agentowi nie powtórzyć tego błędu.",
	"undo.sessionScope": "Ta sesja", "undo.globalScope": "Wszystkie projekty", "undo.saveLesson": "Zapisz wskazówkę",
	"sandbox.allowConfirm": "Pozwolić agentowi czytać, przeszukiwać i modyfikować pliki poza aktywnym projektem? Wrażliwe foldery systemowe Windows nadal będą zablokowane.",
    "set.nextSession": "następna sesja", "set.resetAll": "Przywróć wszystkie domyślne",
    "set.state.default": "domyślnie", "set.state.auto": "auto", "set.state.on": "włącz", "set.state.off": "wyłącz",
    "set.source.default": "domyślne", "set.source.manual": "własne",
    "set.resetAllHint": "Usuwa wszystkie zarządzane klucze powyżej; dostawcy i klucze API zostają.",
    "set.hint": "wartość · źródło — te same pokrętła co panel /settings w TUI, zapisywane w config.toml.",
    "app.theme": "Motyw", "app.dark": "Ciemny", "app.light": "Jasny", "app.midnight": "Północ", "app.lang": "Język",
	"app.scale": "Rozmiar interfejsu", "app.auto": "Automatyczny", "app.badge": "Licznik na ikonie aplikacji",
    "app.uiFont": "Czcionka interfejsu", "app.codeFont": "Czcionka kodu", "app.notify": "Powiadomienia",
    "app.sound": "Dźwięk po zakończeniu pracy", "app.desktop": "Powiadomienie systemowe gdy karta ukryta",
    "prov.configured": "Skonfigurowani dostawcy", "prov.addNew": "Dodaj dostawcę", "prov.templates": "Szablony",
    "prov.name": "Nazwa", "prov.type": "Typ", "prov.baseUrl": "Base URL", "prov.apiKey": "Klucz API",
    "prov.apiKeyHint": "opcjonalny dla lokalnych", "prov.model": "Domyślny model",
    "prov.addScan": "Dodaj i skanuj modele", "prov.keepKey": "zapisany klucz jest wczytywany do edycji",
    "prov.clearKey": "Wyczyść zapisany klucz API", "prov.save": "Zapisz zmiany", "prov.none": "Brak dostawców.",
    "prov.showKey": "Pokaż", "prov.hideKey": "Ukryj", "prov.loadingKey": "Wczytywanie zapisanego klucza…",
    "prov.keyLoaded": "Zapisany klucz wczytano lokalnie.", "prov.keyLoadFailed": "Nie udało się pokazać klucza; puste pole nadal go zachowa.",
    "prov.key": "klucz", "prov.noKey": "brak klucza", "prov.models": "modeli",
    "prov.added": "Provider został dodany.", "prov.addFailed": "Błąd — provider nie został dodany",
    "runtime.title": "Serwery modeli", "runtime.hint": "Pasywne sprawdzenie stanu. Odświeżenie nie uruchamia inferencji ani nie ładuje modelu.",
    "runtime.empty": "Brak skonfigurowanych providerów.", "runtime.checking": "sprawdzanie", "runtime.online": "online",
    "runtime.offline": "offline", "runtime.disabled": "wyłączony", "runtime.active": "aktywny",
    "runtime.endpoint": "endpoint", "runtime.model": "wybrany model", "runtime.models": "zgłoszone modele",
    "runtime.latency": "opóźnienie endpointu", "runtime.ttft": "pierwszy token", "runtime.speed": "generowanie",
    "runtime.duration": "ostatnie wywołanie", "runtime.context": "limit kontekstu", "runtime.tools": "wywołania narzędzi",
    "runtime.available": "dostępne", "runtime.unknown": "brak danych", "runtime.lastRun": "bieżące uruchomienie",
    "runtime.measured": "zmierzone", "runtime.reported": "zgłoszone", "runtime.catalog": "katalog",
    "runtime.limits": "Endpoint tego nie udostępnia", "runtime.hardware": "VRAM / RAM / GPU",
    "runtime.backendQueue": "kolejka backendu", "runtime.details": "modele i ograniczenia",
    "runtime.enable": "włącz", "runtime.disable": "wyłącz", "runtime.switchFirst": "Przed wyłączeniem aktywnego providera zmień model.",
    "acct.title": "Konta Codex", "acct.login": "Zaloguj", "acct.logout": "Wyloguj",
    "acct.refreshTok": "Odśwież token", "acct.loggingIn": "logowanie…", "acct.loggedOut": "wylogowany",
    "mcp.servers": "Serwery MCP", "mcp.none": "Brak serwerów MCP.", "mcp.addServer": "Dodaj serwer MCP",
    "mcp.command": "Polecenie", "mcp.args": "Argumenty (po przecinku)", "mcp.env": "Env (KLUCZ=WARTOŚĆ, po jednym w linii)",
    "mcp.editJson": "edytuj jako JSON", "mcp.saveJson": "Zapisz JSON", "mcp.backToList": "wróć do listy",
    "mcp.portable": "Przenośne paczki MCP", "mcp.portableHint": "Wrzuć samodzielny folder paczki z manifest.toml do supercli-data/mcp. Przenosi się razem z SuperCli i startuje dopiero, gdy agent jej potrzebuje.",
    "mcp.openPackages": "Otwórz folder paczek", "mcp.noPackages": "Brak zainstalowanych paczek przenośnych.",
    "mcp.ready": "gotowa", "mcp.running": "uruchomiona", "mcp.disabled": "wyłączona", "mcp.unavailable": "niedostępna", "mcp.lazy": "start na żądanie",
    "mem.empty": "Brak wpisów pamięci.", "goal.none": "Brak aktywnego celu.",
    "goal.noneHint": "Utwórz go dla pracy obejmującej kilka kroków lub sesji.",
    "goal.new": "Nowy cel", "goal.title": "Co ma zostać osiągnięte?", "goal.description": "Kontekst (opcjonalnie)",
    "goal.criteria": "Warunek ukończenia (opcjonalnie)", "goal.create": "Utwórz cel", "goal.tasks": "Kroki",
    "goal.noTasks": "Brak kroków.", "goal.addTask": "Dodaj krok", "goal.taskPlaceholder": "Następny konkretny krok",
    "goal.start": "Rozpocznij", "goal.complete": "Ukończ", "goal.skip": "Pomiń", "goal.reopen": "Otwórz ponownie",
    "goal.finish": "Zakończ cel", "goal.abandon": "Porzuć", "goal.finishConfirm": "Oznaczyć ten cel jako ukończony?",
    "goal.abandonConfirm": "Porzucić ten cel? Jego historia pozostanie zapisana.", "goal.progress": "Postęp",
    "goal.notes": "Notatka postępu", "goal.notePlaceholder": "Decyzja, wynik lub blokada", "goal.addNote": "Dodaj notatkę",
    "goal.descriptionLabel": "Kontekst", "goal.criteriaLabel": "Gotowe, gdy", "goal.updated": "Cel zaktualizowany.",
    "goal.verification": "Weryfikacja końcowa", "goal.verificationWaiting": "Najpierw ukończ lub świadomie pomiń wszystkie otwarte kroki.",
    "goal.verificationReady": "Sprawdź gotową pracę względem warunku ukończenia i zapisz konkretny dowód.",
    "goal.verificationPassed": "Zweryfikowano — można zakończyć", "goal.verificationFailed": "Weryfikacja nieudana — potrzebna jest dalsza praca",
    "goal.verificationEvidence": "Dowód", "goal.verifyPlaceholder": "Wykonane sprawdzenia i ich wyniki", "goal.verifyPass": "Weryfikacja udana",
    "goal.verifyFail": "Wymaga poprawek", "goal.finishBlocked": "Najpierw ukończ kroki i zapisz pozytywną weryfikację.",
    "goal.awaitingVerification": "Czeka na weryfikację", "goal.readyToFinish": "Zweryfikowano — można zakończyć",
    "goal.status.active": "aktywny", "goal.status.pending": "oczekuje", "goal.status.in_progress": "w toku",
    "goal.status.done": "gotowe", "goal.status.skipped": "pominięte",
    "goal.manage": "zarządzaj", "goal.sideHelpLabel": "Jak działa Cel?",
    "goal.sideHelpTitle": "Jak działa Cel",
    "goal.sideHelpPurpose": "To trwały plan dla pracy obejmującej wiele wiadomości lub sesji.",
    "goal.sideHelpAgent": "Agent widzi aktywny cel i otwarte kroki w każdej turze. Podczas pracy może aktualizować kroki oraz dopisywać wyniki, decyzje i blokady.",
    "goal.sideHelpFinish": "Po wykonaniu kroków wynik musi przejść konkretną weryfikację. Dopiero wtedy Ty lub agent możecie świadomie zamknąć cel.",
    "goal.sideHelpNoBackground": "Cel sam nie uruchamia pracy w tle ani dodatkowych wywołań modelu.",
    "goal.sideEmpty": "Brak aktywnego celu.", "goal.sideEmptyHint": "Użyj go, gdy praca powinna przetrwać więcej niż jedną wiadomość.",
    "goal.openSteps": "otwarte kroki", "goal.currentStep": "Bieżący krok", "goal.nextSteps": "Następne kroki",
    "usage.model": "Model", "usage.session": "Tokeny sesji", "usage.daily": "Tokeny dziś",
    "usage.context": "Raport kontekstu", "usage.summary": "Podsumowanie sesji", "usage.details": "Szczegóły sesji",
    "usage.noSession": "Rozpocznij lub wybierz sesję, aby zbierać trwałe statystyki użycia.",
    "usage.contextEmpty": "Nie zapisano jeszcze migawki kontekstu.",
    "telemetry.title": "Telemetria wydajności", "telemetry.empty": "Poużywaj tej wersji przez kilka tur, aby zebrać próbki czasu.",
    "telemetry.scope.7d": "ostatnie 7 dni",
    "telemetry.samples": "zmierzonych odpowiedzi", "telemetry.average": "Średnia odpowiedź", "telemetry.steps": "Kroki agenta",
    "telemetry.model": "Backend modelu", "telemetry.tools": "Narzędzia", "telemetry.cli": "Narzut CLI", "telemetry.persist": "Zapis sesji",
    "telemetry.topTools": "Najwolniejsze narzędzia", "telemetry.bottleneck.model": "Ograniczeniem jest backend modelu",
    "telemetry.bottleneck.tools": "Ograniczeniem jest wykonywanie narzędzi", "telemetry.bottleneck.cli": "Ograniczeniem jest narzut CLI",
    "telemetry.signal.collect_more": "Kilka kolejnych odpowiedzi zwiększy wiarygodność diagnozy.",
    "telemetry.signal.model_bound": "CLI jest poza gorącą ścieżką; największy efekt da backend lub sprzęt.",
    "telemetry.signal.cli_overhead": "Przygotowanie po stronie klienta jest mierzalne i warto je dalej profilować.",
    "telemetry.signal.many_steps": "Agent zużywa wiele tur modelu na odpowiedź; pomóc może grupowanie pracy lub lepsze wyniki narzędzi.",
    "telemetry.signal.tool_failures": "Część narzędzi kończy się błędem; usunięcie powtórek oszczędzi pełne tury modelu.",
    "telemetry.signal.model_failures": "Wywołania providera nie powiodły się lub zostały anulowane; sprawdź stabilność endpointu i timeouty.",
    "telemetry.signal.low_cache": "Ponowne użycie cache promptu jest niskie; prefiks systemu i narzędzi powinien pozostać bajtowo stały.",
    "telemetry.signal.slow_persist": "Zapis sesji jest wolniejszy niż oczekiwano, choć nie blokuje inferencji.",
    "cost.estimated": "Szacunek", "cost.manual": "Stawka ręczna", "cost.subscription": "Subskrypcja",
    "cost.local": "Lokalny", "cost.free": "Bezpłatny", "cost.unknown": "Nieznany", "cost.partial": "Częściowy szacunek",
    "cost.subscriptionValue": "W ramach subskrypcji", "cost.localValue": "Działa lokalnie", "cost.freeValue": "Bezpłatnie",
    "cost.unknownValue": "Brak danych o cenie", "cost.partialValue": "Częściowe pokrycie",
    "cost.source": "Źródło", "cost.source.manual": "stawka ręczna", "cost.source.official": "oficjalny cennik",
    "cost.source.provider": "cennik dostawcy", "cost.source.catalog": "katalog cen",
    "cost.source.mixed": "mieszane stawki", "cost.source.free": "model bezpłatny",
    "cost.source.subscription": "subskrypcja", "cost.source.local": "lokalne uruchomienie",
    "cost.unknownCalls.one": "wywołanie bez znanej ceny", "cost.unknownCalls.few": "wywołania bez znanej ceny",
    "cost.unknownCalls.many": "wywołań bez znanej ceny", "cost.unknownCalls.other": "wywołania bez znanej ceny",
    "cost.includedCalls.one": "wywołanie w planie lub lokalne", "cost.includedCalls.few": "wywołania w planie lub lokalne",
    "cost.includedCalls.many": "wywołań w planie lub lokalnych", "cost.includedCalls.other": "wywołania w planie lub lokalne",
    "cost.cacheUnknown": "Brak osobnej stawki cache; w tym szacunku użyto zwykłej ceny wejścia.",
    "price.title": "Stawki i ręczne nadpisanie", "price.hint": "USD za 1 mln tokenów dla tej dokładnej pary dostawca/model.",
    "price.input": "Wejście", "price.cache": "Wejście z cache", "price.output": "Wyjście", "price.unit": "USD / 1 mln tokenów",
    "price.cacheHint": "Wpisz 0, jeśli dostawca nie publikuje osobnej stawki cache.",
    "price.save": "Zapisz stawkę ręczną", "price.remove": "Usuń stawkę ręczną",
    "price.saved": "Zapisano stawkę ręczną.", "price.removed": "Usunięto stawkę ręczną.",
    "price.invalid": "Wpisz skończone wartości od 0 do 1 000 000.",
    "price.identityMissing": "Przed zapisaniem stawki wymagane są dostawca i model.",
    "price.removeConfirm": "Usunąć ręczną stawkę dla tego dostawcy i modelu?",
    "context.user": "Użytkownik", "context.assistant": "Asystent", "context.tools": "Narzędzia", "context.other": "Inne",
    "files.up": "wyżej", "files.save": "Zapisz", "files.saved": "Zapisano.",
    "about.desc": "Agent kodowania w jednej binarce z przenośnymi danymi. To GUI jest cienkim frontem nad tym samym silnikiem co TUI.",
    "about.workspace": "Projekt", "about.model": "Model", "about.shortcuts": "Skróty klawiszowe",
    "about.rebindHint": "Kliknij klawisz, aby zmienić przypisanie.",
    "kb.send": "Wyślij", "kb.newline": "Nowa linia", "kb.close": "Zamknij nakładkę",
    "kb.focus": "Fokus na polu tekstowym", "kb.panel": "Panel sterowania", "kb.sidebar": "Pokaż/ukryj panel boczny",
    "kb.thinking": "Zwiń/rozwiń myślenie", "kb.tools": "Zwiń/rozwiń narzędzia",
    "status.connected": "połączono", "status.connecting": "łączenie…", "status.offline": "serwer niedostępny",
  },
};
Object.assign(I18N.en, {
  "data.export": "Export without secrets",
  "data.exportFull": "Export everything",
  "data.exportFullHint": "Portable ZIP with conversations, memory, goals, provider keys, Codex accounts, MCP/skills, user tools and prompt profiles. It contains readable secrets, so store it securely.",
  "data.importHint": "Accepts a safe or full SuperCli ZIP. After restart it replaces the matching data and creates a rescue copy."
});
Object.assign(I18N.pl, {
  "data.export": "Eksportuj bez sekret\u00f3w",
  "data.exportFull": "Eksportuj wszystko",
  "data.exportFullHint": "Przeno\u015bny ZIP z rozmowami, pami\u0119ci\u0105, celami, kluczami provider\u00f3w, kontami Codex, MCP/skills, narz\u0119dziami i profilami promptu. Zawiera czytelne sekrety, wi\u0119c przechowuj go bezpiecznie.",
  "data.importHint": "Przyjmuje bezpieczny lub pe\u0142ny ZIP SuperCli. Po restarcie zast\u0119puje odpowiednie dane i tworzy kopi\u0119 ratunkow\u0105."
});
Object.assign(I18N.en, {
  "context.safe": "Safe",
  "context.approaching": "Approaching limit",
  "context.compactionRecommended": "Compaction recommended",
  "context.pruneThreshold": "old tool results may be shortened",
  "context.compactThreshold": "automatic compaction",
  "context.compactNow": "Compact now",
  "context.compacting": "Compacting\u2026",
  "context.compactHint": "Summarizes the older model-visible context. The complete conversation remains saved and searchable.",
  "context.compacted": "Context compacted: {n} older messages replaced by a summary.",
  "context.selectSession": "Open a conversation before compacting its context.",
  "context.busy": "Wait for the current answer to finish before compacting."
});
Object.assign(I18N.pl, {
  "context.safe": "Bezpieczny",
  "context.approaching": "Zbli\u017ca si\u0119 do limitu",
  "context.compactionRecommended": "Zalecana kompakcja",
  "context.pruneThreshold": "mog\u0105 zosta\u0107 skr\u00f3cone stare wyniki narz\u0119dzi",
  "context.compactThreshold": "automatyczna kompakcja",
  "context.compactNow": "Kompaktuj teraz",
  "context.compacting": "Kompaktowanie\u2026",
  "context.compactHint": "Streszcza starszy kontekst widziany przez model. Pe\u0142na rozmowa pozostaje zapisana i dost\u0119pna do wyszukiwania.",
  "context.compacted": "Kontekst skompaktowany: {n} starszych wiadomo\u015bci zast\u0105piono podsumowaniem.",
  "context.selectSession": "Otw\u00f3rz rozmow\u0119, zanim skompaktujesz jej kontekst.",
  "context.busy": "Poczekaj na zako\u0144czenie bie\u017c\u0105cej odpowiedzi."
});
function t(key) {
  var lang = ui.lang || "en";
  return (I18N[lang] && I18N[lang][key]) || I18N.en[key] || key;
}

// Config keys remain the stable API, but they are implementation details.
// The panel leads with task-oriented names and keeps the raw key in a tooltip.
var SETTING_COPY = {
  en: {
    orchestrator: ["Orchestrator", "Delegate substantial work adaptively, always, or never."],
    allow_all: ["Access outside the project", "Allow file operations beyond the active project; sensitive system folders remain blocked."],
    thinking: ["Thinking mode", "Enable explicit thinking for local models that support a soft switch."],
    navigator: ["Task navigator", "Choose whether requests are routed as chat, advice, or coordinated work."],
    stable_toolset: ["Stable tool set", "Keep the tool catalog unchanged during a session to preserve the KV cache."],
    cache_prompt: ["Prompt cache", "Ask compatible local llama.cpp servers to reuse their KV prompt cache."],
    darwin_parallel: ["Parallel Darwin variants", "Run best-of-N candidate agents concurrently when the backend can handle it."],
    task_parallel: ["Parallel delegations", "Let independent delegated tasks run at the same time."],
    memory_briefing_tokens: ["Startup memory budget", "Maximum tokens used by the memory briefing at session start."],
    task_max_steps: ["Worker step limit", "Maximum number of model turns a delegated worker may take."],
    task_max_tokens: ["Worker token limit", "Maximum total token spend for one delegated worker."],
    task_model: ["Worker model", "Optional model or provider/model used for delegated work."],
    noop_gate: ["Skip unchanged batch runs", "Avoid an unnecessary model call when an identical batch request has no new changes."],
    preflight_repo: ["Repository preflight", "Add a compact repository-state briefing to the first message and delegated tasks."],
    draft_verify: ["Draft and verify", "Draft file changes with a worker, then verify them before applying the result."],
    draft_verify_max_rounds: ["Draft revision limit", "Maximum revise rounds before the coordinator takes over."],
    verify_commands: ["Verification commands", "Commands used as objective checks for draft verification, separated with semicolons."],
    default_model: ["Default model", "Model selected when SuperCli starts."],
    default_provider: ["Default provider", "Provider selected when SuperCli starts."]
  },
  pl: {
    orchestrator: ["Orkiestrator", "Deleguj większe zadania automatycznie, zawsze albo nigdy."],
    allow_all: ["Dostęp poza projektem", "Pozwala działać na plikach poza aktywnym projektem; wrażliwe foldery systemowe pozostają zablokowane."],
    thinking: ["Tryb myślenia", "Włącza jawne myślenie w lokalnych modelach obsługujących miękkie przełączanie."],
    navigator: ["Nawigator zadań", "Rozpoznaje, czy prośba jest rozmową, poradą czy pracą wymagającą koordynacji."],
    stable_toolset: ["Stały zestaw narzędzi", "Nie zmienia katalogu narzędzi w trakcie sesji, aby zachować cache KV."],
    cache_prompt: ["Pamięć promptu", "Prosi zgodne serwery llama.cpp o ponowne użycie pamięci KV promptu."],
    darwin_parallel: ["Równoległe warianty Darwin", "Uruchamia kandydatów best-of-N równolegle, gdy backend sobie z tym poradzi."],
    task_parallel: ["Równoległe delegacje", "Pozwala wykonywać niezależne delegowane zadania w tym samym czasie."],
    memory_briefing_tokens: ["Budżet pamięci startowej", "Maksymalna liczba tokenów na przypomnienie pamięci przy starcie sesji."],
    task_max_steps: ["Limit kroków workera", "Maksymalna liczba tur modelu dla jednego delegowanego workera."],
    task_max_tokens: ["Limit tokenów workera", "Maksymalny łączny koszt tokenów jednego delegowanego workera."],
    task_model: ["Model workerów", "Opcjonalny model lub dostawca/model używany do delegowanej pracy."],
    noop_gate: ["Pomijanie niezmienionych zadań", "Nie wywołuje modelu ponownie, gdy identyczne zadanie wsadowe nie ma nowych zmian."],
    preflight_repo: ["Wstępny kontekst repozytorium", "Dodaje krótki stan repozytorium do pierwszej wiadomości i delegowanych zadań."],
    draft_verify: ["Szkic i weryfikacja", "Worker przygotowuje zmiany, a system sprawdza je przed zastosowaniem."],
    draft_verify_max_rounds: ["Limit poprawek szkicu", "Maksymalna liczba rund poprawek przed przejęciem pracy przez koordynatora."],
    verify_commands: ["Polecenia weryfikacyjne", "Polecenia obiektywnie sprawdzające szkic, rozdzielone średnikami."],
    default_model: ["Domyślny model", "Model wybierany przy uruchomieniu SuperCli."],
    default_provider: ["Domyślny dostawca", "Dostawca wybierany przy uruchomieniu SuperCli."]
  }
};
function settingCopy(k) {
  var lang = SETTING_COPY[ui.lang] || SETTING_COPY.en;
  return lang[k.key] || SETTING_COPY.en[k.key] || [k.label || k.key, k.desc || ""];
}
function applyI18n() {
  $$("[data-i18n]").forEach(function (n) { n.textContent = t(n.dataset.i18n); });
  $$("[data-i18n-ph]").forEach(function (n) { n.placeholder = t(n.dataset.i18nPh); });
  $$("[data-i18n-title]").forEach(function (n) { n.title = t(n.dataset.i18nTitle); });
  $$("[data-i18n-aria]").forEach(function (n) { n.setAttribute("aria-label", t(n.dataset.i18nAria)); });
}

/* ═══ UI settings (server-persisted blob) ═══ */

var ui = {
  theme: "dark", lang: /^pl(?:-|_|$)/i.test((navigator.languages && navigator.languages[0]) || navigator.language || "") ? "pl" : "en", uiFont: "system", codeFont: "system", uiScale: "auto",
  notifySound: false, notifyDesktop: false, appBadge: true, sidebarHidden: true, rememberSessionRuntime: true,
  keybinds: { panel: "Ctrl+,", sidebar: "Ctrl+B", focus: "/", thinking: "Shift+T", tools: "Shift+E" },
};
var uiBlob = {}; // last blob seen from the server (read-only mirror)
var sidebarCompactMQ = window.matchMedia("(max-width: 1180px)");

// Real, commonly-installed font choices. Every option must visibly
// change rendering on a stock Windows box — a knob that does nothing
// breaks the "it just works" contract.
var UI_FONTS = {
  system: '-apple-system, "Segoe UI", system-ui, sans-serif',
  segoe: '"Segoe UI", system-ui, sans-serif',
  arial: 'Arial, Helvetica, sans-serif',
  tahoma: 'Tahoma, Geneva, sans-serif',
  georgia: 'Georgia, "Times New Roman", serif',
  aptos: 'Aptos, Calibri, "Segoe UI", sans-serif',
  verdana: 'Verdana, Geneva, sans-serif',
  trebuchet: '"Trebuchet MS", Arial, sans-serif',
};
var CODE_FONTS = {
  system: 'ui-monospace, "Cascadia Mono", Consolas, monospace',
  cascadia: '"Cascadia Mono", "Cascadia Code", Consolas, monospace',
  consolas: 'Consolas, "Lucida Console", monospace',
  courier: '"Courier New", Courier, monospace',
  lucida: '"Lucida Console", Consolas, monospace',
  jetbrains: '"JetBrains Mono", "Cascadia Mono", Consolas, monospace',
  fira: '"Fira Code", "Cascadia Mono", Consolas, monospace',
};

var pushTimer = null;
// saveUI persists ONLY the keys this client owns (ui.*). The server
// merges them into the existing blob, so server-side keys (last model,
// model cache) can never be wiped by a stale browser state.
function saveUI() {
  var patch = {};
  Object.keys(ui).forEach(function (k) { patch["ui." + k] = ui[k]; });
  clearTimeout(pushTimer);
  pushTimer = setTimeout(function () {
    jpost("/api/settings", patch).catch(function () {});
  }, 300);
  try { localStorage.setItem("supercli-ui", JSON.stringify(ui)); } catch (e) {}
}
function saveBlobKey(key, value) {
  var patch = {};
  patch[key] = value;
  jpost("/api/settings", patch).catch(function () {});
}
function applyUI() {
  document.documentElement.dataset.theme = ui.theme || "dark";
  document.documentElement.style.setProperty("--sans", UI_FONTS[ui.uiFont] || UI_FONTS.system);
  document.documentElement.style.setProperty("--mono", CODE_FONTS[ui.codeFont] || CODE_FONTS.system);
  var scale = ({compact:.9, normal:1, large:1.1, xlarge:1.25, huge:1.4})[ui.uiScale];
  if (ui.uiScale === "auto" || !scale) {
    scale = 1;
    if (window.innerWidth >= 3000 && window.innerHeight >= 1500) scale = 1.2;
    else if (window.innerWidth >= 2300 && window.innerHeight >= 1200) scale = 1.12;
    else if (window.innerWidth >= 1800 && window.innerHeight >= 950) scale = 1.06;
  }
  document.documentElement.style.zoom = scale;
  $("#shell").classList.toggle("sidebar-hidden", !!ui.sidebarHidden);
  applyI18n();
  if (typeof promptQueue !== "undefined") renderPromptQueue();
}
window.addEventListener("resize", function () {
  if (ui.uiScale === "auto") applyUI();
});
async function loadUI() {
  try { Object.assign(ui, JSON.parse(localStorage.getItem("supercli-ui") || "{}")); } catch (e) {}
  applyUI();
  try {
    var got = await j("/api/settings");
    if (got && got.settings) {
      uiBlob = got.settings;
      Object.keys(ui).forEach(function (k) {
        if (uiBlob["ui." + k] !== undefined) ui[k] = uiBlob["ui." + k];
      });
      if (Array.isArray(uiBlob["supercli-model-cache"])) modelCache = uiBlob["supercli-model-cache"];
    }
  } catch (e) {}
  // A persisted desktop-open inspector must not cover the conversation when
  // the app is reopened on a smaller monitor or in a narrow window.
  if (sidebarCompactMQ.matches) ui.sidebarHidden = true;
  applyUI();
}

/* ═══ markdown-ish renderer (safe: escape first, then decorate) ═══ */

function mdInline(s) {
  s = s.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/__(.+?)__/g, "<strong>$1</strong>");
  s = s.replace(/(^|\s)\*([^*\s][^*]*?)\*(\s|$)/g, "$1<em>$2</em>$3");
  s = s.replace(/(^|\s)_([^_\s][^_]*?)_(\s|$)/g, "$1<em>$2</em>$3");
  s = s.replace(/`([^`]+)`/g, "<code>$1</code>");
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
  s = s.replace(/~~(.+?)~~/g, "<del>$1</del>");
  return s;
}

function renderMarkdownish(text) {
  var parts = String(text || "").split(/```/);
  var html = "";
  for (var i = 0; i < parts.length; i++) {
    if (i % 2 === 1) {
      var lang = "", code = parts[i], nl = code.indexOf("\n");
      if (nl > 0) { lang = code.slice(0, nl).trim(); code = code.slice(nl + 1); }
      html += '<pre data-lang="' + escAttr(lang) + '"><code>' + escHtml(code.replace(/\s+$/, "")) + "</code></pre>";
    } else {
      // Some providers emit empty HTML comment markers while transitioning
      // between reasoning and visible text. They carry no content and should
      // not appear as literal "<!-- -->" paragraphs. Fenced code is handled
      // by the branch above and remains byte-for-byte visible.
      html += mdBlocks(parts[i].replace(/^[ \t]*<!--\s*-->\s*$/gm, ""));
    }
  }
  return html;
}

function mdBlocks(text) {
  var lines = text.split("\n");
  var out = "", i = 0, inList = false, listType = "";
  function closeList() { if (inList) { out += "</" + listType + ">"; inList = false; listType = ""; } }
  while (i < lines.length) {
    var trimmed = lines[i].trim();
    i++;
    if (trimmed === "") { closeList(); continue; }
    if (/^(-{3,}|\*{3,}|_{3,})\s*$/.test(trimmed)) { closeList(); out += "<hr>"; continue; }
    var h = trimmed.match(/^(#{1,6})\s+(.+)/);
    if (h) { closeList(); out += "<h" + h[1].length + ">" + mdInline(escHtml(h[2])) + "</h" + h[1].length + ">"; continue; }
    if (trimmed.indexOf("> ") === 0 || trimmed === ">") {
      closeList();
      var bq = [trimmed.replace(/^>\s?/, "")];
      while (i < lines.length && lines[i].trim().indexOf(">") === 0) { bq.push(lines[i].trim().replace(/^>\s?/, "")); i++; }
      out += "<blockquote>" + bq.map(function (l) { return mdInline(escHtml(l)); }).join("<br>") + "</blockquote>";
      continue;
    }
    var ul = trimmed.match(/^[-*+]\s+(.+)/);
    if (ul) {
      if (!inList || listType !== "ul") { closeList(); out += "<ul>"; inList = true; listType = "ul"; }
      out += "<li>" + mdInline(escHtml(ul[1])) + "</li>";
      continue;
    }
    var ol = trimmed.match(/^\d+[.)]\s+(.+)/);
    if (ol) {
      if (!inList || listType !== "ol") { closeList(); out += "<ol>"; inList = true; listType = "ol"; }
      out += "<li>" + mdInline(escHtml(ol[1])) + "</li>";
      continue;
    }
    if (trimmed.indexOf("|") >= 0) {
      var tbl = [trimmed];
      while (i < lines.length && lines[i].trim().indexOf("|") >= 0) { tbl.push(lines[i].trim()); i++; }
      var sep = tbl.length >= 2 && /^\|[\s\-:|]+\|$/.test(tbl[1]);
      if (sep) { closeList(); out += renderTable(tbl); continue; }
      for (var tj = tbl.length - 1; tj >= 1; tj--) lines.splice(i - (tbl.length - tj), 0, tbl[tj]);
      i -= tbl.length - 1;
    }
    closeList();
    var para = [trimmed];
    while (i < lines.length && lines[i].trim() !== "" &&
      !/^(#{1,6}\s|>\s|[-*+]\s|\d+[.)]\s|-{3,}|\|)/.test(lines[i].trim())) {
      para.push(lines[i].trim());
      i++;
    }
    out += "<p>" + mdInline(escHtml(para.join(" "))) + "</p>";
  }
  closeList();
  return out;
}

function renderTable(lines) {
  function cells(line) {
    return line.replace(/^\||\|$/g, "").split("|").map(function (c) { return c.trim(); });
  }
  var head = cells(lines[0]), align = [];
  cells(lines[1]).forEach(function (c) {
    if (/^:.*:$/.test(c)) align.push("center");
    else if (/:$/.test(c)) align.push("right");
    else align.push("left");
  });
  var html = '<div class="md-table-wrap"><table><thead><tr>';
  head.forEach(function (c, idx) {
    html += '<th style="text-align:' + (align[idx] || "left") + '">' + mdInline(escHtml(c)) + "</th>";
  });
  html += "</tr></thead><tbody>";
  for (var r = 2; r < lines.length; r++) {
    var row = cells(lines[r]);
    html += "<tr>";
    for (var c = 0; c < head.length; c++) {
      html += '<td style="text-align:' + (align[c] || "left") + '">' + mdInline(escHtml(row[c] || "")) + "</td>";
    }
    html += "</tr>";
  }
  return html + "</tbody></table></div>";
}

// renderText splits <thinking>/<think> reasoning into quiet blocks.
// Thinking is OPEN by default (it has value — project principle);
// the user can fold a block and the fold survives re-renders.
var _thinkId = 0;
function renderThinkBlock(text) {
  var id = "think-" + (++_thinkId);
  return '<details class="think-block" open data-think-id="' + id + '">' +
    '<summary><span>' + escHtml(t("role.thinking")) + '</span><span class="think-line"></span></summary>' +
    '<div class="think-content">' + renderMarkdownish(String(text).trim()) + "</div></details>";
}
function renderText(text) {
  _thinkId = 0;
  var src = String(text || ""), html = "", outside = 0, inside = 0, depth = 0, thought = "", m;
  var renderedThinking = false;
  // Local servers are inconsistent: some use <think>, others <thinking>,
  // and a few emit a second opening marker or an orphan closing marker when
  // native reasoning_content switches back to visible content. A depth-aware
  // parser keeps nested/split streams renderable and never shows protocol tags
  // as assistant prose.
  var tags = /<\/?(?:thinking|think|reasoning|reflection)>/gi;
  while ((m = tags.exec(src)) !== null) {
    var closing = m[0].charAt(1) === "/";
    if (depth === 0) {
      if (m.index > outside) html += renderMarkdownish(src.slice(outside, m.index));
      if (closing) {
        // Orphan close: provider/model both closed the same native channel.
        outside = tags.lastIndex;
        continue;
      }
      depth = 1;
      thought = "";
      inside = tags.lastIndex;
      outside = tags.lastIndex;
      continue;
    }
    thought += src.slice(inside, m.index);
    if (closing) depth--;
    else depth++;
    inside = tags.lastIndex;
    if (depth === 0) {
      if (thought.trim()) {
        // One assistant segment has one reasoning phase. Some local servers
        // incorrectly open the native channel again around the final answer;
        // keep the first block as reasoning and recover later blocks as prose.
        html += renderedThinking ? renderMarkdownish(thought) : renderThinkBlock(thought);
        renderedThinking = true;
      }
      thought = "";
      outside = tags.lastIndex;
    }
  }
  if (depth > 0) {
    thought += src.slice(inside);
    if (thought.trim()) html += renderedThinking ? renderMarkdownish(thought) : renderThinkBlock(thought);
  } else if (outside < src.length) {
    html += renderMarkdownish(src.slice(outside));
  }
  return html;
}

/* ═══ transcript ═══ */

var stage = $("#stage"), stream = $("#stream"), welcome = $("#welcome");
var streamAppendTarget = null;

function appendStream(node) {
  (streamAppendTarget || stream).appendChild(node);
}

function nearBottom() { return stage.scrollHeight - stage.scrollTop - stage.clientHeight < 120; }
function smartScroll(force) {
  if (streamAppendTarget) return;
  if (force || nearBottom()) stage.scrollTop = stage.scrollHeight;
}
function hideWelcome() { if (welcome) welcome.style.display = "none"; }
function showWelcome() { if (welcome) welcome.style.display = ""; }

function addUserMsg(text, seq) {
  hideWelcome();
  var m = el("div", "msg-user", text);
  if (seq) addMessageRewind(m, seq, text);
  appendStream(m);
  smartScroll(true);
  return m;
}

function addMessageRewind(node, seq, text) {
  if (!node || !seq || node.querySelector(".msg-rewind")) return;
  var button = el("button", "msg-rewind", "↶ " + t("workflow.rewind"));
  button.type = "button";
  button.title = t("workflow.rewindHint");
  button.setAttribute("aria-label", t("workflow.rewind"));
  button.addEventListener("click", function (event) {
    event.preventDefault();
    event.stopPropagation();
    showRewindDialog(activeSessionID, seq, text, button);
  });
  node.appendChild(button);
}

async function showRewindDialog(sessionID, seq, text, trigger) {
  if (trigger) trigger.disabled = true;
  var preview = null;
  try {
    preview = await j("/api/checkpoint/rewind?session=" + encodeURIComponent(sessionID) + "&from_seq=" + seq);
  } catch (e) {}
  if (trigger) trigger.disabled = false;
  var overlay = el("div", "question-overlay rewind-dialog");
  var panel = el("div", "question-panel compact");
  panel.appendChild(el("div", "question-kicker", t("workflow.rewindWhy")));
  panel.appendChild(el("div", "question-option-desc", t("workflow.rewindWhyHint")));
  var input = el("textarea", "question-custom");
  input.rows = 3;
  input.maxLength = 400;
  input.placeholder = t("workflow.rewindReason");
  panel.appendChild(input);
  var rewindFiles = null;
  if (preview && preview.available && (preview.files || []).length) {
    var fileOption = el("label", "question-option rewind-files");
    rewindFiles = document.createElement("input");
    rewindFiles.type = "checkbox";
    fileOption.appendChild(rewindFiles);
    var fileCopy = el("span", "question-option-copy");
    fileCopy.appendChild(el("strong", "", t("workflow.rewindFiles")));
    fileCopy.appendChild(el("span", "question-option-desc",
      preview.checkpoints + " " + t("workflow.rewindFilesHint").replace("file(s)", (preview.files || []).length + " file(s)").replace("plików", (preview.files || []).length + " plików")));
    fileOption.appendChild(fileCopy);
    panel.appendChild(fileOption);
  }
  var actions = el("div", "question-actions");
  var cancel = el("button", "btn", t("common.cancel"));
  var confirm = el("button", "btn primary", t("workflow.rewindContinue"));
  cancel.type = confirm.type = "button";
  function close() { overlay.remove(); }
  cancel.addEventListener("click", close);
  confirm.addEventListener("click", async function () {
    confirm.disabled = true;
    cancel.disabled = true;
    var ok = await rewindSession(sessionID, seq, text, input.value.trim(), !!(rewindFiles && rewindFiles.checked), trigger);
    if (ok) close();
    else { confirm.disabled = false; cancel.disabled = false; }
  });
  actions.appendChild(cancel);
  actions.appendChild(confirm);
  panel.appendChild(actions);
  overlay.appendChild(panel);
  document.body.appendChild(overlay);
  input.focus();
}

async function addLatestMessageRewind(node, text) {
  if (!node || !node.isConnected || !activeSessionID) return;
  var sessionID = activeSessionID;
  try {
    var page = await j("/api/transcript?id=" + encodeURIComponent(sessionID) + "&limit=24");
    if (sessionID !== activeSessionID || !node.isConnected) return;
    var messages = page.messages || [];
    for (var i = messages.length - 1; i >= 0; i--) {
      if (messages[i].role === "user" && messages[i].content === text) {
        addMessageRewind(node, messages[i].seq, text);
        return;
      }
    }
  } catch (e) {}
}
function addAssistantMsg() {
  var m = el("div", "msg-assistant");
  m._raw = "";
  m._renderTimer = null;
  appendStream(m);
  return m;
}
function renderAssistant(node) {
  // Thinking is open by default; preserve the blocks the user FOLDED
  // across streaming re-renders (ids are positional and stable).
  var closed = {};
  node.querySelectorAll("details[data-think-id]:not([open])").forEach(function (d) {
    closed[d.dataset.thinkId] = true;
  });
  node.innerHTML = renderText(node._raw);
  node.querySelectorAll("details[data-think-id]").forEach(function (d) {
    if (closed[d.dataset.thinkId]) d.open = false;
  });
}

// Providers often send a token in each SSE frame. Rebuilding all accumulated
// Markdown for every token makes long answers progressively more expensive and
// can freeze the WebView UI. Batch chunks and render at an adaptive cadence.
function scheduleAssistantRender(node) {
  if (!node || node._renderTimer !== null) return;
  var n = node._raw.length;
  var delay = n > 32000 ? 200 : (n > 8000 ? 100 : 40);
  node._renderTimer = setTimeout(function () {
    node._renderTimer = null;
    renderAssistant(node);
    smartScroll();
  }, delay);
}

function flushAssistantRender(node) {
  if (!node) return;
  if (node._renderTimer !== null) {
    clearTimeout(node._renderTimer);
    node._renderTimer = null;
  }
  renderAssistant(node);
  smartScroll();
}
function addEventLine(text, cls, tag) {
  var line = el("div", "event-line" + (cls ? " " + cls : ""));
  if (tag) {
    var tg = el("span", "tag", "[" + tag + "] ");
    line.appendChild(tg);
  }
  line.appendChild(document.createTextNode(text));
  appendStream(line);
  smartScroll();
  return line;
}

// noticeTag classifies backend notice text into a quiet [tag].
function noticeTag(text) {
  if (/^pruned /.test(text)) return "prune";
  if (/^preflight:/.test(text)) return "preflight";
  if (/^draft-verify:/.test(text)) return "draft";
  if (/^task:/.test(text)) return "task";
  if (/^hid \d+ old/.test(text)) return "context";
  if (/^consult:/.test(text)) return "consult";
  return "note";
}

/* Tool rows */
var toolRows = {}; // provider tool-call id -> row
var workerRows = {}; // worker task id -> delegated-task row
var openToolOrder = []; // ids without results yet

function toolDisplayName(name) {
  var key = "tool." + name;
  var translated = t(key);
  return translated === key ? name : translated;
}

function commandPreview(command) {
  if (!Array.isArray(command)) command = [command];
  return "$ " + command.filter(function (part) { return part != null && String(part) !== ""; }).map(function (part) {
    part = String(part);
    return /^[\w@%+=:,./\\-]+$/.test(part) ? part : JSON.stringify(part);
  }).join(" ");
}

function toolHint(name, args) {
  try {
    var a = JSON.parse(args || "{}");
    if (name === "task") {
      var kind = a.agent || "general";
      return {
        name: t("task.delegation") + " · " + kind + (a.advise ? " (advise)" : ""),
        hint: clip(a.prompt || "", 90), agent: kind, prompt: a.prompt || "",
      };
    }
    var display = toolDisplayName(name);
    if (a.command) {
      var action = name === "process_session" && a.action ? String(a.action) + " · " : "";
      return { name: display, hint: clip(action + commandPreview(a.command), 150) };
    }
    if (a.cmd) return { name: display, hint: clip(commandPreview(a.cmd), 150) };
    var keys = ["path", "file", "dir", "query", "pattern", "prompt", "text"];
    for (var i = 0; i < keys.length; i++) {
      if (a[keys[i]]) {
        var detail = String(a[keys[i]]);
        if (a.line) detail += " · " + a.line;
        else if (a.from) detail += " · " + a.from + (a.to ? "–" + a.to : "");
        return { name: display, hint: clip(detail, 150) };
      }
    }
    var flat = Object.keys(a).map(function (k) { return k + "=" + clip(JSON.stringify(a[k]), 30); }).join(" ");
    return { name: display, hint: clip(flat, 150) };
  } catch (e) {
    return { name: toolDisplayName(name), hint: clip(args || "", 150) };
  }
}

var DIFF_TOOLS = {
  edit_line: true, edit_lines: true, insert_after: true, delete_lines: true,
  apply_patch: true, patch: true,
};

var FILE_READ_TOOLS = {
  read_lines: true, read_context: true, read_many: true,
};

function appendFileReadPayload(body, text) {
  var viewer = el("div", "tool-file-view");
  String(text == null ? "" : text).split(/\r?\n/).forEach(function (line) {
    var numbered = line.match(/^\s*(\d+)\s*\|\s?(.*)$/);
    if (numbered) {
      var row = el("div", "file-code-line");
      row.appendChild(el("span", "file-line-no", numbered[1]));
      row.appendChild(el("code", "file-line-code", numbered[2] || " "));
      viewer.appendChild(row);
      return;
    }
    var section = line.match(/^==\s*(.*?)\s*==$/);
    if (section) {
      viewer.appendChild(el("div", "file-code-section", section[1]));
      return;
    }
    if (/^\[read_many:/.test(line)) {
      viewer.appendChild(el("div", "file-code-summary", line));
      return;
    }
    if (line || viewer.childNodes.length === 0) {
      viewer.appendChild(el("div", /^error:/i.test(line) ? "file-code-note error" : "file-code-note", line || " "));
    }
  });
  body.appendChild(viewer);
}

function toolChangeStats(name, text) {
  if (!DIFF_TOOLS[name]) return { added: 0, removed: 0, diff: false };
  var stats = { added: 0, removed: 0, diff: false };
  String(text || "").split(/\r?\n/).forEach(function (line) {
    if (/^\+(?!\+\+)/.test(line)) { stats.added++; stats.diff = true; }
    else if (/^-(?!---)/.test(line)) { stats.removed++; stats.diff = true; }
  });
  return stats;
}

function appendToolPayload(body, label, text, name, isError) {
  var raw = String(text == null ? "" : text);
  if (FILE_READ_TOOLS[name] && !isError) {
    appendFileReadPayload(body, raw);
    return;
  }
  body.appendChild(el("div", "lbl", label));
  if (name === "ctx_execute" && !isError) {
    try {
      var execution = JSON.parse(raw);
      if (execution && Object.prototype.hasOwnProperty.call(execution, "exit_code")) {
        var meta = el("div", "tool-exec-meta");
        meta.appendChild(el("span", execution.exit_code === 0 ? "ok" : "err", "exit " + execution.exit_code));
        if (execution.duration_ms != null) meta.appendChild(el("span", "", fmtDuration(Number(execution.duration_ms))));
        if (execution.workdir) meta.appendChild(el("span", "", execution.workdir));
        body.appendChild(meta);
        if (execution.stdout) appendToolPayload(body, t("tool.stdout"), execution.stdout, "", false);
        if (execution.stderr) appendToolPayload(body, t("tool.stderr"), execution.stderr, "", true);
        if (!execution.stdout && !execution.stderr) body.appendChild(el("pre", "tool-output", "(empty)"));
        return;
      }
    } catch (e) {}
  }
  var changes = toolChangeStats(name, raw);
  if (!changes.diff) {
    body.appendChild(el("pre", "tool-output" + (isError ? " error" : ""), raw || "(empty)"));
    return;
  }
  var diff = el("div", "tool-diff");
  raw.split(/\r?\n/).forEach(function (line) {
    var cls = "diff-line";
    if (/^\+(?!\+\+)/.test(line)) cls += " added";
    else if (/^-(?!---)/.test(line)) cls += " removed";
    else cls += " context";
    diff.appendChild(el("div", cls, line || " "));
  });
  body.appendChild(diff);
}

function setToolResultStatus(row, elapsed, err, changes) {
  row._stat.innerHTML = "";
  if (changes && changes.added) row._stat.appendChild(el("span", "change-add", "+" + changes.added));
  if (changes && changes.removed) row._stat.appendChild(el("span", "change-remove", "−" + changes.removed));
  row._stat.appendChild(el("span", err ? "status-error" : "", (err ? "× · " : "") + fmtDuration(elapsed)));
}

// task results use a compact XML envelope for the model. Keep that protocol
// out of the UI: extract its stable fields and render the report as content.
function parseTaskNotification(text) {
  var src = String(text || "");
  if (src.indexOf("<task-notification>") < 0) return null;
  function field(name) {
    var open = "<" + name + ">", close = "</" + name + ">";
    var from = src.indexOf(open);
    var to = name === "result" ? src.lastIndexOf(close) : src.indexOf(close, from + open.length);
    if (from < 0 || to < from) return "";
    return src.slice(from + open.length, to).trim();
  }
  return {
    id: field("task-id"), agent: field("agent") || "worker",
    status: field("status") || "done", summary: field("summary"),
    tools: field("tools"), result: field("result"),
  };
}

function taskStatusLabel(status) {
  if (status === "running") return t("tool.running");
  if (status === "failed") return t("task.failed");
  if (status === "stopped") return t("task.stopped");
  return t("task.done");
}

function taskMetrics(note) {
  var summary = String(note.summary || "");
  var parts = [], steps = summary.match(/(\d+)\s+steps?/i);
  var tokens = summary.match(/(\d+)\s+in\/(\d+)\s+out\s+tok/i);
  var model = summary.match(/(?:^|\s·\s)model=([^·]+)/i);
  if (steps) {
    var n = Number(steps[1]);
    parts.push(fmtInteger(n) + " " + t(n === 1 ? "task.step" : "task.steps"));
  }
  if (tokens) {
    parts.push(fmtCompactNumber(Number(tokens[1])) + " " + t("task.input"));
    parts.push(fmtCompactNumber(Number(tokens[2])) + " " + t("task.output"));
  }
  if (model) parts.push(model[1].trim());
  var agent = String(note.agent || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  var status = String(note.status || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return parts.join(" · ") || summary.replace(new RegExp("^" + agent + "\\s+" + status + "\\s*[·-]?\\s*", "i"), "");
}

function renderTaskResult(row, note, elapsed, prompt, err) {
  var activity = row._activity;
  row.classList.add("task-row");
  row.classList.remove("running", "done", "failed");
  row.classList.add(err || note.status === "failed" ? "failed" : (note.status === "running" ? "running" : "done"));
  row._tname.textContent = t("task.delegation") + " · " + note.agent;
  row._thint.textContent = taskMetrics(note);
  row._stat.textContent = taskStatusLabel(err ? "failed" : note.status) + (elapsed != null ? " · " + fmtDuration(elapsed) : "");
  row._stat.classList.toggle("err", !!err || note.status === "failed");
  row._body.innerHTML = "";
  if (prompt) {
    row._body.appendChild(el("div", "lbl", t("task.brief")));
    row._body.appendChild(el("div", "task-brief", prompt));
  }
  if (activity && activity.childNodes.length) {
    row._body.appendChild(el("div", "lbl", t("task.activity")));
    row._body.appendChild(activity);
  } else if (note.tools) {
    row._body.appendChild(el("div", "lbl", t("task.activity")));
    activity = el("div", "task-activity");
    String(note.tools).split(/,\s*/).filter(Boolean).forEach(function (name) {
      var item = el("div", "task-activity-item done");
      item.appendChild(el("span", "activity-dot"));
      item.appendChild(el("span", "activity-name", name));
      item.appendChild(el("span", "activity-hint", ""));
      item.appendChild(el("span", "activity-status", t("task.done")));
      activity.appendChild(item);
    });
    row._activity = activity;
    row._body.appendChild(activity);
  }
  if (note.result) {
    row._body.appendChild(el("div", "lbl", t("task.report")));
    var report = el("div", "task-report msg-assistant");
    report.innerHTML = renderText(note.result);
    row._body.appendChild(report);
  }
}

function addHistoryTask(note) {
  var row = document.createElement("details");
  row.className = "tool-row task-row done";
  var sum = el("summary");
  row._tname = el("span", "tname");
  row._thint = el("span", "thint");
  row._stat = el("span", "tstat");
  sum.appendChild(row._tname); sum.appendChild(row._thint); sum.appendChild(row._stat);
  row.appendChild(sum);
  row._body = el("div", "tbody");
  row.appendChild(row._body);
  renderTaskResult(row, note, null, "", note.status === "failed");
  row.querySelectorAll("details[data-think-id]").forEach(function (d) { d.open = false; });
  appendStream(row);
  return row;
}
function addToolCall(name, args, id) {
  var info = toolHint(name, args);
  var row = document.createElement("details");
  row.className = "tool-row";
  var sum = el("summary");
  var title = el("span", "tname", info.name);
  title.title = name;
  var hint = el("span", "thint", info.hint);
  sum.appendChild(title);
  sum.appendChild(hint);
  var stat = el("span", "tstat");
  sum.appendChild(stat);
  row.appendChild(sum);
  var body = el("div", "tbody");
  body.hidden = true;
  if (!FILE_READ_TOOLS[name]) {
    var lblA = el("div", "lbl", t("tool.input"));
    body.appendChild(lblA);
    body.appendChild(el("pre", "", prettyJSON(args)));
  }
  row.appendChild(body);
  row.addEventListener("toggle", function () { body.hidden = !row.open; });
  row._stat = stat; row._body = body; row._tname = title; row._thint = hint;
  row._toolName = name; row._toolArgs = args || "{}"; row._taskAgent = info.agent || ""; row._taskPrompt = info.prompt || "";
  row._t0 = performance.now();
  if (name === "task") {
    row.classList.add("task-row");
    body.innerHTML = "";
    body.appendChild(el("div", "lbl", t("task.brief")));
    body.appendChild(el("div", "task-brief", info.prompt || ""));
    body.appendChild(el("div", "lbl", t("task.activity")));
    row._activity = el("div", "task-activity");
    row._workerCalls = {};
    body.appendChild(row._activity);
  }
  row.classList.add("running");
  function updateElapsed() {
    stat.textContent = t("tool.running") + " · " + fmtDuration(performance.now() - row._t0);
  }
  updateElapsed();
  row._clock = setInterval(updateElapsed, 250);
  appendStream(row);
  var key = id || ("~" + openToolOrder.length);
  toolRows[key] = row;
  openToolOrder.push(key);
  runToolCount++;
  smartScroll();
}
function addToolResult(id, output, err) {
  var key = id && toolRows[id] ? id : openToolOrder[0];
  var row = toolRows[key];
  openToolOrder = openToolOrder.filter(function (k) { return k !== key; });
  if (!row) return;
  clearInterval(row._clock);
  row._clock = null;
  row.classList.remove("running");
  row.classList.add(err ? "failed" : "done");
  var ms = performance.now() - row._t0;
  var task = row._toolName === "task" ? parseTaskNotification(output || err) : null;
  if (task) {
    if (task.id) workerRows[task.id] = row;
    renderTaskResult(row, task, ms, row._taskPrompt, err);
    smartScroll();
    return;
  }
  var payload = err || output || "";
  var changes = toolChangeStats(row._toolName, payload);
  if (changes.diff) row.classList.add("has-changes");
  if (err) row._stat.classList.add("err");
  setToolResultStatus(row, ms, err, changes);
  appendToolPayload(row._body, err ? t("tool.error") : t("tool.output"), payload, row._toolName, !!err);
  smartScroll();
}
function settleOpenTools() {
  openToolOrder.forEach(function (k) {
    var row = toolRows[k];
    if (row) {
      clearInterval(row._clock);
      row._clock = null;
      row.classList.remove("running");
      row._stat.textContent = "—";
    }
  });
  openToolOrder = [];
}
function prettyJSON(s) {
  try { return JSON.stringify(JSON.parse(s), null, 2); } catch (e) { return s || ""; }
}

function findTaskRow(taskID, agentName) {
  if (taskID && workerRows[taskID]) return workerRows[taskID];
  for (var i = openToolOrder.length - 1; i >= 0; i--) {
    var candidate = toolRows[openToolOrder[i]];
    if (candidate && candidate._toolName === "task" &&
      (!agentName || candidate._taskAgent === agentName)) return candidate;
  }
  return null;
}

function addWorkerProgress(ev) {
  var row = findTaskRow(ev.id, ev.name);
  if (!row) return false;
  if (ev.id) workerRows[ev.id] = row;
  if (!row._activity) row._activity = el("div", "task-activity");
  if (!row._workerCalls) row._workerCalls = {};
  var item = ev.call_id ? row._workerCalls[ev.call_id] : null;
  if (ev.kind === "tool_call") {
    var info = toolHint(ev.tool || "tool", ev.args || "{}");
    item = el("div", "task-activity-item running");
    item.appendChild(el("span", "activity-dot"));
    item.appendChild(el("span", "activity-name", ev.tool || "tool"));
    item.appendChild(el("span", "activity-hint", info.hint || ""));
    item.appendChild(el("span", "activity-status", t("tool.running")));
    row._activity.appendChild(item);
    if (ev.call_id) row._workerCalls[ev.call_id] = item;
  } else if (ev.kind === "tool_result") {
    if (!item) {
      item = el("div", "task-activity-item");
      item.appendChild(el("span", "activity-dot"));
      item.appendChild(el("span", "activity-name", ev.tool || "tool"));
      item.appendChild(el("span", "activity-hint", ""));
      item.appendChild(el("span", "activity-status"));
      row._activity.appendChild(item);
    }
    item.classList.remove("running");
    item.classList.add(ev.err ? "failed" : "done");
    item.querySelector(".activity-status").textContent = ev.err ? t("task.failed") : t("task.done");
    if (ev.err || ev.output) item.title = ev.err || ev.output;
  }
  if (!row.open) row.open = true;
  row._body.hidden = false;
  smartScroll();
  return true;
}

// Telemetry line: time · cache/eval/gen · cached% · think · tools
function addTurnMeta(ev, elapsed, toolCount, seq) {
	if (toolCount == null) toolCount = runToolCount;
  var parts = [fmtDuration(elapsed)];
  var evalTok = (ev.tok_in || 0) - (ev.tok_cached || 0);
  if (ev.tok_cached) {
    parts.push("cache " + fmtTok(ev.tok_cached) + " · eval " + fmtTok(evalTok) + " · gen " + fmtTok(ev.tok_out));
  } else if (ev.tok_total) {
    parts.push("in " + fmtTok(ev.tok_in) + " · gen " + fmtTok(ev.tok_out));
  }
  if (ev.cache_hit_pct) parts.push(ev.cache_hit_pct + "% " + t("run.cached"));
  if (ev.reasoning_tok) parts.push(t("run.think") + " " + fmtTok(ev.reasoning_tok));
	if (toolCount) parts.push(toolCount + " " + t("run.tools"));
  var line = el("div", "turn-meta");
	line.innerHTML = parts.map(function (p, i) { return i === 0 ? "<b>" + escHtml(p) + "</b>" : escHtml(p); }).join(" · ");
	if (seq) {
	  var fork = el("button", "turn-undo", t("workflow.fork")); fork.type="button";
	  fork.addEventListener("click", function(){ forkSession(activeSessionID, seq); }); line.appendChild(fork);
	}
	appendStream(line);
  smartScroll();
}

/* ═══ chat streaming ═══ */

var streaming = false, abortCtl = null, activeSessionID = "", projectEpoch = 0;
var sessionRuntimeReady = Promise.resolve();
var activeQuestionOverlay = null;
var runStart = 0, runTimer = null, runToolCount = 0;
var lastTurn = null;    // last done-event payload + elapsed (stats pane)
var workersSeen = [];   // worker notifications this browser session
var promptEl = $("#prompt"), sendBtn = $("#send-btn"), runStatus = $("#run-status");
var promptQueue = [], pendingImmediate = null, pauseQueue = false, unreadDone = 0;
var pendingRewind = null;
var appFocused = !document.hidden && document.hasFocus();

function updateAppBadge() {
  // The taskbar number means "finished while you were away", not queue depth.
  // Pending prompts already have their own visible queue and must not look like
  // unread results after the user returns to the app.
  var count = unreadDone;
  if (!ui.appBadge) count = 0;
  if (navigator.setAppBadge) {
    (count ? navigator.setAppBadge(count) : navigator.clearAppBadge()).catch(function () {});
  }
  if (window.supercliSetBadge) window.supercliSetBadge(count).catch(function () {});
  document.title = count ? "(" + count + ") SuperCli" : "SuperCli";
}
function renderPromptQueue() {
  var host = $("#prompt-queue");
  host.innerHTML = "";
  host.hidden = !promptQueue.length;
  if (pauseQueue && promptQueue.length) {
    var resume = el("button", "queue-resume", "▶ " + t("composer.resume"));
    resume.type = "button";
    resume.addEventListener("click", function () {
      pauseQueue = false;
      var next = promptQueue[0];
      if (next && !streaming) runQueuedTask(next, false);
    });
    host.appendChild(resume);
  }
  promptQueue.forEach(function (item, index) {
    var row = el("div", "queue-row");
    row.appendChild(el("span", "queue-index", String(index + 1).padStart(2, "0")));
    row.appendChild(el("span", "queue-text", item.text));
    var now = el("button", "queue-action", t("composer.sendNow")); now.type = "button";
    now.addEventListener("click", function () { runQueuedTask(item, true); });
    var remove = el("button", "queue-remove", "×"); remove.type = "button"; remove.title = t("composer.remove");
    remove.addEventListener("click", function () { removeQueuedTask(item.id); });
    row.appendChild(now); row.appendChild(remove); host.appendChild(row);
  });
  updateAppBadge();
  renderTaskCenter();
}
async function enqueuePrompt(text) {
  try {
    var q = await jpost("/api/tasks", { session_id: activeSessionID, prompt: text });
    q.text = q.prompt;
    promptQueue.push(q);
    pauseQueue = false;
    renderPromptQueue();
  } catch (e) { toast(e.message); return; }
  setRunState("running", promptQueue.length + " · " + t("composer.queued"));
}
async function loadPromptQueue() {
  try { promptQueue = await j("/api/tasks") || []; promptQueue.forEach(function (q) { q.text = q.prompt; }); pauseQueue = promptQueue.length > 0; }
  catch (e) { promptQueue = []; }
  renderPromptQueue();
}
async function removeQueuedTask(id) {
  try { await j("/api/tasks?id=" + encodeURIComponent(id), { method: "DELETE" }); promptQueue = promptQueue.filter(function (q) { return q.id !== id; }); renderPromptQueue(); return true; }
  catch (e) { toast(e.message); return false; }
}
async function runQueuedTask(item, interrupt) {
  if (!await removeQueuedTask(item.id)) return;
  pauseQueue = false;
  if (interrupt) interruptAndSend(item.text, item);
  else { await prepareQueuedTask(item); sendPrompt(item.text); }
}
function renderTaskCenter() {
  var host = $("#task-list"); if (!host) return; host.innerHTML = "";
  if (!promptQueue.length) { host.appendChild(el("div", "side-empty", ui.lang === "pl" ? "Brak oczekujących zadań." : "No queued tasks.")); return; }
  promptQueue.forEach(function (item, i) {
    var row = el("div", "task-center-row"); row.appendChild(el("span", "task-center-index", String(i + 1).padStart(2, "0")));
    var copy = el("div", "task-center-copy"); copy.appendChild(el("span", "t", item.text)); copy.appendChild(el("span", "s", item.session_id ? clip(item.session_id, 18) : "new session")); row.appendChild(copy);
    var go = el("button", "queue-action", t("composer.sendNow")); go.addEventListener("click", function () { runQueuedTask(item, true); }); row.appendChild(go); host.appendChild(row);
	if (i > 0) { var up=el("button","queue-action","↑"); up.title="Move up"; up.addEventListener("click",async function(){try{await j("/api/tasks",{method:"PATCH",headers:{"Content-Type":"application/json"},body:JSON.stringify({id:item.id,position:i-1})});await loadPromptQueue();}catch(e){toast(e.message);}}); row.insertBefore(up,go); }
  });
}

var sideGoalSeq = 0;
function renderSideGoal(got) {
  var host = $("#side-goal");
  if (!host) return;
  host.innerHTML = "";
  host.removeAttribute("aria-busy");
  if (!got) {
    var empty = el("div", "side-goal-empty");
    empty.appendChild(el("div", "side-goal-empty-title", t("goal.sideEmpty")));
    empty.appendChild(el("div", "side-goal-empty-copy", t("goal.sideEmptyHint")));
    var create = el("button", "side-goal-manage", t("goal.create"));
    create.type = "button";
    create.addEventListener("click", function () { openPanel("goal"); });
    empty.appendChild(create);
    host.appendChild(empty);
    return;
  }

  var tasks = got.tasks || [];
  var open = tasks.filter(function (task) { return task.status !== "done" && task.status !== "skipped"; });
  var terminal = tasks.length - open.length;
  var intro = el("div", "side-goal-intro");
  var status = el("div", "side-goal-status");
  status.appendChild(el("span", "side-goal-dot"));
  status.appendChild(document.createTextNode(goalStatus(got.status)));
  intro.appendChild(status);
  intro.appendChild(el("div", "side-goal-title", got.title));
  host.appendChild(intro);

  var progressMeta = el("div", "side-goal-progress-meta");
  progressMeta.appendChild(el("span", "", t("goal.progress")));
  progressMeta.appendChild(el("span", "", terminal + " / " + tasks.length));
  host.appendChild(progressMeta);
  var progress = el("div", "side-goal-progress");
  var fill = el("span");
  fill.style.width = (tasks.length ? terminal * 100 / tasks.length : 0) + "%";
  progress.appendChild(fill);
  progress.setAttribute("aria-label", t("goal.progress") + ": " + terminal + " / " + tasks.length);
  host.appendChild(progress);

  if (!open.length) {
	var verificationCopy = t("goal.awaitingVerification");
	var verificationClass = " awaiting";
	if (got.verification_status === "passed") {
	  verificationCopy = t("goal.readyToFinish");
	  verificationClass = " passed";
	} else if (got.verification_status === "failed") {
	  verificationCopy = t("goal.verificationFailed");
	  verificationClass = " failed";
	}
	host.appendChild(el("div", "side-goal-all-done" + verificationClass, verificationCopy));
    return;
  }
  var active = open.filter(function (task) { return task.status === "in_progress"; })[0] || null;
  host.appendChild(el("div", "side-goal-steps-label", active ? t("goal.currentStep") : t("goal.nextSteps")));
  var visible = active ? [active].concat(open.filter(function (task) { return task !== active; })) : open;
  visible.slice(0, 3).forEach(function (task) {
    var row = el("div", "side-goal-step" + (task === active ? " current" : ""));
    row.appendChild(el("span", "side-goal-step-index", String(task.seq).padStart(2, "0")));
    var copy = el("div", "side-goal-step-copy");
    copy.appendChild(el("div", "side-goal-step-title", task.title));
    copy.appendChild(el("div", "side-goal-step-status", goalStatus(task.status)));
    row.appendChild(copy);
    host.appendChild(row);
  });
  if (visible.length > 3) host.appendChild(el("div", "side-goal-more", "+" + (visible.length - 3) + " · " + t("goal.openSteps")));
}
async function loadSideGoal() {
  var host = $("#side-goal");
  if (!host) return;
  var seq = ++sideGoalSeq;
  host.setAttribute("aria-busy", "true");
  try {
    var got = await j("/api/goal");
    if (seq === sideGoalSeq) renderSideGoal(got);
  } catch (e) {
    if (seq !== sideGoalSeq) return;
    host.removeAttribute("aria-busy");
    host.innerHTML = "";
    host.appendChild(el("div", "side-empty", e.message));
  }
}
$("#reload-tasks").addEventListener("click", loadPromptQueue);
$("#manage-side-goal").addEventListener("click", function () { openPanel("goal"); });
async function prepareQueuedTask(item) {
  if (!item || !item.session_id) return;
  activeSessionID = item.session_id;
  if (sessionByID[item.session_id]) await restoreSessionRuntime(sessionByID[item.session_id]);
}
function interruptAndSend(text, item) {
  pendingImmediate = item || { text: text, session_id: activeSessionID };
  pauseQueue = false;
  if (abortCtl) abortCtl.abort(); else { var next=pendingImmediate; pendingImmediate=null; prepareQueuedTask(next).then(function(){sendPrompt(next.text);}); }
}

function setRunState(state, text) {
  runStatus.textContent = text || "";
  $("#status-dot").classList.toggle("busy", state === "running");
}

async function sendPrompt(text) {
  if (streaming) return;
  // A reopened conversation becomes visible before its remembered model has
  // necessarily finished switching. Never let an immediate Enter use the
  // previous session's runtime; if another session wins meanwhile, await it.
  for (;;) {
    var readyRuntime = sessionRuntimeReady;
    await readyRuntime;
    if (readyRuntime === sessionRuntimeReady) break;
  }
  if (streaming) return;
  // Once a live turn starts, the DOM becomes append-only again. Retire the
  // history pager so a late older-page response cannot rebuild the transcript
  // over messages currently arriving from SSE.
  if (transcriptAbortCtl) transcriptAbortCtl.abort();
  transcriptAbortCtl = null;
  transcriptHasMore = false;
  transcriptBeforeSeq = 0;
  loadedTranscriptMessages = [];
  var olderHistory = stream.querySelector(".history-older");
  if (olderHistory) olderHistory.remove();
  streaming = true;
  var rewindInfo = pendingRewind;
  pendingRewind = null;
  abortCtl = new AbortController();
  runToolCount = 0;
  toolRows = {}; workerRows = {}; openToolOrder = [];
  sendBtn.textContent = t("composer.queue");
  sendBtn.classList.add("queue");
  sendBtn.type = "submit";
  $("#stop-run-btn").hidden = false;
  $("#interrupt-btn").hidden = false;
  var liveUserNode = addUserMsg(text);
  var current = null;
  var terminalSeen = false;
  var lastProgressAt = Date.now();
  runStart = Date.now();
  setRunState("running", t("composer.working"));
  runTimer = setInterval(function () {
    var now = Date.now();
    var quiet = now - lastProgressAt;
    runStatus.textContent = (quiet >= 10000 ? t("composer.waiting") + " " + fmtDuration(quiet) :
      t("composer.working") + " " + fmtDuration(now - runStart));
  }, 100);

  try {
    var resp = await fetch("/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        prompt: text,
        session_id: activeSessionID,
        rewound: rewindInfo !== null,
        rewind_reason: rewindInfo ? rewindInfo.reason : "",
        rewind_files: !!(rewindInfo && rewindInfo.files),
        rewind_files_restored: !!(rewindInfo && rewindInfo.filesRestored),
      }),
      signal: abortCtl.signal,
    });
    if (!resp.ok || !resp.body) {
      addEventLine(t("common.error") + ": " + resp.status, "error");
      return;
    }
    var reader = resp.body.getReader();
    var decoder = new TextDecoder();
    var buf = "";
    function processFrame(frame) {
      var line = frame.replace(/^data: /, "").trim();
      if (!line) return;
      try {
        var ev = JSON.parse(line);
        lastProgressAt = Date.now();
        if (ev.type === "done" || ev.type === "error") terminalSeen = true;
        current = handleEvent(ev, current);
      } catch (e) {}
    }
    for (;;) {
      var chunk = await reader.read();
      if (chunk.done) break;
      buf += decoder.decode(chunk.value, { stream: true });
      var frames = buf.split(/\r?\n\r?\n/);
      buf = frames.pop();
      for (var f = 0; f < frames.length; f++) {
        processFrame(frames[f]);
      }
    }
    buf += decoder.decode();
    if (buf.trim()) processFrame(buf);
    flushAssistantRender(current);
    if (!terminalSeen) {
      addEventLine(t("chat.streamEnded"), "error", "error");
      setRunState("idle", t("common.error"));
    }
  } catch (e) {
    if (e.name === "AbortError") {
      addEventLine(t("chat.stopped"), "", "stop");
      setRunState("idle", t("composer.stopped"));
    } else {
      addEventLine(t("chat.connError") + ": " + e.message, "error");
      setRunState("idle", t("chat.connError"));
    }
	} finally {
	  flushAssistantRender(current);
	  closeQuestionOverlay();
	  streaming = false;
    abortCtl = null;
    clearInterval(runTimer);
    settleOpenTools();
    sendBtn.textContent = t("composer.send");
    sendBtn.classList.remove("queue");
    sendBtn.type = "submit";
    $("#stop-run-btn").hidden = true;
    $("#interrupt-btn").hidden = true;
    if (runStatus.textContent.indexOf(t("composer.working")) === 0) setRunState("idle", t("composer.ready"));
    $("#status-dot").classList.remove("busy");
    promptEl.focus();
    loadSessions();
    renderStats();
    addLatestMessageRewind(liveUserNode, text);
    // A model may have updated the durable goal through its tool during this
    // turn. Keep an already-open Goal panel in sync without polling.
    if (!overlay.hidden && currentSection === "goal" && sections.goal) sections.goal();
    if ($("#side-tab-goal").classList.contains("active")) loadSideGoal();
    var immediate = pendingImmediate;
    pendingImmediate = null;
	var next = "";
	if (immediate) { await prepareQueuedTask(immediate); next = immediate.text; }
    if (!next && !pauseQueue && promptQueue.length) {
      var queued = promptQueue[0];
      if (!await removeQueuedTask(queued.id)) queued = null;
	  if (!queued) { renderPromptQueue(); return; }
	  await prepareQueuedTask(queued);
      next = queued.text;
    }
    renderPromptQueue();
    if (next) setTimeout(function () { sendPrompt(next); }, 0);
  }
}

function handleEvent(ev, current) {
  switch (ev.type) {
    case "session":
      if (ev.session_id) activeSessionID = ev.session_id;
      return current;
    case "message":
      if (!current) current = addAssistantMsg();
      current._raw += ev.text;
      scheduleAssistantRender(current);
      return current;
    case "tool_call":
      flushAssistantRender(current);
      addToolCall(ev.name, ev.args, ev.id);
      return null;
    case "tool_result":
      addToolResult(ev.id, ev.output, ev.err);
      return current;
    case "compact":
      addEventLine(ev.text || "", "", "compact");
      return null;
    case "notice":
      addEventLine(ev.text || "", "", noticeTag(ev.text || ""));
      return current;
	case "worker_progress":
      addWorkerProgress(ev);
	  return current;
	case "question":
	  if (ev.question) showQuestion(ev.question);
	  setRunState("running", t("question.waiting"));
	  return current;
    case "worker":
      workersSeen.push({ name: ev.name || "worker", status: ev.status || "", summary: ev.output || "" });
      var task = parseTaskNotification(ev.text) || {
        id: ev.id || "", agent: ev.name || "worker", status: ev.status || "done",
        summary: ev.output || "", result: "",
      };
      var workerRow = findTaskRow(task.id, task.agent);
      if (workerRow) {
        clearInterval(workerRow._clock);
        workerRow._clock = null;
        if (task.id) workerRows[task.id] = workerRow;
        renderTaskResult(workerRow, task, performance.now() - workerRow._t0, workerRow._taskPrompt, task.status === "failed");
        return current;
      }
      addEventLine((ev.name || "worker") + " · " + (ev.status || "") + (ev.output ? " — " + clip(ev.output, 160) : ""), "", "worker");
      return current;
    case "reflection":
      addEventLine(clip(ev.text || "", 120), "", "reflection");
      return current;
    case "sisyphus":
      addEventLine(clip(ev.text || "goal continuation", 120), "", "goal");
      return current;
    case "done":
      flushAssistantRender(current);
      var elapsed = Date.now() - runStart;
      lastTurn = { ev: ev, elapsed: elapsed, tools: runToolCount };
      addTurnMeta(ev, elapsed);
      setRunState("idle", t("run.done") + " · " + fmtDuration(elapsed));
      notifyDone(elapsed);
      return null;
    case "error":
      flushAssistantRender(current);
      addEventLine(ev.err || "error", "error", "error");
      setRunState("idle", t("common.error"));
      return null;
    default:
      return current;
  }
}

function closeQuestionOverlay() {
  if (activeQuestionOverlay) activeQuestionOverlay.remove();
  activeQuestionOverlay = null;
}

function showQuestion(q) {
  closeQuestionOverlay();
  var overlay = el("div", "question-overlay");
  var panel = el("form", "question-panel");
  panel.setAttribute("aria-label", q.header || t("question.title"));
  var head = el("div", "question-head");
  head.appendChild(el("span", "question-kicker", q.header || t("question.title")));
  head.appendChild(el("h2", "question-text", q.question || ""));
  panel.appendChild(head);

  var options = el("div", "question-options" + ((q.options || []).some(function (o) { return o.image; }) ? " visual" : ""));
  (q.options || []).forEach(function (opt, idx) {
    var card = el("label", "question-option");
    var input = document.createElement("input");
    input.type = q.multi_select ? "checkbox" : "radio";
    input.name = "question-choice";
    input.value = opt.label;
    card.appendChild(input);
    if (opt.image) {
      var preview = el("button", "question-image-button");
      preview.type = "button";
      preview.title = t("question.zoom");
      var img = document.createElement("img");
      img.src = opt.image;
      img.alt = opt.label;
      preview.appendChild(img);
      preview.addEventListener("click", function (e) {
        e.preventDefault();
        var light = el("div", "question-lightbox");
        var full = document.createElement("img"); full.src = opt.image; full.alt = opt.label;
        light.appendChild(full);
        light.addEventListener("click", function () { light.remove(); });
        overlay.appendChild(light);
      });
      card.appendChild(preview);
    }
    var copy = el("span", "question-option-copy");
    copy.appendChild(el("strong", "question-option-label", opt.label));
    if (opt.description) copy.appendChild(el("span", "question-option-desc", opt.description));
    if (opt.preview) copy.appendChild(el("span", "question-option-preview", opt.preview));
    if (opt.image_prompt) {
      var details = document.createElement("details"); details.className = "question-prompt";
      var summary = document.createElement("summary"); summary.textContent = t("question.prompt");
      details.appendChild(summary);
      details.appendChild(el("code", "", opt.image_prompt));
      var copyBtn = el("button", "question-copy", t("question.copy")); copyBtn.type = "button";
      copyBtn.addEventListener("click", function (e) {
        e.preventDefault();
        navigator.clipboard.writeText(opt.image_prompt).then(function () { toast(t("question.copied")); });
      });
      details.appendChild(copyBtn); copy.appendChild(details);
    }
    card.appendChild(copy);
    options.appendChild(card);
  });
  panel.appendChild(options);
  var custom = document.createElement("textarea");
  custom.className = "question-custom"; custom.rows = 3; custom.placeholder = t("question.custom");
  if (q.allow_custom !== false) panel.appendChild(custom);
  var error = el("div", "question-error"); panel.appendChild(error);
  var actions = el("div", "question-actions");
  var cancel = el("button", "btn", t("question.cancel")); cancel.type = "button";
  var submit = el("button", "btn primary", t("question.submit")); submit.type = "submit";
  actions.appendChild(cancel); actions.appendChild(submit); panel.appendChild(actions);
  overlay.appendChild(panel); document.body.appendChild(overlay); activeQuestionOverlay = overlay;

  function answer(cancelled) {
    var selected = Array.prototype.slice.call(panel.querySelectorAll('input[name="question-choice"]:checked')).map(function (x) { return x.value; });
    var own = custom.value.trim();
    if (!cancelled && !selected.length && !own) { error.textContent = t("question.pick"); return; }
    submit.disabled = true; cancel.disabled = true;
    jpost("/api/question/answer", { id: q.id, selected: selected, custom: own, cancelled: !!cancelled })
      .then(function () { closeQuestionOverlay(); setRunState("running", t("composer.working")); })
      .catch(function (e) { error.textContent = e.message; submit.disabled = false; cancel.disabled = false; });
  }
  panel.addEventListener("submit", function (e) { e.preventDefault(); answer(false); });
  cancel.addEventListener("click", function () { answer(true); });
  setTimeout(function () { var first = panel.querySelector("input"); if (first) first.focus(); }, 0);
}

$("#composer").addEventListener("submit", function (e) {
  e.preventDefault();
  var text = promptEl.value.trim();
  if (!text) return;
  promptEl.value = "";
  promptEl.style.height = "auto";
  if (streaming) { enqueuePrompt(text); return; }
  sendPrompt(text);
});
$("#stop-run-btn").addEventListener("click", function () {
  pauseQueue = true;
  if (abortCtl) abortCtl.abort();
});
$("#interrupt-btn").addEventListener("click", function () {
  var text = promptEl.value.trim();
  if (!text) return;
  promptEl.value = ""; promptEl.style.height = "auto";
  interruptAndSend(text);
});
promptEl.addEventListener("keydown", function (e) {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    $("#composer").requestSubmit();
  }
});
promptEl.addEventListener("input", function () {
  this.style.height = "auto";
  this.style.height = Math.min(this.scrollHeight, 200) + "px";
});
$$(".welcome .hints button").forEach(function (b) {
  b.addEventListener("click", function () { if (!streaming) sendPrompt(b.dataset.prompt); });
});

function newSession() {
  if (streaming) return;
  pendingRewind = null;
  if (transcriptAbortCtl) transcriptAbortCtl.abort();
  transcriptAbortCtl = null;
  loadedTranscriptMessages = [];
  transcriptHasMore = false;
  transcriptBeforeSeq = 0;
  transcriptSessionID = "";
  activeSessionID = "";
  stream.innerHTML = "";
  toolRows = {}; workerRows = {}; openToolOrder = [];
  lastTurn = null; workersSeen = [];
  showWelcome();
  setRunState("idle", t("composer.ready"));
  loadSessions();
  renderStats();
  promptEl.focus();
}
$("#new-session").addEventListener("click", newSession);

/* ═══ notifications ═══ */

function notifyDone(elapsed) {
  if (!appFocused || document.hidden) { unreadDone++; updateAppBadge(); }
  if (ui.notifySound) {
    try {
      var ctx = new (window.AudioContext || window.webkitAudioContext)();
      var osc = ctx.createOscillator(), gain = ctx.createGain();
      osc.connect(gain); gain.connect(ctx.destination);
      osc.frequency.value = 660; gain.gain.value = 0.04;
      osc.start(); osc.stop(ctx.currentTime + 0.12);
    } catch (e) {}
  }
  if (ui.notifyDesktop && (!appFocused || document.hidden) && window.Notification && Notification.permission === "granted") {
    try { new Notification("SuperCli", { body: t("run.done") + " · " + fmtDuration(elapsed) }); } catch (e) {}
  }
}
window.addEventListener("blur", function () { appFocused = false; });
window.addEventListener("focus", function () { appFocused = true; unreadDone = 0; updateAppBadge(); });
document.addEventListener("visibilitychange", function () {
  appFocused = !document.hidden && document.hasFocus();
  if (appFocused) { unreadDone = 0; updateAppBadge(); }
});

/* ═══ health / status ═══ */

var activeModelID = "";
var activeWorkspacePath = "";
function workspaceDisplayName(path) {
  var clean = String(path || "").replace(/[\\/]+$/, "");
  return clean.split(/[\\/]/).pop() || clean;
}
$("#open-project-folder").addEventListener("click", function () {
  if (activeWorkspacePath) openWorkspaceFolder(activeWorkspacePath);
});
async function checkHealth() {
  var dot = $("#status-dot");
  try {
    var h = await j("/api/health");
    dot.className = "status-dot ok" + (streaming ? " busy" : "");
    dot.title = t("status.connected") + " · " + (h.model || "");
    activeWorkspacePath = h.home || "";
    $("#workspace").textContent = workspaceDisplayName(activeWorkspacePath);
    $("#workspace").title = activeWorkspacePath;
    $("#open-project-folder").disabled = !activeWorkspacePath;
    if (h.model) {
      var modelChanged = h.model !== activeModelID;
      activeModelID = h.model;
      $("#model-name").textContent = h.model;
      if (modelChanged) loadReasoning();
    }
    return h;
  } catch (e) {
    dot.className = "status-dot err";
    dot.title = t("status.offline");
    $("#open-project-folder").disabled = !activeWorkspacePath;
    return null;
  }
}

/* ═══ side panel: tabs ═══ */

function activateSideTab(button) {
  if (!button) return;
  $$("#side-tabs button[data-tab]").forEach(function (tab) {
    var selected = tab === button;
    tab.classList.toggle("active", selected);
    tab.setAttribute("aria-selected", selected ? "true" : "false");
    tab.tabIndex = selected ? 0 : -1;
  });
  $$(".side-pane").forEach(function (pane) {
    var selected = pane.id === "tab-" + button.dataset.tab;
    pane.classList.toggle("active", selected);
    pane.hidden = !selected;
  });
}
(function initSideTabs() {
  var tabs = $("#side-tabs");
  tabs.setAttribute("role", "tablist");
  $$("#side-tabs button[data-tab]").forEach(function (button) {
    var pane = $("#tab-" + button.dataset.tab);
    button.id = "side-tab-" + button.dataset.tab;
    button.setAttribute("role", "tab");
    button.setAttribute("aria-controls", pane.id);
    pane.setAttribute("role", "tabpanel");
    pane.setAttribute("aria-labelledby", button.id);
  });
  activateSideTab($("#side-tabs button.active") || $("#side-tabs button[data-tab]"));
})();
$("#side-tabs").addEventListener("click", function (e) {
  var b = e.target.closest("button[data-tab]");
  if (!b) return;
  activateSideTab(b);
  if (b.dataset.tab === "stats") renderStats();
  if (b.dataset.tab === "sessions") loadSessions();
  if (b.dataset.tab === "projects") loadProjects();
  if (b.dataset.tab === "goal") loadSideGoal();
});
$("#side-tabs").addEventListener("keydown", function (e) {
  if (["ArrowLeft", "ArrowRight", "Home", "End"].indexOf(e.key) < 0) return;
  var tabs = $$("#side-tabs button[data-tab]");
  var index = tabs.indexOf(document.activeElement);
  if (index < 0) return;
  e.preventDefault();
  if (e.key === "Home") index = 0;
  else if (e.key === "End") index = tabs.length - 1;
  else index = (index + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
  tabs[index].focus();
  tabs[index].click();
});
function toggleSidebar() {
  ui.sidebarHidden = !ui.sidebarHidden;
  applyUI();
  saveUI();
  if (!ui.sidebarHidden) renderStats();
}
function closeSidebar() {
  if (ui.sidebarHidden) return;
  ui.sidebarHidden = true;
  applyUI();
  saveUI();
}
$("#toggle-sidebar").addEventListener("click", toggleSidebar);
$("#close-sidebar").addEventListener("click", closeSidebar);
$("#sidebar-backdrop").addEventListener("click", closeSidebar);
sidebarCompactMQ.addEventListener("change", function (event) {
  if (event.matches) closeSidebar();
});

/* ═══ stats pane ═══ */

function statRow(label, value) {
  var row = el("div", "stat-row");
  row.appendChild(el("span", "", label));
  var val = el("span", "v", value);
  val.title = value;
  row.appendChild(val);
  return row;
}

function statNumber(value, fallback) {
  var n = Number(value);
  return Number.isFinite(n) ? n : (fallback || 0);
}
function statNullableNumber(value) {
  var n = Number(value);
  return value !== null && value !== "" && Number.isFinite(n) ? n : null;
}
function normalizeStats(raw) {
  raw = raw || {};
  var session = raw.session || {};
  var tokens = raw.tokens || {};
  var context = raw.context || {};
  var breakdown = context.breakdown || {};
  var cost = raw.cost || {};
  var telemetry = raw.telemetry || {};
  var input = statNumber(tokens.input);
  var cached = statNumber(tokens.cached_input);
  var output = statNumber(tokens.output);
  var total = Object.prototype.hasOwnProperty.call(tokens, "total")
    ? statNumber(tokens.total) : statNumber(raw.session_tokens, input + output);
  return {
    model: raw.model || session.model || activeModelID || "",
    dailyTokens: statNumber(raw.daily_tokens),
    session: {
      id: session.id || "", title: session.title || "", provider: session.provider || "",
      providerType: session.provider_type || "", model: session.model || raw.model || activeModelID || "",
      createdAt: session.created_at || "", updatedAt: session.updated_at || "",
      messages: statNumber(session.messages), userMessages: statNumber(session.user_messages),
      assistantMessages: statNumber(session.assistant_messages), toolMessages: statNumber(session.tool_messages),
      toolCalls: statNumber(session.tool_calls),
    },
    tokens: {
      input: input, evaluatedInput: Object.prototype.hasOwnProperty.call(tokens, "evaluated_input")
        ? statNumber(tokens.evaluated_input) : Math.max(0, input - cached),
      cachedInput: cached, output: output, reasoning: statNumber(tokens.reasoning), total: total,
      hasCached: !!tokens.has_cached || cached > 0, hasReasoning: !!tokens.has_reasoning || statNumber(tokens.reasoning) > 0,
    },
    context: {
      window: statNumber(context.window), estimatedUsed: statNumber(context.estimated_used),
      percent: Math.max(0, Math.min(100, statNumber(context.percent))),
      breakdown: {
        user: statNumber(breakdown.user), assistant: statNumber(breakdown.assistant),
        tools: statNumber(breakdown.tools), other: statNumber(breakdown.other),
      },
    },
    cost: {
      state: String(cost.state || "unknown").toLowerCase(), amount: statNullableNumber(cost.amount),
      currency: cost.currency || "USD", source: String(cost.source || ""), estimated: !!cost.estimated,
      partial: !!cost.partial, calls: statNumber(cost.calls), unknownCalls: statNumber(cost.unknown_calls),
      includedCalls: statNumber(cost.included_calls), inputPerMillion: statNullableNumber(cost.input_per_million),
      cachedInputPerMillion: statNullableNumber(cost.cached_input_per_million),
      outputPerMillion: statNullableNumber(cost.output_per_million),
      cacheDiscountKnown: !!cost.cache_discount_known, manual: !!cost.manual,
    },
    telemetry: {
      scope: telemetry.scope || "",
      samples: statNumber(telemetry.samples), steps: statNumber(telemetry.steps),
      durationMS: statNumber(telemetry.duration_ms), averageMS: statNumber(telemetry.average_ms),
      modelMS: statNumber(telemetry.model_ms), toolsMS: statNumber(telemetry.tools_ms),
      cliMS: statNumber(telemetry.cli_ms), persistMS: statNumber(telemetry.persist_ms),
      modelCalls: statNumber(telemetry.model_calls), helperCalls: statNumber(telemetry.helper_calls),
      failedCalls: statNumber(telemetry.failed_calls), canceledCalls: statNumber(telemetry.canceled_calls),
      toolFailures: statNumber(telemetry.tool_failures), bottleneck: telemetry.bottleneck || "",
      bottleneckShare: statNumber(telemetry.bottleneck_share),
      signals: Array.isArray(telemetry.signals) ? telemetry.signals : [],
      tools: Array.isArray(telemetry.tools) ? telemetry.tools : [],
    },
  };
}
function statsURL() {
  return "/api/stats" + (activeSessionID ? "?session=" + encodeURIComponent(activeSessionID) : "");
}
function normalizedCostState(cost) {
  if (cost && cost.partial) return "partial";
  var state = cost && cost.state || "unknown";
  return ["estimated", "manual", "subscription", "local", "free", "unknown"].indexOf(state) >= 0 ? state : "unknown";
}
function costStateLabel(cost) { return t("cost." + normalizedCostState(cost)); }
function costSourceLabel(source) {
  if (!source) return "";
  var key = "cost.source." + source;
  var label = t(key);
  return label === key ? source : label;
}
function costPrimary(cost) {
  var state = normalizedCostState(cost);
  if ((state === "estimated" || state === "manual" || state === "partial") && cost.amount !== null) {
    return (state === "manual" ? "" : "~") + fmtMoney(cost.amount, cost.currency);
  }
  if (state === "subscription") return t("cost.subscriptionValue");
  if (state === "local") return t("cost.localValue");
  if (state === "free") return t("cost.freeValue");
  if (state === "partial") return t("cost.partialValue");
  return t("cost.unknownValue");
}
function costMeta(cost) {
  var parts = [costStateLabel(cost)];
  var source = costSourceLabel(cost.source);
  if (source && ["estimated", "manual", "partial"].indexOf(normalizedCostState(cost)) >= 0) parts.push(source);
  return parts.join(" · ");
}
function costCoverage(cost) {
  var parts = [];
  if (cost.unknownCalls) parts.push(fmtCountLabel(cost.unknownCalls, "cost.unknownCalls"));
  if (cost.includedCalls) parts.push(fmtCountLabel(cost.includedCalls, "cost.includedCalls"));
  return parts.join(" · ");
}
function fmtCountLabel(value, baseKey) {
  var category = "other";
  try { category = new Intl.PluralRules(statsLocale()).select(value); } catch (e) {}
  var key = baseKey + "." + category;
  var label = t(key);
  if (label === key) label = t(baseKey + ".other");
  return fmtInteger(value) + " " + label;
}
function appendCompactContext(parent, context) {
  if (!context || context.window <= 0) return;
  var pct = Math.max(0, Math.min(100, context.percent));
  var bar = el("div", "ctx-bar");
  bar.setAttribute("role", "progressbar");
  bar.setAttribute("aria-label", t("stats.currentContext"));
  bar.setAttribute("aria-valuemin", "0");
  bar.setAttribute("aria-valuemax", "100");
  bar.setAttribute("aria-valuenow", String(pct));
  bar.setAttribute("aria-valuetext", pct + "% · " + fmtInteger(context.estimatedUsed) + " / " + fmtInteger(context.window));
  var fill = el("div");
  fill.style.width = pct + "%";
  bar.appendChild(fill);
  parent.appendChild(bar);
  parent.appendChild(el("div", "stats-note", t("stats.currentContext") + ": " + pct + "% · " +
    fmtCompactNumber(context.estimatedUsed) + " / " + fmtCompactNumber(context.window)));
}
function appendSidebarCost(parent, cost) {
  var state = normalizedCostState(cost);
  var block = el("div", "side-cost cost-state-" + state);
  block.appendChild(el("div", "side-cost-label", t("stats.totalCost")));
  block.appendChild(el("div", "side-cost-value" + (cost.amount === null ? " text" : ""), costPrimary(cost)));
  block.appendChild(el("div", "side-cost-meta", costMeta(cost)));
  var coverage = costCoverage(cost);
  if (coverage) block.appendChild(el("div", "side-cost-coverage", coverage));
  parent.appendChild(block);
}

var statsRenderSeq = 0;
async function renderStats() {
  var box = $("#stats-block");
  if (!box || ui.sidebarHidden) return;
  var seq = ++statsRenderSeq;
  var requestSession = activeSessionID;
  box.innerHTML = "";
  box.setAttribute("role", "region");
  box.setAttribute("aria-label", t("side.stats"));
  box.setAttribute("aria-live", "polite");
  box.setAttribute("aria-busy", "true");

  if (lastTurn) {
    box.appendChild(el("div", "stats-head", t("stats.turn")));
    box.appendChild(statRow(t("stats.model"), activeModelID || "—"));
    var ev = lastTurn.ev;
    var evalTok = (ev.tok_in || 0) - (ev.tok_cached || 0);
    if (ev.tok_cached) box.appendChild(statRow("cache / eval / gen", fmtCompactNumber(ev.tok_cached) + " / " + fmtCompactNumber(evalTok) + " / " + fmtCompactNumber(ev.tok_out)));
    else if (ev.tok_total) box.appendChild(statRow("in / gen", fmtCompactNumber(ev.tok_in) + " / " + fmtCompactNumber(ev.tok_out)));
    if (ev.cache_hit_pct) box.appendChild(statRow(t("run.cached"), ev.cache_hit_pct + "%"));
    if (ev.reasoning_tok) box.appendChild(statRow(t("run.think"), fmtCompactNumber(ev.reasoning_tok)));
    if (lastTurn.tools) box.appendChild(statRow(t("run.tools"), String(lastTurn.tools)));
  }

  var cfgPromise = j("/api/config").catch(function () { return null; });
  try {
    var stats = normalizeStats(await j(statsURL()));
    if (seq !== statsRenderSeq || requestSession !== activeSessionID) return;
    box.appendChild(el("div", "stats-head", t("stats.sessionSection")));
    if (stats.session.provider) box.appendChild(statRow(t("stats.provider"), stats.session.provider));
    box.appendChild(statRow(t("stats.model"), stats.session.model || stats.model || "—"));
    box.appendChild(statRow(t("stats.totalTokens"), fmtCompactNumber(stats.tokens.total)));
    if (stats.dailyTokens > 0) box.appendChild(statRow(t("stats.daily"), fmtCompactNumber(stats.dailyTokens)));
    appendCompactContext(box, stats.context);
    appendSidebarCost(box, stats.cost);
  } catch (e) {
    if (seq !== statsRenderSeq) return;
    box.appendChild(el("div", "side-empty", t("common.error") + ": " + e.message));
  }

  // Delegations stay visible when present, but an empty section would make the
  // narrow inspector feel like a dashboard rather than a compact HUD.
  if (workersSeen.length) {
    box.appendChild(el("div", "stats-head", t("stats.workers")));
    workersSeen.slice(-8).forEach(function (wk) {
      box.appendChild(statRow(wk.name + (wk.status ? " · " + wk.status : ""), clip(wk.summary, 40) || "—"));
    });
  }

  // Orchestrator switch — visible without digging (config.toml knob).
  var cfg = await cfgPromise;
  if (seq === statsRenderSeq && cfg) {
    var knob = null;
    (cfg.knobs || []).forEach(function (k) { if (k.key === "orchestrator") knob = k; });
    if (knob) {
      box.appendChild(el("div", "stats-head", t("stats.orch")));
      var row = el("div", "stat-row");
      row.appendChild(el("span", "", t("stats.orchDesc")));
      var seg = el("span", "seg");
      ["default", "on", "off"].forEach(function (st) {
        var label = st === "default" ? t("stats.orchAuto") : (st === "on" ? t("stats.orchOn") : t("stats.orchOff"));
        var b = el("button", st === (knob.state || "default") ? "on" : "", label);
        b.type = "button";
        b.addEventListener("click", function () {
          jpost("/api/config", { key: "orchestrator", value: st })
            .then(renderStats)
            .catch(function (e2) { toast(e2.message); });
        });
        seg.appendChild(b);
      });
      row.appendChild(seg);
      box.appendChild(row);
    }
  }
  if (seq === statsRenderSeq) box.setAttribute("aria-busy", "false");
}

/* ═══ sessions ═══ */

var sessionByID = {};
var sessionResumeSeq = 0;

function setSessionOpening(id) {
  $$("#session-list .side-item").forEach(function (item) {
    var opening = !!id && item.dataset.sessionId === id;
    item.classList.toggle("opening", opening);
    item.classList.toggle("active", opening || item.dataset.sessionId === activeSessionID);
    if (opening) item.setAttribute("aria-busy", "true");
    else item.removeAttribute("aria-busy");
  });
}

function compactSessionModel(model) {
  var value = String(model || "").replace(/\.gguf$/i, "");
  if (value.length <= 32) return value;
  return value.slice(0, 21) + "…" + value.slice(-10);
}

function sessionDateGroup(iso) {
  var date = new Date(iso);
  if (isNaN(date)) return { key: "unknown", label: "" };
  var today = new Date();
  var startToday = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  var startDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  var days = Math.round((startToday - startDate) / 86400000);
  if (days === 0) return { key: "today", label: t("session.today") };
  if (days === 1) return { key: "yesterday", label: t("session.yesterday") };
  var key = date.getFullYear() + "-" + date.getMonth() + "-" + date.getDate();
  var label;
  try {
    label = new Intl.DateTimeFormat(statsLocale(), {
      day: "numeric", month: "short", year: date.getFullYear() === today.getFullYear() ? undefined : "numeric",
    }).format(date);
  } catch (e) { label = date.toLocaleDateString(); }
  return { key: key, label: label };
}

async function loadSessions() {
  var list = $("#session-list");
  try {
    var rows = await j("/api/sessions?limit=40");
	sessionByID = {}; (rows || []).forEach(function(s){sessionByID[s.id]=s;});
    list.innerHTML = "";
    if (!rows || !rows.length) {
      list.appendChild(el("div", "side-empty", t("side.noSessions")));
      return;
    }
    var currentDateGroup = "";
    rows.forEach(function (s) {
      var dateGroup = sessionDateGroup(s.started_at);
      if (dateGroup.key !== currentDateGroup) {
        currentDateGroup = dateGroup.key;
        if (dateGroup.label) list.appendChild(el("div", "session-date", dateGroup.label));
      }
      var b = el("button", "side-item" + (s.id === activeSessionID ? " active" : ""));
      b.type = "button";
      b.dataset.sessionId = s.id;
      b.appendChild(el("span", "t", (s.parent_id ? "\u2514 " : "") + (s.first_user_msg || s.id)));
      var sessionMeta = fmtWhen(s.started_at) + " · " + s.message_count;
      if (s.model) sessionMeta += " · " + compactSessionModel(s.model);
      var meta = el("span", "s", sessionMeta);
      meta.title = s.model || "";
      b.appendChild(meta);
      b.addEventListener("click", function () { resumeSession(s.id, s); });

      var actions = el("span", "session-actions");
      var rename = el("span", "session-action rename", "\u270E");
      rename.setAttribute("role", "button");
      rename.tabIndex = 0;
      rename.title = t("session.rename");
      rename.setAttribute("aria-label", t("session.rename"));
      function doRename(e) {
        e.preventDefault();
        e.stopPropagation();
        renameSession(s);
      }
      rename.addEventListener("click", doRename);
      rename.addEventListener("keydown", function (e) { if (e.key === "Enter" || e.key === " ") doRename(e); });
      actions.appendChild(rename);

	  var fork = el("span", "session-action fork", "\u2387");
	  fork.setAttribute("role", "button"); fork.tabIndex = 0; fork.title = t("workflow.fork");
	  function doFork(e) { e.preventDefault(); e.stopPropagation(); forkSession(s.id, 0); }
	  fork.addEventListener("click", doFork); fork.addEventListener("keydown", function(e){if(e.key==="Enter"||e.key===" ")doFork(e);}); actions.appendChild(fork);

      var remove = el("span", "session-action delete", "\u00D7");
      remove.setAttribute("role", "button");
      remove.tabIndex = 0;
      remove.title = t("session.delete");
      remove.setAttribute("aria-label", t("session.delete"));
      function doDelete(e) {
        e.preventDefault();
        e.stopPropagation();
        deleteSession(s.id);
      }
      remove.addEventListener("click", doDelete);
      remove.addEventListener("keydown", function (e) { if (e.key === "Enter" || e.key === " ") doDelete(e); });
      actions.appendChild(remove);
      b.appendChild(actions);
      list.appendChild(b);
    });
  } catch (e) {
    list.innerHTML = "";
    list.appendChild(el("div", "side-empty", t("common.error")));
  }
}

async function forkSession(id, throughSeq) {
  var chosen = window.prompt(t("workflow.fork") + " · model", activeModelID || "");
  if (chosen === null) return;
  try {
    var branch = await jpost("/api/branches", { session_id:id, through_seq:throughSeq||0, model:chosen.trim() });
    await loadSessions();
    await resumeSession(branch.id, branch);
    toast(t("workflow.fork") + " ✓");
  } catch(e) { toast(e.message); }
}

async function rewindSession(id, selectedSeq, text, reason, rewindFiles, button) {
  if (!id || !selectedSeq || streaming) return false;
  if (button) button.disabled = true;
  try {
    var branch = await jpost("/api/branches", {
      session_id: id,
      through_seq: selectedSeq > 1 ? selectedSeq - 1 : -1,
      selected_seq: selectedSeq,
      rewind_files: !!rewindFiles,
    });
	if (branch.file_rewind) rememberFileRewind(branch.id, branch.file_rewind);
    await resumeSession(branch.id, branch);
    pendingRewind = { reason: reason || "", files: !!rewindFiles, filesRestored: false };
    promptEl.value = text || "";
    promptEl.dispatchEvent(new Event("input"));
    promptEl.focus();
    promptEl.setSelectionRange(promptEl.value.length, promptEl.value.length);
    toast(t("workflow.rewindDone"));
    return true;
  } catch (e) {
    var message = e.message;
    try {
      var detail = JSON.parse(e.message);
      if (detail.conflicts && detail.conflicts.length) {
        message = t("workflow.rewindConflict") + ": " + detail.conflicts.join(", ");
      }
    } catch (ignore) {}
    toast(message);
    if (button && button.isConnected) button.disabled = false;
    return false;
  }
}

var fileRewindStorageKey = "supercli-file-rewind-receipts";
function fileRewindReceipts() {
  try { return JSON.parse(localStorage.getItem(fileRewindStorageKey) || "{}") || {}; }
  catch (e) { return {}; }
}
function rememberFileRewind(sessionID, receipt) {
  var all = fileRewindReceipts(); all[sessionID] = receipt;
  try { localStorage.setItem(fileRewindStorageKey, JSON.stringify(all)); } catch (e) {}
}
function forgetFileRewind(sessionID) {
  var all = fileRewindReceipts(); delete all[sessionID];
  try { localStorage.setItem(fileRewindStorageKey, JSON.stringify(all)); } catch (e) {}
}
function renderFileRewindAction(sessionID) {
  var previous = stream.querySelector(".rewind-restore");
  if (previous) previous.remove();
  var receipt = fileRewindReceipts()[sessionID];
  if (!receipt || !(receipt.checkpoint_ids || []).length) return;
  var row = el("div", "rewind-restore");
  var copy = el("div", "rewind-restore-copy");
  copy.appendChild(el("strong", "", t("workflow.rewindFileCount").replace("{n}", (receipt.files || []).length)));
  copy.appendChild(el("span", "", t("workflow.restoreFilesHint")));
  var restore = el("button", "btn", t("workflow.restoreFiles"));
  restore.type = "button";
  restore.addEventListener("click", async function () {
    restore.disabled = true;
    try {
      await jpost("/api/checkpoint/rewind", {
        session_id: receipt.session_id,
        branch_session_id: sessionID,
        checkpoint_ids: receipt.checkpoint_ids,
      });
      forgetFileRewind(sessionID);
      row.remove();
      if (pendingRewind) { pendingRewind.files = false; pendingRewind.filesRestored = true; }
      toast(t("workflow.filesRestored"));
    } catch (e) {
      restore.disabled = false;
      toast(e.message);
    }
  });
  row.appendChild(copy); row.appendChild(restore); stream.appendChild(row);
}

async function renameSession(session) {
  var current = session.first_user_msg || "";
  var title = window.prompt(t("session.namePrompt"), current);
  if (title === null) return;
  title = title.trim();
  if (!title || title === current) return;
  try {
    await j("/api/sessions", {
      method: "PATCH", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: session.id, title: title }),
    });
    toast(t("session.renamed"));
    await loadSessions();
    renderStats();
  } catch (e) { toast(e.message); }
}

async function deleteSession(id) {
  if (streaming && id === activeSessionID) {
    toast(t("session.stopRun"));
    return;
  }
  if (!window.confirm(t("session.deleteConfirm"))) return;
  try {
    await j("/api/sessions?id=" + encodeURIComponent(id), { method: "DELETE" });
    if (id === activeSessionID) newSession();
    else await loadSessions();
    toast(t("session.deleted"));
  } catch (e) { toast(e.message); }
}

async function restoreSessionRuntime(session) {
  if (!ui.rememberSessionRuntime || !session || !session.model) return;
  try {
    await jpost("/api/model", { model: session.model, provider: session.provider || "" });
    if (session.runtime_known) {
      await jpost("/api/reasoning", { level: session.reasoning_effort || "default" });
    }
    // The runtime is ready after the two mutations above. Catalog and health
    // are presentation refreshes and must never delay opening the transcript.
    loadModels();
    checkHealth();
  } catch (e) {
    toast(t("session.runtimeFailed"));
  }
}

var transcriptAbortCtl = null;
var loadedTranscriptMessages = [];
var transcriptHasMore = false;
var transcriptBeforeSeq = 0;
var transcriptSessionID = "";
var transcriptPageSize = 100;

function buildHistoryFragment(messages) {
  var historyCalls = {};
  var fragment = document.createDocumentFragment();
  streamAppendTarget = fragment;
  try {
    (messages || []).forEach(function (m) {
      if (m.role === "user") {
        addUserMsg(m.content, m.seq);
      } else if (m.role === "assistant") {
        (m.tool_calls || []).forEach(function (call) { historyCalls[call.id] = call; });
        if (!m.content) return;
        var node = addAssistantMsg();
        node._raw = m.content;
        renderAssistant(node);
        node.querySelectorAll("details[data-think-id]").forEach(function (d) { d.open = false; });
        if (m.turn) addTurnMeta(m.turn, m.turn.elapsed_ms || 0, m.turn.tool_calls || 0, m.seq);
      } else if (m.role === "tool") {
        var task = (m.name === "task" || String(m.content || "").indexOf("<task-notification>") >= 0) ?
          parseTaskNotification(m.content) : null;
        if (task) {
          addHistoryTask(task);
          return;
        }
        var persistedCall = historyCalls[m.tool_call_id] || null;
        var persistedName = (persistedCall && persistedCall.name) || m.name || "tool";
        var persistedArgs = persistedCall ? persistedCall.arguments : "";
        var historyInfo = persistedArgs ? toolHint(persistedName, persistedArgs) :
          { name: toolDisplayName(persistedName), hint: clip(m.content || "", 90) };
        var row = document.createElement("details");
        row.className = "tool-row done";
        var sum = el("summary");
        var historyName = el("span", "tname", historyInfo.name);
        historyName.title = persistedName;
        sum.appendChild(historyName);
        sum.appendChild(el("span", "thint", historyInfo.hint));
        var historyStat = el("span", "tstat", "");
        var historyChanges = toolChangeStats(persistedName, m.content);
        if (historyChanges.added) historyStat.appendChild(el("span", "change-add", "+" + historyChanges.added));
        if (historyChanges.removed) historyStat.appendChild(el("span", "change-remove", "−" + historyChanges.removed));
        if (historyChanges.diff) row.classList.add("has-changes");
        sum.appendChild(historyStat);
        row.appendChild(sum);
        var body = el("div", "tbody");
        if (persistedArgs && !FILE_READ_TOOLS[persistedName]) {
          body.appendChild(el("div", "lbl", t("tool.input")));
          body.appendChild(el("pre", "", prettyJSON(persistedArgs)));
        }
        appendToolPayload(body, t("tool.output"), m.content || "", persistedName, false);
        row.appendChild(body);
        appendStream(row);
      }
    });
  } finally {
    streamAppendTarget = null;
  }
  return fragment;
}

function renderLoadedTranscript(preserveScroll) {
  var oldHeight = stage.scrollHeight;
  var oldTop = stage.scrollTop;
  stream.innerHTML = "";
  toolRows = {}; workerRows = {}; openToolOrder = [];
  lastTurn = null; workersSeen = [];
  hideWelcome();
  if (transcriptHasMore) {
    var older = el("button", "history-older", t("session.older"));
    older.type = "button";
    older.addEventListener("click", loadOlderTranscript);
    stream.appendChild(older);
  }
  stream.appendChild(buildHistoryFragment(loadedTranscriptMessages));
  renderFileRewindAction(transcriptSessionID);
  if (preserveScroll) {
    stage.scrollTop = oldTop + Math.max(0, stage.scrollHeight - oldHeight);
  } else {
    smartScroll(true);
  }
}

async function loadOlderTranscript() {
  if (!transcriptHasMore || !transcriptBeforeSeq || !transcriptSessionID) return;
  var sessionID = transcriptSessionID;
  var button = stream.querySelector(".history-older");
  if (button) {
    button.disabled = true;
    button.textContent = t("session.loadingOlder");
  }
  if (transcriptAbortCtl) transcriptAbortCtl.abort();
  var controller = new AbortController();
  transcriptAbortCtl = controller;
  try {
    var page = await j("/api/transcript?id=" + encodeURIComponent(sessionID) +
      "&limit=" + transcriptPageSize + "&before=" + transcriptBeforeSeq, { signal: controller.signal });
    if (sessionID !== transcriptSessionID || controller !== transcriptAbortCtl) return;
    loadedTranscriptMessages = (page.messages || []).concat(loadedTranscriptMessages);
    transcriptHasMore = !!page.has_more;
    transcriptBeforeSeq = page.before_seq || 0;
    renderLoadedTranscript(true);
  } catch (e) {
    if (e.name !== "AbortError") {
      toast(t("common.error") + ": " + e.message);
      if (button && button.isConnected) {
        button.disabled = false;
        button.textContent = t("session.older");
      }
    }
  } finally {
    if (controller === transcriptAbortCtl) transcriptAbortCtl = null;
  }
}

async function resumeSession(id, session) {
  if (streaming) {
    toast(t("session.stopRun"));
    return;
  }
  pendingRewind = null;
  var resumeSeq = ++sessionResumeSeq;
  var epoch = projectEpoch;
  var releaseRuntimeReady;
  var runtimeQueued = false;
  sessionRuntimeReady = new Promise(function (resolve) { releaseRuntimeReady = resolve; });
  if (transcriptAbortCtl) transcriptAbortCtl.abort();
  var controller = new AbortController();
  transcriptAbortCtl = controller;
  setSessionOpening(id);
  try {
    var page = await j("/api/transcript?id=" + encodeURIComponent(id) + "&limit=" + transcriptPageSize,
      { signal: controller.signal });
    var msgs = page.messages || [];
    if (epoch !== projectEpoch || resumeSeq !== sessionResumeSeq) return;
    activeSessionID = id;
    transcriptSessionID = id;
    loadedTranscriptMessages = msgs;
    transcriptHasMore = !!page.has_more;
    transcriptBeforeSeq = page.before_seq || 0;
    stream.innerHTML = "";
    toolRows = {}; workerRows = {}; openToolOrder = [];
    lastTurn = null; workersSeen = [];
    hideWelcome();
    var historyCalls = {};
	var historyFragment = document.createDocumentFragment();
	if (transcriptHasMore) {
	  var older = el("button", "history-older", t("session.older"));
	  older.type = "button";
	  older.addEventListener("click", loadOlderTranscript);
	  historyFragment.appendChild(older);
	}
	streamAppendTarget = historyFragment;
	(msgs || []).forEach(function (m) {
      if (m.role === "user") {
        addUserMsg(m.content, m.seq);
	      } else if (m.role === "assistant") {
        (m.tool_calls || []).forEach(function (call) { historyCalls[call.id] = call; });
        if (!m.content) return;
        var node = addAssistantMsg();
        node._raw = m.content;
        renderAssistant(node);
        // History replay: thinking folded (only live streams open it).
	        node.querySelectorAll("details[data-think-id]").forEach(function (d) { d.open = false; });
        if (m.turn) addTurnMeta(m.turn, m.turn.elapsed_ms || 0, m.turn.tool_calls || 0, m.seq);
      } else if (m.role === "tool") {
        var task = (m.name === "task" || String(m.content || "").indexOf("<task-notification>") >= 0) ?
          parseTaskNotification(m.content) : null;
        if (task) {
          addHistoryTask(task);
          return;
        }
        var persistedCall = historyCalls[m.tool_call_id] || null;
        var persistedName = (persistedCall && persistedCall.name) || m.name || "tool";
        var persistedArgs = persistedCall ? persistedCall.arguments : "";
        var historyInfo = persistedArgs ? toolHint(persistedName, persistedArgs) :
          { name: toolDisplayName(persistedName), hint: clip(m.content || "", 90) };
        var row = document.createElement("details");
        row.className = "tool-row done";
        var sum = el("summary");
        var historyName = el("span", "tname", historyInfo.name);
        historyName.title = persistedName;
        sum.appendChild(historyName);
        sum.appendChild(el("span", "thint", historyInfo.hint));
        var historyStat = el("span", "tstat", "");
        var historyChanges = toolChangeStats(persistedName, m.content);
        if (historyChanges.added) historyStat.appendChild(el("span", "change-add", "+" + historyChanges.added));
        if (historyChanges.removed) historyStat.appendChild(el("span", "change-remove", "−" + historyChanges.removed));
        if (historyChanges.diff) row.classList.add("has-changes");
        sum.appendChild(historyStat);
        row.appendChild(sum);
        var body = el("div", "tbody");
        if (persistedArgs && !FILE_READ_TOOLS[persistedName]) {
          body.appendChild(el("div", "lbl", t("tool.input")));
          body.appendChild(el("pre", "", prettyJSON(persistedArgs)));
        }
        appendToolPayload(body, t("tool.output"), m.content || "", persistedName, false);
        row.appendChild(body);
        appendStream(row);
      }
	});
	streamAppendTarget = null;
	stream.appendChild(historyFragment);
	renderFileRewindAction(id);
	smartScroll(true);
    loadSessions();
    renderStats();
    promptEl.focus();
    // Remembered provider/model restoration continues after the conversation
    // is already usable. Errors retain the current model and are surfaced by
    // restoreSessionRuntime without rolling back the opened transcript.
    runtimeQueued = true;
    restoreSessionRuntime(session).then(releaseRuntimeReady, releaseRuntimeReady);
  } catch (e) {
    if (e.name !== "AbortError") toast(t("common.error") + ": " + e.message);
  } finally {
    if (!runtimeQueued) releaseRuntimeReady();
    streamAppendTarget = null;
    if (controller === transcriptAbortCtl) transcriptAbortCtl = null;
    if (resumeSeq === sessionResumeSeq) setSessionOpening("");
  }
}
$("#reload-sessions").addEventListener("click", loadSessions);

/* ═══ projects ═══ */

async function loadProjects() {
  var list = $("#project-list");
  try {
    var got = await j("/api/projects");
    list.innerHTML = "";
    var projects = got.projects || [];
    if (!projects.length) {
      list.appendChild(el("div", "side-empty", t("side.noProjects")));
      return;
    }
    projects.forEach(function (p) {
      var b = el("button", "side-item" + (p.cwd ? " active" : ""));
      b.type = "button";
      var tspan = el("span", "t");
      if (p.cwd) tspan.appendChild(el("span", "dot", "●"));
      tspan.appendChild(document.createTextNode(p.name || p.path));
      b.appendChild(tspan);
      b.appendChild(el("span", "s", p.path));
      b.title = p.path;
      var x = el("span", "x", "×");
      x.title = t("common.remove");
      x.addEventListener("click", function (e) {
        e.stopPropagation();
        projectAction("remove", p.path);
      });
      b.appendChild(x);
      b.addEventListener("click", function () { projectAction("use", p.path); });
      list.appendChild(b);
    });
  } catch (e) {
    list.innerHTML = "";
    list.appendChild(el("div", "side-empty", t("common.error")));
  }
}
async function projectAction(action, target, name) {
  if (streaming && (action === "use" || action === "add")) {
    toast(t("project.stopRun"));
    return;
  }
  try {
    await jpost("/api/projects", { action: action, target: target, name: name || "" });
    if (action === "use" || action === "add") projectEpoch++;
    await checkHealth();
    loadProjects();
    // A conversation belongs to the workspace where it was created. Switching
    // projects starts a clean browser conversation and reloads only that
    // project's history; the old session remains stored under its project.
    if (action === "use" || action === "add") { newSession(); loadPromptQueue(); }
  } catch (e) {
    toast(e.message);
  }
}
$("#add-project").addEventListener("click", async function () {
  try {
    var got = await j("/api/folder-picker");
    if (got && got.path) projectAction("add", got.path);
  } catch (e) {
    // Headless / non-Windows fallback: register current workspace.
    projectAction("add", "");
  }
});

/* ═══ model palette ═══ */

var palette = $("#palette"), modelCache = [];
function togglePalette(show) {
  var want = show !== undefined ? show : palette.hidden;
  palette.hidden = !want;
  if (want) {
    toggleReasoningMenu(false);
    renderModelList($("#model-search").value.trim().toLowerCase()); // cached list: instant
    loadModels(); // then refresh quietly
    setTimeout(function () { $("#model-search").focus(); }, 30);
  }
}
$("#model-btn").addEventListener("click", function () { togglePalette(); });
document.addEventListener("click", function (e) {
  if (!palette.hidden && !palette.contains(e.target) && !$("#model-btn").contains(e.target)) togglePalette(false);
  var reasoningControl = $("#reasoning-control");
  if (!$("#reasoning-menu").hidden && !reasoningControl.contains(e.target)) toggleReasoningMenu(false);
});

// slimModels reduces /api/models entries to what the cached list needs.
function slimModels(models) {
  return (models || []).map(function (m) {
    return {
      id: m.id, provider: m.provider, hidden: !!m.hidden, active: !!m.active,
      context_length: m.context_length || 0, reasoning: !!m.reasoning,
      vision: !!m.vision, tool_use: !!m.tool_use,
    };
  });
}

async function loadModels() {
  try {
    var got = await j("/api/models");
    if (got.active) {
      activeModelID = got.active;
      $("#model-name").textContent = got.active;
    }
    $("#model-prov").textContent = got.provider || "";
    // A successful response is authoritative, including an empty list. This
    // prevents paid models surviving after a key was removed or expired.
    modelCache = slimModels(got.models || []);
    saveBlobKey("supercli-model-cache", modelCache);
    renderModelList($("#model-search").value.trim().toLowerCase());
    renderReasoning(got.reasoning);
    return got;
  } catch (e) {
    if (!modelCache.length) $("#model-list").innerHTML = '<div class="side-empty">' + escHtml(e.message) + "</div>";
  }
}
function renderModelList(filter) {
  var list = $("#model-list");
  list.innerHTML = "";
  modelCache.forEach(function (m) {
    if (filter && (m.id + " " + m.provider).toLowerCase().indexOf(filter) < 0) return;
    var isActive = m.id === activeModelID || m.active;
    var row = el("div", "prow" + (isActive ? " active" : "") + (m.hidden ? " hidden-model" : ""));
    // Small state dot: green = visible, red = hidden (information, not lacquer).
    row.appendChild(el("span", "state-dot " + (m.hidden ? "off" : "on")));
    row.appendChild(el("span", "pid", m.id));
    row.appendChild(el("span", "pprov", m.provider || ""));
    if (m.context_length) row.appendChild(el("span", "pbadge", fmtTok(m.context_length)));
    if (m.reasoning) row.appendChild(el("span", "pbadge", "think"));
    if (m.vision) row.appendChild(el("span", "pbadge", "vision"));
    var act = el("span", "pact");
    var bd = el("button", "", t("model.setDefault"));
    bd.title = "Set as CLI default (config.toml)";
    bd.addEventListener("click", function (e) {
      e.stopPropagation();
      jpost("/api/model/default", { model: m.id, provider: m.provider })
        .then(function () { toast("CLI default: " + m.id); })
        .catch(function (err) { toast(err.message); });
    });
    act.appendChild(bd);
    var bh = el("button", "", m.hidden ? t("model.show") : t("model.hide"));
    bh.addEventListener("click", function (e) {
      e.stopPropagation();
      jpost("/api/model/toggle", { provider: m.provider, model: m.id }).then(function () {
        m.hidden = !m.hidden;
        saveBlobKey("supercli-model-cache", modelCache);
        renderModelList(filter);
      });
    });
    act.appendChild(bh);
    row.appendChild(act);
    row.addEventListener("click", function () {
      jpost("/api/model", { model: m.id, provider: m.provider })
        .then(function () { togglePalette(false); loadReasoning(); loadModels(); checkHealth(); })
        .catch(function (err) { toast(err.message); });
    });
    list.appendChild(row);
  });
  if (!list.children.length) list.innerHTML = '<div class="side-empty">—</div>';
}
$("#model-search").addEventListener("input", function () {
  renderModelList(this.value.trim().toLowerCase());
});
$("#model-scan").addEventListener("click", async function () {
  this.textContent = "…";
  try { await jpost("/api/provider/scan", {}); } catch (e) {}
  this.textContent = t("common.scan");
  loadModels();
});
function renderReasoning(r) {
  var control = $("#reasoning-control");
  var btn = $("#reasoning-btn");
  var configured = (r && r.configured) || "";
  // The control is a stable part of the model cluster. Do not hide it while
  // model discovery is pending (or when a provider omitted capability
  // metadata); the backend negotiates/omits unsupported parameters safely.
  control.hidden = false;

  $("#reasoning-level").textContent = configured || t("model.auto");
  btn.classList.toggle("active", !!configured);
  btn.title = t("model.reasoning") + ": " + (configured || t("model.default")) +
    (r && r.adjusted ? " (effective: " + r.effective + ")" : "");

  var options = $("#reasoning-options");
  options.innerHTML = "";
  [{ value: "", label: t("model.default") }].concat((r && r.levels ? r.levels : []).map(function (lv) {
    return { value: lv, label: lv };
  })).forEach(function (item) {
    var option = el("button", "reasoning-option" + (item.value === configured ? " selected" : ""));
    option.type = "button";
    option.dataset.level = item.value;
    option.setAttribute("role", "menuitemradio");
    option.setAttribute("aria-checked", item.value === configured ? "true" : "false");
    option.tabIndex = item.value === configured ? 0 : -1;
    option.appendChild(el("span", "reasoning-check", "◆"));
    option.appendChild(el("span", "", item.label));
    options.appendChild(option);
  });
}

async function loadReasoning() {
  try {
    renderReasoning(await j("/api/reasoning"));
  } catch (e) {
    // Keep the always-visible default control; health status already reports
    // connectivity and a transient request failure must not move the toolbar.
  }
}

function toggleReasoningMenu(show) {
  var menu = $("#reasoning-menu");
  var want = show !== undefined ? show : menu.hidden;
  menu.hidden = !want;
  $("#reasoning-btn").setAttribute("aria-expanded", want ? "true" : "false");
  if (want) {
    togglePalette(false);
    setTimeout(function () {
      var selected = $("#reasoning-options .selected") || $("#reasoning-options .reasoning-option");
      if (selected) selected.focus();
    }, 0);
  }
}

var reasoningSaving = false;
$("#reasoning-btn").addEventListener("click", function () {
  if (!reasoningSaving) toggleReasoningMenu();
});
$("#reasoning-options").addEventListener("click", async function (e) {
  var option = e.target.closest("button[data-level]");
  if (!option || reasoningSaving) return;
  var btn = $("#reasoning-btn");
  reasoningSaving = true;
  toggleReasoningMenu(false);
  btn.setAttribute("aria-busy", "true");
  btn.focus();
  try {
    var got = await jpost("/api/reasoning", { level: option.dataset.level || "default" });
    renderReasoning(got);
  } catch (err) {
    toast(err.message);
    await loadModels();
  } finally {
    reasoningSaving = false;
    btn.removeAttribute("aria-busy");
  }
});
$("#reasoning-menu").addEventListener("keydown", function (e) {
  var options = $$("#reasoning-options .reasoning-option");
  if (!options.length) return;
  var index = options.indexOf(document.activeElement);
  if (e.key === "Escape") {
    e.preventDefault();
    e.stopPropagation();
    toggleReasoningMenu(false);
    $("#reasoning-btn").focus();
    return;
  }
  if (e.key === "Home" || e.key === "End" || e.key === "ArrowDown" || e.key === "ArrowUp") {
    e.preventDefault();
    if (e.key === "Home") index = 0;
    else if (e.key === "End") index = options.length - 1;
    else if (e.key === "ArrowDown") index = (index + 1) % options.length;
    else index = (index <= 0 ? options.length : index) - 1;
    options[index].focus();
  }
});

/* ═══ control panel ═══ */

var overlay = $("#panel-overlay"), panelContent = $("#panel-content");
var currentSection = "settings";
var sections = {}; // name -> render function

function openPanel(section) {
  overlay.hidden = false;
  showSection(section || currentSection);
}
function closePanel() { overlay.hidden = true; }
$("#open-panel").addEventListener("click", function () { openPanel(); });
$("#panel-close").addEventListener("click", closePanel);
overlay.addEventListener("click", function (e) { if (e.target === overlay) closePanel(); });
$("#panel-nav").addEventListener("click", function (e) {
  var b = e.target.closest("button[data-sec]");
  if (b) showSection(b.dataset.sec);
});
function showSection(name) {
  currentSection = name;
  $$("#panel-nav button[data-sec]").forEach(function (b) {
    b.classList.toggle("active", b.dataset.sec === name);
  });
  $("#panel-title").textContent = t("panel." + name);
  panelContent.innerHTML = '<div class="note">' + escHtml(t("common.loading")) + "</div>";
  var rendered = (sections[name] || function () {})();
  // Async sections share one surface. If an older request finishes after the
  // user switched tabs, redraw the current tab so stale content cannot win.
  Promise.resolve(rendered).finally(function () {
    if (currentSection !== name) showSection(currentSection);
  });
}

/* ── Settings (config.toml knobs — TUI /settings parity) ── */

sections.workflow = async function () {
  panelContent.innerHTML = "";
  var queue = el("div", "group"); queue.appendChild(el("div", "g-label", t("workflow.queue")));
  queue.appendChild(el("div", "workflow-lead", String(promptQueue.length).padStart(2,"0") + " · " + t("composer.queued")));
  queue.appendChild(el("div", "note", ui.lang === "pl" ? "Kolejka przeżywa restart aplikacji i czeka na wznowienie." : "The queue survives app restarts and waits for you to resume it.")); panelContent.appendChild(queue);

  var branches = el("div", "group"), bh=el("div","g-label",t("workflow.branches"));
  var fork=el("button","g-act",t("workflow.fork"));fork.disabled=!activeSessionID;fork.addEventListener("click",function(){forkSession(activeSessionID,0);});bh.appendChild(fork);branches.appendChild(bh);
  var selected=[];
  if(activeSessionID){
    try{var rows=await j("/api/branches?session="+encodeURIComponent(activeSessionID));
      (rows||[]).forEach(function(s){var row=el("label","branch-row"),cb=document.createElement("input");cb.type="checkbox";cb.addEventListener("change",function(){if(cb.checked){if(selected.length>=2){cb.checked=false;return;}selected.push(s.id);}else selected=selected.filter(function(id){return id!==s.id;});});row.appendChild(cb);var copy=el("span","branch-copy");copy.appendChild(el("span","branch-title",s.first_user_msg||s.id));copy.appendChild(el("span","branch-meta",(s.model||"—")+" · "+s.message_count+" msg"));row.appendChild(copy);var open=el("button","queue-action",t("workflow.open"));open.type="button";open.addEventListener("click",function(e){e.preventDefault();resumeSession(s.id,s);closePanel();});row.appendChild(open);branches.appendChild(row);});
      if(!rows||rows.length<2)branches.appendChild(el("div","note",t("workflow.noBranches")));
      var compare=el("button","btn",t("workflow.compare"));compare.addEventListener("click",function(){if(selected.length!==2){toast(ui.lang==="pl"?"Wybierz dokładnie dwie gałęzie.":"Select exactly two branches.");return;}compareBranches(selected);});branches.appendChild(compare);
	  var runBoth=el("button","btn",t("workflow.runBoth"));runBoth.addEventListener("click",async function(){if(selected.length!==2){toast(ui.lang==="pl"?"Wybierz dokładnie dwie gałęzie.":"Select exactly two branches.");return;}var prompt=window.prompt(t("composer.ph"),"");if(!prompt||!prompt.trim())return;try{for(var i=0;i<selected.length;i++){var q=await jpost("/api/tasks",{session_id:selected[i],prompt:prompt.trim()});q.text=q.prompt;promptQueue.push(q);}pauseQueue=true;renderPromptQueue();toast(t("workflow.runBoth")+" ✓");}catch(e){toast(e.message);}});branches.appendChild(runBoth);
    }catch(e){branches.appendChild(el("div","note",e.message));}
  }else branches.appendChild(el("div","note",t("workflow.noBranches")));
  panelContent.appendChild(branches);

  var profile=el("div","group");profile.appendChild(el("div","g-label",t("workflow.profile")));
  try{var p=await j("/api/prompt/profile");profile.appendChild(el("div","file-path",p.path));var openProfiles=el("button","btn",t("common.openFolder"));openProfiles.addEventListener("click",function(){openWorkspaceFolder(p.path.replace(/[\\\/][^\\\/]+$/, ""));});profile.appendChild(openProfiles);var ta=el("textarea","editor-area profile-editor");ta.value=p.content||"";ta.placeholder=ui.lang==="pl"?"Np. preferuj krótkie wywołania narzędzi.":"Example: prefer short tool calls.";profile.appendChild(ta);var save=el("button","btn primary",t("common.save"));save.addEventListener("click",async function(){try{await jpost("/api/prompt/profile",{content:ta.value});toast(t("common.save")+" ✓");}catch(e){toast(e.message);}});profile.appendChild(save);profile.appendChild(el("div","note",t("workflow.profileHint")));}catch(e){profile.appendChild(el("div","note",e.message));}
  panelContent.appendChild(profile);

  var scratch=el("div","group");scratch.appendChild(el("div","g-label",t("workflow.scratch")));try{var sc=await j("/api/scratchpad");scratch.appendChild(el("div","file-path",sc.path));var openScratch=el("button","btn",t("common.openFolder"));openScratch.addEventListener("click",function(){openWorkspaceFolder(sc.path);});scratch.appendChild(openScratch);scratch.appendChild(el("div","note",sc.notes.length?sc.notes.join(" · "):(ui.lang==="pl"?"Notatnik jest pusty.":"Scratchpad is empty.")));}catch(e){scratch.appendChild(el("div","note",e.message));}panelContent.appendChild(scratch);

  var hard=el("div","group");hard.appendChild(el("div","g-label",t("workflow.hard")));var run=el("button","btn primary",t("workflow.runHard")),output=el("pre","pre-block","");run.addEventListener("click",async function(){run.disabled=true;run.textContent=t("common.loading");output.textContent="";try{var report=await jpost("/api/test/hard",{});output.textContent=(report.ok?"PASS":"FAIL")+" · "+fmtDuration(report.duration_ms)+"\n"+(report.checks||[]).map(function(c){return(c.ok?"✓ ":"× ")+c.name+" · "+fmtDuration(c.duration_ms)+(c.ok?"":"\n"+c.output);}).join("\n");}catch(e){output.textContent=e.message;}finally{run.disabled=false;run.textContent=t("workflow.runHard");}});hard.appendChild(run);hard.appendChild(output);panelContent.appendChild(hard);
};

async function compareBranches(ids){
  try{var all=await Promise.all(ids.map(function(id){return j("/api/transcript?id="+encodeURIComponent(id));})),overlay=el("div","question-overlay branch-compare"),panel=el("div","question-panel compare-panel"),head=el("div","question-actions"),close=el("button","btn",t("common.cancel"));close.addEventListener("click",function(){overlay.remove();});head.appendChild(close);panel.appendChild(head);var grid=el("div","compare-grid");all.forEach(function(msgs,i){var col=el("div","compare-col");col.appendChild(el("div","g-label",clip(ids[i],22)));var answer="";for(var n=msgs.length-1;n>=0;n--){if(msgs[n].role==="assistant"){answer=msgs[n].content;break;}}var body=el("div","msg-assistant");body.innerHTML=renderText(answer||"—");col.appendChild(body);grid.appendChild(col);});panel.appendChild(grid);overlay.appendChild(panel);document.body.appendChild(overlay);}catch(e){toast(e.message);}
}

async function openWorkspaceFolder(path) {
  try { await jpost("/api/folder/open", { path: path }); }
  catch (e) { toast(e.message); }
}

sections.settings = async function () {
  var got;
  try { got = await j("/api/config"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  panelContent.appendChild(el("div", "note", t("set.hint")));
  var wrap = el("div", "group");
  wrap.style.marginTop = "10px";
  (got.knobs || []).forEach(function (k) { wrap.appendChild(knobRow(k)); });
  panelContent.appendChild(wrap);
  var reset = el("button", "btn danger", t("set.resetAll"));
  reset.addEventListener("click", async function () {
    await jpost("/api/config", { reset_all: true }).catch(function (e) { toast(e.message); });
    sections.settings();
  });
  panelContent.appendChild(reset);
  panelContent.appendChild(el("div", "note", " "));
  panelContent.appendChild(el("div", "note", t("set.resetAllHint")));
};

function knobRow(k) {
  var row = el("div", "knob-row");
  var copy = settingCopy(k);
  var name = el("div", "k-name", copy[0]);
  name.title = "config.toml · " + k.key;
  var d = el("small", "", copy[1]);
  name.appendChild(d);
  row.appendChild(name);
  if (k.next_session) row.appendChild(el("span", "k-next", "(" + t("set.nextSession") + ")"));

  function post(value) {
	if (k.key === "allow_all" && value === "on" && !window.confirm(t("sandbox.allowConfirm"))) return;
    jpost("/api/config", { key: k.key, value: value })
      .then(function () { sections.settings(); })
      .catch(function (e) { toast(e.message); });
  }

  if (k.kind === "tri" || k.kind === "tri_auto" || k.kind === "nav") {
    var states = k.kind === "tri" ? ["default", "on", "off"] : ["auto", "on", "off"];
    var seg = el("span", "seg");
    states.forEach(function (st) {
      var label = t("set.state." + st);
      if (k.key === "orchestrator") {
        label = st === "default" ? t("stats.orchAuto") : (st === "on" ? t("stats.orchOn") : t("stats.orchOff"));
      } else if (st === "default" && k.default) {
        label += " (" + t("set.state." + k.default) + ")";
      }
      var b = el("button", "", label);
      b.type = "button";
      if (st === (k.state || "default") || (st === "auto" && (k.state || "default") === "default")) b.classList.add("on");
      b.addEventListener("click", function () { post(st === "auto" ? "default" : st); });
      seg.appendChild(b);
    });
    row.appendChild(el("span", "k-val", k.source === "default" ? k.value : ""));
    row.appendChild(seg);
  } else if (k.kind === "int" || k.kind === "text") {
    var input = el("input", "k-edit");
    input.value = k.raw || "";
    input.placeholder = k.source === "default" ? k.value : "";
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); post(input.value.trim()); }
      if (e.key === "Escape") { input.value = k.raw || ""; input.blur(); }
    });
    input.addEventListener("blur", function () {
      if (input.value.trim() !== (k.raw || "")) post(input.value.trim());
    });
    row.appendChild(input);
  } else {
    row.appendChild(el("span", "k-val", k.value));
  }
  var src = el("span", "k-src" + (k.source !== "default" ? " manual" : ""), t("set.source." + k.source));
  row.appendChild(src);
  return row;
}

/* ── Appearance ── */

sections.appearance = function () {
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", t("app.theme")));
  var seg = el("span", "seg");
  [["dark", t("app.dark")], ["midnight", t("app.midnight")], ["light", t("app.light")]].forEach(function (pair) {
    var b = el("button", ui.theme === pair[0] ? "on" : "", pair[1]);
    b.type = "button";
    b.addEventListener("click", function () {
      ui.theme = pair[0]; applyUI(); saveUI(); sections.appearance();
    });
    seg.appendChild(b);
  });
  g.appendChild(seg);
  panelContent.appendChild(g);

  function selectRow(label, key, options) {
    var gg = el("div", "group");
    var tr = el("label", "toggle-row");
    tr.appendChild(el("span", "", label));
    var sel = el("select", "field-select");
    options.forEach(function (o) {
      var opt = el("option", "", o[1]);
      opt.value = o[0];
      sel.appendChild(opt);
    });
    sel.value = ui[key];
    sel.addEventListener("change", function () {
      ui[key] = sel.value;
      applyUI();
      saveUI();
      if (key === "lang") {
        $("#panel-title").textContent = t("panel." + currentSection);
        renderStats();
        sections.appearance();
      }
    });
    tr.appendChild(sel);
    gg.appendChild(tr);
    return gg;
  }
  panelContent.appendChild(selectRow(t("app.lang"), "lang", [["en", "English"], ["pl", "Polski"]]));
  panelContent.appendChild(selectRow(t("app.uiFont"), "uiFont",
    [["system", "System"], ["segoe", "Segoe UI"], ["aptos", "Aptos"], ["arial", "Arial"], ["tahoma", "Tahoma"], ["verdana", "Verdana"], ["trebuchet", "Trebuchet MS"], ["georgia", "Georgia"]]));
  panelContent.appendChild(selectRow(t("app.codeFont"), "codeFont",
    [["system", "System monospace"], ["cascadia", "Cascadia Mono"], ["jetbrains", "JetBrains Mono"], ["fira", "Fira Code"], ["consolas", "Consolas"], ["courier", "Courier New"], ["lucida", "Lucida Console"]]));
  panelContent.appendChild(selectRow(t("app.scale"), "uiScale", [["auto", t("app.auto")], ["compact", "90%"], ["normal", "100%"], ["large", "110%"], ["xlarge", "125%"], ["huge", "140%"]]));

  var gn = el("div", "group");
  gn.appendChild(el("div", "g-label", t("app.notify")));
  [["notifySound", t("app.sound")], ["notifyDesktop", t("app.desktop")], ["appBadge", t("app.badge")]].forEach(function (pair) {
    var tr = el("label", "toggle-row");
    tr.appendChild(el("span", "", pair[1]));
    var cb = document.createElement("input");
    cb.type = "checkbox";
    cb.checked = !!ui[pair[0]];
    cb.addEventListener("change", function () {
      ui[pair[0]] = cb.checked;
      saveUI();
      if (pair[0] === "appBadge") updateAppBadge();
      if (pair[0] === "notifyDesktop" && cb.checked && window.Notification && Notification.permission === "default") {
        Notification.requestPermission();
      }
    });
    tr.appendChild(cb);
    gn.appendChild(tr);
  });
  panelContent.appendChild(gn);
};

function sessionRuntimePreferenceGroup() {
  var gs = el("div", "group");
  gs.appendChild(el("div", "g-label", t("session.runtime")));
  var sr = el("label", "toggle-row");
  var sc = el("span");
  sc.appendChild(el("span", "", t("session.runtime")));
  sc.appendChild(el("span", "toggle-hint", t("session.runtimeHint")));
  sr.appendChild(sc);
  var sessionRuntime = document.createElement("input");
  sessionRuntime.type = "checkbox";
  sessionRuntime.checked = !!ui.rememberSessionRuntime;
  sessionRuntime.addEventListener("change", function () {
    ui.rememberSessionRuntime = sessionRuntime.checked;
    saveUI();
  });
  sr.appendChild(sessionRuntime);
  gs.appendChild(sr);
  return gs;
}

/* ── Models (visibility management) ── */

var modelProviderTab = "";
var modelSettingsSearch = "";
sections.models = async function () {
  var got;
  try { got = await j("/api/models"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  panelContent.appendChild(sessionRuntimePreferenceGroup());
  var g = el("div", "group");
  var lbl = el("div", "g-label", t("panel.models"));
  var scan = el("button", "g-act", t("common.scan"));
  scan.addEventListener("click", async function () {
    scan.textContent = "…";
    try { await jpost("/api/provider/scan", {}); } catch (e) {}
    sections.models();
  });
  lbl.appendChild(scan);
  g.appendChild(lbl);
  // /api/models is authoritative even when it is empty. Falling back to the
  // browser cache here used to resurrect paid models after a key was removed.
  var models = slimModels(got.models || []);
  var counts = {};
  models.forEach(function (m) {
    var provider = m.provider || "—";
    if (!counts[provider]) counts[provider] = { total: 0, visible: 0 };
    counts[provider].total++;
    if (!m.hidden) counts[provider].visible++;
  });
  var providers = Object.keys(counts).sort(function (a, b) { return a.localeCompare(b); });
  if (modelProviderTab && providers.indexOf(modelProviderTab) < 0) modelProviderTab = "";

  var tabs = el("div", "model-provider-tabs");
  tabs.setAttribute("role", "tablist");
  function addProviderTab(provider, label, total, visible) {
    var b = el("button", modelProviderTab === provider ? "active" : "");
    b.type = "button";
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", modelProviderTab === provider ? "true" : "false");
    b.appendChild(el("span", "", label));
    b.appendChild(el("small", "", visible + "/" + total));
    b.addEventListener("click", function () { modelProviderTab = provider; sections.models(); });
    tabs.appendChild(b);
  }
  var allVisible = models.filter(function (m) { return !m.hidden; }).length;
  addProviderTab("", t("model.allProviders"), models.length, allVisible);
  providers.forEach(function (provider) {
    addProviderTab(provider, provider, counts[provider].total, counts[provider].visible);
  });
  g.appendChild(tabs);

  var searchWrap = el("div", "model-settings-search");
  var searchInput = document.createElement("input");
  searchInput.type = "search";
  searchInput.className = "field-input";
  searchInput.placeholder = t("model.search");
  searchInput.setAttribute("aria-label", t("model.search"));
  searchInput.autocomplete = "off";
  searchInput.spellcheck = false;
  searchInput.value = modelSettingsSearch;
  searchWrap.appendChild(searchInput);
  g.appendChild(searchWrap);

  var providerModels = models.filter(function (m) { return !modelProviderTab || (m.provider || "—") === modelProviderTab; });
  function matchingModels() {
    var query = modelSettingsSearch.trim().toLocaleLowerCase();
    if (!query) return providerModels;
    return providerModels.filter(function (m) {
      var features = [m.id, m.provider, m.tool_use ? "tools" : "", m.reasoning ? "think reasoning" : "", m.vision ? "vision" : "", m.hidden ? "hidden" : "visible"];
      return features.join(" ").toLocaleLowerCase().indexOf(query) >= 0;
    });
  }
  var tools = el("div", "model-provider-actions");
  var matchCount = el("span", "note", "");
  tools.appendChild(matchCount);
  var bulkButtons = [];
  function bulkButton(label, hidden) {
    var b = el("button", "btn", label); b.type = "button";
    b.addEventListener("click", async function () {
      var filtered = matchingModels();
      b.disabled = true;
      try {
        await jpost("/api/model/visibility", { refs: filtered.map(function (m) { return { provider: m.provider, model: m.id }; }), hidden: hidden });
        await loadModels();
        await sections.models();
      } catch (e) { toast(e.message); b.disabled = false; }
    });
    tools.appendChild(b);
    bulkButtons.push({ button: b, hidden: hidden });
  }
  bulkButton(t("model.hideAll"), true);
  bulkButton(t("model.showAll"), false);
  g.appendChild(tools);

  var rows = el("div", "model-provider-rows");
  function renderFilteredModels() {
    var filtered = matchingModels();
    rows.innerHTML = "";
    matchCount.textContent = filtered.length + " / " + providerModels.length + " " + t("prov.models");
    bulkButtons.forEach(function (item) {
      item.button.disabled = !filtered.length || filtered.every(function (m) { return !!m.hidden === item.hidden; });
    });
    if (!filtered.length) rows.appendChild(el("div", "note model-no-matches", t("model.noMatches")));
    filtered.forEach(function (m) {
      var row = el("div", "list-row");
      row.appendChild(el("span", "state-dot " + (m.hidden ? "off" : "on")));
      var main = el("div", "lr-main");
      var title = el("div", "lr-title");
      title.innerHTML = "<code>" + escHtml(m.id) + "</code>" + (m.id === activeModelID ? ' <span style="color:var(--accent)">●</span>' : "");
      main.appendChild(title);
      var caps = [];
      if (m.context_length) caps.push("ctx " + fmtTok(m.context_length));
      if (m.tool_use) caps.push("tools");
      if (m.reasoning) caps.push("think");
      if (m.vision) caps.push("vision");
      main.appendChild(el("div", "lr-sub", (m.provider || "") + (caps.length ? " · " + caps.join(" · ") : "")));
      row.appendChild(main);
      var act = el("div", "lr-act");
      var bh = el("button", "", m.hidden ? t("model.show") : t("model.hide"));
      bh.addEventListener("click", function () {
        jpost("/api/model/toggle", { provider: m.provider, model: m.id }).then(sections.models);
      });
      act.appendChild(bh);
      var bd = el("button", "", t("model.setDefault"));
      bd.addEventListener("click", function () {
        jpost("/api/model/default", { model: m.id, provider: m.provider })
          .then(function () { toast("CLI default: " + m.id); })
          .catch(function (e) { toast(e.message); });
      });
      act.appendChild(bd);
      row.appendChild(act);
      rows.appendChild(row);
    });
  }
  searchInput.addEventListener("input", function () {
    modelSettingsSearch = searchInput.value;
    renderFilteredModels();
  });
  searchInput.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && searchInput.value) {
      searchInput.value = "";
      modelSettingsSearch = "";
      renderFilteredModels();
    }
  });
  renderFilteredModels();
  g.appendChild(rows);
  panelContent.appendChild(g);
};

/* ── Providers ── */

var runtimeRenderSeq = 0;
sections.runtime = async function () {
  var seq = ++runtimeRenderSeq;
  panelContent.innerHTML = "";
  var intro = el("div", "runtime-intro");
  var introCopy = el("div");
  introCopy.appendChild(el("div", "runtime-title", t("runtime.title")));
  introCopy.appendChild(el("div", "note", t("runtime.hint")));
  intro.appendChild(introCopy);
  var refreshAll = el("button", "btn", t("common.refresh")); refreshAll.type = "button";
  refreshAll.addEventListener("click", function () { sections.runtime(); });
  intro.appendChild(refreshAll); panelContent.appendChild(intro);

  var got;
  try { got = await j("/api/providers"); } catch (e) {
    panelContent.appendChild(el("div", "note", e.message));
    return;
  }
  if (seq !== runtimeRenderSeq || currentSection !== "runtime") return;
  var providers = got.providers || [];
  if (!providers.length) {
    panelContent.appendChild(el("div", "runtime-empty", t("runtime.empty")));
    return;
  }
  var list = el("div", "runtime-list"); panelContent.appendChild(list);
  providers.forEach(function (p) {
    var row = el("section", "runtime-row checking");
    var head = el("div", "runtime-head");
    var identity = el("div", "runtime-identity");
    identity.appendChild(el("span", "runtime-dot"));
    identity.appendChild(el("strong", "", p.Name));
    identity.appendChild(el("span", "runtime-kind", p.Type || ""));
    head.appendChild(identity);
    var state = el("span", "runtime-state", t("runtime.checking") + "…"); head.appendChild(state);
    row.appendChild(head);
    var endpoint = el("div", "runtime-endpoint", p.BaseURL || "—"); row.appendChild(endpoint);
    var facts = el("div", "runtime-facts"); row.appendChild(facts);
    var details = document.createElement("details"); details.className = "runtime-details";
    var summary = document.createElement("summary"); summary.textContent = t("runtime.details");
    details.appendChild(summary); details.appendChild(el("div", "runtime-detail-body", t("common.loading")));
    row.appendChild(details);
    var actions = el("div", "runtime-actions");
    var refresh = el("button", "", t("common.refresh")); refresh.type = "button";
    var toggle = el("button", "", p.Disabled ? t("runtime.enable") : t("runtime.disable")); toggle.type = "button";
    actions.appendChild(refresh); actions.appendChild(toggle); row.appendChild(actions); list.appendChild(row);

    async function update() {
      row.className = "runtime-row checking";
      state.textContent = t("runtime.checking") + "…";
      refresh.disabled = true;
      try {
        var d = await j("/api/provider/diagnostics?name=" + encodeURIComponent(p.Name));
        if (seq !== runtimeRenderSeq || currentSection !== "runtime") return;
        renderRuntimeDiagnostic(row, facts, details, endpoint, state, d);
        toggle.textContent = d.disabled ? t("runtime.enable") : t("runtime.disable");
        toggle.dataset.disabled = d.disabled ? "1" : "0";
        toggle.dataset.active = d.active ? "1" : "0";
      } catch (e) {
        row.className = "runtime-row offline";
        state.textContent = t("runtime.offline");
        facts.innerHTML = "";
        facts.appendChild(runtimeFact(t("common.error"), e.message, ""));
      } finally { refresh.disabled = false; }
    }
    refresh.addEventListener("click", update);
    toggle.addEventListener("click", async function () {
      if (toggle.dataset.active === "1" && toggle.dataset.disabled !== "1") {
        toast(t("runtime.switchFirst")); return;
      }
      toggle.disabled = true;
      try {
        await j("/api/providers", {method:"PUT", headers:{"Content-Type":"application/json"}, body:JSON.stringify({name:p.Name, disabled:toggle.dataset.disabled !== "1"})});
        await loadModels(); await update();
      } catch (e) { toast(e.message); }
      finally { toggle.disabled = false; }
    });
    update();
  });
};

function runtimeFact(label, value, source) {
  var fact = el("div", "runtime-fact");
  fact.appendChild(el("span", "runtime-fact-label", label));
  var right = el("span", "runtime-fact-right");
  right.appendChild(el("strong", "", value || "—"));
  if (source) right.appendChild(el("small", "", source));
  fact.appendChild(right);
  return fact;
}

function renderRuntimeDiagnostic(row, facts, details, endpoint, state, d) {
  row.className = "runtime-row " + (d.status || "offline");
  state.textContent = t("runtime." + (d.status || "offline")) + (d.active ? " · " + t("runtime.active") : "");
  endpoint.textContent = d.endpoint || "—";
  var kind = row.querySelector(".runtime-kind");
  kind.textContent = (d.server || d.type || "") + (d.scope ? " · " + d.scope.toUpperCase() : "");
  facts.innerHTML = "";
  if (d.status === "offline") {
    facts.appendChild(runtimeFact(t("common.error"), d.error || t("runtime.offline"), ""));
  } else if (d.status !== "disabled") {
    facts.appendChild(runtimeFact(t("runtime.latency"), d.latency_ms + " ms", t("runtime.measured")));
    facts.appendChild(runtimeFact(t("runtime.model"), d.selected_model || "—", t("runtime.reported")));
    facts.appendChild(runtimeFact(t("runtime.models"), String((d.models || []).length), t("runtime.reported")));
    if (d.context_window) facts.appendChild(runtimeFact(t("runtime.context"), fmtTok(d.context_window), t("runtime.reported")));
    if (d.capability_known) facts.appendChild(runtimeFact(t("runtime.tools"), d.tool_use ? t("runtime.available") : "—", t("runtime.catalog")));
  }
  if (d.last_call) {
    var c = d.last_call;
    if (c.ttft_ms) facts.appendChild(runtimeFact(t("runtime.ttft"), fmtDuration(c.ttft_ms), t("runtime.lastRun")));
    if (c.tokens_per_second) facts.appendChild(runtimeFact(t("runtime.speed"), c.tokens_per_second.toFixed(1) + " tok/s", t("runtime.lastRun")));
    if (c.duration_ms) facts.appendChild(runtimeFact(t("runtime.duration"), fmtDuration(c.duration_ms), t("runtime.lastRun")));
  }
  var body = details.querySelector(".runtime-detail-body"); body.innerHTML = "";
  if ((d.models || []).length) {
    var models = el("div", "runtime-models");
    d.models.forEach(function (model) { models.appendChild(el("code", "", model)); });
    body.appendChild(models);
  }
  var limits = el("div", "runtime-limits");
  limits.appendChild(el("div", "runtime-limit-title", t("runtime.limits")));
  limits.appendChild(el("span", "", t("runtime.hardware")));
  limits.appendChild(el("span", "", t("runtime.backendQueue")));
  body.appendChild(limits);
}

sections.providers = function () { renderProvidersList(); };

function providerModelCount(models) {
  return Array.isArray(models) ? models.length : (Number(models) || 0);
}

async function renderProvidersList() {
  var got;
  try { got = await j("/api/providers"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var lbl = el("div", "g-label", t("prov.configured"));
  var addB = el("button", "g-act", "＋ " + t("prov.addNew"));
  addB.addEventListener("click", function () { renderProviderForm(got.templates || [], null); });
  lbl.appendChild(addB);
  g.appendChild(lbl);
  var provs = got.providers || [];
  if (!provs.length) g.appendChild(el("div", "note", t("prov.none")));
  provs.forEach(function (p) {
    var row = el("div", "list-row" + (p.Disabled ? " provider-disabled" : ""));
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(p.Name) + "</strong> <span class='note'>" + escHtml(p.Type || "") + "</span>";
    main.appendChild(title);
    var sub = (p.BaseURL || "") + " · " + (p.HasKey ? t("prov.key") : t("prov.noKey")) +
      ((p.Models || []).length ? " · " + p.Models.length + " " + t("prov.models") : "");
    if (p.Disabled) sub = (ui.lang === "pl" ? "wyłączony · " : "disabled · ") + sub;
    main.appendChild(el("div", "lr-sub", sub));
    row.appendChild(main);
    var act = el("div", "lr-act");
    var bt = el("button", "", p.Disabled ? (ui.lang === "pl" ? "włącz" : "enable") : (ui.lang === "pl" ? "wyłącz" : "disable"));
    bt.addEventListener("click", async function () {
      try {
        await j("/api/providers", { method:"PUT", headers:{"Content-Type":"application/json"}, body:JSON.stringify({name:p.Name, disabled:!p.Disabled}) });
        await loadModels(); await renderProvidersList();
      } catch (e) { toast(e.message); }
    });
    act.appendChild(bt);
    var bs = el("button", "", t("common.scan"));
    bs.disabled = !!p.Disabled;
    bs.addEventListener("click", async function () {
      bs.textContent = "…";
      try {
        var res = await jpost("/api/provider/scan?name=" + encodeURIComponent(p.Name), {});
        toast(res.error ? res.error : providerModelCount(res.models) + " " + t("prov.models"));
      } catch (e) { toast(e.message); }
      renderProvidersList();
    });
    act.appendChild(bs);
    var be = el("button", "", t("common.edit"));
    be.addEventListener("click", function () { renderProviderForm(got.templates || [], p); });
    act.appendChild(be);
    var bx = el("button", "danger", t("common.remove"));
    bx.addEventListener("click", async function () {
      try {
        await fetch("/api/providers?name=" + encodeURIComponent(p.Name), { method: "DELETE" });
        renderProvidersList();
      } catch (e) { toast(e.message); }
    });
    act.appendChild(bx);
    row.appendChild(act);
    g.appendChild(row);
  });
  panelContent.appendChild(g);
}

function renderProviderForm(templates, existing) {
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var lbl = el("div", "g-label", existing ? t("prov.save") + ": " + existing.Name : t("prov.addNew"));
  var back = el("button", "g-act", "‹ " + t("common.back"));
  back.addEventListener("click", renderProvidersList);
  lbl.appendChild(back);
  g.appendChild(lbl);

  var form = el("form", "form-grid");
  form.autocomplete = "off";
  function field(labelText, name, type, ph, full) {
    var lab = el("label", full ? "fw" : "");
    lab.appendChild(el("span", "", labelText));
    var inp = document.createElement(type === "select" ? "select" : "input");
    inp.className = type === "select" ? "field-select" : "field-input";
    inp.name = name;
    if (type !== "select") { inp.type = type; inp.placeholder = ph || ""; }
    lab.appendChild(inp);
    form.appendChild(lab);
    return inp;
  }
  var nameI = field(t("prov.name"), "name", "text", "my-provider");
  var typeI = field(t("prov.type"), "type", "select");
  [["openai", "OpenAI Chat Completions"], ["responses", "OpenAI Responses API"], ["anthropic", "Anthropic Messages"], ["opencode", "OpenCode gateway"]].forEach(function (o) {
    var opt = el("option", "", o[1]); opt.value = o[0]; typeI.appendChild(opt);
  });
  var urlI = field(t("prov.baseUrl"), "base_url", "text", "http://localhost:8080/v1", true);
  var keyI = field(t("prov.apiKey") + " (" + (existing ? t("prov.keepKey") : t("prov.apiKeyHint")) + ")", "api_key", "password", "sk-…", true);
  keyI.autocomplete = "off";
  keyI.spellcheck = false;
  var initialKey = "";
  var keyLabel = keyI.parentNode;
  var keyWrap = el("div", "secret-field");
  keyLabel.insertBefore(keyWrap, keyI);
  keyWrap.appendChild(keyI);
  var keyToggle = el("button", "secret-toggle", t("prov.showKey"));
  keyToggle.type = "button";
  keyToggle.disabled = true;
  keyToggle.setAttribute("aria-pressed", "false");
  keyWrap.appendChild(keyToggle);
  var keyNote = el("span", "secret-note", existing ? t("prov.loadingKey") : "");
  keyLabel.appendChild(keyNote);

  function setKeyVisible(visible) {
    var show = !!visible && !!keyI.value;
    keyI.type = show ? "text" : "password";
    keyToggle.textContent = show ? t("prov.hideKey") : t("prov.showKey");
    keyToggle.setAttribute("aria-pressed", show ? "true" : "false");
  }
  function syncKeyControls() {
    keyToggle.disabled = keyI.disabled || !keyI.value;
    if (!keyI.value || keyI.disabled) setKeyVisible(false);
  }
  keyToggle.addEventListener("click", function () {
    setKeyVisible(keyI.type === "password");
  });
  keyI.addEventListener("input", syncKeyControls);

  var modelI = existing ? field(t("prov.model"), "model", "text", "") : null;
  var clearI = null;
  if (existing) {
    var lab = el("label", "fw");
    lab.style.flexDirection = "row";
    lab.style.alignItems = "center";
    lab.style.gap = "8px";
    clearI = document.createElement("input");
    clearI.type = "checkbox";
    lab.appendChild(clearI);
    lab.appendChild(el("span", "", t("prov.clearKey")));
    form.appendChild(lab);
    clearI.addEventListener("change", function () {
      keyI.disabled = clearI.checked;
      syncKeyControls();
    });
  }
  var submit = el("button", "btn primary fw", existing ? t("prov.save") : t("prov.addScan"));
  submit.type = "submit";
  submit.disabled = !!existing;
  form.appendChild(submit);
  var status = el("div", "note fw", "");
  form.appendChild(status);

  if (existing) {
    nameI.value = existing.Name; nameI.disabled = true;
    typeI.value = existing.Type || "openai";
    urlI.value = existing.BaseURL || "";
    if (modelI) modelI.value = existing.Model || "";
    keyI.disabled = true;
  }

  async function loadStoredKey(fallbackKey) {
    var hasFallback = arguments.length > 0;
    try {
      var revealed = await jpost("/api/provider/key/reveal", { name: existing.Name });
      initialKey = revealed.api_key || "";
      keyI.value = initialKey;
      keyNote.textContent = initialKey ? t("prov.keyLoaded") : t("prov.noKey");
    } catch (err) {
      // An unavailable reveal must never turn into an implicit clear. The PUT
      // path below omits api_key while this field stays unchanged.
      initialKey = hasFallback ? fallbackKey : "";
      keyI.value = initialKey;
      keyNote.textContent = t("prov.keyLoadFailed");
    } finally {
      keyI.disabled = !!(clearI && clearI.checked);
      submit.disabled = false;
      syncKeyControls();
    }
  }

  form.addEventListener("submit", async function (e) {
    e.preventDefault();
    status.textContent = "…";
    try {
      if (existing) {
        var body = { name: existing.Name, type: typeI.value, base_url: urlI.value.trim() };
        if (modelI) body.model = modelI.value.trim();
        if (clearI && clearI.checked) body.api_key = "";
        else if (keyI.value && keyI.value !== initialKey) body.api_key = keyI.value;
        var res = await j("/api/providers", {
          method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
        });
        if (Object.prototype.hasOwnProperty.call(body, "api_key")) {
          if (clearI) clearI.checked = false;
          // The backend strips paste wrappers such as "Bearer ". Reload only
          // through the local secret endpoint so the form mirrors what was
          // actually stored without exposing it in the general PUT response.
          await loadStoredKey(body.api_key);
        }
        status.textContent = res.scan_error ? "scan: " + res.scan_error : "OK · " + (providerModelCount(res.models) + " " + t("prov.models"));
      } else {
        var res2 = await jpost("/api/providers", {
          name: nameI.value.trim(), type: typeI.value, base_url: urlI.value.trim(), api_key: keyI.value, model: "",
        });
        toast(t("prov.added") + " " + providerModelCount(res2.models) + " " + t("prov.models"));
        await loadModels();
        await renderProvidersList();
      }
    } catch (err) {
      status.textContent = existing ? err.message : t("prov.addFailed") + ": " + err.message;
    }
  });
  g.appendChild(form);
  panelContent.appendChild(g);
  if (existing) loadStoredKey();

  if (!existing && templates.length) {
    var tg = el("div", "group");
    tg.appendChild(el("div", "g-label", t("prov.templates")));
    var grid = el("div", "tpl-grid");
    templates.forEach(function (tpl) {
      var b = el("button");
      b.type = "button";
      b.appendChild(el("span", "tt", tpl.Name));
      b.appendChild(el("span", "ts", tpl.Desc || tpl.BaseURL || ""));
      b.addEventListener("click", function () {
        grid.querySelectorAll("button").forEach(function (x) { x.classList.remove("sel"); });
        b.classList.add("sel");
        nameI.value = tpl.Name;
        typeI.value = tpl.Type || "openai";
        urlI.value = tpl.BaseURL || "";
      });
      grid.appendChild(b);
    });
    tg.appendChild(grid);
    panelContent.appendChild(tg);
  }
}

/* ── Accounts (Codex) ── */

var codexPollTimer = null;
sections.accounts = async function () {
  clearTimeout(codexPollTimer);
  var got;
  try { got = await j("/api/codex/accounts"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", t("acct.title")));
  var anyInProgress = (got.pending_logins || []).length > 0;
  (got.accounts || []).forEach(function (a) {
    if (a.login_in_progress) anyInProgress = true;
    var row = el("div", "list-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(a.label) + "</strong> " +
      (a.login_in_progress ? '<span class="note">' + escHtml(t("acct.loggingIn")) + "</span>"
        : a.logged_in ? '<span style="color:var(--ok)">●</span>'
        : '<span class="note">' + escHtml(t("acct.loggedOut")) + "</span>");
    main.appendChild(title);
    var sub = [a.email, a.plan_type, a.last_refresh ? "refreshed " + fmtWhen(a.last_refresh) : ""].filter(Boolean).join(" · ");
    if (a.login_error) sub = "login error: " + a.login_error;
    main.appendChild(el("div", "lr-sub", sub));
    if (a.limits) {
      main.appendChild(el("div", "lr-sub", "limits: " + JSON.stringify(a.limits)));
    }
    row.appendChild(main);
    var act = el("div", "lr-act");
    if (a.logged_in) {
      var br = el("button", "", t("acct.refreshTok"));
      br.addEventListener("click", async function () {
        try { await jpost("/api/codex/refresh", { label: a.label }); toast("OK"); } catch (e) { toast(e.message); }
        sections.accounts();
      });
      act.appendChild(br);
      var bo = el("button", "danger", t("acct.logout"));
      bo.addEventListener("click", async function () {
        try { await jpost("/api/codex/logout", { label: a.label }); } catch (e) { toast(e.message); }
        sections.accounts();
      });
      act.appendChild(bo);
    } else if (!a.login_in_progress) {
      var bl = el("button", "", t("acct.login"));
      bl.addEventListener("click", async function () {
        try { await jpost("/api/codex/login", { label: a.label }); } catch (e) { toast(e.message); }
        sections.accounts();
      });
      act.appendChild(bl);
    }
    row.appendChild(act);
    g.appendChild(row);
  });
  panelContent.appendChild(g);
  if (anyInProgress && currentSection === "accounts" && !overlay.hidden) {
    codexPollTimer = setTimeout(sections.accounts, 2000);
  }
};

/* ── MCP ── */

sections.mcp = function () { renderMcpList(); };

async function renderMcpList() {
  var got;
  try { got = await j("/api/mcp/servers"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  var portable = el("div", "group");
  var portableLabel = el("div", "g-label", t("mcp.portable"));
  var openPackages = el("button", "g-act", t("mcp.openPackages"));
  openPackages.addEventListener("click", async function () {
    try { await jpost("/api/mcp/folder", {}); }
    catch (e) { toast(e.message); }
  });
  portableLabel.appendChild(openPackages);
  portable.appendChild(portableLabel);
  portable.appendChild(el("div", "note mcp-portable-hint", t("mcp.portableHint")));
  var packages = got.packages || [];
  if (!packages.length) portable.appendChild(el("div", "note", t("mcp.noPackages")));
  packages.forEach(function (p) {
    var row = el("div", "list-row mcp-package-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(p.name || p.id) + "</strong>" +
      (p.version ? " <small>" + escHtml(p.version) + "</small>" : "");
    main.appendChild(title);
    if (p.description) main.appendChild(el("div", "lr-desc", p.description));
    main.appendChild(el("div", "lr-sub", p.manifest || "manifest.toml"));
    if (p.error) main.appendChild(el("div", "mcp-package-error", p.error));
    row.appendChild(main);
    var state = p.running ? t("mcp.running") : (p.available ? t("mcp.ready") : (p.enabled ? t("mcp.unavailable") : t("mcp.disabled")));
    var badge = el("span", "mcp-state " + (p.running ? "running" : (p.available ? "ready" : "error")), state);
    badge.title = p.running ? ((p.tools || 0) + " tools") : (p.available ? t("mcp.lazy") : (p.error || ""));
    row.appendChild(badge);
    portable.appendChild(row);
  });
  panelContent.appendChild(portable);

  var g = el("div", "group");
  var lbl = el("div", "g-label", t("mcp.servers"));
  var jb = el("button", "g-act", t("mcp.editJson"));
  jb.addEventListener("click", function () { renderMcpJSON(got.servers || []); });
  lbl.appendChild(jb);
  g.appendChild(lbl);
  var servers = got.servers || [];
  if (!servers.length) g.appendChild(el("div", "note", t("mcp.none")));
  servers.forEach(function (s) {
    var row = el("div", "list-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(s.name) + "</strong>";
    main.appendChild(title);
    main.appendChild(el("div", "lr-sub", s.command + " " + (s.args || []).join(" ")));
    row.appendChild(main);
    var act = el("div", "lr-act");
    var bx = el("button", "danger", t("common.remove"));
    bx.addEventListener("click", async function () {
      try { await jpost("/api/mcp/remove", { name: s.name }); } catch (e) { toast(e.message); }
      renderMcpList();
    });
    act.appendChild(bx);
    row.appendChild(act);
    g.appendChild(row);
  });
  panelContent.appendChild(g);

  var ga = el("div", "group");
  ga.appendChild(el("div", "g-label", t("mcp.addServer")));
  var form = el("form", "form-grid");
  form.autocomplete = "off";
  function fld(labelText, ph, full, tag) {
    var lab = el("label", full ? "fw" : "");
    lab.appendChild(el("span", "", labelText));
    var inp = document.createElement(tag || "input");
    inp.className = "field-input";
    inp.placeholder = ph || "";
    if (tag === "textarea") { inp.rows = 3; inp.style.fontFamily = "var(--mono)"; }
    lab.appendChild(inp);
    form.appendChild(lab);
    return inp;
  }
  var nameI = fld(t("prov.name"), "context7");
  var cmdI = fld(t("mcp.command"), "npx");
  var argsI = fld(t("mcp.args"), "-y, @upstash/context7-mcp", true);
  var envI = fld(t("mcp.env"), "KEY=value", true, "textarea");
  var submit = el("button", "btn primary fw", t("common.add"));
  submit.type = "submit";
  form.appendChild(submit);
  form.addEventListener("submit", async function (e) {
    e.preventDefault();
    var env = {};
    envI.value.split("\n").forEach(function (line) {
      var eq = line.indexOf("=");
      if (eq > 0) env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    });
    try {
      await jpost("/api/mcp/add", {
        name: nameI.value.trim(),
        command: cmdI.value.trim(),
        args: argsI.value.split(",").map(function (s) { return s.trim(); }).filter(Boolean),
        env: env,
      });
      renderMcpList();
    } catch (err) { toast(err.message); }
  });
  ga.appendChild(form);
  panelContent.appendChild(ga);
}

function renderMcpJSON(servers) {
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var lbl = el("div", "g-label", t("mcp.editJson"));
  var back = el("button", "g-act", "‹ " + t("mcp.backToList"));
  back.addEventListener("click", renderMcpList);
  lbl.appendChild(back);
  g.appendChild(lbl);
  var obj = {};
  servers.forEach(function (s) { obj[s.name] = { command: s.command, args: s.args || [], env: s.env || {} }; });
  var ta = el("textarea", "editor-area");
  ta.spellcheck = false;
  ta.value = JSON.stringify(obj, null, 2);
  g.appendChild(ta);
  var save = el("button", "btn primary", t("mcp.saveJson"));
  save.style.marginTop = "8px";
  var status = el("span", "note");
  status.style.marginLeft = "10px";
  save.addEventListener("click", async function () {
    var parsed;
    try { parsed = JSON.parse(ta.value); } catch (e) { status.textContent = "JSON: " + e.message; return; }
    try {
      // Remove servers not present anymore, then upsert the rest.
      var names = Object.keys(parsed);
      for (var i = 0; i < servers.length; i++) {
        if (names.indexOf(servers[i].name) < 0) await jpost("/api/mcp/remove", { name: servers[i].name });
      }
      for (var n = 0; n < names.length; n++) {
        var sc = parsed[names[n]] || {};
        await jpost("/api/mcp/add", { name: names[n], command: sc.command || "", args: sc.args || [], env: sc.env || {} });
      }
      status.textContent = "OK";
      renderMcpList();
    } catch (e) { status.textContent = e.message; }
  });
  g.appendChild(save);
  g.appendChild(status);
  panelContent.appendChild(g);
}

/* ── Memory ── */

sections.data = async function () {
  panelContent.innerHTML = "";
  panelContent.appendChild(el("div", "note", t("data.hint")));
  var status;
  try { status = await j("/api/data/status"); }
  catch (e) { panelContent.appendChild(el("div", "note", e.message)); return; }
  var facts = el("div", "data-facts");
  [["data.sessions", status.sessions], ["data.memories", status.memory_entries], ["data.goals", status.goals]].forEach(function (item) {
    var fact = el("div", "data-fact");
    fact.appendChild(el("span", "", t(item[0])));
    fact.appendChild(el("strong", "", fmtCompactNumber(item[1] || 0)));
    facts.appendChild(fact);
  });
  panelContent.appendChild(facts);
  if (status.import_pending) panelContent.appendChild(el("div", "data-pending", t("data.importPending")));

  function dataAction(title, hint, button) {
    var row = el("div", "data-action"), copy = el("div", "data-action-copy");
    copy.appendChild(el("strong", "", title)); copy.appendChild(el("span", "", hint));
    row.appendChild(copy); row.appendChild(button); return row;
  }
  var backup = el("div", "group data-group"); backup.appendChild(el("div", "g-label", t("data.backup")));
  var exportBtn = el("button", "btn", t("data.export")); exportBtn.type = "button";
  exportBtn.addEventListener("click", function () {
    var link = document.createElement("a"); link.href = "/api/data/export"; link.download = "";
    document.body.appendChild(link); link.click(); link.remove();
  });
  backup.appendChild(dataAction(t("data.export"), t("data.exportHint"), exportBtn));
  var fullExportBtn = el("button", "btn", t("data.exportFull")); fullExportBtn.type = "button";
  fullExportBtn.addEventListener("click", function () {
    var link = document.createElement("a"); link.href = "/api/data/export/full"; link.download = "";
    document.body.appendChild(link); link.click(); link.remove();
  });
  backup.appendChild(dataAction(t("data.exportFull"), t("data.exportFullHint"), fullExportBtn));
  var importBtn = el("button", "btn", t("data.import")); importBtn.type = "button";
  var importInput = document.createElement("input"); importInput.type = "file"; importInput.accept = ".zip,application/zip"; importInput.hidden = true;
  importBtn.addEventListener("click", function () { importInput.click(); });
  importInput.addEventListener("change", async function () {
    if (!importInput.files || !importInput.files[0]) return;
    var selectedFile = importInput.files[0];
    importBtn.disabled = true;
    var form = new FormData(); form.append("backup", selectedFile);
    try {
      var response = await fetch("/api/data/import", { method:"POST", body:form });
      if (!response.ok) throw new Error((await response.text()).trim() || String(response.status));
      toast(t("data.importReady")); await sections.data();
    } catch (e) { toast(e.message); importBtn.disabled = false; }
    finally { importInput.value = ""; }
  });
  var importRow = dataAction(t("data.import"), t("data.importHint"), importBtn); importRow.appendChild(importInput); backup.appendChild(importRow);
  panelContent.appendChild(backup);

  var danger = el("div", "group data-group danger-zone"); danger.appendChild(el("div", "g-label", t("data.danger")));
  function destructiveAction(labelKey, hintKey, action, after) {
    var button = el("button", "btn danger", t(labelKey)); button.type = "button"; button.disabled = streaming;
    button.addEventListener("click", async function () {
      if (!window.confirm(t(hintKey))) return;
      button.disabled = true;
      try { await jpost("/api/data/clear", { action:action }); toast(t("data.cleared")); if (after) after(); await sections.data(); }
      catch (e) { toast(e.message); button.disabled = false; }
    });
    danger.appendChild(dataAction(t(labelKey), t(hintKey), button));
  }
  destructiveAction("data.clearSessions", "data.confirmSessions", "sessions", function () { try { localStorage.removeItem(fileRewindStorageKey); } catch (e) {} newSession(); loadSessions(); renderStats(); });
  destructiveAction("data.clearMemory", "data.confirmMemory", "memory", function () {});
  panelContent.appendChild(danger);
};

sections.memory = async function () {
  var got;
  try { got = await j("/api/memory?limit=100"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", t("panel.memory")));
  if (!got || !got.length) g.appendChild(el("div", "note", t("mem.empty")));
  (got || []).forEach(function (m) {
    var row = el("div", "list-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title", m.content);
    title.style.whiteSpace = "normal";
    main.appendChild(title);
    main.appendChild(el("div", "lr-sub", [m.scope, (m.tags || []).join(","), fmtWhen(m.updated_at)].filter(Boolean).join(" · ")));
    row.appendChild(main);
    g.appendChild(row);
  });
  panelContent.appendChild(g);
};

/* ── Goal ── */

function goalStatus(status) { return t("goal.status." + status); }

async function goalMutate(button, body) {
  if (button) button.disabled = true;
  try {
    var got = await jpost("/api/goal", body);
    renderGoalPanel(got);
    if ($("#side-tab-goal").classList.contains("active")) renderSideGoal(got);
    toast(t("goal.updated"));
  } catch (e) {
    toast(e.message);
    if (button) button.disabled = false;
  }
}

function goalInput(placeholder, multiline) {
  var input = el(multiline ? "textarea" : "input", "field-input goal-input");
  input.placeholder = placeholder;
  if (!multiline) input.type = "text";
  return input;
}

function renderGoalCreate() {
  var empty = el("div", "goal-empty");
  empty.appendChild(el("div", "goal-empty-title", t("goal.none")));
  empty.appendChild(el("div", "note", t("goal.noneHint")));
  var form = el("form", "goal-create-form");
  var title = goalInput(t("goal.title"), false);
  title.required = true;
  var description = goalInput(t("goal.description"), true);
  var criteria = goalInput(t("goal.criteria"), true);
  var submit = el("button", "btn primary", t("goal.create"));
  submit.type = "submit";
  form.appendChild(title);
  form.appendChild(description);
  form.appendChild(criteria);
  form.appendChild(submit);
  form.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var value = title.value.trim();
    if (!value) { title.focus(); return; }
    goalMutate(submit, { action: "set", title: value, description: description.value.trim(),
      success_criteria: criteria.value.trim(), parent_session_id: activeSessionID });
  });
  empty.appendChild(form);
  panelContent.appendChild(empty);
  title.focus();
}

function goalTaskAction(label, task, status) {
  var button = el("button", "goal-task-action", label);
  button.type = "button";
  button.addEventListener("click", function () {
    goalMutate(button, { action: "set_task_status", task_seq: task.seq, status: status });
  });
  return button;
}

function renderGoalVerification(got) {
  var section = el("section", "group goal-verification status-" + (got.verification_status || "pending"));
  section.appendChild(el("div", "g-label", t("goal.verification")));
  var state = el("div", "goal-verification-state");
  var mark = el("span", "goal-verification-mark");
  state.appendChild(mark);
  var copy = el("div", "goal-verification-copy");
  if (!got.ready_for_verification) {
    copy.appendChild(el("div", "goal-verification-title", t("goal.verificationWaiting")));
    copy.appendChild(el("div", "goal-verification-hint", t("goal.finishBlocked")));
  } else if (got.verification_status === "passed") {
    copy.appendChild(el("div", "goal-verification-title", t("goal.verificationPassed")));
    if (got.verification_evidence) {
      copy.appendChild(el("div", "goal-verification-evidence", got.verification_evidence));
    }
  } else {
    copy.appendChild(el("div", "goal-verification-title",
      got.verification_status === "failed" ? t("goal.verificationFailed") : t("goal.awaitingVerification")));
    copy.appendChild(el("div", "goal-verification-hint", t("goal.verificationReady")));
    if (got.verification_evidence) {
      copy.appendChild(el("div", "goal-verification-evidence", got.verification_evidence));
    }
  }
  state.appendChild(copy);
  section.appendChild(state);

  if (got.ready_for_verification && got.verification_status !== "passed") {
    var form = el("form", "goal-verify-form");
    var evidence = goalInput(t("goal.verifyPlaceholder"), true);
    evidence.required = true;
    var actions = el("div", "goal-verify-actions");
    var pass = el("button", "btn primary", t("goal.verifyPass"));
    pass.type = "submit";
    var fail = el("button", "btn", t("goal.verifyFail"));
    fail.type = "button";
    actions.appendChild(pass);
    actions.appendChild(fail);
    form.appendChild(evidence);
    form.appendChild(actions);
    function verify(passed, button) {
      var value = evidence.value.trim();
      if (!value) { evidence.focus(); return; }
      goalMutate(button, { action: "verify", passed: passed, text: value });
    }
    form.addEventListener("submit", function (ev) { ev.preventDefault(); verify(true, pass); });
    fail.addEventListener("click", function () { verify(false, fail); });
    section.appendChild(form);
  }
  return section;
}

function renderGoalPanel(got) {
  panelContent.innerHTML = "";
  if (!got) {
    renderGoalCreate();
    return;
  }

  var head = el("section", "goal-overview");
  var copy = el("div", "goal-overview-copy");
  var eyebrow = el("div", "goal-eyebrow");
  eyebrow.appendChild(el("span", "goal-live-dot"));
  eyebrow.appendChild(document.createTextNode(goalStatus(got.status)));
  copy.appendChild(eyebrow);
  copy.appendChild(el("h3", "goal-title", got.title));
  if (got.description) {
    var desc = el("div", "goal-detail");
    desc.appendChild(el("span", "goal-detail-label", t("goal.descriptionLabel")));
    desc.appendChild(el("span", "", got.description));
    copy.appendChild(desc);
  }
  if (got.success_criteria) {
    var criteria = el("div", "goal-detail");
    criteria.appendChild(el("span", "goal-detail-label", t("goal.criteriaLabel")));
    criteria.appendChild(el("span", "", got.success_criteria));
    copy.appendChild(criteria);
  }
  head.appendChild(copy);
  var goalActions = el("div", "goal-head-actions");
  var finish = el("button", "btn primary", t("goal.finish"));
	finish.disabled = !got.can_finish;
	if (!got.can_finish) finish.title = t("goal.finishBlocked");
  finish.addEventListener("click", function () {
    if (window.confirm(t("goal.finishConfirm"))) goalMutate(finish, { action: "set_status", status: "done" });
  });
  var abandon = el("button", "btn danger", t("goal.abandon"));
  abandon.addEventListener("click", function () {
    if (window.confirm(t("goal.abandonConfirm"))) goalMutate(abandon, { action: "set_status", status: "abandoned" });
  });
  goalActions.appendChild(finish);
  goalActions.appendChild(abandon);
  head.appendChild(goalActions);
  panelContent.appendChild(head);

  var tasks = got.tasks || [];
  var terminal = tasks.filter(function (task) { return task.status === "done" || task.status === "skipped"; }).length;
  var group = el("section", "group goal-task-group");
  var label = el("div", "g-label", t("goal.tasks"));
  label.appendChild(el("span", "g-act", tasks.length ? terminal + " / " + tasks.length : ""));
  group.appendChild(label);
  if (tasks.length) {
    var progress = el("div", "goal-progress");
    var bar = el("span");
    bar.style.width = (terminal * 100 / tasks.length) + "%";
    progress.appendChild(bar);
    progress.setAttribute("aria-label", t("goal.progress") + ": " + terminal + " / " + tasks.length);
    group.appendChild(progress);
  }
  if (!tasks.length) group.appendChild(el("div", "goal-no-tasks", t("goal.noTasks")));
  tasks.forEach(function (task) {
    var row = el("div", "goal-task status-" + task.status);
    var mark = task.status === "done" ? "✓" : (task.status === "skipped" ? "—" : (task.status === "in_progress" ? "●" : "○"));
    row.appendChild(el("span", "goal-task-mark", mark));
    var taskCopy = el("div", "goal-task-copy");
    taskCopy.appendChild(el("div", "goal-task-title", task.title));
    taskCopy.appendChild(el("div", "goal-task-status", goalStatus(task.status)));
    row.appendChild(taskCopy);
    var actions = el("div", "goal-task-actions");
    if (task.status === "pending") actions.appendChild(goalTaskAction(t("goal.start"), task, "in_progress"));
    if (task.status === "pending" || task.status === "in_progress") {
      actions.appendChild(goalTaskAction(t("goal.complete"), task, "done"));
      actions.appendChild(goalTaskAction(t("goal.skip"), task, "skipped"));
    } else {
      actions.appendChild(goalTaskAction(t("goal.reopen"), task, "pending"));
    }
    row.appendChild(actions);
    group.appendChild(row);
  });
  var addForm = el("form", "goal-inline-form");
  var taskInput = goalInput(t("goal.taskPlaceholder"), false);
  var add = el("button", "btn", t("goal.addTask"));
  add.type = "submit";
  addForm.appendChild(taskInput);
  addForm.appendChild(add);
  addForm.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var value = taskInput.value.trim();
    if (!value) { taskInput.focus(); return; }
    goalMutate(add, { action: "add_task", title: value });
  });
  group.appendChild(addForm);
  panelContent.appendChild(group);
	panelContent.appendChild(renderGoalVerification(got));

  var notes = el("section", "group goal-notes");
  notes.appendChild(el("div", "g-label", t("goal.notes")));
  if (got.notes) notes.appendChild(el("div", "goal-notes-history", got.notes));
  var noteForm = el("form", "goal-inline-form");
  var noteInput = goalInput(t("goal.notePlaceholder"), false);
  var addNote = el("button", "btn", t("goal.addNote"));
  addNote.type = "submit";
  noteForm.appendChild(noteInput);
  noteForm.appendChild(addNote);
  noteForm.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var value = noteInput.value.trim();
    if (!value) { noteInput.focus(); return; }
    goalMutate(addNote, { action: "add_note", text: value });
  });
  notes.appendChild(noteForm);
  panelContent.appendChild(notes);
}

sections.goal = async function () {
  try { renderGoalPanel(await j("/api/goal")); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
  }
};

/* ── Usage (stats + context report) ── */

function usageLeadMetric(label, value, meta, extraClass) {
  var metric = el("div", "usage-lead-metric" + (extraClass ? " " + extraClass : ""));
  metric.appendChild(el("div", "usage-lead-label", label));
  var main = el("div", "usage-lead-value", value);
  main.title = value;
  metric.appendChild(main);
  if (meta) metric.appendChild(el("div", "usage-lead-meta", meta));
  metric.setAttribute("aria-label", label + ": " + value + (meta ? ", " + meta : ""));
  return metric;
}
function usageFact(label, value, note) {
  var item = el("div", "usage-fact");
  item.appendChild(el("dt", "usage-fact-label", label));
  var dd = el("dd", "usage-fact-value", value == null || value === "" ? "—" : String(value));
  dd.title = dd.textContent;
  item.appendChild(dd);
  if (note) item.appendChild(el("div", "usage-fact-note", note));
  return item;
}
function messageBreakdown(session) {
  return fmtInteger(session.userMessages) + " " + t("stats.userMessages") + " · " +
    fmtInteger(session.assistantMessages) + " " + t("stats.assistantMessages") + " · " +
    fmtInteger(session.toolMessages) + " " + t("stats.toolMessages");
}
function contextItems(context) {
  return [
    { key: "user", label: t("context.user"), value: context.breakdown.user },
    { key: "assistant", label: t("context.assistant"), value: context.breakdown.assistant },
    { key: "tools", label: t("context.tools"), value: context.breakdown.tools },
    { key: "other", label: t("context.other"), value: context.breakdown.other },
  ];
}
function normalizedContext(raw) {
  raw = raw || {};
  var breakdown = raw.breakdown || {};
  return {
    window: statNumber(raw.window), estimatedUsed: statNumber(raw.estimated_used, raw.estimatedUsed),
    percent: Math.max(0, Math.min(100, statNumber(raw.percent))),
    breakdown: {
      user: statNumber(breakdown.user), assistant: statNumber(breakdown.assistant),
      tools: statNumber(breakdown.tools), other: statNumber(breakdown.other),
    },
  };
}
function contextState(percent) {
  if (percent >= 80) return { key: "context.compactionRecommended", cls: "danger" };
  if (percent >= 60) return { key: "context.approaching", cls: "warning" };
  return { key: "context.safe", cls: "safe" };
}
function renderContextInspector(context) {
  var section = el("section", "usage-section usage-context");
  var head = el("div", "usage-section-head");
  head.appendChild(el("h3", "usage-section-title", t("stats.currentContext")));
  if (context.window > 0) {
    head.appendChild(el("div", "usage-section-value", context.percent + "% · " +
      fmtInteger(context.estimatedUsed) + " / " + fmtInteger(context.window)));
  }
  section.appendChild(head);

  var items = contextItems(context);
  var total = items.reduce(function (sum, item) { return sum + item.value; }, 0);
  if (total <= 0) {
    section.appendChild(el("div", "usage-empty", t("usage.contextEmpty")));
    return section;
  }

  var state = contextState(context.percent);
  var status = el("div", "context-status " + state.cls);
  status.appendChild(el("span", "context-status-dot"));
  status.appendChild(el("strong", "", t(state.key)));
  section.appendChild(status);

  var ariaParts = [t("stats.currentContext") + " " + context.percent + "%", t(state.key)];
  var meter = el("div", "context-budget");
  meter.setAttribute("role", "img");
  var used = el("div", "context-used");
  used.style.width = context.percent + "%";
  items.forEach(function (item) {
    var pct = item.value * 100 / total;
    ariaParts.push(item.label + " " + Math.round(pct) + "%");
    if (item.value <= 0) return;
    var segment = el("span", "context-segment context-" + item.key);
    segment.style.width = pct + "%";
    segment.title = item.label + ": " + fmtInteger(item.value) + " (" + Math.round(pct) + "%)";
    used.appendChild(segment);
  });
  meter.appendChild(used);
  [60, 80].forEach(function (threshold) {
    var marker = el("span", "context-threshold threshold-" + threshold);
    marker.style.left = threshold + "%";
    marker.title = threshold + "% \u00b7 " + t(threshold === 60 ? "context.pruneThreshold" : "context.compactThreshold");
    meter.appendChild(marker);
  });
  meter.setAttribute("aria-label", ariaParts.join(". "));
  section.appendChild(meter);

  var thresholdLegend = el("div", "context-threshold-legend");
  thresholdLegend.appendChild(el("span", "", "60% \u00b7 " + t("context.pruneThreshold")));
  thresholdLegend.appendChild(el("span", "", "80% \u00b7 " + t("context.compactThreshold")));
  section.appendChild(thresholdLegend);

  var legend = el("div", "context-legend");
  items.forEach(function (item) {
    var pct = total > 0 ? Math.round(item.value * 100 / total) : 0;
    var entry = el("div", "context-legend-item");
    entry.appendChild(el("span", "context-dot context-" + item.key));
    entry.appendChild(el("span", "context-name", item.label));
    entry.appendChild(el("span", "context-value", pct + "% · " + fmtCompactNumber(item.value)));
    legend.appendChild(entry);
  });
  section.appendChild(legend);
  var footer = el("div", "context-actions");
  footer.appendChild(el("div", "usage-caption", t("context.compactHint")));
  var compact = el("button", "btn context-compact", t("context.compactNow"));
  compact.type = "button";
  compact.disabled = !activeSessionID;
  compact.title = t("context.compactHint");
  compact.addEventListener("click", async function () {
    if (!activeSessionID) { toast(t("context.selectSession")); return; }
    if (streaming) { toast(t("context.busy")); return; }
    compact.disabled = true;
    compact.classList.add("busy");
    compact.textContent = t("context.compacting");
    try {
      var result = await jpost("/api/context/compact", { session_id: activeSessionID });
      var next = normalizedContext(result.context);
      section.replaceWith(renderContextInspector(next));
      toast(t("context.compacted").replace("{n}", fmtInteger(result.removed)));
    } catch (err) {
      toast(err.message);
      compact.disabled = false;
      compact.classList.remove("busy");
      compact.textContent = t("context.compactNow");
    }
  });
  footer.appendChild(compact);
  section.appendChild(footer);
  return section;
}
function priceField(label, name, value, required, hint) {
  var wrap = el("label", "price-field");
  wrap.appendChild(el("span", "price-field-label", label));
  var input = el("input", "field-input");
  input.type = "number";
  input.name = name;
  input.min = "0";
  input.max = "1000000";
  input.step = "0.000001";
  input.inputMode = "decimal";
  input.required = !!required;
  input.value = value === null ? (required ? "" : "0") : String(value);
  input.setAttribute("aria-label", label + " · " + t("price.unit"));
  wrap.appendChild(input);
  if (hint) wrap.appendChild(el("span", "price-field-hint", hint));
  return { root: wrap, input: input };
}
function priceValue(input, optional) {
  var raw = input.value.trim();
  if (raw === "") return optional ? 0 : NaN;
  return Number(raw);
}
function validPrice(value) {
  return Number.isFinite(value) && value >= 0 && value <= 1000000;
}
function renderPriceDetails(stats) {
  var cost = stats.cost;
  var state = normalizedCostState(cost);
  var details = el("details", "usage-rates");
  details.open = state === "unknown" || state === "partial";
  details.appendChild(el("summary", "usage-rates-summary", t("price.title")));
  var body = el("div", "usage-rates-body");

  var rates = el("dl", "price-rates");
  rates.appendChild(usageFact(t("cost.source"), costSourceLabel(cost.source) || "—"));
  rates.appendChild(usageFact(t("price.input"), cost.inputPerMillion === null ? "—" : fmtMoney(cost.inputPerMillion, cost.currency, true)));
  rates.appendChild(usageFact(t("price.cache"), cost.cachedInputPerMillion === null ? "—" : fmtMoney(cost.cachedInputPerMillion, cost.currency, true)));
  rates.appendChild(usageFact(t("price.output"), cost.outputPerMillion === null ? "—" : fmtMoney(cost.outputPerMillion, cost.currency, true)));
  body.appendChild(rates);
  var coverage = costCoverage(cost);
  if (coverage) body.appendChild(el("div", "price-note", coverage));
  if (stats.tokens.cachedInput > 0 && !cost.cacheDiscountKnown && ["estimated", "manual", "partial"].indexOf(state) >= 0) {
    body.appendChild(el("div", "price-note warning", t("cost.cacheUnknown")));
  }

  var form = el("form", "manual-price-form");
  form.setAttribute("aria-label", t("price.title"));
  form.appendChild(el("p", "price-hint", t("price.hint")));
  var identity = el("div", "price-identity");
  identity.appendChild(el("code", "", (stats.session.provider || "—") + " / " + (stats.session.model || stats.model || "—")));
  form.appendChild(identity);
  var fields = el("div", "price-fields");
  var inputField = priceField(t("price.input"), "input_per_million", cost.inputPerMillion, true);
  var cacheField = priceField(t("price.cache"), "cached_input_per_million", cost.cachedInputPerMillion, false, t("price.cacheHint"));
  var outputField = priceField(t("price.output"), "output_per_million", cost.outputPerMillion, true);
  fields.appendChild(inputField.root);
  fields.appendChild(cacheField.root);
  fields.appendChild(outputField.root);
  form.appendChild(fields);

  var actions = el("div", "price-actions");
  var save = el("button", "btn primary", t("price.save"));
  save.type = "submit";
  actions.appendChild(save);
  var remove = null;
  if (cost.manual || cost.state === "manual") {
    remove = el("button", "btn danger", t("price.remove"));
    remove.type = "button";
    actions.appendChild(remove);
  }
  var status = el("span", "price-status");
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  actions.appendChild(status);
  form.appendChild(actions);

  var provider = stats.session.provider || "";
  var model = stats.session.model || stats.model || "";
  if (!provider || !model) {
    save.disabled = true;
    status.textContent = t("price.identityMissing");
  }
  function setPriceBusy(busy) {
    save.disabled = busy || !provider || !model;
    if (remove) remove.disabled = busy;
    inputField.input.disabled = busy;
    cacheField.input.disabled = busy;
    outputField.input.disabled = busy;
  }
  form.addEventListener("submit", async function (e) {
    e.preventDefault();
    var input = priceValue(inputField.input, false);
    var cached = priceValue(cacheField.input, true);
    var output = priceValue(outputField.input, false);
    if (![input, cached, output].every(validPrice)) {
      status.textContent = t("price.invalid");
      return;
    }
    setPriceBusy(true);
    status.textContent = t("common.loading");
    try {
      await jpost("/api/model/price", {
        provider: provider, model: model, input_per_million: input,
        cached_input_per_million: cached, output_per_million: output,
      });
      toast(t("price.saved"));
      await sections.usage();
      renderStats();
    } catch (err) {
      status.textContent = err.message;
      setPriceBusy(false);
    }
  });
  if (remove) {
    remove.addEventListener("click", async function () {
      if (!window.confirm(t("price.removeConfirm"))) return;
      setPriceBusy(true);
      status.textContent = t("common.loading");
      try {
        await jpost("/api/model/price", { provider: provider, model: model, remove: true });
        toast(t("price.removed"));
        await sections.usage();
        renderStats();
      } catch (err) {
        status.textContent = err.message;
        setPriceBusy(false);
      }
    });
  }
  body.appendChild(form);
  details.appendChild(body);
  return details;
}
function renderUsageInspector(stats) {
  var root = el("div", "usage-inspector");
  root.setAttribute("role", "region");
  root.setAttribute("aria-label", t("panel.usage"));
  if (!stats.session.id) root.appendChild(el("div", "usage-empty top", t("usage.noSession")));

  var lead = el("div", "usage-lead");
  lead.appendChild(usageLeadMetric(t("stats.totalTokens"), fmtInteger(stats.tokens.total), t("usage.session"), "tokens"));
  var costMetric = usageLeadMetric(t("stats.totalCost"), costPrimary(stats.cost), costMeta(stats.cost),
    "cost cost-state-" + normalizedCostState(stats.cost));
  var coverage = costCoverage(stats.cost);
  if (coverage) costMetric.appendChild(el("div", "usage-lead-coverage", coverage));
  lead.appendChild(costMetric);
  root.appendChild(lead);

  root.appendChild(renderPerformanceTelemetry(stats.telemetry));

  var factsSection = el("section", "usage-section");
  factsSection.appendChild(el("h3", "usage-section-title", t("usage.details")));
  var facts = el("dl", "usage-facts");
  facts.appendChild(usageFact(t("stats.provider"), stats.session.provider || "—", stats.session.providerType));
  facts.appendChild(usageFact(t("stats.model"), stats.session.model || stats.model || "—"));
  facts.appendChild(usageFact(t("stats.inputTokens"), fmtInteger(stats.tokens.input)));
  facts.appendChild(usageFact(t("stats.outputTokens"), fmtInteger(stats.tokens.output)));
  if (stats.tokens.hasCached) {
    facts.appendChild(usageFact(t("stats.evaluatedInput"), fmtInteger(stats.tokens.evaluatedInput)));
    facts.appendChild(usageFact(t("stats.cachedInput"), fmtInteger(stats.tokens.cachedInput)));
  }
  if (stats.tokens.hasReasoning) facts.appendChild(usageFact(t("stats.reasoningTokens"), fmtInteger(stats.tokens.reasoning)));
  facts.appendChild(usageFact(t("stats.calls"), fmtInteger(stats.cost.calls)));
  facts.appendChild(usageFact(t("stats.messages"), fmtInteger(stats.session.messages), messageBreakdown(stats.session)));
  facts.appendChild(usageFact(t("stats.toolCalls"), fmtInteger(stats.session.toolCalls)));
  facts.appendChild(usageFact(t("usage.daily"), fmtInteger(stats.dailyTokens)));
  facts.appendChild(usageFact(t("stats.contextWindow"), stats.context.window > 0 ? fmtInteger(stats.context.window) : "—"));
  facts.appendChild(usageFact(t("stats.created"), fmtDateTime(stats.session.createdAt)));
  facts.appendChild(usageFact(t("stats.updated"), fmtDateTime(stats.session.updatedAt)));
  factsSection.appendChild(facts);
  root.appendChild(factsSection);

  root.appendChild(renderContextInspector(stats.context));
  root.appendChild(renderPriceDetails(stats));
  return root;
}

function renderPerformanceTelemetry(telemetry) {
  var section = el("section", "usage-section telemetry-section");
  var head = el("div", "usage-section-head");
  head.appendChild(el("h3", "usage-section-title", t("telemetry.title")));
  if (telemetry.samples) {
    var sampleLabel = fmtInteger(telemetry.samples) + " " + t("telemetry.samples");
    if (telemetry.scope) sampleLabel += " · " + t("telemetry.scope." + telemetry.scope);
    head.appendChild(el("div", "usage-section-value", sampleLabel));
  }
  section.appendChild(head);
  if (!telemetry.samples) {
    section.appendChild(el("div", "usage-empty", t("telemetry.empty")));
    return section;
  }

  var verdict = el("div", "telemetry-verdict");
  verdict.appendChild(el("strong", "", t("telemetry.bottleneck." + telemetry.bottleneck)));
  verdict.appendChild(el("span", "", " " + fmtInteger(telemetry.bottleneckShare) + "%"));
  section.appendChild(verdict);

  var pipeline = telemetry.modelMS + telemetry.toolsMS + telemetry.cliMS;
  var split = el("div", "telemetry-split");
  [["model", telemetry.modelMS], ["tools", telemetry.toolsMS], ["cli", telemetry.cliMS]].forEach(function (part) {
    var seg = el("span", "telemetry-segment telemetry-" + part[0]);
    seg.style.width = (pipeline > 0 ? part[1] * 100 / pipeline : 0) + "%";
    split.appendChild(seg);
  });
  section.appendChild(split);

  var facts = el("dl", "usage-facts telemetry-facts");
  facts.appendChild(usageFact(t("telemetry.model"), fmtDuration(telemetry.modelMS)));
  facts.appendChild(usageFact(t("telemetry.tools"), fmtDuration(telemetry.toolsMS)));
  facts.appendChild(usageFact(t("telemetry.cli"), fmtDuration(telemetry.cliMS)));
  facts.appendChild(usageFact(t("telemetry.average"), fmtDuration(telemetry.averageMS)));
  facts.appendChild(usageFact(t("telemetry.steps"), fmtInteger(telemetry.steps)));
  if (telemetry.persistMS) facts.appendChild(usageFact(t("telemetry.persist"), fmtDuration(telemetry.persistMS)));
  if (telemetry.tools.length) {
    facts.appendChild(usageFact(t("telemetry.topTools"), telemetry.tools.map(function (tool) {
      return tool.name + " " + fmtDuration(statNumber(tool.duration_ms));
    }).join(" · ")));
  }
  section.appendChild(facts);

  if (telemetry.signals.length) {
    var notes = el("div", "telemetry-signals");
    telemetry.signals.forEach(function (signal) {
      notes.appendChild(el("p", "", t("telemetry.signal." + signal)));
    });
    section.appendChild(notes);
  }
  return section;
}

var usageRenderSeq = 0;
sections.usage = async function () {
  var seq = ++usageRenderSeq;
  var requestSession = activeSessionID;
  panelContent.innerHTML = "";
  panelContent.setAttribute("aria-busy", "true");
  panelContent.appendChild(el("div", "usage-loading", t("common.loading")));
  try {
    var stats = normalizeStats(await j(statsURL()));
    if (seq !== usageRenderSeq || currentSection !== "usage" || requestSession !== activeSessionID) return;
    panelContent.innerHTML = "";
    panelContent.appendChild(renderUsageInspector(stats));
  } catch (e) {
    if (seq === usageRenderSeq && currentSection === "usage") {
      panelContent.innerHTML = "";
      panelContent.appendChild(el("div", "usage-empty top", t("common.error") + ": " + e.message));
    }
  }
  if (seq === usageRenderSeq && currentSection === "usage") panelContent.setAttribute("aria-busy", "false");
};

/* ── Files ── */

var filesCwd = "";
sections.files = function () { renderFiles(filesCwd); };

async function renderFiles(dir) {
  var got;
  try { got = await j("/api/files" + (dir ? "?dir=" + encodeURIComponent(dir) : "")); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  filesCwd = got.dir;
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var lbl = el("div", "g-label", t("panel.files"));
  var up = el("button", "g-act", "↑ " + t("files.up"));
  up.addEventListener("click", function () {
    var parent = filesCwd.replace(/[\\/][^\\/]+$/, "");
    renderFiles(parent);
  });
  lbl.appendChild(up);
  g.appendChild(lbl);
  g.appendChild(el("div", "file-path", got.dir));
  (got.files || []).sort(function (a, b) {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
    return a.name.localeCompare(b.name);
  }).forEach(function (f) {
    var b = el("button", "file-row");
    b.type = "button";
    b.appendChild(el("span", "fi", f.is_dir ? "▸" : "·"));
    b.appendChild(el("span", "", f.name));
    if (!f.is_dir) b.appendChild(el("span", "fs", fmtSize(f.size)));
    b.addEventListener("click", function () {
      var sep = filesCwd.indexOf("\\") >= 0 ? "\\" : "/";
      var p = filesCwd + sep + f.name;
      if (f.is_dir) renderFiles(p);
      else openEditor(p);
    });
    g.appendChild(b);
  });
  panelContent.appendChild(g);
}

async function openEditor(path) {
  var got;
  try { got = await j("/api/file/read?path=" + encodeURIComponent(path)); } catch (e) {
    toast(e.message);
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var lbl = el("div", "g-label", path.split(/[\\/]/).pop());
  var back = el("button", "g-act", "‹ " + t("common.back"));
  back.addEventListener("click", function () { renderFiles(filesCwd); });
  lbl.appendChild(back);
  g.appendChild(lbl);
  g.appendChild(el("div", "file-path", got.path));
  var ta = el("textarea", "editor-area");
  ta.spellcheck = false;
  ta.value = got.content;
  g.appendChild(ta);
  var save = el("button", "btn primary", t("files.save"));
  save.style.marginTop = "8px";
  var status = el("span", "note");
  status.style.marginLeft = "10px";
  save.addEventListener("click", async function () {
    try {
      await jpost("/api/file/write", { path: got.path, content: ta.value });
      status.textContent = t("files.saved");
    } catch (e) { status.textContent = e.message; }
  });
  g.appendChild(save);
  g.appendChild(status);
  panelContent.appendChild(g);
}

/* ── About + keybinds ── */

sections.about = async function () {
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", "SuperCli"));
  g.appendChild(el("div", "note", t("about.desc")));
  panelContent.appendChild(g);

  var gi = el("div", "group");
  var h = await checkHealth();
  [[t("about.workspace"), h ? h.home : "—"], [t("about.model"), h ? h.model : "—"]].forEach(function (pair) {
    var row = el("div", "toggle-row");
    row.appendChild(el("span", "", pair[0]));
    var v = el("span", "note", pair[1]);
    v.style.fontFamily = "var(--mono)";
    row.appendChild(v);
    gi.appendChild(row);
  });
  panelContent.appendChild(gi);

  var gk = el("div", "group");
  gk.appendChild(el("div", "g-label", t("about.shortcuts")));
  gk.appendChild(el("div", "note", t("about.rebindHint")));
  var fixed = [["Enter", t("kb.send")], ["Shift+Enter", t("kb.newline")], ["Esc", t("kb.close")]];
  fixed.forEach(function (pair) {
    var row = el("div", "toggle-row");
    row.appendChild(el("span", "", pair[1]));
    var k = el("kbd", "", pair[0]);
    k.style.cursor = "default";
    row.appendChild(k);
    gk.appendChild(row);
  });
  [["focus", t("kb.focus")], ["panel", t("kb.panel")], ["sidebar", t("kb.sidebar")], ["thinking", t("kb.thinking")], ["tools", t("kb.tools")]].forEach(function (pair) {
    var row = el("div", "toggle-row");
    row.appendChild(el("span", "", pair[1]));
    var k = el("kbd", "", ui.keybinds[pair[0]] || "—");
    k.title = t("about.rebindHint");
    k.addEventListener("click", function () {
      k.classList.add("recording");
      k.textContent = "…";
      function capture(e) {
        e.preventDefault();
        e.stopPropagation();
        if (e.key === "Shift" || e.key === "Control" || e.key === "Alt" || e.key === "Meta") return;
        var combo = comboOf(e);
        ui.keybinds[pair[0]] = combo;
        saveUI();
        k.classList.remove("recording");
        k.textContent = combo;
        document.removeEventListener("keydown", capture, true);
      }
      document.addEventListener("keydown", capture, true);
    });
    row.appendChild(k);
    gk.appendChild(row);
  });
  panelContent.appendChild(gk);
};

/* ═══ keyboard ═══ */

function comboOf(e) {
  var parts = [];
  if (e.ctrlKey) parts.push("Ctrl");
  if (e.altKey) parts.push("Alt");
  if (e.shiftKey) parts.push("Shift");
  var k = e.key.length === 1 ? e.key.toUpperCase() : e.key;
  if (k === " ") k = "Space";
  parts.push(k);
  return parts.join("+");
}
document.addEventListener("keydown", function (e) {
  if (e.key === "Escape") {
    if (!overlay.hidden) { closePanel(); return; }
    if (!$("#reasoning-menu").hidden) { toggleReasoningMenu(false); $("#reasoning-btn").focus(); return; }
    if (!palette.hidden) { togglePalette(false); return; }
    return;
  }
  var combo = comboOf(e);
  var typing = /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName);
  if (combo === ui.keybinds.panel) { e.preventDefault(); overlay.hidden ? openPanel() : closePanel(); return; }
  if (combo === ui.keybinds.sidebar) { e.preventDefault(); toggleSidebar(); return; }
  if (typing) return;
  if (combo === ui.keybinds.focus) { e.preventDefault(); promptEl.focus(); return; }
  if (combo === ui.keybinds.thinking) {
    e.preventDefault();
    var blocks = $$(".think-block");
    var anyOpen = blocks.some(function (b) { return b.open; });
    blocks.forEach(function (b) { b.open = !anyOpen; });
    return;
  }
  if (combo === ui.keybinds.tools) {
    e.preventDefault();
    var rows = $$(".tool-row");
    var anyOpenT = rows.some(function (r) { return r.open; });
    rows.forEach(function (r) { r.open = !anyOpenT; });
  }
});

/* ═══ init ═══ */

(async function init() {
  await loadUI();
  applyI18n();
  var h = await checkHealth();
  if (h) {
    if (h.model) loadReasoning();
    loadModels();
  }
  loadSessions();
  loadProjects();
	loadPromptQueue();
  renderStats();
  setInterval(checkHealth, 30000);
  promptEl.focus();
})();
