// SuperCli Web GUI — OpenCode-inspired front-end
// Vanilla ES5-compatible JS, no build step, served from embedded Go assets.
// Talks JSON + SSE to the Go backend.
"use strict";

// ── Shortcuts ──
const $  = (s, p) => (p || document).querySelector(s);
const $$ = (s, p) => Array.from((p || document).querySelectorAll(s));
const el = (tag, cls) => { const e = document.createElement(tag); if (cls) e.className = cls; return e; };

// ── Globals ──
let streaming  = false;
let abortController = null;
let modelCache = [];
let activePanel = "activity";
let activeSessionID = "";
var runStartTime = 0;
var runTimerInterval = null;

// ── Server-side settings persistence ──
// The GUI opens in an app-mode browser at http://127.0.0.1:<port>/ where
// the port is OS-assigned fresh on every launch. Because localStorage is
// partitioned by origin (which includes the port), every restart hands the
// page a brand-new empty bucket — so theme/font/keybind choices appear not
// to persist. To fix that we mirror the localStorage UI keys to the Go
// backend (/api/settings), which stores them in the data dir, and hydrate
// them back into localStorage at boot regardless of the port.
var SETTINGS_KEYS = ["supercli-settings", "supercli-theme", "supercli-keybinds", "supercli-last-model"];

// pushSettings serializes the tracked localStorage keys and POSTs them to
// the backend so they survive a restart. Fire-and-forget; failures are
// non-fatal (localStorage still serves the current session).
function pushSettings() {
  var blob = {};
  for (var i = 0; i < SETTINGS_KEYS.length; i++) {
    try { var v = localStorage.getItem(SETTINGS_KEYS[i]); if (v !== null) blob[SETTINGS_KEYS[i]] = v; } catch (e) {}
  }
  try {
    fetch("/api/settings", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(blob) }).catch(function(){});
  } catch (e) {}
}

// hydrateFromServer pulls the persisted blob and writes any missing/changed
// keys back into localStorage, then re-applies theme + fonts so a fresh
// browser bucket reflects the user's saved choices. Returns a promise.
function hydrateFromServer() {
  return fetch("/api/settings").then(function(r) { return r.json(); }).then(function(j) {
    var s = j && j.settings;
    if (!s || typeof s !== "object") return;
    for (var i = 0; i < SETTINGS_KEYS.length; i++) {
      var k = SETTINGS_KEYS[i];
      if (typeof s[k] === "string") { try { localStorage.setItem(k, s[k]); } catch (e) {} }
    }
    // Re-apply visual settings now that localStorage is populated.
    try { var theme = localStorage.getItem("supercli-theme") || "dark"; if (typeof applyTheme === "function") applyTheme(theme); } catch (e) {}
    try {
      var st = JSON.parse(localStorage.getItem("supercli-settings") || "{}");
      if (typeof applyFonts === "function") applyFonts(st);
      // Re-apply persisted language so the UI matches the saved choice.
      if (typeof setLang === "function") setLang((st.lang === "pl" || st.lang === "en") ? st.lang : "en");
    } catch (e) {}
    try { if (typeof loadKeybinds === "function") loadKeybinds(); } catch (e) {}
  }).catch(function(){});
}

// ── Utilities ──
function escHtml(s) {
  return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escAttr(s) {
  return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// ── Smart Scroll ──
// Sticky scroll-to-bottom for the transcript: it follows streaming output
// only while the user is already at (or near) the bottom. Scrolling up to
// read unsticks it; scrolling back down re-arms it. force=true (your own
// prompt, session load) always jumps. The double rAF measures scrollHeight
// after the DOM update has actually been laid out — writing scrollTop
// before layout is what used to leave the view a strip above the bottom.
var stickToBottom = true;
function isNearBottom(container) {
  return container.scrollHeight - container.scrollTop - container.clientHeight < 48;
}
function smartScroll(container, force) {
  if (!container) return;
  var sticky = container.id === "transcript";
  if (sticky) {
    if (force) stickToBottom = true;
    if (!stickToBottom) return;
  }
  requestAnimationFrame(function() {
    container.scrollTop = container.scrollHeight;
    // Second pass after paint — content may have grown meanwhile.
    requestAnimationFrame(function() {
      if (!sticky || stickToBottom) container.scrollTop = container.scrollHeight;
    });
  });
}

// ── Duration formatting ──
function formatDuration(ms) {
  if (ms < 1000) return ms + "ms";
  var s = ms / 1000;
  if (s < 60) return s.toFixed(1) + "s";
  var m = Math.floor(s / 60);
  var rem = (s % 60).toFixed(0);
  return m + "m" + rem + "s";
}

// ── i18n (lightweight, no framework) ──
// Bilingual PL/EN. Static markup is tagged with data-i18n / data-i18n-ph /
// data-i18n-title and translated at runtime by applyI18n(). Dynamic strings
// rendered from JS are wrapped in t("…"). The active language lives in the
// "supercli-settings" blob (key: lang) so it persists via the existing
// pushSettings()/hydrateFromServer() flow. t() falls back: pl → en → key.
var I18N = {
  en: {
    // rail tooltips
    "rail.nav": "Navigation rail",
    "rail.chat": "Chat",
    "rail.sessions": "Sessions",
    "rail.models": "Models",
    "rail.providers": "Providers",
    "rail.memory": "Memory",
    "rail.goal": "Goal",
    "rail.projects": "Projects",
    "rail.settings": "Settings",
    // sidebar
    "sidebar.newSession": "New session",
    "sidebar.sessions": "Sessions",
    "sidebar.projects": "Projects",
    "sidebar.model": "Model",
    "sidebar.changeModel": "Click to change model",
    "sidebar.workspace": "Workspace",
    // topbar
    "topbar.title": "New Session",
    "topbar.reasoning": "Reasoning effort",
    "topbar.reasoningDefault": "reasoning: default",
    "topbar.addProvider": "Add provider",
    "topbar.memory": "Memory",
    "topbar.goal": "Goal",
    // model picker
    "model.search": "Search models…",
    "model.none": "no model",
    "model.noneFound": "No models found — scan or add providers",
    "model.activeModel": "active model",
    "model.unknown": "unknown",
    "model.setDefault": "Set as CLI default",
    "model.setDefaultOk": "saved as CLI default",
    "model.setDefaultErr": "set CLI default failed",
    // welcome
    "welcome.heading": "What do you want to build?",
    "welcome.subtext": "SuperCli agent — code, inspect, edit, and ship faster.",
    "welcome.summarize": "Summarize project",
    "welcome.status": "Check status",
    "welcome.history": "Show history",
    // composer
    "composer.ready": "Ready",
    "composer.placeholder": "Ask SuperCli to build, inspect, edit, test…",
    "composer.send": "Send",
    "composer.stop": "■ Stop",
    "composer.working": "Agent working… (Esc or Stop to cancel)",
    "composer.stopped": "Stopped",
    "composer.requestFailed": "Request failed",
    // run states (metric-run-state values)
    "run.idle": "idle",
    "run.running": "running",
    "run.error": "error",
    "run.done": "Done",
    "run.tokens": "tokens",
    // inspector tabs
    "insp.activity": "Activity",
    "insp.history": "History",
    "insp.usage": "Usage",
    "insp.models": "Models",
    "insp.providers": "Providers",
    "insp.codex": "Codex",
    "insp.context": "Context",
    "insp.files": "Files",
    "insp.projects": "Projects",
    "insp.memory": "Memory",
    "insp.goal": "Goal",
    "insp.transcript": "Transcript",
    "projects.none": "No projects yet. Add the current directory to start.",
    "projects.useHint": "Click to switch workspace",
    "projects.addCwd": "＋ current dir",
    "projects.note": "Selecting a project switches the workspace immediately for new chats and file browsing.",
    "projects.addTitle": "Add project",
    "projects.addHint": "Name + directory. The directory becomes the agent's sandbox root.",
    "projects.nameLabel": "Name",
    "projects.namePh": "project name (optional)",
    "projects.pathLabel": "Directory",
    "projects.addBtn": "Add project",
    "projects.pickerOpening": "Opening folder picker…",
    "projects.selectedToast": "Project switched — workspace updated",
    "projects.addedToast": "Project added and selected",
    "projects.removedToast": "Project removed",
    "projects.errToast": "Projects: ",
    "common.remove": "remove",
    // metrics
    "metric.run": "Run",
    "metric.idle": "idle",
    "metric.tokens": "Tokens",
    "metric.time": "Time",
    "metric.tools": "Tools",
    // run timeline
    "insp.timeline": "Run timeline",
    "timeline.args": "arguments",
    "timeline.result": "result",
    "timeline.error": "error",
    "timeline.cancelled": "cancelled",
    // activity / empty states
    "activity.empty": "Tool calls and run events appear here.",
    "sessions.selectHint": "Select a session from the sidebar.",
    "sessions.none": "No saved sessions",
    "sessions.empty": "Empty session",
    "sessions.untitled": "(untitled)",
    "sessions.msgs": "msgs",
    // stats cards
    "stats.model": "Model",
    "stats.tokensToday": "Tokens today",
    "stats.sessionTokens": "Session tokens",
    "stats.costToday": "Cost today",
    "stats.sessionCost": "Session cost",
    // models panel
    "models.reasoningEffort": "Reasoning effort",
    "models.loading": "Loading models…",
    "models.reasoningSupported": "Supported · effective: ",
    "models.reasoningUnsupported": "Not supported by current model",
    // providers
    "providers.add": "Add provider",
    "providers.addHint": "Open setup popup with templates and custom endpoint config.",
    "providers.none": "No providers configured",
    "providers.default": "default",
    "providers.models": "models",
    "providers.configured": "Configured providers",
    "providers.editing": "Editing",
    "providers.defaultModel": "Default model",
    "providers.clearKey": "Clear stored API key",
    "providers.saveChanges": "Save changes",
    "providers.addNew": "Add new provider",
    "providers.confirmDelete": "Delete provider",
    "providers.deleteWarning": "This removes the provider and its model list. This cannot be undone.",
    "providers.saving": "Saving…",
    "providers.saved": "Saved",
    "providers.keySetKeepEmpty": "(key set — leave empty to keep current)",
    "providers.noKeyKeepEmpty": "(no key — leave empty to skip)",
    "providers.addBtn": "Add provider",
    "providers.addAccount": "Add account",
    "providers.labelPh": "account name (e.g. work, personal)",
    "providers.loggedIn": "logged in",
    "providers.loggedOut": "not logged in",
    "providers.loggingIn": "logging in…",
    "providers.waitLogin": "awaiting browser sign-in…",
    "providers.unknownEmail": "unknown email",
    "providers.lastRefresh": "last refresh",
    "providers.weekly": "weekly",
    "providers.login": "Sign in",
    "providers.logout": "Sign out",
    "providers.refresh": "Refresh",
    "providers.confirmLogout": "Sign out of account",
    "common.edit": "edit",
    "common.delete": "delete",
    "common.back": "Back",
    // codex
    "codex.accounts": "Codex Accounts",
    "codex.none": "No codex accounts — run /login in TUI first",
    "codex.weekly": "weekly",
    "codex.noRefresh": "no refresh",
    // context / memory / goal
    "memory.empty": "Memory is empty",
    "goal.none": "No active goal",
    "goal.noTasks": "No tasks",
    "goal.status": "status",
    // files
    "files.empty": "Empty directory",
    // provider modal
    "provider.title": "Add Provider",
    "provider.subtitle": "Connect cloud or local model providers. Pick a template, review settings, then add and scan models.",
    "provider.choose": "Choose provider",
    "provider.searchPh": "Search providers…",
    "provider.configure": "Configure",
    "provider.custom": "Custom provider",
    "provider.customHint": "Pick a provider on the left or configure your own endpoint.",
    "provider.name": "Name",
    "provider.type": "Type",
    "provider.baseUrl": "Base URL",
    "provider.apiKey": "API key",
    "provider.apiKeyHint": "(optional for local/free)",
    "provider.addScan": "Add & Scan Models",
    "provider.statusHint": "Choose a provider template or configure a custom OpenAI-compatible endpoint.",
    "provider.adding": "Adding ",
    "provider.addedScanning": "Added! Scanning…",
    "provider.ready": " — ready!",
    // settings modal
    "settings.title": "Settings",
    "settings.general": "General",
    "settings.appearance": "Appearance",
    "settings.models": "Models",
    "settings.providers": "Providers",
    "settings.mcp": "MCP",
    "settings.keybinds": "Keybinds",
    "settings.about": "About",
    "settings.language": "Language",
    "settings.notifications": "Agent notifications",
    "settings.notifySound": "Play sound when agent finishes",
    "settings.notifyDesktop": "Show desktop notification",
    "settings.permissions": "Permissions",
    "settings.permWrite": "Ask before file writes outside project",
    "settings.permNetwork": "Ask before network requests",
    "settings.theme": "Theme",
    "settings.dark": "Dark",
    "settings.light": "Light",
    "settings.uiFont": "Interface font",
    "settings.codeFont": "Code / terminal font",
    "settings.modelVisibility": "Model visibility",
    "settings.addProvider": "＋ Add provider",
    // mcp
    "mcp.servers": "MCP Servers",
    "mcp.editJson": "Edit as JSON",
    "mcp.addServer": "Add MCP Server",
    "mcp.command": "Command",
    "mcp.args": "Args (comma-separated)",
    "mcp.env": "Env (KEY=VALUE, one per line)",
    "mcp.editJsonTitle": "Edit MCP config as JSON",
    "mcp.backToList": "Back to list",
    "mcp.saveJson": "Save JSON",
    "mcp.none": "No MCP servers configured.<br>Add one below to extend agent capabilities.",
    "mcp.remove": "Remove",
    // keybinds
    "keybinds.title": "Keyboard shortcuts",
    "keybinds.send": "Send prompt",
    "keybinds.cancel": "Cancel / close",
    "keybinds.cancelModal": "Cancel / close modal",
    "keybinds.palette": "Command palette",
    "keybinds.openPalette": "Open command palette",
    "keybinds.thinking": "Toggle thinking",
    "keybinds.expand": "Expand tool output",
    "keybinds.newline": "New line",
    "keybinds.scroll": "Scroll transcript",
    // about
    "about.title": "SuperCli Web GUI",
    "about.desc": "Single-binary AI coding agent with portable data.",
    "about.binary": "Binary",
    "about.data": "Data",
    "about.go": "Go",
    // editor
    "editor.save": "Save",
    "editor.saving": "Saving…",
    "editor.saved": "Saved!",
    // common
    "common.refresh": "refresh",
    "common.scan": "scan",
    "common.clear": "clear",
    "common.cancel": "Cancel",
    "common.loading": "Loading…",
    "common.error": "Error: ",
    "common.empty": "(empty)",
    "common.failed": "Failed",
    "common.failedLoad": "Failed to load",
    "common.failedLoadModels": "Failed to load models",
    // role tags / markers
    "role.you": "You",
    "role.thinking": "Thinking",
    // connection status
    "status.connecting": "connecting…",
    "status.connected": "connected",
    "status.disconnected": "disconnected"
  },
  pl: {
    "rail.nav": "Pasek nawigacji",
    "rail.chat": "Czat",
    "rail.sessions": "Sesje",
    "rail.models": "Modele",
    "rail.providers": "Dostawcy",
    "rail.memory": "Pamięć",
    "rail.goal": "Cel",
    "rail.projects": "Projekty",
    "rail.settings": "Ustawienia",
    "sidebar.newSession": "Nowa sesja",
    "sidebar.sessions": "Sesje",
    "sidebar.projects": "Projekty",
    "sidebar.model": "Model",
    "sidebar.changeModel": "Kliknij, aby zmienić model",
    "sidebar.workspace": "Obszar roboczy",
    "topbar.title": "Nowa sesja",
    "topbar.reasoning": "Poziom rozumowania",
    "topbar.reasoningDefault": "rozumowanie: domyślne",
    "topbar.addProvider": "Dodaj dostawcę",
    "topbar.memory": "Pamięć",
    "topbar.goal": "Cel",
    "model.search": "Szukaj modeli…",
    "model.none": "brak modelu",
    "model.noneFound": "Nie znaleziono modeli — przeskanuj lub dodaj dostawcę",
    "model.activeModel": "aktywny model",
    "model.unknown": "nieznany",
    "model.setDefault": "Ustaw jako domyślny dla CLI",
    "model.setDefaultOk": "zapisano jako domyślny CLI",
    "model.setDefaultErr": "nie udało się ustawić domyślnego CLI",
    "welcome.heading": "Co chcesz zbudować?",
    "welcome.subtext": "Agent SuperCli — koduj, analizuj, edytuj i wdrażaj szybciej.",
    "welcome.summarize": "Podsumuj projekt",
    "welcome.status": "Sprawdź status",
    "welcome.history": "Pokaż historię",
    "composer.ready": "Gotowy",
    "composer.placeholder": "Zapytaj SuperCli o cokolwiek…",
    "composer.send": "Wyślij",
    "composer.stop": "■ Zatrzymaj",
    "composer.working": "Agent pracuje… (Esc lub Zatrzymaj, aby anulować)",
    "composer.stopped": "Zatrzymano",
    "composer.requestFailed": "Żądanie nie powiodło się",
    "run.idle": "bezczynny",
    "run.running": "pracuje",
    "run.error": "błąd",
    "run.done": "Gotowe",
    "run.tokens": "tokeny",
    "insp.activity": "Aktywność",
    "insp.history": "Historia",
    "insp.usage": "Zużycie",
    "insp.models": "Modele",
    "insp.providers": "Dostawcy",
    "insp.codex": "Codex",
    "insp.context": "Kontekst",
    "insp.files": "Pliki",
    "insp.projects": "Projekty",
    "insp.memory": "Pamięć",
    "insp.goal": "Cel",
    "insp.transcript": "Transkrypcja",
    "projects.none": "Brak projektów. Dodaj bieżący katalog, aby zacząć.",
    "projects.useHint": "Kliknij, aby przełączyć obszar roboczy",
    "projects.addCwd": "＋ bieżący katalog",
    "projects.note": "Wybór projektu od razu przełącza obszar roboczy dla nowych czatów i przeglądania plików.",
    "projects.addTitle": "Dodaj projekt",
    "projects.addHint": "Nazwa + katalog. Katalog staje się katalogiem głównym (sandbox) agenta.",
    "projects.nameLabel": "Nazwa",
    "projects.namePh": "nazwa projektu (opcjonalna)",
    "projects.pathLabel": "Katalog",
    "projects.addBtn": "Dodaj projekt",
    "projects.pickerOpening": "Otwieranie wyboru folderu…",
    "projects.selectedToast": "Przełączono projekt — obszar roboczy zaktualizowany",
    "projects.addedToast": "Dodano i wybrano projekt",
    "projects.removedToast": "Usunięto projekt",
    "projects.errToast": "Projekty: ",
    "common.remove": "usuń",
    "metric.run": "Stan",
    "metric.idle": "bezczynny",
    "metric.tokens": "Tokeny",
    "metric.time": "Czas",
    "metric.tools": "Narzędzia",
    "insp.timeline": "Przebieg zadania",
    "timeline.args": "argumenty",
    "timeline.result": "wynik",
    "timeline.error": "błąd",
    "timeline.cancelled": "anulowano",
    "activity.empty": "Wywołania narzędzi i zdarzenia pojawią się tutaj.",
    "sessions.selectHint": "Wybierz sesję z paska bocznego.",
    "sessions.none": "Brak zapisanych sesji",
    "sessions.empty": "Pusta sesja",
    "sessions.untitled": "(bez tytułu)",
    "sessions.msgs": "wiad.",
    "stats.model": "Model",
    "stats.tokensToday": "Tokeny dziś",
    "stats.sessionTokens": "Tokeny sesji",
    "stats.costToday": "Koszt dziś",
    "stats.sessionCost": "Koszt sesji",
    "models.reasoningEffort": "Poziom rozumowania",
    "models.loading": "Ładowanie modeli…",
    "models.reasoningSupported": "Obsługiwane · efektywnie: ",
    "models.reasoningUnsupported": "Nieobsługiwane przez bieżący model",
    "providers.add": "Dodaj dostawcę",
    "providers.addHint": "Otwórz okno konfiguracji z szablonami i własnym punktem końcowym.",
    "providers.none": "Nie skonfigurowano dostawców",
    "providers.default": "domyślny",
    "providers.models": "modeli",
    "providers.configured": "Skonfigurowani dostawcy",
    "providers.editing": "Edycja",
    "providers.defaultModel": "Domyślny model",
    "providers.clearKey": "Wyczyść zapisany klucz API",
    "providers.saveChanges": "Zapisz zmiany",
    "providers.addNew": "Dodaj nowego dostawcę",
    "providers.confirmDelete": "Usunąć dostawcę",
    "providers.deleteWarning": "To usunie dostawcę i jego listę modeli. Tego nie da się cofnąć.",
    "providers.saving": "Zapisywanie…",
    "providers.saved": "Zapisano",
    "providers.keySetKeepEmpty": "(klucz ustawiony — zostaw puste aby zachować)",
    "providers.noKeyKeepEmpty": "(brak klucza — zostaw puste aby pominąć)",
    "providers.addBtn": "Dodaj dostawcę",
    "providers.addAccount": "Dodaj konto",
    "providers.labelPh": "nazwa konta (np. praca, prywatne)",
    "providers.loggedIn": "zalogowano",
    "providers.loggedOut": "niezalogowano",
    "providers.loggingIn": "logowanie…",
    "providers.waitLogin": "oczekiwanie na logowanie w przeglądarce…",
    "providers.unknownEmail": "nieznany e-mail",
    "providers.lastRefresh": "ost. odświeżenie",
    "providers.weekly": "tygodniowo",
    "providers.login": "Zaloguj",
    "providers.logout": "Wyloguj",
    "providers.refresh": "Odśwież",
    "providers.confirmLogout": "Wylogować konto",
    "common.edit": "edytuj",
    "common.delete": "usuń",
    "common.back": "Wstecz",
    "codex.accounts": "Konta Codex",
    "codex.none": "Brak kont Codex — uruchom najpierw /login w TUI",
    "codex.weekly": "tygodniowo",
    "codex.noRefresh": "brak odświeżenia",
    "memory.empty": "Pamięć jest pusta",
    "goal.none": "Brak aktywnego celu",
    "goal.noTasks": "Brak zadań",
    "goal.status": "status",
    "files.empty": "Pusty katalog",
    "provider.title": "Dodaj dostawcę",
    "provider.subtitle": "Połącz dostawców modeli w chmurze lub lokalnych. Wybierz szablon, sprawdź ustawienia, a następnie dodaj i przeskanuj modele.",
    "provider.choose": "Wybierz dostawcę",
    "provider.searchPh": "Szukaj dostawców…",
    "provider.configure": "Konfiguracja",
    "provider.custom": "Własny dostawca",
    "provider.customHint": "Wybierz dostawcę po lewej lub skonfiguruj własny punkt końcowy.",
    "provider.name": "Nazwa",
    "provider.type": "Typ",
    "provider.baseUrl": "Adres bazowy",
    "provider.apiKey": "Klucz API",
    "provider.apiKeyHint": "(opcjonalny dla lokalnych/darmowych)",
    "provider.addScan": "Dodaj i przeskanuj modele",
    "provider.statusHint": "Wybierz szablon dostawcy lub skonfiguruj własny punkt końcowy zgodny z OpenAI.",
    "provider.adding": "Dodawanie ",
    "provider.addedScanning": "Dodano! Skanowanie…",
    "provider.ready": " — gotowe!",
    "settings.title": "Ustawienia",
    "settings.general": "Ogólne",
    "settings.appearance": "Wygląd",
    "settings.models": "Modele",
    "settings.providers": "Dostawcy",
    "settings.mcp": "MCP",
    "settings.keybinds": "Skróty klawiszowe",
    "settings.about": "O programie",
    "settings.language": "Język",
    "settings.notifications": "Powiadomienia agenta",
    "settings.notifySound": "Odtwórz dźwięk po zakończeniu pracy agenta",
    "settings.notifyDesktop": "Pokaż powiadomienie na pulpicie",
    "settings.permissions": "Uprawnienia",
    "settings.permWrite": "Pytaj przed zapisem plików poza projektem",
    "settings.permNetwork": "Pytaj przed żądaniami sieciowymi",
    "settings.theme": "Motyw",
    "settings.dark": "Ciemny",
    "settings.light": "Jasny",
    "settings.uiFont": "Czcionka interfejsu",
    "settings.codeFont": "Czcionka kodu / terminala",
    "settings.modelVisibility": "Widoczność modeli",
    "settings.addProvider": "＋ Dodaj dostawcę",
    "mcp.servers": "Serwery MCP",
    "mcp.editJson": "Edytuj jako JSON",
    "mcp.addServer": "Dodaj serwer MCP",
    "mcp.command": "Polecenie",
    "mcp.args": "Argumenty (oddzielone przecinkami)",
    "mcp.env": "Zmienne środowiskowe (KLUCZ=WARTOŚĆ, jedna na wiersz)",
    "mcp.editJsonTitle": "Edytuj konfigurację MCP jako JSON",
    "mcp.backToList": "Powrót do listy",
    "mcp.saveJson": "Zapisz JSON",
    "mcp.none": "Nie skonfigurowano serwerów MCP.<br>Dodaj jeden poniżej, aby rozszerzyć możliwości agenta.",
    "mcp.remove": "Usuń",
    "keybinds.title": "Skróty klawiszowe",
    "keybinds.send": "Wyślij polecenie",
    "keybinds.cancel": "Anuluj / zamknij",
    "keybinds.cancelModal": "Anuluj / zamknij okno",
    "keybinds.palette": "Paleta poleceń",
    "keybinds.openPalette": "Otwórz paletę poleceń",
    "keybinds.thinking": "Przełącz rozumowanie",
    "keybinds.expand": "Rozwiń wynik narzędzia",
    "keybinds.newline": "Nowa linia",
    "keybinds.scroll": "Przewiń transkrypcję",
    "about.title": "SuperCli Web GUI",
    "about.desc": "Jednoplikowy agent kodujący AI z przenośnymi danymi.",
    "about.binary": "Plik wykonywalny",
    "about.data": "Dane",
    "about.go": "Go",
    "editor.save": "Zapisz",
    "editor.saving": "Zapisywanie…",
    "editor.saved": "Zapisano!",
    "common.refresh": "odśwież",
    "common.scan": "skanuj",
    "common.clear": "wyczyść",
    "common.cancel": "Anuluj",
    "common.loading": "Ładowanie…",
    "common.error": "Błąd: ",
    "common.empty": "(puste)",
    "common.failed": "Niepowodzenie",
    "common.failedLoad": "Nie udało się załadować",
    "common.failedLoadModels": "Nie udało się załadować modeli",
    "role.you": "Ty",
    "role.thinking": "Myślenie",
    "status.connecting": "łączenie…",
    "status.connected": "połączono",
    "status.disconnected": "rozłączono"
  }
};

var currentLang = "en";
try {
  var _s0 = JSON.parse(localStorage.getItem("supercli-settings") || "{}");
  if (_s0 && (_s0.lang === "pl" || _s0.lang === "en")) currentLang = _s0.lang;
} catch (e) {}

// t(key) — translate with fallback pl → en → key.
function t(key) {
  var lang = I18N[currentLang] ? currentLang : "en";
  var v = I18N[lang] && I18N[lang][key];
  if (v == null) v = I18N.en[key];
  return v == null ? key : v;
}

// setLang(lang) — switch active language and re-render static + dynamic UI.
function setLang(lang) {
  currentLang = (lang === "pl") ? "pl" : "en";
  try { document.documentElement.setAttribute("lang", currentLang); } catch (e) {}
  applyI18n();
}

// applyI18n() — walk tagged static nodes and translate them. innerHTML is
// used only for keys that intentionally contain markup (mcp.none); textContent
// otherwise. data-i18n-ph → placeholder, data-i18n-title → title + aria-label.
function applyI18n() {
  var nodesText = document.querySelectorAll("[data-i18n]");
  for (var i = 0; i < nodesText.length; i++) {
    var n = nodesText[i];
    var val = t(n.getAttribute("data-i18n"));
    if (val.indexOf("<") !== -1) n.innerHTML = val; else n.textContent = val;
  }
  var nodesPh = document.querySelectorAll("[data-i18n-ph]");
  for (var p = 0; p < nodesPh.length; p++) {
    nodesPh[p].setAttribute("placeholder", t(nodesPh[p].getAttribute("data-i18n-ph")));
  }
  var nodesTitle = document.querySelectorAll("[data-i18n-title]");
  for (var q = 0; q < nodesTitle.length; q++) {
    var tv = t(nodesTitle[q].getAttribute("data-i18n-title"));
    nodesTitle[q].setAttribute("title", tv);
    nodesTitle[q].setAttribute("aria-label", tv);
  }
}

// ── Full Markdown renderer (no libraries, XSS-safe) ──
// Escapes everything first, then applies formatting on safe text.
// Handles: headings, bold, italic, code blocks, inline code,
//          unordered/ordered lists, horizontal rules, blockquotes,
//          links, paragraphs.

function mdInline(s) {
  // bold **text** or __text__
  s = s.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/__(.+?)__/g, "<strong>$1</strong>");
  // italic *text* or _text_  (but not inside words to avoid breaking paths / URLs)
  s = s.replace(/(^|\s)\*([^*\s][^*]*?)\*(\s|$)/g, "$1<em>$2</em>$3");
  s = s.replace(/(^|\s)_([^_\s][^_]*?)_(\s|$)/g, "$1<em>$2</em>$3");
  // inline code `text`
  s = s.replace(/`([^`]+)`/g, "<code>$1</code>");
  // links [text](url)
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
  // strikethrough ~~text~~
  s = s.replace(/~~(.+?)~~/g, "<del>$1</del>");
  return s;
}

function renderMarkdownish(text) {
  var src = String(text || "");
  // Split on code fences first to protect them from other processing
  var parts = src.split(/```/);
  var html = "";
  for (var i = 0; i < parts.length; i++) {
    if (i % 2 === 1) {
      // Code block — preserve language hint but escape content
      var lang = "";
      var code = parts[i];
      var nl = code.indexOf("\n");
      if (nl > 0) { lang = code.slice(0, nl).trim(); code = code.slice(nl + 1); }
      html += '<pre data-lang="' + escAttr(lang) + '"><code>' + escHtml(code.trimEnd()) + '</code></pre>';
    } else {
      // Normal text — process line by line for block elements
      html += mdBlocks(parts[i]);
    }
  }
  return html;
}

