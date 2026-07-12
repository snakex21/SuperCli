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
    "side.stats": "Stats", "side.sessions": "Sessions", "side.projects": "Projects",
    "side.add": "add", "side.noSessions": "No sessions yet.", "side.noProjects": "No projects registered.",
    "session.rename": "Rename", "session.delete": "Delete", "session.namePrompt": "Conversation name",
    "session.deleteConfirm": "Delete this conversation permanently?", "session.renamed": "Conversation renamed.",
    "session.deleted": "Conversation deleted.", "session.stopRun": "Stop the current run before deleting this conversation.",
    "session.runtime": "Remember chat model", "session.runtimeHint": "Restore this session's provider, model and reasoning level when it is opened.",
    "session.runtimeFailed": "Could not restore the session model; keeping the current selection.",
    "project.stopRun": "Stop the current run before switching projects.",
    "common.refresh": "refresh", "common.scan": "scan", "common.back": "Back", "common.save": "Save",
    "common.cancel": "Cancel", "common.edit": "edit", "common.remove": "remove", "common.add": "Add",
    "common.loading": "Loading…", "common.error": "error",
    "model.none": "no model", "model.search": "Search models…", "model.reasoning": "Reasoning effort",
    "model.think": "think", "model.auto": "auto", "model.default": "default",
    "model.hide": "hide", "model.show": "show", "model.setDefault": "CLI default",
    "welcome.title": "What are we building?",
    "welcome.sub": "The agent reads, edits and runs code in the active workspace.",
    "welcome.h1": "Summarize this project", "welcome.h2": "Check configuration status", "welcome.h3": "How do I run the tests?",
    "composer.ph": "Message SuperCli…", "composer.send": "Send", "composer.stop": "Stop",
    "composer.ready": "Ready", "composer.working": "Working…", "composer.waiting": "Waiting for provider…", "composer.stopped": "Stopped.",
    "run.done": "Done", "run.tools": "tools", "run.think": "think", "run.cached": "cached", "tool.running": "running",
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
    "panel.models": "Models", "panel.providers": "Providers", "panel.accounts": "Accounts",
    "panel.mcp": "MCP", "panel.memory": "Memory", "panel.goal": "Goal", "panel.usage": "Usage",
    "panel.files": "Files", "panel.about": "About",
    "set.nextSession": "next session", "set.resetAll": "Reset all to defaults",
    "set.resetAllHint": "Removes every managed key above; providers and API keys stay untouched.",
    "set.hint": "value · source — same knobs as the TUI /settings panel, stored in config.toml.",
    "app.theme": "Theme", "app.dark": "Dark", "app.light": "Light", "app.lang": "Language",
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
    "acct.title": "Codex accounts", "acct.login": "Log in", "acct.logout": "Log out",
    "acct.refreshTok": "Refresh token", "acct.loggingIn": "logging in…", "acct.loggedOut": "logged out",
    "mcp.servers": "MCP servers", "mcp.none": "No MCP servers configured.", "mcp.addServer": "Add MCP server",
    "mcp.command": "Command", "mcp.args": "Args (comma-separated)", "mcp.env": "Env (KEY=VALUE, one per line)",
    "mcp.editJson": "edit as JSON", "mcp.saveJson": "Save JSON", "mcp.backToList": "back to list",
    "mem.empty": "No memory entries.", "goal.none": "No active goal.",
    "usage.model": "Model", "usage.session": "Session tokens", "usage.daily": "Tokens today",
    "usage.context": "Context report", "usage.summary": "Session summary", "usage.details": "Session details",
    "usage.noSession": "Start or select a session to collect persistent usage statistics.",
    "usage.contextEmpty": "No context snapshot has been recorded yet.",
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
    "side.stats": "Statystyki", "side.sessions": "Sesje", "side.projects": "Projekty",
    "side.add": "dodaj", "side.noSessions": "Brak sesji.", "side.noProjects": "Brak projektów.",
    "session.rename": "Zmień nazwę", "session.delete": "Usuń", "session.namePrompt": "Nazwa rozmowy",
    "session.deleteConfirm": "Usunąć tę rozmowę na stałe?", "session.renamed": "Zmieniono nazwę rozmowy.",
    "session.deleted": "Usunięto rozmowę.", "session.stopRun": "Zatrzymaj trwającą odpowiedź przed usunięciem tej rozmowy.",
    "session.runtime": "Pamiętaj model czatu", "session.runtimeHint": "Po otwarciu sesji przywróć jej provider, model i poziom myślenia.",
    "session.runtimeFailed": "Nie udało się przywrócić modelu sesji; pozostawiono bieżący wybór.",
    "project.stopRun": "Zatrzymaj bieżące zadanie przed zmianą projektu.",
    "common.refresh": "odśwież", "common.scan": "skanuj", "common.back": "Wróć", "common.save": "Zapisz",
    "common.cancel": "Anuluj", "common.edit": "edytuj", "common.remove": "usuń", "common.add": "Dodaj",
    "common.loading": "Wczytywanie…", "common.error": "błąd",
    "model.none": "brak modelu", "model.search": "Szukaj modeli…", "model.reasoning": "Wysiłek rozumowania",
    "model.think": "think", "model.auto": "auto", "model.default": "domyślny",
    "model.hide": "ukryj", "model.show": "pokaż", "model.setDefault": "domyślny CLI",
    "welcome.title": "Co dziś budujemy?",
    "welcome.sub": "Agent czyta, edytuje i uruchamia kod w aktywnym projekcie.",
    "welcome.h1": "Podsumuj ten projekt", "welcome.h2": "Sprawdź stan konfiguracji", "welcome.h3": "Jak uruchomić testy?",
    "composer.ph": "Napisz do SuperCli…", "composer.send": "Wyślij", "composer.stop": "Stop",
    "composer.ready": "Gotowy", "composer.working": "Pracuję…", "composer.waiting": "Czekam na odpowiedź providera…", "composer.stopped": "Zatrzymano.",
    "run.done": "Gotowe", "run.tools": "narzędzia", "run.think": "myślenie", "run.cached": "z cache", "tool.running": "pracuje",
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
    "panel.models": "Modele", "panel.providers": "Dostawcy", "panel.accounts": "Konta",
    "panel.mcp": "MCP", "panel.memory": "Pamięć", "panel.goal": "Cel", "panel.usage": "Zużycie",
    "panel.files": "Pliki", "panel.about": "O programie",
    "set.nextSession": "następna sesja", "set.resetAll": "Przywróć wszystkie domyślne",
    "set.resetAllHint": "Usuwa wszystkie zarządzane klucze powyżej; dostawcy i klucze API zostają.",
    "set.hint": "wartość · źródło — te same pokrętła co panel /settings w TUI, zapisywane w config.toml.",
    "app.theme": "Motyw", "app.dark": "Ciemny", "app.light": "Jasny", "app.lang": "Język",
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
    "acct.title": "Konta Codex", "acct.login": "Zaloguj", "acct.logout": "Wyloguj",
    "acct.refreshTok": "Odśwież token", "acct.loggingIn": "logowanie…", "acct.loggedOut": "wylogowany",
    "mcp.servers": "Serwery MCP", "mcp.none": "Brak serwerów MCP.", "mcp.addServer": "Dodaj serwer MCP",
    "mcp.command": "Polecenie", "mcp.args": "Argumenty (po przecinku)", "mcp.env": "Env (KLUCZ=WARTOŚĆ, po jednym w linii)",
    "mcp.editJson": "edytuj jako JSON", "mcp.saveJson": "Zapisz JSON", "mcp.backToList": "wróć do listy",
    "mem.empty": "Brak wpisów pamięci.", "goal.none": "Brak aktywnego celu.",
    "usage.model": "Model", "usage.session": "Tokeny sesji", "usage.daily": "Tokeny dziś",
    "usage.context": "Raport kontekstu", "usage.summary": "Podsumowanie sesji", "usage.details": "Szczegóły sesji",
    "usage.noSession": "Rozpocznij lub wybierz sesję, aby zbierać trwałe statystyki użycia.",
    "usage.contextEmpty": "Nie zapisano jeszcze migawki kontekstu.",
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
function t(key) {
  var lang = ui.lang || "en";
  return (I18N[lang] && I18N[lang][key]) || I18N.en[key] || key;
}
function applyI18n() {
  $$("[data-i18n]").forEach(function (n) { n.textContent = t(n.dataset.i18n); });
  $$("[data-i18n-ph]").forEach(function (n) { n.placeholder = t(n.dataset.i18nPh); });
}

