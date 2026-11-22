"use strict";

// English and language metadata are served from the embedded JSON catalog by
// /locales/en.js. Only the selected additional catalog is loaded on demand.
var languageLoads = Object.create(null);
// Store dynamic refreshers on their DOM owner, so removed panels and their
// captured report data become collectible without waiting for a language change.
function registerLanguageRefresh(node, refresh) {
  node._languageRefresh = refresh;
  node.setAttribute("data-i18n-refresh", "");
}
function normalizeLanguage(value) {
  var locale = String(value || "").trim().replace(/_/g, "-").split(/[:.@]/)[0].toLowerCase();
  if (locale === "no" || locale.indexOf("no-") === 0) locale = "nb";
  var language = locale.split("-")[0];
  var exact = UI_LANGUAGES.find(function (item) { return item.code.toLowerCase() === locale; });
  if (exact) return exact.code;
  var match = UI_LANGUAGES.find(function (item) { return item.code.toLowerCase().split("-")[0] === language; });
  return match ? match.code : "";
}
function detectedLanguage() {
  var candidates = (navigator.languages || []).concat([navigator.language || ""]);
  for (var i = 0; i < candidates.length; i++) {
    var language = normalizeLanguage(candidates[i]);
    if (language) return language;
  }
  return "en";
}
async function loadLanguage(value) {
  var code = normalizeLanguage(value) || "en";
  if (I18N[code]) return code;
  if (!languageLoads[code]) {
    languageLoads[code] = fetch("/locales/" + encodeURIComponent(code) + ".json", { cache: "no-store" })
      .then(function (response) {
        if (!response.ok) throw new Error("Locale HTTP " + response.status);
        return response.json();
      }).then(function (catalog) {
        if (!catalog || Array.isArray(catalog) || typeof catalog !== "object") throw new Error("Invalid locale catalog");
        var safe = Object.create(null);
        Object.keys(I18N.en).forEach(function (key) {
          if (typeof catalog[key] === "string" && catalog[key]) safe[key] = catalog[key];
        });
        I18N[code] = safe;
        return code;
      }).catch(function (error) { delete languageLoads[code]; throw error; });
  }
  return languageLoads[code];
}
function i18nEl(tag, className, key) {
  var node = el(tag, className, t(key));
  node.dataset.i18nText = key;
  node.i18nTextNode = node.firstChild;
  return node;
}
function t(key) {
  var lang = normalizeLanguage(ui.lang) || "en";
  return (I18N[lang] && I18N[lang][key]) || I18N.en[key] || key;
}
// Plural messages use one catalog key with CLDR forms, e.g.
// "one {# tool} few {# tools} other {# tools}" ("#" is the number). The form
// is picked by Intl.PluralRules for the UI language; "other" is the fallback.
// Go uses the same format (uilang.Plural), so TUI and GUI agree.
var pluralRulesCache = Object.create(null);
function pluralCategory(lang, n) {
  try {
    if (!pluralRulesCache[lang]) pluralRulesCache[lang] = new Intl.PluralRules(lang);
    return pluralRulesCache[lang].select(n);
  } catch (e) {
    return n === 1 ? "one" : "other";
  }
}
function pluralForms(text) {
  var forms = Object.create(null), re = /(zero|one|two|few|many|other)\s*\{([^{}]*)\}/g, m;
  while ((m = re.exec(text))) forms[m[1]] = m[2];
  return forms;
}
function tn(key, n) {
  var lang = normalizeLanguage(ui.lang) || "en";
  var forms = pluralForms(t(key));
  var form = forms[pluralCategory(lang, Math.abs(Number(n) || 0))];
  if (form === undefined) form = forms.other;
  if (form === undefined) return n + " " + t(key);
  return form.replace(/#/g, String(n));
}

// The agent loop still emits a few fixed English notices and stop messages.
// Known ones are shown in the UI language by key (named groups fill {args});
// anything unknown stays as received. The original text is kept for title=.
var SYSTEM_TEXTS = [
  { key: "notice.preflight", re: /^preflight: repo context ~(\d+) tok \(project turn\)$/, args: ["n"] },
  { key: "notice.loopWarning", re: /^repeated tool evidence: loop warning injected$/, args: [] },
  // Keep in sync with agent.EmptyReplyNotice.
  { key: "notice.emptyReply", re: /^empty reply: the model returned no answer; ask again or choose another model$/, args: [] },
  // Retry progress is "3/5" or "12/∞" (max_retries = -1); keep in sync with llm.rateLimitNotice and the TUI localizeNotice.
  { key: "notice.rateLimitRetry", re: /^(.+): rate limited \(HTTP 429\) — retrying in (\S+) \(retry (\d+)\/(\d+|∞)\)$/, args: ["model", "wait", "attempt", "max"] },
  { key: "notice.providerRetry", re: /^(.+): (?:temporary )?provider error \(HTTP (\d+)\) — retrying in (\S+) \(retry (\d+)\/(\d+|∞)\)$/, args: ["model", "status", "wait", "attempt", "max"] },
  { key: "notice.connectionRetry", re: /^connection to the model dropped \(([\s\S]*)\) — retrying in (\S+) \(retry (\d+)\/(\d+|∞)\)$/, args: ["reason", "wait", "attempt", "max"] },
  { key: "notice.connectionWait", re: /^Model connection unavailable — waiting (\S+) before retry (\d+)\/(\d+|∞)\.$/, args: ["wait", "attempt", "max"] },
  { key: "notice.requestLimit", re: /^(.+): request limit (\d+)\/min reached — waiting (\S+)$/, args: ["host", "n", "wait"] },
  { key: "notice.toolsUnsupported", re: /^model "(.+)" does not support tool calling on this endpoint; continuing without tools \(answers only\) — pick a function-calling model for agent work$/, args: ["model"] },
  { key: "error.loopUnchanged", re: /^agent: stopped — the last (\d+) consecutive tool rounds returned only previously observed successful results/, args: ["n"] },
  { key: "error.loopSource", re: /^agent: stopped — the last (\d+) source-reading rounds only revisited unchanged lines/, args: ["n"] },
  { key: "error.loopFailure", re: /^agent: stopped — the same tool call failed (\d+) consecutive times/, args: ["n"] },
];
function localizeSystemText(text) {
  text = String(text || "");
  for (var i = 0; i < SYSTEM_TEXTS.length; i++) {
    var entry = SYSTEM_TEXTS[i], m = entry.re.exec(text);
    if (!m) continue;
    var message = t(entry.key);
    if (message === entry.key) return text;
    var args = {};
    entry.args.forEach(function (name, index) { args[name] = m[index + 1]; });
    return message.replace(/\{([a-z_]+)\}/g, function (all, name) {
      return Object.prototype.hasOwnProperty.call(args, name) ? args[name] : all;
    });
  }
  return text;
}

function selectedModelID(value) {
  var model = String(value || "").trim();
  return model.toLowerCase() === "no model" ? "" : model;
}
function modelDisplayName(value) { return selectedModelID(value) || t("model.none"); }
function refreshModelFallback() {
  if (typeof $ !== "function") return;
  if (typeof activeModelID !== "undefined" && selectedModelID(activeModelID)) return;
  var name = $("#model-name");
  if (name) name.textContent = t("model.none");
}

function uiWarningText(warnings) {
  return (warnings || []).map(function (warning) {
    return t(warning.code).replace("{n}", warning.name || "").replace("{c}", warning.count || "");
  }).join(" ");
}
function settingCopy(k) {
  var labelKey = "setting." + k.key + ".label", descKey = "setting." + k.key + ".desc";
  return [I18N.en[labelKey] ? t(labelKey) : (k.label || k.key), I18N.en[descKey] ? t(descKey) : (k.desc || "")];
}
function instructionsCopy() {
  var copy = {};
  Object.keys(I18N.en).filter(function (key) { return key.indexOf("instructions.") === 0; }).forEach(function (key) {
    copy[key.slice("instructions.".length)] = t(key);
  });
  return copy;
}
// Change text nodes in place so active inputs, icons and streamed content survive.
function applyI18n() {
  var language = normalizeLanguage(ui.lang) || "en";
  document.documentElement.lang = I18N[language] ? language : "en";
  document.documentElement.dir = "ltr";
  refreshModelFallback();
  $$("[data-i18n-text]").forEach(function (n) {
    if (n.i18nTextNode && n.i18nTextNode.parentNode === n) n.i18nTextNode.nodeValue = t(n.dataset.i18nText);
  });
  $$("[data-i18n]").forEach(function (n) { n.textContent = t(n.dataset.i18n); });
  $$("[data-i18n-ph]").forEach(function (n) { n.placeholder = t(n.dataset.i18nPh); });
  $$("[data-i18n-title]").forEach(function (n) { n.title = t(n.dataset.i18nTitle); });
  $$("[data-i18n-aria]").forEach(function (n) { n.setAttribute("aria-label", t(n.dataset.i18nAria)); });
  $$("[data-i18n-refresh]").forEach(function (node) {
    if (typeof node._languageRefresh === "function") node._languageRefresh();
  });
  // Tab labels change width with the language; refresh the scroll fades.
  if (typeof updateSideTabFades === "function") updateSideTabFades();
}