function mdBlocks(text) {
  // Protect inline HTML that's already escaped is fine — we work on safe text.
  // We don't escape here because escHtml was already called on code fence
  // segments; for normal text we need to process THEN escape at the end.
  // But we want bold/italic/links to work, so we process raw, then sanitize.

  var lines = text.split("\n");
  var out = "";
  var i = 0;
  var inList = false;
  var listType = "";

  function closeList() {
    if (inList) { out += "</" + listType + ">"; inList = false; listType = ""; }
  }

  while (i < lines.length) {
    var line = lines[i];
    var trimmed = line.trim();
    i++;

    // Blank line: close any open list, add paragraph break
    if (trimmed === "") {
      closeList();
      continue;
    }

    // Horizontal rule
    if (/^(-{3,}|\*{3,}|_{3,})\s*$/.test(trimmed)) {
      closeList();
      out += "<hr>";
      continue;
    }

    // Heading
    var h = trimmed.match(/^(#{1,6})\s+(.+)/);
    if (h) {
      closeList();
      var level = h[1].length;
      out += "<h" + level + ">" + escHtml(h[2]) + "</h" + level + ">";
      continue;
    }

    // Blockquote
    if (trimmed.startsWith("> ")) {
      closeList();
      var bqLines = [];
      while (i <= lines.length && lines[i - 1] && (lines[i - 1].trim().startsWith("> ") || (lines[i - 1].trim() === "" && i < lines.length && lines[i].trim().startsWith("> ")))) {
        if (lines[i - 1].trim() !== "") {
          bqLines.push(escHtml(lines[i - 1].trim().replace(/^>\s?/, "")));
        }
        i++;
      }
      i--; // we went one too far
      out += "<blockquote>" + bqLines.join("<br>") + "</blockquote>";
      continue;
    }

    // Unordered list
    var ul = trimmed.match(/^[-*+]\s+(.+)/);
    if (ul) {
      if (!inList || listType !== "ul") { closeList(); out += "<ul>"; inList = true; listType = "ul"; }
      out += "<li>" + mdInline(escHtml(ul[1])) + "</li>";
      continue;
    }

    // Ordered list
    var ol = trimmed.match(/^\d+[.)]\s+(.+)/);
    if (ol) {
      if (!inList || listType !== "ol") { closeList(); out += "<ol>"; inList = true; listType = "ol"; }
      out += "<li>" + mdInline(escHtml(ol[1])) + "</li>";
      continue;
    }

    // Table: two or more consecutive | lines where at least one is a separator
    if (trimmed.indexOf("|") >= 0) {
      var tableLines = [trimmed];
      while (i < lines.length && lines[i].trim().indexOf("|") >= 0) {
        tableLines.push(lines[i].trim());
        i++;
      }
      // Check: at least 2 lines, all contain |
      var allPipes = true;
      for (var tp = 0; tp < tableLines.length; tp++) {
        if (tableLines[tp].indexOf("|") === -1) { allPipes = false; break; }
      }
      // A separator row: |---|----|:---| etc.
      function isSepRow(s) { return /^\|[\s\-:|]+\|$/.test(s); }
      var sepIdx = -1;
      for (var ts = 0; ts < tableLines.length; ts++) {
        if (isSepRow(tableLines[ts])) { sepIdx = ts; break; }
      }
      if (allPipes && tableLines.length >= 2 && sepIdx === 1) {
        closeList();
        out += renderTable(tableLines);
        continue;
      }
      // Put back non-table lines
      for (var tj = tableLines.length - 1; tj >= 0; tj--) {
        lines.splice(i - (tableLines.length - tj), 0, tableLines[tj]);
      }
      i -= tableLines.length;
    }

    // Regular paragraph line
    closeList();
    // Gather consecutive non-empty, non-block lines into one paragraph
    var paraLines = [trimmed];
    while (i < lines.length && lines[i].trim() !== "" &&
           !/^(#{1,6}\s|>\s|[-*+]\s|\d+[.)]\s|---)/.test(lines[i].trim())) {
      paraLines.push(lines[i].trim());
      i++;
    }
    var para = paraLines.join(" ");
    out += "<p>" + mdInline(escHtml(para)) + "</p>";
  }

  closeList();
  return out;
}

// Render a markdown table from an array of |...| lines.
// First line is header, second is separator, rest are body.
function renderTable(lines) {
  if (lines.length < 2) return "";
  // Parse cells: split by |, trim, drop empty first/last from leading/trailing pipe
  function cells(line) {
    var parts = line.replace(/^\||\|$/g, "").split("|");
    return parts.map(function(c) { return c.trim(); });
  }
  var headerCells = cells(lines[0]);
  // Determine alignment from separator row
  var align = [];
  if (lines.length >= 2) {
    var sepCells = cells(lines[1]);
    for (var a = 0; a < sepCells.length; a++) {
      var c = sepCells[a];
      if (/^:.*:$/.test(c)) align.push("center");
      else if (/:$/.test(c)) align.push("right");
      else align.push("left");
    }
  }
  var html = '<div class="md-table-wrap"><table>';
  // Header
  html += "<thead><tr>";
  for (var h = 0; h < headerCells.length; h++) {
    html += '<th' + (align[h] ? ' style="text-align:' + align[h] + '"' : "") + '>' + mdInlineInline(headerCells[h]) + '</th>';
  }
  html += "</tr></thead><tbody>";
  for (var r = 2; r < lines.length; r++) {
    var rowCells = cells(lines[r]);
    html += "<tr>";
    for (var c = 0; c < headerCells.length; c++) {
      html += '<td' + (align[c] ? ' style="text-align:' + align[c] + '"' : "") + '>' + mdInlineInline(c < rowCells.length ? rowCells[c] : "") + '</td>';
    }
    html += "</tr>";
  }
  html += "</tbody></table></div>";
  return html;
}

// Inline markdown for table cells (applies bold/italic/code on escaped text)
function mdInlineInline(s) {
  return mdInline(escHtml(String(s || "")));
}

var _thinkIdCounter = 0;
function renderThinkBlock(text) {
  var content = renderMarkdownish(String(text).trim());
  var id = "think-" + (++_thinkIdCounter);
  return '<details class="think-block" data-think-id="' + id + '">' +
    '<summary><span class="think-label">' + escHtml(t("role.thinking")) + '</span><span class="think-line"></span></summary>' +
    '<div class="think-content">' + content + '</div>' +
    '</details>';
}

// Render markdown-ish text safely, but split model reasoning tags into a
// separate OpenCode-like separator instead of showing raw <thinking> inline.
function renderText(text) {
  // Think-block ids must be stable across re-renders of the same message,
  // or the open-state preservation in handleEvent("message") can never
  // match them again — that was the "thinking collapses while streaming"
  // glitch. Resetting the counter per render keeps the ids positional.
  _thinkIdCounter = 0;
  var src = String(text || "");
  var html = "";
  var re = /<(?:thinking|think)>[\s\S]*?<\/(?:thinking|think)>/ig;
  var last = 0;
  var match;
  while ((match = re.exec(src)) !== null) {
    if (match.index > last) html += renderMarkdownish(src.slice(last, match.index));
    var block = match[0]
      .replace(/^<(?:thinking|think)>/i, "")
      .replace(/<\/(?:thinking|think)>$/i, "");
    html += renderThinkBlock(block);
    last = re.lastIndex;
  }
  if (last < src.length) {
    var tail = src.slice(last);
    var openIdx = tail.search(/<(?:thinking|think)>/i);
    if (openIdx >= 0) {
      html += renderMarkdownish(tail.slice(0, openIdx));
      html += renderThinkBlock(tail.slice(openIdx).replace(/^<(?:thinking|think)>/i, ""));
    } else {
      html += renderMarkdownish(tail);
    }
  }
  return html;
}

// ── Panel Navigation ──
function closeModelPicker() {
  var menu = $("#model-menu");
  if (menu) menu.hidden = true;
}

function showInspector(name) {
  activePanel = name;
  closeModelPicker();
  // Update inspector tabs
  $$(".inspector-tab").forEach(function(b) {
    b.classList.toggle("active", b.dataset.inspector === name);
  });
  // Show matching panel
  $$(".inspector-panel").forEach(function(p) { p.classList.remove("active"); });
  var panel = $("#panel-" + name);
  if (panel) panel.classList.add("active");
  // Load data
  if (loaders[name]) loaders[name]();
}

// Rail button clicks
var chatBtn = $("#rail-chat-btn");
if (chatBtn) {
  chatBtn.addEventListener("click", function() {
    showInspector("activity");
    $("#prompt").focus();
  });
}

// Inspector tab clicks
$("#inspector-tabs").addEventListener("click", function(e) {
  var tab = e.target.closest(".inspector-tab");
  if (!tab) return;
  showInspector(tab.dataset.inspector);
});

// Sidebar model card click -> open model picker
$("#sidebar-model-card").addEventListener("click", function() { toggleModelPicker(); });

// Topbar quick action buttons
$$(".topbar-actions [data-panel]").forEach(function(btn) {
  btn.addEventListener("click", function() { showInspector(btn.dataset.panel); });
});

// ── Health / Status ──
async function checkHealth() {
  try {
    var r = await fetch("/api/health");
    var j = await r.json();
    $("#rail-status").className = "status-dot ok";
    $("#status-text").textContent = t("status.connected");
    $("#sidebar-model-name").textContent = j.model || "—";
    $("#sidebar-model-provider").textContent = t("model.activeModel");
    $("#model-picker-name").textContent = j.model || t("model.none");
    $("#model-picker-provider").textContent = t("model.activeModel");
    $("#sidebar-home").textContent = j.home || "—";
  } catch (e) {
    $("#rail-status").className = "status-dot err";
    $("#status-text").textContent = t("status.disconnected");
  }
}

// ── Chat (SSE streaming) ──
var transcript = $("#transcript");
var promptEl   = $("#prompt");
var sendBtn    = $("#send-btn");
var activityList = $("#activity-list");
var composerStatus = $("#composer-status");
var metricRunState = $("#metric-run-state");
var metricTokens   = $("#metric-tokens");
var metricTools    = $("#metric-tools");
var topbarReasoning = $("#topbar-reasoning");

// Re-arm / disarm the sticky follow depending on where the user scrolled.
transcript.addEventListener("scroll", function() {
  stickToBottom = isNearBottom(transcript);
});

function dismissWelcome() {
  var card = $("#welcome-card");
  if (card) card.remove();
  transcript.classList.remove("empty-state-mode");
}

function addMsg(role, text) {
  dismissWelcome();
  var m = el("div", "message " + role);
  var tag = el("span", "role-tag");
  tag.textContent = role === "user" ? t("role.you") : "SuperCli";
  m.appendChild(tag);
  var body = el("div");
  body.innerHTML = renderText(text);
  m.appendChild(body);
  transcript.appendChild(m);
  // Your own prompt always snaps to the bottom; streamed assistant
  // messages respect the sticky state.
  smartScroll(transcript, role === "user");
  return body;
}

function setTopbarTitle(text) {
  var title = $(".topbar-left h1");
  if (title) title.textContent = text || t("topbar.title");
}

function renderChatTranscript(msgs, title) {
  activeSessionID = activeSessionID || "";
  transcript.innerHTML = "";
  transcript.classList.remove("empty-state-mode");
  var rendered = 0;
  for (var i = 0; i < (msgs || []).length; i++) {
    var m = msgs[i];
    if (!m || !m.content) continue;
    if (m.role !== "user" && m.role !== "assistant") continue;
    addMsg(m.role === "user" ? "user" : "assistant", m.content);
    rendered++;
  }
  if (!rendered) {
    transcript.innerHTML = '<div class="empty-state">' + escHtml(t("sessions.empty")) + '</div>';
  }
  setTopbarTitle(title || t("sessions.untitled"));
  setRunState("idle", t("composer.ready"));
  smartScroll(transcript, true);
  promptEl.focus();
}

// ── Run timeline (Activity panel) ──
// The right-hand panel is a chronological timeline of the current run:
// one step per tool call (with live duration, status colour and expandable
// args/result), plus compact notes for run-level events. Raw event dumps
// that merely duplicated the chat are gone.
var openToolSteps = []; // FIFO of steps still awaiting their tool_result
var runToolCount = 0;

function clipText(s, n) {
  s = String(s == null ? "" : s);
  return s.length > n ? s.slice(0, n) + "\n… (truncated)" : s;
}

function tlAppend(node) {
  var empty = activityList.querySelector(".empty-state");
  if (empty) empty.remove();
  activityList.appendChild(node);
  activityList.scrollTop = activityList.scrollHeight;
}

// addActivity renders a compact one-line note in the timeline. Long details
// stay readable: they expand on click instead of being cut off.
function addActivity(kind, title, detail) {
  var item = el("div", "tl-note" + (kind ? " " + kind : ""));
  var head = el("div", "tl-note-head");
  var strong = el("strong");
  strong.textContent = title;
  head.appendChild(strong);
  item.appendChild(head);
  if (detail) {
    var span = el("span", "tl-note-detail");
    span.textContent = clipText(detail, 4000);
    item.appendChild(span);
    item.addEventListener("click", function() { item.classList.toggle("expanded"); });
  }
  tlAppend(item);
}

// tlRunStart resets the per-run counters and adds a run header with the
// prompt excerpt and wall-clock start time.
function tlRunStart(promptText) {
  runToolCount = 0;
  openToolSteps = [];
  if (metricTools) metricTools.textContent = "0";
  var head = el("div", "tl-run");
  var dot = el("span", "tl-run-dot");
  var title = el("span", "tl-run-title");
  title.textContent = (promptText || "").slice(0, 70) || "run";
  var when = el("span", "tl-run-when");
  when.textContent = new Date().toLocaleTimeString();
  head.appendChild(dot);
  head.appendChild(title);
  head.appendChild(when);
  tlAppend(head);
}

// tlToolStart adds a running step for one tool call.
function tlToolStart(name, args) {
  runToolCount++;
  if (metricTools) metricTools.textContent = String(runToolCount);
  var step = el("div", "tl-step running");
  var head = el("div", "tl-head");
  var status = el("span", "tl-status");
  var nm = el("span", "tl-name");
  nm.textContent = name;
  var dur = el("span", "tl-dur");
  dur.textContent = "…";
  head.appendChild(status);
  head.appendChild(nm);
  head.appendChild(dur);
  step.appendChild(head);
  var body = el("div", "tl-body");
  body.hidden = true;
  if (args && args !== "{}") {
    var lbl = el("div", "tl-body-label");
    lbl.textContent = t("timeline.args");
    var pre = el("pre");
    pre.textContent = clipText(args, 4000);
    body.appendChild(lbl);
    body.appendChild(pre);
  }
  step.appendChild(body);
  head.addEventListener("click", function() {
    if (body.childNodes.length) {
      body.hidden = !body.hidden;
      step.classList.toggle("open", !body.hidden);
    }
  });
  step._t0 = performance.now();
  step._durEl = dur;
  step._bodyEl = body;
  openToolSteps.push(step);
  tlAppend(step);
}

// tlToolEnd closes the oldest open step with its duration, status colour
// and the (expandable) result or error text.
function tlToolEnd(output, err) {
  var step = openToolSteps.shift();
  if (!step) return;
  step.classList.remove("running");
  step.classList.add(err ? "err" : "ok");
  step._durEl.textContent = formatDuration(Math.round(performance.now() - step._t0));
  var lbl = el("div", "tl-body-label");
  lbl.textContent = err ? t("timeline.error") : t("timeline.result");
  var pre = el("pre");
  pre.textContent = clipText(err || output || "(empty)", 4000);
  step._bodyEl.appendChild(lbl);
  step._bodyEl.appendChild(pre);
}

// tlSettleOpenSteps marks any steps still "running" (stream aborted or
// errored mid-tool) so nothing keeps spinning forever.
function tlSettleOpenSteps() {
  while (openToolSteps.length) {
    var step = openToolSteps.shift();
    step.classList.remove("running");
    step.classList.add("err");
    step._durEl.textContent = t("timeline.cancelled");
  }
}

function setRunState(state, text) {
  metricRunState.textContent = state;
  composerStatus.textContent = text || state;
  composerStatus.classList.toggle("busy", state === "running");
  var prog = $("#run-progress");
  if (prog) prog.classList.toggle("running", state === "running");
}

function addToolCall(name, args) {
  tlToolStart(name, args);
  var t = el("div", "tool-card");
  t.innerHTML = '<div class="tool-name">\u2699 ' + escHtml(name) + "</div>";
  if (args && args !== "{}") {
    var d = el("details");
    d.innerHTML = "<summary>arguments</summary><pre>" + escHtml(args) + "</pre>";
    t.appendChild(d);
  }
  transcript.appendChild(t);
  smartScroll(transcript);
}

function addToolResult(output, err) {
  var cls = err ? "tool-error" : "tool-success";
  tlToolEnd(output, err);
  var t = el("div", "tool-card " + cls);
  var d = el("details");
  d.innerHTML = "<summary>" + (err ? "error" : "result") + "</summary><pre>" + escHtml(err || output || "(empty)") + "</pre>";
  t.appendChild(d);
  transcript.appendChild(t);
  smartScroll(transcript);
}

function addMarker(text) {
  var m = el("div", "marker");
  m.textContent = "\u2014 " + text + " \u2014";
  transcript.appendChild(m);
  smartScroll(transcript);
}

async function sendPrompt(text) {
  if (streaming) return;
  closeModelPicker();
  streaming = true;
  abortController = new AbortController();
  // Switch button to Stop
  sendBtn.textContent = t("composer.stop");
  sendBtn.classList.add("btn-stop");
  sendBtn.type = "button"; // prevent form submit while stopping
  setRunState("running", t("composer.working"));
  tlRunStart(text);
  addMsg("user", text);
  var current = null;
  // Start duration timer: topbar pill + Time metric + open timeline steps.
  runStartTime = Date.now();
  var metricTime = $("#metric-time");
  var topbarDur = $("#topbar-duration");
  if (metricTime) metricTime.textContent = "0.0s";
  if (topbarDur) { topbarDur.textContent = "0.0s"; topbarDur.classList.add("visible"); }
  if (runTimerInterval) clearInterval(runTimerInterval);
  runTimerInterval = setInterval(function() {
    var elapsed = Date.now() - runStartTime;
    if (metricTime) metricTime.textContent = formatDuration(elapsed);
    if (topbarDur) topbarDur.textContent = formatDuration(elapsed);
    // Tick the still-running tool steps too.
    for (var i = 0; i < openToolSteps.length; i++) {
      openToolSteps[i]._durEl.textContent = formatDuration(Math.round(performance.now() - openToolSteps[i]._t0));
    }
  }, 100);

  try {
    var resp = await fetch("/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ prompt: text, session_id: activeSessionID }),
      signal: abortController.signal,
    });
    if (!resp.ok || !resp.body) {
      addMarker("error: " + resp.status);
      addActivity("err", "request failed", String(resp.status));
      setRunState("error", t("composer.requestFailed"));
      return;
    }
    var reader = resp.body.getReader();
    var decoder = new TextDecoder();
    var buf = "";
    while (true) {
      var chunk = await reader.read();
      if (chunk.done) break;
      buf += decoder.decode(chunk.value, { stream: true });
      var frames = buf.split("\n\n");
      buf = frames.pop();
      for (var f = 0; f < frames.length; f++) {
        var line = frames[f].replace(/^data: /, "").trim();
        if (!line) continue;
        try {
          var ev = JSON.parse(line);
          current = handleEvent(ev, current);
        } catch (ex) { /* skip bad frames */ }
      }
    }
  } catch (e) {
    if (e.name === "AbortError") {
      addMarker("stopped by user");
      addActivity("", "run stopped", "user cancelled");
      setRunState("idle", t("composer.stopped"));
    } else {
      addMarker("connection error: " + e.message);
      addActivity("err", "connection", e.message);
      setRunState("error", e.message);
    }
  } finally {
    streaming = false;
    abortController = null;
    // Stop timer if still running (error/abort cases)
    if (runTimerInterval) { clearInterval(runTimerInterval); runTimerInterval = null; }
    // Steps still open at this point will never get a result event.
    tlSettleOpenSteps();
    // Restore button to Send
    sendBtn.textContent = t("composer.send");
    sendBtn.classList.remove("btn-stop");
    sendBtn.type = "submit";
    if (metricRunState.textContent === "running") setRunState("idle", t("composer.ready"));
    promptEl.focus();
  }
}

// Stop the current run
function stopRun() {
  if (abortController) {
    abortController.abort();
  }
}

function handleEvent(ev, current) {
  switch (ev.type) {
    case "message":
      if (!current) current = addMsg("assistant", "");
      current._raw = (current._raw || "") + ev.text;
      // Preserve <details> open state across re-renders to prevent
      // thinking-block glitch when user clicks during streaming.
      var openDetails = current.querySelectorAll("details[open]");
      var openKeys = {};
      for (var di = 0; di < openDetails.length; di++) {
        var dk = openDetails[di].getAttribute("data-think-id");
        if (dk) openKeys[dk] = true;
      }
      current.innerHTML = renderText(current._raw);
      // Restore open state
      if (Object.keys(openKeys).length) {
        var restored = current.querySelectorAll("details[data-think-id]");
        for (var ri = 0; ri < restored.length; ri++) {
          if (openKeys[restored[ri].getAttribute("data-think-id")]) {
            restored[ri].open = true;
          }
        }
      }
      smartScroll(transcript);
      return current;
    case "session":
      if (ev.session_id) activeSessionID = ev.session_id;
      return current;
    case "tool_call":
      addToolCall(ev.name, ev.args);
      return null;
    case "tool_result":
      addToolResult(ev.output, ev.err);
      return current;
    case "reflection":
      addMarker("reflection: " + (ev.text || "").slice(0, 80));
      addActivity("", "reflection", (ev.text || "").slice(0, 160));
      return current;
    case "sisyphus":
      addMarker("goal continuation");
      addActivity("", "goal step", ev.text || "");
      return current;
    case "compact":
      addMarker(ev.text);
      addActivity("", "context compacted", ev.text || "");
      return null;
    case "notice":
      // Provider status line (e.g. rate-limit retry wait) — visible
      // but never part of the assistant message.
      addMarker(ev.text || "");
      addActivity("", "notice", ev.text || "");
      return current;
    case "done":
      // Stop timer and compute final elapsed
      if (runTimerInterval) { clearInterval(runTimerInterval); runTimerInterval = null; }
      var elapsed = runStartTime ? (Date.now() - runStartTime) : 0;
      var elapsedStr = formatDuration(elapsed);
      var metricTime = $("#metric-time");
      var topbarDur = $("#topbar-duration");
      if (metricTime) metricTime.textContent = elapsedStr;
      if (topbarDur) { topbarDur.textContent = elapsedStr; topbarDur.classList.add("visible"); }
      metricTokens.textContent = String(ev.tok_total || 0);
      setRunState("idle", t("run.done") + " \u00b7 " + elapsedStr + " \u00b7 " + t("run.tokens") + ": " + (ev.tok_total || 0));
      addMarker("done \u00b7 " + elapsedStr + " \u00b7 tokens: " + (ev.tok_total || 0));
      addActivity("ok", "done", elapsedStr + " \u00b7 tokens: " + (ev.tok_total || 0));
      // Add duration badge to last assistant message
      var msgs = transcript.querySelectorAll(".message.assistant");
      if (msgs.length) {
        var lastMsg = msgs[msgs.length - 1];
        if (!lastMsg.querySelector(".msg-duration")) {
          var durBadge = el("span", "msg-duration");
          durBadge.textContent = elapsedStr;
          lastMsg.appendChild(durBadge);
        }
      }
      playNotifySound();
      scheduleRefresh();
      return null;
    case "error":
      setRunState("error", t("common.error") + ev.err);
      addMarker("error: " + ev.err);
      addActivity("err", "agent error", ev.err || "");
      return null;
    default:
      return current;
  }
}

// ── Composer ──
$("#composer").addEventListener("submit", function(e) {
  e.preventDefault();
  if (streaming) return; // stop button is active, don't send
  var text = promptEl.value.trim();
  if (!text) return;
  promptEl.value = "";
  promptEl.style.height = "auto";
  sendPrompt(text);
});

// Single button: Send → toggles to Stop while streaming
sendBtn.addEventListener("click", function(e) {
  if (streaming) {
    e.preventDefault();
    stopRun();
  }
  // otherwise normal submit proceeds
});

promptEl.addEventListener("keydown", function(e) {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    $("#composer").requestSubmit();
  }
});