/* ═══ UI settings (server-persisted blob) ═══ */

var ui = {
  theme: "dark", lang: "en", uiFont: "system", codeFont: "system",
  notifySound: false, notifyDesktop: false, sidebarHidden: true, rememberSessionRuntime: true,
  keybinds: { panel: "Ctrl+,", sidebar: "Ctrl+B", focus: "/", thinking: "Shift+T", tools: "Shift+E" },
};
var uiBlob = {}; // last blob seen from the server (read-only mirror)

// Real, commonly-installed font choices. Every option must visibly
// change rendering on a stock Windows box — a knob that does nothing
// breaks the "it just works" contract.
var UI_FONTS = {
  system: '-apple-system, "Segoe UI", system-ui, sans-serif',
  segoe: '"Segoe UI", system-ui, sans-serif',
  arial: 'Arial, Helvetica, sans-serif',
  tahoma: 'Tahoma, Geneva, sans-serif',
  georgia: 'Georgia, "Times New Roman", serif',
};
var CODE_FONTS = {
  system: 'ui-monospace, "Cascadia Mono", Consolas, monospace',
  cascadia: '"Cascadia Mono", "Cascadia Code", Consolas, monospace',
  consolas: 'Consolas, "Lucida Console", monospace',
  courier: '"Courier New", Courier, monospace',
  lucida: '"Lucida Console", Consolas, monospace',
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
  document.documentElement.dataset.theme = ui.theme === "light" ? "light" : "dark";
  document.documentElement.style.setProperty("--sans", UI_FONTS[ui.uiFont] || UI_FONTS.system);
  document.documentElement.style.setProperty("--mono", CODE_FONTS[ui.codeFont] || CODE_FONTS.system);
  $("#shell").classList.toggle("sidebar-hidden", !!ui.sidebarHidden);
  applyI18n();
}
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
  var src = String(text || ""), html = "", last = 0, m;
  var re = /<(?:thinking|think)>[\s\S]*?<\/(?:thinking|think)>/gi;
  while ((m = re.exec(src)) !== null) {
    if (m.index > last) html += renderMarkdownish(src.slice(last, m.index));
    html += renderThinkBlock(m[0].replace(/^<(?:thinking|think)>/i, "").replace(/<\/(?:thinking|think)>$/i, ""));
    last = re.lastIndex;
  }
  if (last < src.length) {
    var tail = src.slice(last);
    var open = tail.search(/<(?:thinking|think)>/i);
    if (open >= 0) {
      html += renderMarkdownish(tail.slice(0, open));
      html += renderThinkBlock(tail.slice(open).replace(/^<(?:thinking|think)>/i, ""));
    } else {
      html += renderMarkdownish(tail);
    }
  }
  return html;
}

