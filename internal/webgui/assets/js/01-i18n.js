"use strict";

// English and language metadata are served from the embedded JSON catalog by
// /locales/en.js. Only the selected additional catalog is loaded on demand.
var languageLoads = Object.create(null);
var languageRefreshers = [];
function registerLanguageRefresh(node, refresh) { languageRefreshers.push({node:node, refresh:refresh}); }
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
  languageRefreshers = languageRefreshers.filter(function (item) { return item.node.isConnected !== false; });
  languageRefreshers.forEach(function (item) { item.refresh(); });
}