promptEl.addEventListener("input", function() {
  this.style.height = "auto";
  this.style.height = Math.min(this.scrollHeight, 180) + "px";
});

// Quick prompts
$$(".welcome-actions [data-prompt]").forEach(function(btn) {
  btn.addEventListener("click", function() {
    var text = btn.dataset.prompt;
    if (!text) return;
    promptEl.value = text;
    $("#composer").requestSubmit();
  });
});

// New session — reset the chat view to a fresh welcome state. Reloads the
// page (the welcome card is removed from the DOM once a message is sent), so
// a clean transcript is the simplest non-destructive reset. Ignored mid-run.
var newSessionBtn = $("#new-session-btn");
if (newSessionBtn) {
  newSessionBtn.addEventListener("click", function() {
    if (streaming) return;
    activeSessionID = "";
    location.reload();
  });
}

// ── Model Picker ──
function toggleModelPicker() {
  var menu = $("#model-menu");
  menu.hidden = !menu.hidden;
  if (!menu.hidden) {
    loaders.models();
    setTimeout(function() { $("#model-search").focus(); }, 50);
  }
}

$("#model-picker-btn").addEventListener("click", toggleModelPicker);

$("#model-search").addEventListener("input", function(e) {
  renderModelRows($("#model-menu-list"), filterModels(e.target.value));
});