/* ═══ transcript ═══ */

var stage = $("#stage"), stream = $("#stream"), welcome = $("#welcome");

function nearBottom() { return stage.scrollHeight - stage.scrollTop - stage.clientHeight < 120; }
function smartScroll(force) {
  if (force || nearBottom()) stage.scrollTop = stage.scrollHeight;
}
function hideWelcome() { if (welcome) welcome.style.display = "none"; }
function showWelcome() { if (welcome) welcome.style.display = ""; }

function addUserMsg(text) {
  hideWelcome();
  var m = el("div", "msg-user", text);
  stream.appendChild(m);
  smartScroll(true);
  return m;
}
function addAssistantMsg() {
  var m = el("div", "msg-assistant");
  m._raw = "";
  m._renderTimer = null;
  stream.appendChild(m);
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
  stream.appendChild(line);
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
    var keys = ["path", "file", "dir", "query", "pattern", "cmd", "command", "prompt", "text"];
    for (var i = 0; i < keys.length; i++) {
      if (a[keys[i]]) return { name: name, hint: clip(String(a[keys[i]]), 90) };
    }
    var flat = Object.keys(a).map(function (k) { return k + "=" + clip(JSON.stringify(a[k]), 30); }).join(" ");
    return { name: name, hint: clip(flat, 90) };
  } catch (e) {
    return { name: name, hint: clip(args || "", 90) };
  }
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
  stream.appendChild(row);
  return row;
}
function addToolCall(name, args, id) {
  var info = toolHint(name, args);
  var row = document.createElement("details");
  row.className = "tool-row";
  var sum = el("summary");
  var title = el("span", "tname", info.name);
  var hint = el("span", "thint", info.hint);
  sum.appendChild(title);
  sum.appendChild(hint);
  var stat = el("span", "tstat");
  sum.appendChild(stat);
  row.appendChild(sum);
  var body = el("div", "tbody");
  body.hidden = true;
  var lblA = el("div", "lbl", "args");
  body.appendChild(lblA);
  body.appendChild(el("pre", "", prettyJSON(args)));
  row.appendChild(body);
  row.addEventListener("toggle", function () { body.hidden = !row.open; });
  row._stat = stat; row._body = body; row._tname = title; row._thint = hint;
  row._toolName = name; row._taskAgent = info.agent || ""; row._taskPrompt = info.prompt || "";
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
  stream.appendChild(row);
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
  var ms = performance.now() - row._t0;
  var task = row._toolName === "task" ? parseTaskNotification(output || err) : null;
  if (task) {
    if (task.id) workerRows[task.id] = row;
    renderTaskResult(row, task, ms, row._taskPrompt, err);
    smartScroll();
    return;
  }
  row._stat.textContent = (err ? "✕ " : "") + fmtDuration(ms);
  if (err) row._stat.classList.add("err");
  var lbl = el("div", "lbl", err ? "error" : "output");
  row._body.appendChild(lbl);
  row._body.appendChild(el("pre", "", err || output || "(empty)"));
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
function addTurnMeta(ev, elapsed) {
  var parts = [fmtDuration(elapsed)];
  var evalTok = (ev.tok_in || 0) - (ev.tok_cached || 0);
  if (ev.tok_cached) {
    parts.push("cache " + fmtTok(ev.tok_cached) + " · eval " + fmtTok(evalTok) + " · gen " + fmtTok(ev.tok_out));
  } else if (ev.tok_total) {
    parts.push("in " + fmtTok(ev.tok_in) + " · gen " + fmtTok(ev.tok_out));
  }
  if (ev.cache_hit_pct) parts.push(ev.cache_hit_pct + "% " + t("run.cached"));
  if (ev.reasoning_tok) parts.push(t("run.think") + " " + fmtTok(ev.reasoning_tok));
  if (runToolCount) parts.push(runToolCount + " " + t("run.tools"));
  var line = el("div", "turn-meta");
  line.innerHTML = parts.map(function (p, i) { return i === 0 ? "<b>" + escHtml(p) + "</b>" : escHtml(p); }).join(" · ");
  stream.appendChild(line);
  smartScroll();
}

/* ═══ chat streaming ═══ */

var streaming = false, abortCtl = null, activeSessionID = "", projectEpoch = 0;
var runStart = 0, runTimer = null, runToolCount = 0;
var lastTurn = null;    // last done-event payload + elapsed (stats pane)
var workersSeen = [];   // worker notifications this browser session
var promptEl = $("#prompt"), sendBtn = $("#send-btn"), runStatus = $("#run-status");

function setRunState(state, text) {
  runStatus.textContent = text || "";
  $("#status-dot").classList.toggle("busy", state === "running");
}

async function sendPrompt(text) {
  if (streaming) return;
  streaming = true;
  abortCtl = new AbortController();
  runToolCount = 0;
  toolRows = {}; workerRows = {}; openToolOrder = [];
  sendBtn.textContent = t("composer.stop");
  sendBtn.classList.add("stop");
  sendBtn.type = "button";
  addUserMsg(text);
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
      body: JSON.stringify({ prompt: text, session_id: activeSessionID }),
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
    streaming = false;
    abortCtl = null;
    clearInterval(runTimer);
    settleOpenTools();
    sendBtn.textContent = t("composer.send");
    sendBtn.classList.remove("stop");
    sendBtn.type = "submit";
    if (runStatus.textContent.indexOf(t("composer.working")) === 0) setRunState("idle", t("composer.ready"));
    $("#status-dot").classList.remove("busy");
    promptEl.focus();
    loadSessions();
    renderStats();
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

$("#composer").addEventListener("submit", function (e) {
  e.preventDefault();
  if (streaming) return;
  var text = promptEl.value.trim();
  if (!text) return;
  promptEl.value = "";
  promptEl.style.height = "auto";
  sendPrompt(text);
});
sendBtn.addEventListener("click", function (e) {
  if (streaming) { e.preventDefault(); if (abortCtl) abortCtl.abort(); }
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
  if (ui.notifySound) {
    try {
      var ctx = new (window.AudioContext || window.webkitAudioContext)();
      var osc = ctx.createOscillator(), gain = ctx.createGain();
      osc.connect(gain); gain.connect(ctx.destination);
      osc.frequency.value = 660; gain.gain.value = 0.04;
      osc.start(); osc.stop(ctx.currentTime + 0.12);
    } catch (e) {}
  }
  if (ui.notifyDesktop && document.hidden && window.Notification && Notification.permission === "granted") {
    try { new Notification("SuperCli", { body: t("run.done") + " · " + fmtDuration(elapsed) }); } catch (e) {}
  }
}

/* ═══ health / status ═══ */

var activeModelID = "";
async function checkHealth() {
  var dot = $("#status-dot");
  try {
    var h = await j("/api/health");
    dot.className = "status-dot ok" + (streaming ? " busy" : "");
    dot.title = t("status.connected") + " · " + (h.model || "");
    $("#workspace").textContent = h.home || "";
    $("#workspace").title = h.home || "";
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
$("#toggle-sidebar").addEventListener("click", toggleSidebar);

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

async function loadSessions() {
  var list = $("#session-list");
  try {
    var rows = await j("/api/sessions?limit=40");
    list.innerHTML = "";
    if (!rows || !rows.length) {
      list.appendChild(el("div", "side-empty", t("side.noSessions")));
      return;
    }
    rows.forEach(function (s) {
      var b = el("button", "side-item" + (s.id === activeSessionID ? " active" : ""));
      b.type = "button";
      b.appendChild(el("span", "t", s.first_user_msg || s.id));
      var sessionMeta = fmtWhen(s.started_at) + " · " + s.message_count;
      if (s.model) sessionMeta += " · " + s.model;
      b.appendChild(el("span", "s", sessionMeta));
      b.addEventListener("click", function () { resumeSession(s.id, s); });

      var actions = el("span", "session-actions");
      var rename = el("span", "session-action rename", "✎");
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

      var remove = el("span", "session-action delete", "×");
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
    await loadModels();
    checkHealth();
  } catch (e) {
    toast(t("session.runtimeFailed"));
  }
}

async function resumeSession(id, session) {
  if (streaming) return;
  var epoch = projectEpoch;
  try {
    await restoreSessionRuntime(session);
    var msgs = await j("/api/transcript?id=" + encodeURIComponent(id));
    if (epoch !== projectEpoch) return;
    activeSessionID = id;
    stream.innerHTML = "";
    toolRows = {}; workerRows = {}; openToolOrder = [];
    lastTurn = null; workersSeen = [];
    hideWelcome();
    (msgs || []).forEach(function (m) {
      if (m.role === "user") {
        addUserMsg(m.content);
      } else if (m.role === "assistant") {
        if (!m.content) return;
        var node = addAssistantMsg();
        node._raw = m.content;
        renderAssistant(node);
        // History replay: thinking folded (only live streams open it).
        node.querySelectorAll("details[data-think-id]").forEach(function (d) { d.open = false; });
      } else if (m.role === "tool") {
        var task = (m.name === "task" || String(m.content || "").indexOf("<task-notification>") >= 0) ?
          parseTaskNotification(m.content) : null;
        if (task) {
          addHistoryTask(task);
          return;
        }
        var row = document.createElement("details");
        row.className = "tool-row";
        var sum = el("summary");
        sum.appendChild(el("span", "tname", m.name || "tool"));
        sum.appendChild(el("span", "thint", clip(m.content || "", 90)));
        sum.appendChild(el("span", "tstat", ""));
        row.appendChild(sum);
        var body = el("div", "tbody");
        body.appendChild(el("pre", "", m.content || ""));
        row.appendChild(body);
        stream.appendChild(row);
      }
    });
    smartScroll(true);
    loadSessions();
    renderStats();
    promptEl.focus();
  } catch (e) {
    toast(t("common.error") + ": " + e.message);
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
    if (action === "use" || action === "add") newSession();
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

var scanKicked = false;
async function loadModels() {
  try {
    var got = await j("/api/models");
    if (got.active) {
      activeModelID = got.active;
      $("#model-name").textContent = got.active;
    }
    $("#model-prov").textContent = got.provider || "";
    if (got.models && got.models.length) {
      // Fresh server list: adopt it and persist as the instant-start
      // cache for the next launch (merged server-side, wipe-safe).
      modelCache = slimModels(got.models);
      saveBlobKey("supercli-model-cache", modelCache);
    } else if (modelCache.length && !scanKicked) {
      // Server knows nothing yet (fresh process): keep showing the
      // cached list and kick one silent background scan.
      scanKicked = true;
      jpost("/api/provider/scan", {}).then(loadModels).catch(function () {});
    }
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
      jpost("/api/model/toggle", { model: m.id }).then(function () {
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
  (sections[name] || function () {})();
}

/* ── Settings (config.toml knobs — TUI /settings parity) ── */

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
  var name = el("div", "k-name", k.key);
  var d = el("small", "", k.desc);
  name.appendChild(d);
  row.appendChild(name);
  if (k.next_session) row.appendChild(el("span", "k-next", "(" + t("set.nextSession") + ")"));

  function post(value) {
    jpost("/api/config", { key: k.key, value: value })
      .then(function () { sections.settings(); })
      .catch(function (e) { toast(e.message); });
  }

  if (k.kind === "tri" || k.kind === "tri_auto" || k.kind === "nav") {
    var states = k.kind === "tri" ? ["default", "on", "off"] : ["auto", "on", "off"];
    var seg = el("span", "seg");
    states.forEach(function (st) {
      var b = el("button", "", st);
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
  var src = el("span", "k-src" + (k.source !== "default" ? " manual" : ""), k.source);
  row.appendChild(src);
  return row;
}

/* ── Appearance ── */

sections.appearance = function () {
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", t("app.theme")));
  var seg = el("span", "seg");
  [["dark", t("app.dark")], ["light", t("app.light")]].forEach(function (pair) {
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
    [["system", "System"], ["segoe", "Segoe UI"], ["arial", "Arial"], ["tahoma", "Tahoma"], ["georgia", "Georgia"]]));
  panelContent.appendChild(selectRow(t("app.codeFont"), "codeFont",
    [["system", "System monospace"], ["cascadia", "Cascadia Mono"], ["consolas", "Consolas"], ["courier", "Courier New"], ["lucida", "Lucida Console"]]));

  var gn = el("div", "group");
  gn.appendChild(el("div", "g-label", t("app.notify")));
  [["notifySound", t("app.sound")], ["notifyDesktop", t("app.desktop")]].forEach(function (pair) {
    var tr = el("label", "toggle-row");
    tr.appendChild(el("span", "", pair[1]));
    var cb = document.createElement("input");
    cb.type = "checkbox";
    cb.checked = !!ui[pair[0]];
    cb.addEventListener("change", function () {
      ui[pair[0]] = cb.checked;
      saveUI();
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
  var models = (got.models && got.models.length) ? slimModels(got.models) : modelCache;
  models.forEach(function (m) {
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
      jpost("/api/model/toggle", { model: m.id }).then(sections.models);
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
    g.appendChild(row);
  });
  panelContent.appendChild(g);
};

/* ── Providers ── */

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
    var row = el("div", "list-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(p.Name) + "</strong> <span class='note'>" + escHtml(p.Type || "") + "</span>";
    main.appendChild(title);
    var sub = (p.BaseURL || "") + " · " + (p.HasKey ? t("prov.key") : t("prov.noKey")) +
      ((p.Models || []).length ? " · " + p.Models.length + " " + t("prov.models") : "");
    main.appendChild(el("div", "lr-sub", sub));
    row.appendChild(main);
    var act = el("div", "lr-act");
    var bs = el("button", "", t("common.scan"));
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

sections.goal = async function () {
  var got;
  try { got = await j("/api/goal"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", t("panel.goal")));
  if (!got) {
    g.appendChild(el("div", "note", t("goal.none")));
  } else {
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(got.title) + "</strong> <span class='note'>" + escHtml(got.status) + "</span>";
    g.appendChild(title);
    (got.tasks || []).forEach(function (task) {
      var row = el("div", "goal-task");
      row.appendChild(el("span", "gs" + (task.status === "done" ? " done" : ""), task.status));
      row.appendChild(el("span", "", task.title));
      g.appendChild(row);
    });
  }
  panelContent.appendChild(g);
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

  var ariaParts = [t("stats.currentContext") + " " + context.percent + "%"];
  var meter = el("div", "context-split");
  meter.setAttribute("role", "img");
  items.forEach(function (item) {
    var pct = item.value * 100 / total;
    ariaParts.push(item.label + " " + Math.round(pct) + "%");
    if (item.value <= 0) return;
    var segment = el("span", "context-segment context-" + item.key);
    segment.style.width = pct + "%";
    segment.title = item.label + ": " + fmtInteger(item.value) + " (" + Math.round(pct) + "%)";
    meter.appendChild(segment);
  });
  meter.setAttribute("aria-label", ariaParts.join(". "));
  section.appendChild(meter);

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
  section.appendChild(el("div", "usage-caption", t("stats.contextShare")));
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
  if (h && h.model) {
    loadReasoning();
    loadModels();
  }
  loadSessions();
  loadProjects();
  renderStats();
  setInterval(checkHealth, 30000);
  promptEl.focus();
})();