$("#model-scan").addEventListener("click", async function() {
  var res = await fetch("/api/provider/scan", { method: "POST" });
  var data = await res.json();
  addActivity(res.ok ? "ok" : "err", "model scan", JSON.stringify(data));
  await loaders.models();
});

function filterModels(query) {
  var q = (query || "").trim().toLowerCase();
  var pool = modelCache.filter(function(m) { return !m.hidden; });
  if (!q) return pool;
  return pool.filter(function(m) {
    return (m.id + " " + (m.provider || "")).toLowerCase().indexOf(q) !== -1;
  });
}

function renderModelRows(target, models) {
  target.innerHTML = "";
  if (!models || !models.length) {
    target.innerHTML = '<div class="empty-state">' + escHtml(t("model.noneFound")) + '</div>';
    return;
  }
  for (var i = 0; i < models.length; i++) {
    target.appendChild(modelRow(models[i]));
  }
}

function modelRow(m) {
  var row = el("div", "model-row" + (m.active ? " active" : ""));
  var info = el("div", "model-info");
  var id = el("div", "model-id");
  id.textContent = m.id;
  info.appendChild(id);
  var meta = el("div", "model-meta");
  meta.innerHTML =
    "<span>" + escHtml(m.provider || t("model.unknown")) + "</span>" +
    (m.reasoning ? '<span class="model-badge reasoning">reasoning</span>' : "") +
    (m.tool_use ? '<span class="model-badge">tools</span>' : "") +
    (m.vision ? '<span class="model-badge vision">vision</span>' : "") +
    (m.context_length ? '<span class="model-badge">ctx ' + m.context_length.toLocaleString() + '</span>' : "");
  info.appendChild(meta);
  row.appendChild(info);
  var price = el("div", "model-price");
  price.textContent = (m.input_cost || m.output_cost) ? "$" + (m.input_cost || 0) + "/$" + (m.output_cost || 0) : "\u2014";
  row.appendChild(price);
  // Explicit opt-in: write this model as the CLI's default_model in
  // config.toml. Normal row click (selectModel) is web-only state.
  var defBtn = el("button", "model-default-btn");
  defBtn.type = "button";
  defBtn.textContent = "CLI";
  defBtn.title = t("model.setDefault");
  defBtn.addEventListener("click", function(e) {
    e.stopPropagation();
    setModelAsCliDefault(m);
  });
  row.appendChild(defBtn);
  row.addEventListener("click", function() { selectModel(m); });
  return row;
}

// setModelAsCliDefault POSTs the explicit "make this the CLI default"
// action \u2014 the only model action that touches config.toml.
async function setModelAsCliDefault(m) {
  try {
    var res = await fetch("/api/model/default", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ model: m.id, provider: m.provider }),
    });
    if (!res.ok) {
      addActivity("err", t("model.setDefaultErr"), (await res.text()).trim());
      return;
    }
    addActivity("ok", t("model.setDefaultOk"), (m.provider || "?") + "/" + m.id);
  } catch (e) {
    addActivity("err", t("model.setDefaultErr"), String(e));
  }
}

async function selectModel(m) {
  addActivity("", "select model", (m.provider || "?") + "/" + m.id);
  var res = await fetch("/api/model", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model: m.id, provider: m.provider }),
  });
  if (!res.ok) {
    addActivity("err", "model switch failed", (await res.text()).trim());
    return;
  }
  // Save immediately to localStorage for instant recall on reload
  try {
    localStorage.setItem("supercli-last-model", JSON.stringify({ id: m.id, provider: m.provider }));
  } catch (e) { /* quota exceeded, ignore */ }
  pushSettings();
  closeModelPicker();
  // Update displays immediately
  $("#sidebar-model-name").textContent = m.id;
  $("#sidebar-model-provider").textContent = m.provider || "active";
  $("#model-picker-name").textContent = m.id;
  $("#model-picker-provider").textContent = m.provider || "active";
  await checkHealth();
  await loaders.models();
  addActivity("ok", "model selected", m.id);
}

// Close model picker on outside click / Escape
document.addEventListener("click", function(e) {
  var menu = $("#model-menu");
  if (!menu || menu.hidden) return;
  if (!e.target.closest(".model-picker")) closeModelPicker();
});

document.addEventListener("keydown", function(e) {
  if (e.key === "Escape") {
    if (!settingsModal.hidden) { settingsModal.hidden = true; return; }
    if (!providerModal.hidden) { closeProviderModal(); return; }
    if (streaming) { stopRun(); return; }
    closeModelPicker();
  }
});

// ── Reasoning Selectors ──

// Populate a reasoning <select> with levels from the API response
function populateReasoningSelect(selectEl, reasoning) {
  if (!selectEl || !reasoning) return;
  selectEl.innerHTML = "";
  var levels = ["default"].concat(reasoning.levels || []);
  for (var i = 0; i < levels.length; i++) {
    var opt = el("option");
    opt.value = levels[i] === "default" ? "" : levels[i];
    opt.textContent = levels[i];
    if ((reasoning.configured || "") === opt.value) opt.selected = true;
    selectEl.appendChild(opt);
  }
}

// Topbar reasoning selector
topbarReasoning.addEventListener("change", async function() {
  await fetch("/api/reasoning", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ level: topbarReasoning.value }),
  });
  await loaders.models();
  addActivity("ok", "reasoning", topbarReasoning.value || "default");
});

// Inspector panel reasoning selector
$("#reasoning-select").addEventListener("change", async function(e) {
  await fetch("/api/reasoning", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ level: e.target.value }),
  });
  await loaders.models();
  addActivity("ok", "reasoning", e.target.value || "default");
});

// ── Panel Loaders ──
var loaders = {};
var refreshTimer = null;
function scheduleRefresh() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(function() {
    if (loaders.stats) loaders.stats();
    if (loaders.models) loaders.models();
  }, 600);
}

// Sessions
loaders.sessions = async function() {
  var list = $("#sessions-list");
  list.innerHTML = "";
  try {
    var r = await fetch("/api/sessions?limit=40");
    var rows = await r.json();
    if (!rows || !rows.length) {
      list.innerHTML = '<div class="empty-state">' + escHtml(t("sessions.none")) + '</div>';
      return;
    }
    for (var i = 0; i < rows.length; i++) {
      var s = rows[i];
      var row = el("div", "list-row");
      row.innerHTML =
        '<div class="row-title">' + escHtml((s.first_user_msg || t("sessions.untitled")).slice(0, 90)) + '</div>' +
        '<div class="row-meta">' + s.message_count + " " + t("sessions.msgs") + " \u00b7 " + (s.started_at || "").replace("T", " ").slice(0, 19) + '</div>';
      row.addEventListener("click", (function(id) { return function() {
        showInspector("sessions");
        loadTranscript(id);
      };})(s.id));
      list.appendChild(row);
    }
  } catch (e) {
    list.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

async function loadTranscript(id) {
  activeSessionID = id || "";
  var view = $("#session-transcript");
  view.innerHTML = '<div class="empty-state">' + escHtml(t("common.loading")) + '</div>';
  try {
    var r = await fetch("/api/transcript?id=" + encodeURIComponent(id));
    var msgs = await r.json();
    view.innerHTML = "";
    if (!msgs || !msgs.length) {
      view.innerHTML = '<div class="empty-state">' + escHtml(t("sessions.empty")) + '</div>';
      return;
    }
    var firstUser = "";
    for (var i = 0; i < msgs.length; i++) {
      var m = msgs[i];
      if (!m.content) continue;
      if (!firstUser && m.role === "user") firstUser = m.content;
      var role = m.role === "user" ? "user" : "assistant";
      var box = el("div", "message " + role);
      var tag = el("span", "role-tag");
      tag.textContent = m.role;
      box.appendChild(tag);
      var body = el("div");
      body.innerHTML = renderText(m.content);
      box.appendChild(body);
      view.appendChild(box);
    }
    renderChatTranscript(msgs, firstUser ? firstUser.slice(0, 80) : t("sessions.untitled"));
    addActivity("ok", "session resumed", activeSessionID);
  } catch (e) {
    view.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
}

// Stats
loaders.stats = async function() {
  var cards = $("#stats-cards");
  try {
    var r = await fetch("/api/stats");
    var s = await r.json();
    cards.innerHTML = "";
    function addCard(label, value) {
      var c = el("div", "card");
      c.innerHTML = '<div class="card-label">' + escHtml(label) + '</div><div class="card-value">' + escHtml(String(value)) + '</div>';
      cards.appendChild(c);
    }
    addCard(t("stats.model"), s.model || "\u2014");
    addCard(t("stats.tokensToday"), (s.daily_tokens || 0).toLocaleString());
    addCard(t("stats.sessionTokens"), (s.session_tokens || 0).toLocaleString());
    addCard(t("stats.costToday"), "$" + (s.daily_cost || 0).toFixed(4));
    addCard(t("stats.sessionCost"), "$" + (s.session_cost || 0).toFixed(4));
  } catch (e) {
    cards.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

// Models
loaders.models = async function() {
  var list = $("#models-list");
  var reasoning = $("#reasoning-select");
  list.innerHTML = "";
  try {
    var r = await fetch("/api/models");
    var data = await r.json();
    $("#model-picker-name").textContent = data.active || t("model.none");
    $("#model-picker-provider").textContent = data.provider || t("model.unknown");
    $("#sidebar-model-name").textContent = data.active || "—";
    $("#sidebar-model-provider").textContent = data.provider || "—";
    // Reasoning selects (topbar + inspector panel)
    populateReasoningSelect(reasoning, data.reasoning);
    populateReasoningSelect(topbarReasoning, data.reasoning);
    $("#reasoning-status").textContent = data.reasoning.supported
      ? t("models.reasoningSupported") + (data.reasoning.effective || "default")
      : t("models.reasoningUnsupported");
    modelCache = data.models || [];
    // Filter out hidden models for the picker
    var visible = modelCache.filter(function(m) { return !m.hidden; });
    renderModelRows(list, visible);
    renderModelRows($("#model-menu-list"), filterModels($("#model-search").value));
    // Auto-scroll to active model in the picker
    setTimeout(function() {
      var active = $("#model-menu-list").querySelector(".model-row.active");
      if (active) active.scrollIntoView({ block: "center", behavior: "smooth" });
    }, 100);
  } catch (e) {
    list.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

// Providers
loaders.providers = async function() {
  var list = $("#providers-list");
  list.innerHTML = "";
  try {
    var r = await fetch("/api/providers");
    var data = await r.json();
    var providers = data.providers || [];
    if (!providers.length) {
      list.innerHTML = '<div class="empty-state">' + escHtml(t("providers.none")) + '</div>';
      return;
    }
    for (var i = 0; i < providers.length; i++) {
      var p = providers[i];
      var row = el("div", "list-row");
      var models = p.Models || p.models || [];
      row.innerHTML =
        '<div class="row-title">' + escHtml(p.Name || p.name) + ' \u00b7 ' + escHtml(p.Type || p.type) + '</div>' +
        '<div class="row-meta">' + escHtml(p.BaseURL || p.base_url || "") + ' \u00b7 ' + models.length + ' ' + t("providers.models") + ' \u00b7 ' + t("providers.default") + ': ' + escHtml(p.Model || p.model || "\u2014") + '</div>';
      list.appendChild(row);
    }
  } catch (e) {
    list.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

$("#scan-providers").addEventListener("click", async function() {
  var r = await fetch("/api/provider/scan", { method: "POST" });
  addActivity("ok", "provider scan", JSON.stringify(await r.json()));
  await loaders.providers();
  await loaders.models();
});

// Codex
loaders.codex = async function() {
  var list = $("#codex-list");
  list.innerHTML = "";
  try {
    var r = await fetch("/api/codex/accounts");
    // Response shape: { accounts: [...], pending_logins: [...] }
    // (older backends returned a bare array — keep tolerating
    // both so a half-upgraded UI keeps working.)
    var raw = await r.json();
    var rows = Array.isArray(raw) ? raw : (raw.accounts || []);
    if (!rows || !rows.length) {
      list.innerHTML = '<div class="empty-state">' + escHtml(t("codex.none")) + '</div>';
      return;
    }
    for (var i = 0; i < rows.length; i++) {
      var a = rows[i];
      var row = el("div", "list-row");
      row.innerHTML =
        '<div class="row-title">' + escHtml(a.label) + ' \u00b7 ' + escHtml(a.email || a.account_id || (a.logged_in ? "logged in" : "not logged in")) + '</div>' +
        '<div class="row-meta">' + escHtml(a.plan_type || "plan ?") + ' \u00b7 ' + (a.last_refresh || t("codex.noRefresh")) + '</div>';
      list.appendChild(row);
      if (a.limits) {
        var wrap = el("div");
        var pct1 = a.limits.PrimaryUsedPct || a.limits.primary_used_pct || 0;
        var pct2 = a.limits.SecondaryUsedPct || a.limits.secondary_used_pct || 0;
        wrap.innerHTML =
          '<div class="row-meta">5h: ' + pct1 + '%<div class="usage-bar' + (pct1 >= 85 ? ' danger' : pct1 >= 65 ? ' warn' : '') + '"><div class="fill" style="width:' + Math.min(100, pct1) + '%"></div></div></div>' +
          '<div class="row-meta">' + t("codex.weekly") + ': ' + pct2 + '%<div class="usage-bar' + (pct2 >= 85 ? ' danger' : pct2 >= 65 ? ' warn' : '') + '"><div class="fill" style="width:' + Math.min(100, pct2) + '%"></div></div></div>';
        list.appendChild(wrap);
      }
    }
  } catch (e) {
    list.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

// Context
loaders.context = async function() {
  var target = $("#context-report");
  target.textContent = t("common.loading");
  try {
    var r = await fetch("/api/context");
    var data = await r.json();
    target.textContent = data.text || JSON.stringify(data.report, null, 2);
  } catch (e) {
    target.textContent = t("common.error") + e.message;
  }
};

// Memory
loaders.memory = async function() {
  var list = $("#memory-list");
  list.innerHTML = "";
  try {
    var r = await fetch("/api/memory?limit=80");
    var rows = await r.json();
    if (!rows || !rows.length) {
      list.innerHTML = '<div class="empty-state">' + escHtml(t("memory.empty")) + '</div>';
      return;
    }
    for (var i = 0; i < rows.length; i++) {
      var m = rows[i];
      var row = el("div", "list-row");
      var tags = "";
      (m.tags || []).forEach(function(t) { tags += '<span class="tag">' + escHtml(t) + '</span>'; });
      row.innerHTML =
        '<div class="row-title">' + escHtml(m.content) + '</div>' +
        '<div class="row-meta"><span style="color:var(--v2-cyan-700)">' + escHtml(m.scope || "") + '</span> ' + tags + ' ' + (m.updated_at || "").replace("T", " ").slice(0, 19) + '</div>';
      list.appendChild(row);
    }
  } catch (e) {
    list.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

// Goal
loaders.goal = async function() {
  var view = $("#goal-view");
  try {
    var r = await fetch("/api/goal");
    var g = await r.json();
    if (!g) {
      view.innerHTML = '<div class="empty-state">' + escHtml(t("goal.none")) + '</div>';
      return;
    }
    var html = '<div class="goal-title">' + escHtml(g.title) + '</div>';
    html += '<div class="goal-status">' + t("goal.status") + ': ' + escHtml(g.status) + '</div>';
    var noTasks = t("goal.noTasks");
    if (g.tasks && g.tasks.length) {
      for (var i = 0; i < g.tasks.length; i++) {
        var gt = g.tasks[i];
        html += '<div class="goal-task"><span class="badge ' + gt.status + '">' + gt.status + '</span> ' + escHtml(gt.title) + '</div>';
      }
    } else {
      html += '<div class="empty-state">' + escHtml(noTasks) + '</div>';
    }
    view.innerHTML = html;
  } catch (e) {
    view.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

// Projects (named workspaces)
loaders.projects = async function() {
  var list = $("#projects-list");
  list.innerHTML = "";
  try {
    var r = await fetch("/api/projects");
    var j = await r.json();
    var rows = (j && j.projects) || [];
    if (!rows.length) {
      list.innerHTML = '<div class="empty-state">' + escHtml(t("projects.none")) + '</div>';
      return;
    }
    rows.forEach(function(p) {
      var row = el("div", "list-row" + (p.active ? " active" : ""));
      var badges = "";
      if (p.active) badges += '<span class="tag" style="color:var(--accent, #ee7330)">★ active</span>';
      if (p.cwd) badges += '<span class="tag">cwd</span>';
      if (p.model) badges += '<span class="tag">' + escHtml(p.model) + '</span>';
      row.innerHTML =
        '<div class="row-title">' + escHtml(p.name) + '</div>' +
        '<div class="row-meta">' + badges + ' ' + escHtml(p.path) + '</div>';
      row.title = t("projects.useHint");
      row.addEventListener("click", function() { projectAction("use", p.path); });
      var rm = el("button", "btn btn-tiny btn-ghost");
      rm.style.marginLeft = "auto";
      rm.textContent = t("common.remove");
      rm.addEventListener("click", function(ev) { ev.stopPropagation(); projectAction("remove", p.path); });
      row.appendChild(rm);
      list.appendChild(row);
    });
  } catch (e) {
    list.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

// ── Toast Notification ──
function showToast(msg, duration) {
  duration = duration || 2500;
  var old = $(".toast-msg");
  if (old) old.remove();
  var t = el("div", "toast-msg");
  t.textContent = msg;
  document.body.appendChild(t);
  // Force reflow for animation
  t.offsetHeight;
  t.classList.add("visible");
  setTimeout(function() { t.classList.remove("visible"); }, duration);
  setTimeout(function() { if (t.parentNode) t.remove(); }, duration + 400);
}

async function projectAction(action, target, name) {
  try {
    var r = await fetch("/api/projects", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: action, target: target || "", name: name || "" })
    });
    if (!r.ok) { showToast(t("projects.errToast") + (await r.text()), 5000); return false; }
    await loaders.projects();
    if (loaders.sidebarProjects) await loaders.sidebarProjects();
    if (loaders.railProjects) await loaders.railProjects();
    if (typeof checkHealth === "function") await checkHealth();
    if (action === "use" || action === "select") {
      showToast(t("projects.selectedToast"));
    } else if (action === "add") {
      showToast(t("projects.addedToast"), 3000);
    } else if (action === "remove" || action === "delete") {
      showToast(t("projects.removedToast"));
    }
    return true;
  } catch (e) {
    showToast(t("projects.errToast") + e.message, 5000);
    return false;
  }
}

// ── Sidebar Projects ──
loaders.sidebarProjects = async function() {
  var list = $("#sidebar-projects-list");
  if (!list) return;
  list.innerHTML = "";
  try {
    var r = await fetch("/api/projects");
    var j = await r.json();
    var rows = (j && j.projects) || [];
    if (!rows.length) {
      list.innerHTML = '<div class="sidebar-proj-empty">' + escHtml(t("projects.none")) + '</div>';
      return;
    }
    rows.forEach(function(p) {
      var item = el("div", "sidebar-proj-item" + (p.active ? " active" : ""));
      var icon = el("span", "sidebar-proj-icon");
      icon.textContent = p.active ? "★" : "◇";
      var info = el("div");
      info.style.cssText = "flex:1;min-width:0;overflow:hidden";
      info.innerHTML =
        '<div class="sidebar-proj-name">' + escHtml(p.name) + '</div>' +
        '<div class="sidebar-proj-path">' + escHtml(p.path || p.cwd || "") + '</div>';
      item.appendChild(icon);
      item.appendChild(info);
      item.title = t("projects.useHint");
      item.addEventListener("click", function() { projectAction("use", p.path); });
      list.appendChild(item);
    });
  } catch (e) {
    list.innerHTML = '<div class="sidebar-proj-empty">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

// ── Rail Projects ──
loaders.railProjects = async function() {
  var rail = $("#rail-projects");
  if (!rail) return;
  rail.innerHTML = "";
  try {
    var r = await fetch("/api/projects");
    var j = await r.json();
    var rows = (j && j.projects) || [];
    rows.forEach(function(p) {
      var btn = el("button", "rail-proj-btn" + (p.active ? " active" : ""));
      btn.title = p.name + "\n" + (p.path || p.cwd || "");
      var initial = el("span", "proj-initial");
      initial.textContent = (p.name || "?").charAt(0).toUpperCase();
      btn.appendChild(initial);
      btn.addEventListener("click", function() { projectAction("use", p.path); });
      rail.appendChild(btn);
    });
  } catch (e) { /* silent */ }
};

// ── Add Project Dialog ──
// Flow: native Windows folder picker first; the picked (or manually typed)
// path lands in a small confirm dialog with an editable display name, so a
// project is always "name + path". Cancelling the picker just falls back to
// typing the path by hand.
async function showAddProjectDialog() {
  var pickedPath = "";
  try {
    showToast(t("projects.pickerOpening"), 1200);
    var resp = await fetch("/api/folder-picker");
    if (!resp.ok) throw new Error((await resp.text()) || ("HTTP " + resp.status));
    var data = await resp.json();
    if (data.path && data.path.length > 0) {
      pickedPath = data.path;
    }
  } catch (e) {
    console.warn("[folder-picker] failed, falling back to manual:", e);
  }
  openAddProjectForm(pickedPath);
}

function openAddProjectForm(prefillPath) {
  var old = $("#add-project-dialog");
  if (old) old.remove();

  var overlay = el("div", "modal-overlay");
  overlay.id = "add-project-dialog";
  overlay.style.cssText = "position:fixed;inset:0;background:rgba(0,0,0,.6);z-index:9999;display:flex;align-items:center;justify-content:center";

  var box = el("div", "add-project-card");
  box.innerHTML =
    '<h3>' + escHtml(t("projects.addTitle")) + '</h3>' +
    '<p>' + escHtml(t("projects.addHint")) + '</p>';

  function basename(p) {
    var parts = String(p || "").replace(/[\\/]+$/, "").split(/[\\/]/);
    return parts[parts.length - 1] || "";
  }

  var nameLbl = el("label");
  nameLbl.textContent = t("projects.nameLabel");
  var nameInput = el("input", "field-input");
  nameInput.type = "text";
  nameInput.placeholder = t("projects.namePh");
  nameInput.value = basename(prefillPath);

  var pathLbl = el("label");
  pathLbl.textContent = t("projects.pathLabel");
  var pathInput = el("input", "field-input");
  pathInput.type = "text";
  pathInput.placeholder = "C:\\path\\to\\project";
  pathInput.style.fontFamily = "var(--mono)";
  pathInput.value = prefillPath || "";
  // Keep the suggested name in sync while the user types the path, until
  // they edit the name themselves.
  var nameTouched = !!nameInput.value;
  nameInput.addEventListener("input", function() { nameTouched = true; });
  pathInput.addEventListener("input", function() {
    if (!nameTouched) nameInput.value = basename(pathInput.value);
    else if (!nameInput.value) { nameTouched = false; nameInput.value = basename(pathInput.value); }
  });

  box.appendChild(nameLbl);
  box.appendChild(nameInput);
  box.appendChild(pathLbl);
  box.appendChild(pathInput);

  var btnRow = el("div", "add-project-actions");

  var cancelBtn = el("button", "btn btn-ghost btn-small");
  cancelBtn.textContent = t("common.cancel");
  cancelBtn.addEventListener("click", function() { overlay.remove(); });

  var addBtn = el("button", "btn btn-primary btn-small");
  addBtn.textContent = "＋ " + t("projects.addBtn");
  addBtn.addEventListener("click", async function() {
    var target = pathInput.value.trim();
    if (!target) { pathInput.focus(); return; }
    var ok = await projectAction("add", target, nameInput.value.trim());
    if (ok) overlay.remove();
  });

  function keyNav(e) {
    if (e.key === "Enter") { addBtn.click(); }
    if (e.key === "Escape") { overlay.remove(); }
  }
  nameInput.addEventListener("keydown", keyNav);
  pathInput.addEventListener("keydown", keyNav);
  overlay.addEventListener("click", function(e) { if (e.target === overlay) overlay.remove(); });

  btnRow.appendChild(cancelBtn);
  btnRow.appendChild(addBtn);
  box.appendChild(btnRow);
  overlay.appendChild(box);
  document.body.appendChild(overlay);
  (prefillPath ? nameInput : pathInput).focus();
}

// ── Reload Buttons ──
$("#reload-projects").addEventListener("click", function() { loaders.projects(); });
$("#project-add-cwd").addEventListener("click", function() { projectAction("add", ""); });
if ($("#sidebar-reload-projects")) $("#sidebar-reload-projects").addEventListener("click", function() { loaders.sidebarProjects(); });
if ($("#sidebar-add-project")) $("#sidebar-add-project").addEventListener("click", function() { showAddProjectDialog(); });
if ($("#rail-add-project")) $("#rail-add-project").addEventListener("click", function() { showAddProjectDialog(); });
$("#reload-sessions").addEventListener("click", function() { loaders.sessions(); });
$("#reload-stats").addEventListener("click", function() { loaders.stats(); });
$("#reload-models").addEventListener("click", function() { loaders.models(); });
$("#reload-codex").addEventListener("click", function() { loaders.codex(); });
$("#reload-context").addEventListener("click", function() { loaders.context(); });
$("#reload-memory").addEventListener("click", function() { loaders.memory(); });
$("#reload-goal").addEventListener("click", function() { loaders.goal(); });
$("#clear-activity").addEventListener("click", function() {
  openToolSteps = [];
  runToolCount = 0;
  if (metricTools) metricTools.textContent = "0";
  activityList.innerHTML = '<div class="empty-state">' + escHtml(t("activity.empty")) + '</div>';
});

// ── Auto-scan models on startup ──
async function autoScanModels() {
  try {
    var r = await fetch("/api/provider/scan", { method: "POST" });
    var data = await r.json();
    if (data.count > 0) {
      addActivity("ok", "auto-scan", data.count + " models found across providers");
    }
  } catch (e) {
    // silent — scan is best-effort
  }
}

// ── Provider Modal ──
var providerModal = $("#provider-modal");
var modalStatus = $("#modal-status");
var providerGrid = $("#provider-templates");
var customForm = $("#provider-custom-form");
var providerTemplateSearch = $("#provider-template-search");
var providerTemplatesCache = [];

function openProviderModal() {
  providerModal.hidden = false;
  modalStatus.textContent = t("provider.statusHint");
  modalStatus.className = "";
  customForm.reset();
  // Default to custom mode — all fields editable
  customForm.elements.namedItem("name").readOnly = false;
  customForm.elements.namedItem("type").disabled = false;
  customForm.elements.namedItem("base_url").readOnly = false;
  // Clear active chip highlight
  $$(".provider-chip", providerGrid).forEach(function(c) { c.classList.remove("active"); });
  updateSelectedProvider(null);
  loadProviderTemplates();
}
function closeProviderModal() { providerModal.hidden = true; }

$("#add-provider-btn").addEventListener("click", openProviderModal);
$("#provider-panel-add").addEventListener("click", openProviderModal);
$("#modal-close").addEventListener("click", closeProviderModal);
providerModal.addEventListener("click", function(e) {
  if (e.target === providerModal) closeProviderModal();
});

async function loadProviderTemplates() {
  providerGrid.innerHTML = '<div class="empty-state">' + escHtml(t("common.loading")) + '</div>';
  try {
    var r = await fetch("/api/providers");
    var data = await r.json();
    providerTemplatesCache = data.templates || [];
    renderProviderTemplates(providerTemplatesCache);
  } catch (e) { providerGrid.innerHTML = '<div class="empty-state">' + escHtml(t("common.failed")) + '</div>'; }
}

function renderProviderTemplates(templates) {
  providerGrid.innerHTML = "";
  if (!templates.length) { providerGrid.innerHTML = '<div class="empty-state">' + escHtml(t("providers.none")) + '</div>'; return; }
  for (var i = 0; i < templates.length; i++) {
    var tpl = templates[i];
    var chip = el("button", "provider-chip");
    chip.type = "button";
    chip.innerHTML =
      '<div class="chip-name">' + escHtml(tpl.Name || tpl.name) + '</div>' +
      '<div class="chip-url">' + escHtml(tpl.BaseURL || tpl.base_url || "custom") + '</div>' +
      '<div class="chip-desc">' + escHtml(tpl.Desc || tpl.desc || "") + '</div>';
    chip.addEventListener("click", (function(tmpl, chipEl) { return function() {
      $$(".provider-chip", providerGrid).forEach(function(c) { c.classList.remove("active"); });
      chipEl.classList.add("active");
      fillProviderForm(tmpl);
    };})(tpl, chip));
    providerGrid.appendChild(chip);
  }
}

providerTemplateSearch.addEventListener("input", function() {
  var q = providerTemplateSearch.value.trim().toLowerCase();
  var filtered = !q ? providerTemplatesCache : providerTemplatesCache.filter(function(t) {
    return ((t.Name || t.name || "") + " " + (t.Desc || t.desc || "") + " " + (t.BaseURL || t.base_url || "")).toLowerCase().indexOf(q) >= 0;
  });
  renderProviderTemplates(filtered);
});

function updateSelectedProvider(tmpl) {
  var box = $("#provider-selected");
  if (!tmpl) {
    box.innerHTML = '<strong>' + escHtml(t("provider.custom")) + '</strong><span>' + escHtml(t("provider.customHint")) + '</span>';
    return;
  }
  box.innerHTML = '<strong>' + escHtml(tmpl.Name || tmpl.name) + '</strong><span>' + escHtml(tmpl.Desc || tmpl.desc || "") + '</span>';
}

function fillProviderForm(tmpl) {
  var name = tmpl.Name || tmpl.name;
  var isCustom = name === "custom";
  updateSelectedProvider(tmpl);

  var nameEl = customForm.elements.namedItem("name");
  var typeEl = customForm.elements.namedItem("type");
  var urlEl  = customForm.elements.namedItem("base_url");
  var keyEl  = customForm.elements.namedItem("api_key");

  nameEl.value = isCustom ? "" : name;
  typeEl.value = tmpl.Type || tmpl.type || "openai";
  urlEl.value = tmpl.BaseURL || tmpl.base_url || "";
  keyEl.value = name === "lmstudio" ? "lm-studio" : (name === "ollama" ? "ollama" : "");

  // Predefined = lock name/type/url, only key editable. Custom = all editable.
  nameEl.readOnly = !isCustom;
  typeEl.disabled = !isCustom;
  urlEl.readOnly = !isCustom;

  if (isCustom) {
    nameEl.focus();
  } else {
    keyEl.focus();
  }
}

customForm.addEventListener("submit", async function(e) {
  e.preventDefault();
  var fd = new FormData(e.currentTarget);
  var body = Object.fromEntries(fd.entries());
  modalStatus.textContent = t("provider.adding") + body.name + "…"; modalStatus.className = "";
  try {
    var r = await fetch("/api/providers", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    if (r.ok) {
      modalStatus.textContent = t("provider.addedScanning"); modalStatus.className = "ok";
      await fetch("/api/provider/scan?name=" + encodeURIComponent(body.name), { method: "POST" });
      await loaders.providers(); await loaders.models();
      modalStatus.textContent = body.name + t("provider.ready");
      setTimeout(closeProviderModal, 1200); customForm.reset();
    } else {
      modalStatus.textContent = t("common.error") + (await r.text()); modalStatus.className = "err";
    }
  } catch (ex) {
    modalStatus.textContent = t("common.error") + ex.message; modalStatus.className = "err";
  }
});

// ── Settings → Providers tab (list / add / edit / codex accounts) ──
// Self-contained module. Three views swap in the same panel:
//   - list : table of configured providers + "Add provider" button
//   - add  : template picker + custom form, with "Back" to list
//   - edit : inline form for editing an existing provider
// Codex providers are rendered with per-account actions
// (login/logout/refresh) instead of an Edit form, because they
// authenticate via OAuth tokens rather than API keys.
var sp = {
  // DOM refs (populated by init)
  viewList:      null,
  viewAdd:       null,
  viewEdit:      null,
  listEl:        null,
  reloadBtn:     null,
  showAddBtn:    null,
  addCancelBtn:  null,
  editCancelBtn: null,
  editForm:      null,
  editNameLbl:   null,
  editStatus:    null,
  clearKeyChk:   null,
  addForm:       null,
  addStatus:     null,
  addCancelBtn2: null,
  templateList:  null,
  templateSearch:null,
  // State
  cache:         [],          // last []ProviderInfo from /api/providers
  templates:     [],          // last templates list
  codexAccounts: [],          // last []codexAccountView from /api/codex/accounts
  pendingLogins:  [],          // last []string of label-level logins in flight
  editingName:   null,        // provider name being edited, or null
  loginPollTimer:null         // setInterval handle for login-progress polling
};

sp.init = function() {
  sp.viewList       = $("#sp-view-list");
  sp.viewAdd        = $("#sp-view-add");
  sp.viewEdit       = $("#sp-view-edit");
  sp.listEl         = $("#sp-list");
  sp.reloadBtn      = $("#sp-reload");
  sp.showAddBtn     = $("#sp-show-add");
  sp.addCancelBtn   = $("#sp-add-cancel");
  sp.editCancelBtn  = $("#sp-edit-cancel");
  sp.editForm       = $("#sp-edit-form");
  sp.editNameLbl    = $("#sp-edit-name");
  sp.editStatus     = $("#sp-edit-status");
  sp.clearKeyChk    = $("#sp-clear-key");
  sp.addForm        = $("#sp-add-form");
  sp.addStatus      = $("#sp-add-status");
  sp.templateList   = $("#sp-templates");
  sp.templateSearch = $("#sp-template-search");

  sp.reloadBtn.addEventListener("click", sp.reload);
  sp.showAddBtn.addEventListener("click", function(){ sp.showView("add"); });
  sp.addCancelBtn.addEventListener("click", function(){ sp.showView("list"); });
  sp.editCancelBtn.addEventListener("click", sp.cancelEdit);
  sp.editForm.addEventListener("submit", sp.onSubmitEdit);
  sp.addForm.addEventListener("submit", sp.onSubmitAdd);
  sp.templateSearch.addEventListener("input", sp.onTemplateSearch);
};

// ── View switching ──
sp.showView = function(name) {
  sp.viewList.style.display = (name === "list") ? "" : "none";
  sp.viewAdd.style.display  = (name === "add")  ? "" : "none";
  sp.viewEdit.style.display = (name === "edit") ? "" : "none";
  if (name === "add") {
    // Pre-fill template list lazily on first show.
    if (!sp.templates.length) sp.fetchTemplates();
    sp.addStatus.textContent = ""; sp.addStatus.className = "";
    sp.addForm.reset();
    $$(".provider-chip", sp.templateList).forEach(function(c){ c.classList.remove("active"); });
  }
};

// ── Data fetchers ──
sp.fetchTemplates = async function() {
  try {
    var r = await fetch("/api/providers");
    var data = await r.json();
    sp.templates = data.templates || [];
    sp.renderTemplates(sp.templates);
  } catch (e) { /* silent */ }
};

sp.fetchCodexAccounts = async function() {
  try {
    var r = await fetch("/api/codex/accounts");
    var raw = await r.json();
    // Tolerate both shapes (new {accounts, pending_logins} and
    // legacy bare array) so the UI is resilient to a backend
    // running an older or newer build than itself.
    if (Array.isArray(raw)) {
      sp.codexAccounts = raw;
      sp.pendingLogins = [];
    } else {
      sp.codexAccounts = raw.accounts || [];
      sp.pendingLogins = raw.pending_logins || [];
    }
  } catch (e) {
    sp.codexAccounts = [];
    sp.pendingLogins = [];
  }
};

sp.reload = async function() {
  sp.listEl.innerHTML = '<div class="empty-state">' + escHtml(t("common.loading")) + '</div>';
  try {
    var r = await fetch("/api/providers");
    var data = await r.json();
    sp.cache = data.providers || [];
    sp.templates = data.templates || [];
    sp.renderTemplates(sp.templates);
    // Codex accounts are a separate resource; fetch them in
    // parallel with the providers list so per-account actions
    // (login/logout/refresh) appear on the codex rows.
    await sp.fetchCodexAccounts();
    sp.renderList(sp.cache);
    // If any login is currently in progress, keep polling.
    if (sp.hasLoginInProgress() && !sp.loginPollTimer) {
      sp.startLoginPoll();
    }
  } catch (e) {
    sp.listEl.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>';
  }
};

sp.hasLoginInProgress = function() {
  // Existing accounts whose login is still running
  for (var i = 0; i < sp.codexAccounts.length; i++) {
    if (sp.codexAccounts[i].login_in_progress || sp.codexAccounts[i].LoginInProgress) return true;
  }
  // Labels whose login has started but no auth file yet (the
  // very first /login for a brand-new account would never show
  // up under existing accounts).
  if (sp.pendingLogins && sp.pendingLogins.length) return true;
  return false;
};

// startLoginPoll kicks a 2-second refresh loop. The loop stops
// when no login is in progress anymore OR a hard 5-minute ceiling
// expires (matches the backend OAuth timeout). Each tick re-fetches
// accounts and re-renders the list — so as soon as the new account
// appears in /api/codex/accounts, the user sees it without a manual
// refresh.
sp.startLoginPoll = function() {
  if (sp.loginPollTimer) return;
  var deadline = Date.now() + 5 * 60 * 1000; // 5 min cap
  sp.loginPollTimer = setInterval(async function() {
    await sp.fetchCodexAccounts();
    sp.renderList(sp.cache);
    var stillGoing = sp.hasLoginInProgress();
    var timedOut = Date.now() >= deadline;
    if (!stillGoing || timedOut) {
      sp.stopLoginPoll();
      // One last render in case the final tick raced ahead of the
      // state change, plus a usage refresh so the sidebar catches up.
      sp.renderList(sp.cache);
      if (loaders.codex) try { await loaders.codex(); } catch (e) {}
    }
  }, 2000);
};

sp.stopLoginPoll = function() {
  if (sp.loginPollTimer) {
    clearInterval(sp.loginPollTimer);
    sp.loginPollTimer = null;
  }
};

// ── Rendering ──
sp.renderList = function(list) {
  sp.listEl.innerHTML = "";
  if (!list || !list.length) {
    sp.listEl.innerHTML = '<div class="empty-state">' + escHtml(t("providers.none")) + '</div>';
    return;
  }
  for (var i = 0; i < list.length; i++) {
    sp.listEl.appendChild(sp.buildRow(list[i]));
  }
};

sp.buildRow = function(p) {
  var type = (p.Type || p.type || "").toLowerCase();
  if (type === "codex") return sp.buildCodexRow(p);
  return sp.buildStandardRow(p);
};

sp.buildStandardRow = function(p) {
  var row = el("div", "sp-provider-row");
  if (sp.editingName && sp.editingName === (p.Name || p.name)) row.classList.add("is-active");

  var head = el("div", "sp-provider-head");
  var titleWrap = el("div");
  titleWrap.innerHTML =
    '<span class="sp-provider-name">' + escHtml(p.Name || p.name) + '</span> ' +
    '<span class="sp-provider-type">' + escHtml(p.Type || p.type || "") + '</span>';
  var keyBadge = el("span", "sp-key-badge " + (p.HasKey ? "has-key" : "no-key"));
  keyBadge.textContent = p.HasKey ? "🔑 key" : "no key";

  var actions = el("div", "sp-provider-actions");
  var editBtn = el("button", "btn btn-tiny btn-ghost");
  editBtn.type = "button"; editBtn.textContent = t("common.edit");
  editBtn.addEventListener("click", (function(name){ return function(){ sp.beginEdit(name); }; })(p.Name || p.name));

  var delBtn = el("button", "btn btn-tiny btn-ghost");
  delBtn.type = "button"; delBtn.textContent = t("common.delete");
  delBtn.addEventListener("click", (function(name, rowEl){ return function(){ sp.confirmDelete(name, rowEl); }; })(p.Name || p.name, row));

  actions.appendChild(editBtn);
  actions.appendChild(delBtn);
  head.appendChild(titleWrap);
  head.appendChild(actions);

  var meta = el("div", "sp-provider-meta");
  var modelCount = (p.Models || p.models || []).length;
  meta.textContent = (p.BaseURL || p.base_url || "") + "  ·  " + modelCount + " " + t("providers.models") + "  ·  " + t("providers.default") + ": " + (p.Model || p.model || "—");

  var footer = el("div", "sp-provider-head");
  footer.style.justifyContent = "space-between";
  footer.appendChild(keyBadge);

  row.appendChild(head);
  row.appendChild(meta);
  row.appendChild(footer);
  return row;
};

sp.buildCodexRow = function(p) {
  var row = el("div", "sp-provider-row sp-provider-codex");

  // Header: provider name + ChatGPT type + delete-only action
  var head = el("div", "sp-provider-head");
  var titleWrap = el("div");
  titleWrap.innerHTML =
    '<span class="sp-provider-name">' + escHtml(p.Name || p.name) + '</span> ' +
    '<span class="sp-provider-type">ChatGPT account</span>';
  var actions = el("div", "sp-provider-actions");
  var delBtn = el("button", "btn btn-tiny btn-ghost");
  delBtn.type = "button"; delBtn.textContent = t("common.delete");
  delBtn.addEventListener("click", (function(name, rowEl){ return function(){ sp.confirmDelete(name, rowEl); }; })(p.Name || p.name, row));
  actions.appendChild(delBtn);
  head.appendChild(titleWrap);
  head.appendChild(actions);

  // Meta: backend URL
  var meta = el("div", "sp-provider-meta");
  meta.textContent = p.BaseURL || p.base_url || "";

  row.appendChild(head);
  row.appendChild(meta);

  // Per-account subrows
  var accounts = sp.codexAccounts || [];
  var pending = sp.pendingLogins || [];
  if (accounts.length || pending.length) {
    var wrap = el("div", "sp-codex-accounts");
    for (var i = 0; i < accounts.length; i++) {
      wrap.appendChild(sp.buildCodexAccountRow(accounts[i]));
    }
    // Pending logins for labels whose auth file does not exist
    // yet (e.g. the very first /login on a fresh install). Without
    // these "ghost" rows the user would not see that their click
    // registered — the spinner only appears after the new auth
    // file lands, which can be 30+ seconds into the OAuth flow.
    for (var j = 0; j < pending.length; j++) {
      wrap.appendChild(sp.buildCodexPendingRow(pending[j]));
    }
    row.appendChild(wrap);
  }

  // Footer: "+ Add account" button + inline label prompt.
  // Matches the TUI flow where the user is asked for an account
  // name (e.g. "praca", "prywatne") before the OAuth browser
  // window opens. Without this prompt the second account would
  // silently overwrite auth-default.json.
  var footer = el("div", "sp-provider-head sp-codex-footer");
  footer.style.justifyContent = "flex-start";
  footer.style.flexWrap = "wrap";

  var addAcctBtn = el("button", "btn btn-tiny btn-ghost");
  addAcctBtn.type = "button";
  addAcctBtn.innerHTML = '<span aria-hidden="true">＋</span> ' + escHtml(t("providers.addAccount"));
  footer.appendChild(addAcctBtn);

  // Inline form (hidden until the button is clicked)
  var addForm = el("form", "sp-codex-add-form");
  addForm.style.display = "none";
  var labelInput = el("input", "field-input sp-codex-label-input");
  labelInput.type = "text";
  labelInput.name = "label";
  labelInput.placeholder = t("providers.labelPh");
  labelInput.autocomplete = "off";
  labelInput.spellcheck = false;
  var submitBtn = el("button", "btn btn-tiny btn-primary");
  submitBtn.type = "submit";
  submitBtn.textContent = t("providers.login");
  var cancelBtn = el("button", "btn btn-tiny btn-ghost");
  cancelBtn.type = "button";
  cancelBtn.textContent = t("common.cancel");
  addForm.appendChild(labelInput);
  addForm.appendChild(submitBtn);
  addForm.appendChild(cancelBtn);
  footer.appendChild(addForm);

  // State machine: button ↔ form. Hiding the button avoids two
  // competing affordances (button + Enter-to-submit form) being
  // visible at the same time.
  function showForm() {
    addAcctBtn.style.display = "none";
    addForm.style.display = "";
    // Always pre-fill with a sensible suggestion so the user can
    // just press Enter. generateLabel() picks "default" for the
    // first account, otherwise the next free slot (account-2,
    // account-3, …). The value is selected on focus so a single
    // keystroke replaces it without manual clearing.
    labelInput.value = sp.generateLabel();
    setTimeout(function(){ labelInput.focus(); labelInput.select(); }, 0);
  }
  function hideForm() {
    addForm.style.display = "none";
    addAcctBtn.style.display = "";
  }
  addAcctBtn.addEventListener("click", showForm);
  cancelBtn.addEventListener("click", hideForm);
  addForm.addEventListener("submit", function(e) {
    e.preventDefault();
    // Empty / whitespace is allowed — we fall back to a generated
    // label so the user can always proceed. This is intentionally
    // more permissive than the TUI label menu, which blocks empty
    // submits; the web UI optimises for "click → Enter → done".
    var label = (labelInput.value || "").trim();
    if (!label) label = sp.generateLabel();
    hideForm();
    sp.startLogin(label);
  });
  // Esc cancels without firing submit
  labelInput.addEventListener("keydown", function(e) {
    if (e.key === "Escape") { e.preventDefault(); hideForm(); }
  });

  row.appendChild(footer);
  return row;
};

sp.buildCodexAccountRow = function(a) {
  var wrap = el("div", "sp-codex-account");
  var label = a.label || a.Label || "";
  var email = a.email || a.Email || "";
  var plan = a.plan_type || a.PlanType || "";
  var loggedIn = a.logged_in || a.LoggedIn;
  var inProg = a.login_in_progress || a.LoginInProgress;
  var loginErr = a.login_error || a.LoginError || "";
  var lastRefresh = a.last_refresh || a.LastRefresh || "";

  // Status badge
  var badge;
  if (inProg) {
    badge = el("span", "sp-status-badge logging-in");
    badge.innerHTML = '<span class="sp-spinner"></span> ' + escHtml(t("providers.loggingIn"));
  } else if (loggedIn) {
    badge = el("span", "sp-status-badge logged-in");
    badge.textContent = "✓ " + t("providers.loggedIn");
  } else {
    badge = el("span", "sp-status-badge logged-out");
    badge.textContent = t("providers.loggedOut");
  }

  // Title row
  var title = el("div", "sp-codex-acct-head");
  var titleText = el("div");
  titleText.innerHTML =
    '<strong>' + escHtml(label) + '</strong> ' +
    '<span class="sp-codex-email">' + escHtml(email || "(" + t("providers.unknownEmail") + ")") + '</span>';
  if (plan) {
    var planPill = el("span", "sp-plan-pill");
    planPill.textContent = plan;
    titleText.appendChild(planPill);
  }
  title.appendChild(titleText);
  title.appendChild(badge);

  // Meta line
  var metaParts = [];
  if (lastRefresh) metaParts.push(t("providers.lastRefresh") + ": " + sp.fmtTime(lastRefresh));
  if (loginErr) metaParts.push("⚠ " + loginErr);
  if (a.limits) {
    var pct1 = a.limits.PrimaryUsedPct || a.limits.primary_used_pct || 0;
    var pct2 = a.limits.SecondaryUsedPct || a.limits.secondary_used_pct || 0;
    metaParts.push("5h: " + pct1 + "% · " + t("providers.weekly") + ": " + pct2 + "%");
  }
  var meta = el("div", "sp-codex-acct-meta");
  meta.textContent = metaParts.join("  ·  ");

  // Action buttons
  var act = el("div", "sp-codex-acct-actions");
  if (inProg) {
    // While logging in, only a disabled "Cancel"-style hint. The
    // backend continues the OAuth flow in the background; the user
    // completes it in the opened browser window.
    var waitBtn = el("button", "btn btn-tiny btn-ghost");
    waitBtn.disabled = true;
    waitBtn.textContent = t("providers.waitLogin");
    act.appendChild(waitBtn);
  } else if (loggedIn) {
    var refBtn = el("button", "btn btn-tiny btn-ghost");
    refBtn.type = "button"; refBtn.textContent = t("providers.refresh");
    refBtn.addEventListener("click", (function(l){ return function(){ sp.refreshAccount(l); }; })(label));
    var outBtn = el("button", "btn btn-tiny btn-ghost");
    outBtn.type = "button"; outBtn.textContent = t("providers.logout");
    outBtn.addEventListener("click", (function(l){ return function(){ sp.confirmLogout(l); }; })(label));
    act.appendChild(refBtn);
    act.appendChild(outBtn);
  } else {
    var loginBtn = el("button", "btn btn-tiny btn-primary");
    loginBtn.type = "button"; loginBtn.textContent = t("providers.login");
    loginBtn.addEventListener("click", (function(l){ return function(){ sp.startLogin(l); }; })(label));
    act.appendChild(loginBtn);
  }

  wrap.appendChild(title);
  if (meta.textContent) wrap.appendChild(meta);
  wrap.appendChild(act);
  return wrap;
};

sp.fmtTime = function(iso) {
  try {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleString();
  } catch (e) { return iso; }
};

// generateLabel returns a free account label that can safely be
// used for a new login without colliding with existing accounts.
// Convention: "default" for the very first account, then
// "account-2", "account-3", … for subsequent ones. Used both to
// pre-fill the inline prompt and as the fallback when the user
// submits an empty name.
sp.generateLabel = function() {
  var taken = {};
  for (var i = 0; i < sp.codexAccounts.length; i++) {
    var l = (sp.codexAccounts[i].label || "").toLowerCase();
    if (l) taken[l] = true;
  }
  // Also count in-flight logins so two rapid "Add account" clicks
  // don't both resolve to the same generated name.
  if (sp.pendingLogins) {
    for (var j = 0; j < sp.pendingLogins.length; j++) {
      taken[(sp.pendingLogins[j] || "").toLowerCase()] = true;
    }
  }
  if (!taken["default"]) return "default";
  var n = 2;
  while (taken["account-" + n]) n++;
  return "account-" + n;
};

// buildCodexPendingRow renders a "ghost" subrow for an in-flight
// login whose label has no auth file yet. Same visual language as
// a real account row (label + status badge + spinner), but no
// actions because the backend is still mid-flow.
sp.buildCodexPendingRow = function(label) {
  var wrap = el("div", "sp-codex-account is-pending");
  var title = el("div", "sp-codex-acct-head");
  var titleText = el("div");
  titleText.innerHTML = '<strong>' + escHtml(label || "(new)") + '</strong>';
  var badge = el("span", "sp-status-badge logging-in");
  badge.innerHTML = '<span class="sp-spinner"></span> ' + escHtml(t("providers.loggingIn"));
  title.appendChild(titleText);
  title.appendChild(badge);

  var meta = el("div", "sp-codex-acct-meta");
  meta.textContent = t("providers.waitLogin");

  wrap.appendChild(title);
  wrap.appendChild(meta);
  return wrap;
};

// ── Codex account actions ──
sp.startLogin = async function(label) {
  try {
    var r = await fetch("/api/codex/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label: label || "" })
    });
    var data = await r.json();
    if (r.ok) {
      // Browser should have opened; begin polling for completion.
      sp.startLoginPoll();
      await sp.fetchCodexAccounts();
      sp.renderList(sp.cache);
    } else {
      alert(t("common.error") + (data.error || await r.text()));
    }
  } catch (e) {
    alert(t("common.error") + e.message);
  }
};

sp.confirmLogout = async function(label) {
  if (!confirm(t("providers.confirmLogout") + " \"" + label + "\"?")) return;
  try {
    var r = await fetch("/api/codex/logout", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label: label })
    });
    if (r.ok) {
      addActivity("ok", "codex", "logged out " + label);
      await sp.fetchCodexAccounts();
      sp.renderList(sp.cache);
    } else {
      alert(t("common.error") + (await r.text()));
    }
  } catch (e) { alert(t("common.error") + e.message); }
};

sp.refreshAccount = async function(label) {
  try {
    var r = await fetch("/api/codex/refresh", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label: label })
    });
    if (r.ok) {
      addActivity("ok", "codex", "refreshed " + label);
      await sp.fetchCodexAccounts();
      sp.renderList(sp.cache);
    } else {
      alert(t("common.error") + (await r.text()));
    }
  } catch (e) { alert(t("common.error") + e.message); }
};

// ── Edit (standard providers only) ──
sp.beginEdit = function(name) {
  var p = null;
  for (var i = 0; i < sp.cache.length; i++) {
    if ((sp.cache[i].Name || sp.cache[i].name) === name) { p = sp.cache[i]; break; }
  }
  if (!p) return;
  sp.editingName = name;
  sp.editNameLbl.textContent = name;
  sp.editForm.elements.namedItem("name").value = name;
  sp.editForm.elements.namedItem("type").value = p.Type || p.type || "openai";
  sp.editForm.elements.namedItem("base_url").value = p.BaseURL || p.base_url || "";
  sp.editForm.elements.namedItem("model").value = p.Model || p.model || "";
  sp.editForm.elements.namedItem("api_key").value = "";
  sp.editForm.elements.namedItem("api_key").placeholder = p.HasKey ? t("providers.keySetKeepEmpty") : t("providers.noKeyKeepEmpty");
  sp.clearKeyChk.checked = false;
  sp.editStatus.textContent = ""; sp.editStatus.className = "";
  sp.showView("edit");
};

sp.cancelEdit = function() {
  sp.editingName = null;
  sp.editStatus.textContent = "";
  sp.showView("list");
};

sp.onSubmitEdit = async function(e) {
  e.preventDefault();
  if (!sp.editingName) return;
  var fd = new FormData(e.currentTarget);
  var body = { name: fd.get("name") };
  body.type     = fd.get("type");
  body.base_url = fd.get("base_url");
  body.model    = fd.get("model");
  var keyVal = (fd.get("api_key") || "").toString();
  var clearKey = sp.clearKeyChk.checked;
  if (clearKey) {
    body.api_key = "";
  } else if (keyVal.trim() !== "") {
    body.api_key = keyVal;
  }
  sp.editStatus.textContent = t("providers.saving"); sp.editStatus.className = "";
  try {
    var r = await fetch("/api/providers", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    var data = await r.json();
    if (r.ok) {
      sp.editStatus.textContent = t("providers.saved"); sp.editStatus.className = "sp-status-ok";
      await sp.reload();
      if (loaders.providers) await loaders.providers();
      if (loaders.models) await loaders.models();
      addActivity("ok", "provider", "updated " + body.name);
      setTimeout(function(){
        if (sp.editingName === body.name) sp.cancelEdit();
      }, 700);
    } else {
      sp.editStatus.textContent = t("common.error") + (data.error || ""); sp.editStatus.className = "sp-status-err";
    }
  } catch (ex) {
    sp.editStatus.textContent = t("common.error") + ex.message; sp.editStatus.className = "sp-status-err";
  }
};

// ── Delete (both codex and standard providers) ──
sp.confirmDelete = async function(name, rowEl) {
  if (!confirm(t("providers.confirmDelete") + " \"" + name + "\"?\n\n" + t("providers.deleteWarning"))) return;
  rowEl.style.opacity = "0.5";
  try {
    var r = await fetch("/api/providers?name=" + encodeURIComponent(name), { method: "DELETE" });
    if (r.ok) {
      if (sp.editingName === name) sp.cancelEdit();
      await sp.reload();
      if (loaders.providers) await loaders.providers();
      if (loaders.models) await loaders.models();
      addActivity("ok", "provider", "deleted " + name);
    } else {
      rowEl.style.opacity = "";
      alert(t("common.error") + (await r.text()));
    }
  } catch (e) {
    rowEl.style.opacity = "";
    alert(t("common.error") + e.message);
  }
};

// ── Add new provider ──
sp.renderTemplates = function(templates) {
  sp.templateList.innerHTML = "";
  if (!templates || !templates.length) {
    sp.templateList.innerHTML = '<div class="empty-state">' + escHtml(t("providers.none")) + '</div>';
    return;
  }
  for (var i = 0; i < templates.length; i++) {
    (function(tpl){
      var chip = el("button", "provider-chip");
      chip.type = "button";
      chip.innerHTML =
        '<div class="chip-name">' + escHtml(tpl.Name || tpl.name) + '</div>' +
        '<div class="chip-url">' + escHtml(tpl.BaseURL || tpl.base_url || "custom") + '</div>' +
        '<div class="chip-desc">' + escHtml(tpl.Desc || tpl.desc || "") + '</div>';
      chip.addEventListener("click", function(){
        $$(".provider-chip", sp.templateList).forEach(function(c){ c.classList.remove("active"); });
        chip.classList.add("active");
        sp.fillAddForm(tpl);
      });
      sp.templateList.appendChild(chip);
    })(templates[i]);
  }
};

sp.fillAddForm = function(tmpl) {
  var name = tmpl.Name || tmpl.name;
  var isCustom = name === "custom";
  var f = sp.addForm;
  f.elements.namedItem("name").value = isCustom ? "" : name;
  f.elements.namedItem("type").value = tmpl.Type || tmpl.type || "openai";
  f.elements.namedItem("base_url").value = tmpl.BaseURL || tmpl.base_url || "";
  f.elements.namedItem("api_key").value = name === "lmstudio" ? "lm-studio" : (name === "ollama" ? "ollama" : "");
  if (isCustom) f.elements.namedItem("name").focus();
  else f.elements.namedItem("api_key").focus();
};

sp.onTemplateSearch = function() {
  var q = sp.templateSearch.value.trim().toLowerCase();
  $$(".provider-chip", sp.templateList).forEach(function(c){
    c.style.display = c.textContent.toLowerCase().indexOf(q) >= 0 ? "" : "none";
  });
};

sp.onSubmitAdd = async function(e) {
  e.preventDefault();
  var fd = new FormData(e.currentTarget);
  var body = Object.fromEntries(fd.entries());
  if (!body.name || !body.name.trim()) return;
  sp.addStatus.textContent = t("provider.adding") + body.name + "…"; sp.addStatus.className = "";
  try {
    var r = await fetch("/api/providers", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    var data = await r.json();
    if (r.ok) {
      sp.addStatus.textContent = t("provider.addedScanning"); sp.addStatus.className = "sp-status-ok";
      await fetch("/api/provider/scan?name=" + encodeURIComponent(body.name), { method: "POST" });
      await sp.reload();
      if (loaders.providers) await loaders.providers();
      if (loaders.models) await loaders.models();
      sp.addStatus.textContent = body.name + t("provider.ready");
      addActivity("ok", "provider", "added " + body.name);
      setTimeout(function(){ sp.showView("list"); }, 900);
    } else {
      sp.addStatus.textContent = t("common.error") + (data.error || ""); sp.addStatus.className = "sp-status-err";
    }
  } catch (ex) {
    sp.addStatus.textContent = t("common.error") + ex.message; sp.addStatus.className = "sp-status-err";
  }
};

// Initialize the module once the DOM is ready (this script is
// loaded at the end of <body>, so elements are present).
sp.init();

// The old "Add Provider" modal is intentionally kept as a
// quick-add shortcut from the topbar "+" and inspector — both
// entry points coexist with the richer Settings → Providers tab.

// ── Settings Modal & Theme ──
var settingsModal = $("#settings-modal");
var themeDark = $("#theme-dark");
var themeLight = $("#theme-light");

function applyTheme(theme) {
  document.documentElement.setAttribute("data-theme", theme);
  try { localStorage.setItem("supercli-theme", theme); } catch(e) {}
  pushSettings();
  themeDark.classList.toggle("active", theme === "dark");
  themeLight.classList.toggle("active", theme === "light");
}

// Init theme
var savedTheme = "dark";
try { savedTheme = localStorage.getItem("supercli-theme") || "dark"; } catch(e) {}
applyTheme(savedTheme);

// Settings tabs
$$(".settings-tab").forEach(function(tab) {
  tab.addEventListener("click", function() {
    // Persist any unsaved changes before switching tabs
    saveSettings();
    $$(".settings-tab").forEach(function(t) { t.classList.remove("active"); });
    tab.classList.add("active");
    $$(".settings-panel").forEach(function(p) { p.classList.remove("active"); });
    var panel = $("#stab-" + tab.dataset.stab);
    if (panel) panel.classList.add("active");
    // Load models when Models tab opened
    if (tab.dataset.stab === "models") loadSettingsModels();
    // Load providers when Providers tab opened
    if (tab.dataset.stab === "providers" && typeof sp !== "undefined") sp.reload();
    // Load MCP when MCP tab opened
    if (tab.dataset.stab === "mcp") loadMcpServers();
    // Apply fonts when Appearance tab opened (ensure visual sync)
    if (tab.dataset.stab === "appearance") {
      var s = {};
      try { s = JSON.parse(localStorage.getItem("supercli-settings") || "{}"); } catch(e) {}
      applyFonts(s);
    }
  });
});

// Open/close
$("#settings-btn").addEventListener("click", function() {
  settingsModal.hidden = false;
  // Reset to General tab
  $$(".settings-tab").forEach(function(t) { t.classList.toggle("active", t.dataset.stab === "general"); });
  $$(".settings-panel").forEach(function(p) { p.classList.toggle("active", p.id === "stab-general"); });
  loadSettingsValues();
});
$("#settings-close").addEventListener("click", function() {
  saveSettings(); // ensure latest state is saved
  settingsModal.hidden = true;
});
settingsModal.addEventListener("click", function(e) {
  if (e.target === settingsModal) { saveSettings(); settingsModal.hidden = true; }
});

themeDark.addEventListener("click", function() { applyTheme("dark"); });
themeLight.addEventListener("click", function() { applyTheme("light"); });

// Add provider from settings
$("#settings-add-provider").addEventListener("click", function() {
  settingsModal.hidden = true;
  openProviderModal();
});

// ── File Browser & Editor ──
var editorModal = $("#editor-modal");
var editorArea = $("#editor-area");
var editorTitle = $("#editor-title");
var editorPathEl = $("#editor-path");
var editorStatus = $("#editor-status");
var currentFilePath = "";

loaders.files = async function(dir) {
  var browser = $("#file-browser");
  browser.innerHTML = '<div class="empty-state">' + escHtml(t("common.loading")) + '</div>';
  dir = dir || "";
  try {
    var r = await fetch("/api/files?dir=" + encodeURIComponent(dir));
    var data = await r.json();
    browser.innerHTML = "";
    var files = data.files || [];
    if (!files.length) { browser.innerHTML = '<div class="empty-state">' + escHtml(t("files.empty")) + '</div>'; return; }
    files.sort(function(a, b) {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
    for (var i = 0; i < files.length; i++) {
      var f = files[i];
      var row = el("div", "file-row");
      row.innerHTML =
        '<span class="file-icon">' + (f.is_dir ? '▸' : '▹') + '</span>' +
        '<span class="file-name">' + escHtml(f.name) + '</span>' +
        '<span class="file-size">' + (f.is_dir ? '' : formatSize(f.size)) + '</span>';
      (function(entry, fullDir) {
        row.addEventListener("click", function() {
          var subPath = fullDir ? fullDir + "/" + entry.name : entry.name;
          if (entry.is_dir) { loaders.files(subPath); }
          else { openFile(subPath); }
        });
      })(f, data.dir);
      browser.appendChild(row);
    }
  } catch(e) { browser.innerHTML = '<div class="empty-state">' + escHtml(t("common.error") + e.message) + '</div>'; }
};

function formatSize(bytes) {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024*1024) return (bytes/1024).toFixed(1) + " KB";
  return (bytes/(1024*1024)).toFixed(1) + " MB";
}

async function openFile(path) {
  currentFilePath = path;
  editorPathEl.textContent = path;
  editorTitle.textContent = path.split("/").pop() || path.split("\\").pop() || path;
  editorArea.value = t("common.loading");
  editorStatus.textContent = "";
  editorStatus.className = "";
  editorModal.hidden = false;
  editorArea.focus();
  try {
    var r = await fetch("/api/file/read?path=" + encodeURIComponent(path));
    var data = await r.json();
    editorArea.value = data.content || "";
  } catch(e) { editorArea.value = "Error: " + e.message; }
}

$("#editor-close").addEventListener("click", function() { editorModal.hidden = true; });
editorModal.addEventListener("click", function(e) { if (e.target === editorModal) editorModal.hidden = true; });

$("#editor-save").addEventListener("click", async function() {
  if (!currentFilePath) return;
  editorStatus.textContent = t("editor.saving"); editorStatus.className = "";
  try {
    var r = await fetch("/api/file/write", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path: currentFilePath, content: editorArea.value }),
    });
    if (r.ok) { editorStatus.textContent = t("editor.saved"); editorStatus.className = "ok"; }
    else { editorStatus.textContent = t("common.error") + (await r.text()); editorStatus.className = "err"; }
  } catch(e) { editorStatus.textContent = t("common.error") + e.message; editorStatus.className = "err"; }
});

editorArea.addEventListener("keydown", function(e) {
  if ((e.ctrlKey || e.metaKey) && e.key === "s") { e.preventDefault(); $("#editor-save").click(); }
});

$("#reload-files").addEventListener("click", function() { loaders.files(); });

// ── MCP Server Management ──
async function loadMcpServers() {
  var list = $("#mcp-server-list");
  list.innerHTML = '<div class="empty-state">' + escHtml(t("common.loading")) + '</div>';
  try {
    var r = await fetch("/api/mcp/servers");
    var data = await r.json();
    var servers = data.servers || [];
    list.innerHTML = "";
    if (!servers.length) {
      list.innerHTML = '<div class="empty-state">' + t("mcp.none") + '</div>';
      return;
    }
    for (var i = 0; i < servers.length; i++) {
      var srv = servers[i];
      var row = el("div", "mcp-server-row");
      row.innerHTML =
        '<div class="mcp-info">' +
        '<strong>' + escHtml(srv.name) + '</strong>' +
        '<span>' + escHtml(srv.command) + ' ' + escHtml((srv.args || []).join(" ")) + '</span>' +
        '</div>' +
        '<button class="btn btn-tiny btn-ghost mcp-remove" data-name="' + escAttr(srv.name) + '">' + escHtml(t("mcp.remove")) + '</button>';
      // Click server row to edit in text editor
      row.style.cursor = "pointer";
      row.addEventListener("click", (function(server) { return function(e) {
        if (e.target.closest(".mcp-remove")) return; // don't open editor on Remove click
        openMcpEditor(server);
      };})(srv));
      list.appendChild(row);
    }
    // Wire remove buttons
    $$(".mcp-remove", list).forEach(function(btn) {
      btn.addEventListener("click", async function() {
        var name = btn.dataset.name;
        await fetch("/api/mcp/remove", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ name: name }),
        });
        loadMcpServers();
      });
    });
  } catch(e) { list.innerHTML = '<div class="empty-state">' + escHtml(t("common.failedLoad")) + '</div>'; }
}

$("#mcp-add-form").addEventListener("submit", async function(e) {
  e.preventDefault();
  var fd = new FormData(e.currentTarget);
  var argsRaw = (fd.get("args") || "").trim();
  var args = argsRaw ? argsRaw.split(",").map(function(s) { return s.trim(); }).filter(function(s) { return s !== ""; }) : [];
  // Parse env: KEY=VALUE per line
  var envRaw = (fd.get("env") || "").trim();
  var env = {};
  if (envRaw) {
    envRaw.split("\n").forEach(function(line) {
      var eq = line.indexOf("=");
      if (eq > 0) env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    });
  }
  var body = {
    name: fd.get("name").trim(),
    command: fd.get("command").trim(),
    args: args,
    env: env,
  };
  var r = await fetch("/api/mcp/add", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (r.ok) {
    e.target.reset();
    loadMcpServers();
  }
});

// ── MCP JSON Editor ──
$("#mcp-edit-json").addEventListener("click", async function() {
  try {
    var r = await fetch("/api/mcp/servers");
    var data = await r.json();
    var config = { mcpServers: {} };
    (data.servers || []).forEach(function(s) {
      var entry = { command: s.command };
      if (s.args && s.args.length) entry.args = s.args;
      if (s.env && Object.keys(s.env).length) entry.env = s.env;
      config.mcpServers[s.name] = entry;
    });
    $("#mcp-json-editor").value = JSON.stringify(config, null, 2);
    $("#mcp-list-view").style.display = "none";
    $("#mcp-json-view").style.display = "flex";
    $("#mcp-json-status").textContent = "";
  } catch(e) {}
});

$("#mcp-back-list").addEventListener("click", function() {
  $("#mcp-json-view").style.display = "none";
  $("#mcp-list-view").style.display = "";
  loadMcpServers();
});

$("#mcp-json-save").addEventListener("click", async function() {
  var raw = $("#mcp-json-editor").value.trim();
  var status = $("#mcp-json-status");
  if (!raw) { status.textContent = "Empty JSON"; return; }
  var config;
  try { config = JSON.parse(raw); } catch(e) { status.textContent = "Invalid JSON: " + e.message; return; }
  var servers = config.mcpServers || {};
  var names = Object.keys(servers);
  if (!names.length) { status.textContent = "No mcpServers found"; return; }
  status.textContent = "Saving " + names.length + " servers…";
  try {
    var existing = await (await fetch("/api/mcp/servers")).json();
    for (var i = 0; i < (existing.servers || []).length; i++) {
      await fetch("/api/mcp/remove", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name: existing.servers[i].name }) });
    }
  } catch(e) {}
  var ok = 0;
  for (var j = 0; j < names.length; j++) {
    var s = servers[names[j]];
    try {
      var r = await fetch("/api/mcp/add", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name: names[j], command: s.command || "", args: s.args || [], env: s.env || {} }) });
      if (r.ok) ok++;
    } catch(e) {}
  }
  status.textContent = "Saved " + ok + " / " + names.length + " servers";
  loadMcpServers();
});

// Keybinds — capture on keypress, not free text
function loadKeybinds() {
  var kb = {};
  try { kb = JSON.parse(localStorage.getItem("supercli-keybinds") || "{}"); } catch(e) {}
  $$("#keybind-list-edit kbd").forEach(function(kbd) {
    var key = kbd.dataset.key;
    if (kb[key]) kbd.textContent = kb[key];
  });
}
function saveKeybinds() {
  var kb = {};
  $$("#keybind-list-edit kbd").forEach(function(kbd) {
    kb[kbd.dataset.key] = kbd.textContent.trim();
  });
  try { localStorage.setItem("supercli-keybinds", JSON.stringify(kb)); } catch(e) {}
  pushSettings();
}

$$("#keybind-list-edit kbd").forEach(function(kbd) {
  kbd.tabIndex = 0;
  kbd.addEventListener("click", function() {
    kbd.textContent = "Press a key…";
    kbd.dataset.capturing = "1";
  });
  kbd.addEventListener("keydown", function(e) {
    e.preventDefault();
    if (kbd.dataset.capturing !== "1") return;
    var parts = [];
    if (e.ctrlKey) parts.push("Ctrl");
    if (e.shiftKey && e.key !== "Shift") parts.push("Shift");
    if (e.altKey && e.key !== "Alt") parts.push("Alt");
    if (e.metaKey) parts.push("Meta");
    var key = e.key;
    if (key === "Control" || key === "Shift" || key === "Alt" || key === "Meta") {
      // Modifier-only — wait for full combo
      return;
    }
    if (key === " ") key = "Space";
    if (key === "Escape") key = "Esc";
    if (key === "ArrowUp") key = "↑";
    if (key === "ArrowDown") key = "↓";
    if (key === "ArrowLeft") key = "←";
    if (key === "ArrowRight") key = "→";
    if (key === "Backspace") key = "⌫";
    if (key === "Delete") key = "⌦";
    if (key === "Tab") key = "Tab";
    if (key === "Enter") key = "Enter";
    if (key === "/") key = "/";
    parts.push(key);
    kbd.textContent = parts.join("+");
    delete kbd.dataset.capturing;
    saveKeybinds();
  });
  kbd.addEventListener("blur", function() {
    if (kbd.dataset.capturing === "1") {
      delete kbd.dataset.capturing;
      loadKeybinds(); // restore previous value
    }
  });
});

// Load saved settings into UI
function loadSettingsValues() {
  var s = {};
  try { s = JSON.parse(localStorage.getItem("supercli-settings") || "{}"); } catch(e) {}
  // Language
  if ($("#setting-lang")) $("#setting-lang").value = s.lang || "en";
  // Notifications
  if ($("#setting-notify-sound")) $("#setting-notify-sound").checked = !!s.notifySound;
  if ($("#setting-notify-desktop")) $("#setting-notify-desktop").checked = !!s.notifyDesktop;
  // Permissions
  if ($("#setting-perm-write")) $("#setting-perm-write").checked = s.permWrite !== false;
  if ($("#setting-perm-network")) $("#setting-perm-network").checked = s.permNetwork !== false;
  // Fonts
  if ($("#setting-ui-font")) $("#setting-ui-font").value = s.uiFont || "inter";
  if ($("#setting-code-font")) $("#setting-code-font").value = s.codeFont || "jetbrains";
  applyFonts(s);
  // About — populate from health
  if ($("#about-data")) $("#about-data").textContent = (window.__supercliHome || "supercli-data/");
  fetch("/api/health").then(function(r) { return r.json(); }).then(function(j) {
    if ($("#about-exe")) $("#about-exe").textContent = "supercli-web.exe";
    if ($("#about-data")) $("#about-data").textContent = j.home || "—";
  }).catch(function(){});
}

// Save settings on change
function saveSettings() {
  var s = {};
  s.lang = $("#setting-lang") ? $("#setting-lang").value : "en";
  s.notifySound = $("#setting-notify-sound") ? $("#setting-notify-sound").checked : false;
  s.notifyDesktop = $("#setting-notify-desktop") ? $("#setting-notify-desktop").checked : false;
  s.permWrite = $("#setting-perm-write") ? $("#setting-perm-write").checked : true;
  s.permNetwork = $("#setting-perm-network") ? $("#setting-perm-network").checked : true;
  s.uiFont = $("#setting-ui-font") ? $("#setting-ui-font").value : "inter";
  s.codeFont = $("#setting-code-font") ? $("#setting-code-font").value : "jetbrains";
  try { localStorage.setItem("supercli-settings", JSON.stringify(s)); } catch(e) {}
  applyFonts(s);
  // Apply language if it changed (re-translates all tagged UI).
  if (s.lang !== currentLang) setLang(s.lang);
  pushSettings();
}

function applyFonts(s) {
  var uiMap = { inter: '"Inter","Segoe UI",system-ui,sans-serif', system: 'system-ui,sans-serif', segoe: '"Segoe UI",system-ui,sans-serif' };
  var codeMap = { jetbrains: '"JetBrainsMono Nerd Font Mono","Cascadia Code",Consolas,monospace', cascadia: '"Cascadia Code",Consolas,monospace', consolas: 'Consolas,monospace', fira: '"Fira Code",Consolas,monospace', monospace: 'monospace' };
  var uiVal = (s.uiFont && uiMap[s.uiFont]) ? uiMap[s.uiFont] : uiMap.inter;
  var codeVal = (s.codeFont && codeMap[s.codeFont]) ? codeMap[s.codeFont] : codeMap.jetbrains;
  // Inject a <style> tag that overrides :root variables with !important.
  // style-injection beats setProperty across all browser engines / WebView2.
  var styleId = "supercli-font-style";
  var el = document.getElementById(styleId);
  if (!el) {
    el = document.createElement("style");
    el.id = styleId;
    document.head.appendChild(el);
  }
  el.textContent = ":root { --font: " + uiVal + " !important; --mono: " + codeVal + " !important; }";
}

// Wire up all setting inputs
["#setting-lang","#setting-notify-sound","#setting-notify-desktop","#setting-perm-write","#setting-perm-network","#setting-ui-font","#setting-code-font"].forEach(function(sel) {
  var el = $(sel);
  if (!el) return;
  el.addEventListener("change", saveSettings);
  // Delayed blur fallback for selects: ensures value is committed before saving
  if (el.tagName === "SELECT") {
    el.addEventListener("blur", function() { setTimeout(saveSettings, 30); });
  }
});

// Init fonts from storage
(function() {
  var s = {};
  try { s = JSON.parse(localStorage.getItem("supercli-settings") || "{}"); } catch(e) {}
  applyFonts(s);
})();

// Load models visibility into settings — grouped by provider
async function loadSettingsModels() {
  var list = $("#settings-model-list");
  if (!list || list.dataset.loaded) return;
  list.dataset.loaded = "1";
  list.innerHTML = '<div class="empty-state">' + escHtml(t("common.loading")) + '</div>';
  try {
    var r = await fetch("/api/models");
    var data = await r.json();
    var models = data.models || [];
    // Group by provider
    var groups = {};
    for (var i = 0; i < models.length; i++) {
      var m = models[i];
      var prov = m.provider || "other";
      if (!groups[prov]) groups[prov] = [];
      groups[prov].push(m);
    }
    list.innerHTML = "";
    var providerNames = Object.keys(groups).sort();
    for (var p = 0; p < providerNames.length; p++) {
      var prov = providerNames[p];
      var header = el("div", "settings-provider-header");
      header.textContent = prov;
      list.appendChild(header);
      var provModels = groups[prov];
      for (var j = 0; j < provModels.length; j++) {
        var m = provModels[j];
        var row = el("label", "toggle-row");
        row.innerHTML = '<span>' + escHtml(m.id) + (m.reasoning ? ' <span class="model-badge reasoning">R</span>' : '') + (m.vision ? ' <span class="model-badge vision">V</span>' : '') + '</span>' +
          '<input type="checkbox" ' + (m.hidden ? '' : 'checked') + ' data-model="' + escAttr(m.id) + '" />';
        list.appendChild(row);
      }
    }
  } catch(e) { list.innerHTML = '<div class="empty-state">' + escHtml(t("common.failedLoadModels")) + '</div>'; }

  // Wire up model toggle checkboxes
  setTimeout(function() {
    $$("#settings-model-list input[type=checkbox]").forEach(function(cb) {
      cb.addEventListener("change", async function() {
        var modelId = cb.dataset.model;
        await fetch("/api/model/toggle", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ model: modelId }),
        });
        // Refresh model picker
        await loaders.models();
      });
    });
  }, 200);
}

// ── Notification sound (Web Audio, no files needed) ──
function playNotifySound() {
  try {
    var s = JSON.parse(localStorage.getItem("supercli-settings") || "{}");
    if (!s.notifySound) return;
  } catch(e) { return; }
  try {
    var ctx = new (window.AudioContext || window.webkitAudioContext)();
    var osc = ctx.createOscillator();
    var gain = ctx.createGain();
    osc.connect(gain);
    gain.connect(ctx.destination);
    osc.type = "sine";
    gain.gain.setValueAtTime(0.3, ctx.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.01, ctx.currentTime + 0.25);
    // Two-tone beep: C5 -> E5
    osc.frequency.setValueAtTime(523, ctx.currentTime);
    osc.frequency.setValueAtTime(659, ctx.currentTime + 0.12);
    osc.start(ctx.currentTime);
    osc.stop(ctx.currentTime + 0.25);
  } catch(e) { /* audio not available */ }
}
closeModelPicker();
// Apply the current language to all static markup on boot. hydrateFromServer()
// re-applies it once the server responds (in case the persisted lang differs
// from the local guess), so this gives an immediate translated first paint.
try { document.documentElement.setAttribute("lang", currentLang); } catch (e) {}
applyI18n();
// Show last selected model from localStorage immediately (before API responds)
try {
  var saved = JSON.parse(localStorage.getItem("supercli-last-model") || "null");
  if (saved && saved.id) {
    $("#model-picker-name").textContent = saved.id;
    $("#model-picker-provider").textContent = saved.provider || "loading…";
    $("#sidebar-model-name").textContent = saved.id;
    $("#sidebar-model-provider").textContent = saved.provider || "loading…";
  }
} catch (e) { /* ignore */ }
// Pull persisted UI settings from the backend and re-apply them. The
// synchronous init above applies defaults from the (possibly empty)
// localStorage; this corrects them once the server responds, so theme,
// fonts, and keybinds survive restarts despite the per-launch random port.
hydrateFromServer();
autoScanModels().then(function() { return loaders.models(); });
checkHealth();
loaders.sessions();
loaders.sidebarProjects();
loaders.railProjects();
loaders.models();
setInterval(checkHealth, 15000);
promptEl.focus();
