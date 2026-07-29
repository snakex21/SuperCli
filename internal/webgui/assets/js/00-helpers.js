// SuperCli Web GUI — front-end
// Vanilla JS, no build step, served from Go embedded assets.
// Design notes: docs/webgui.md. Talks JSON + SSE to the local server.
"use strict";

var superCliUI = window.SuperCliUI;

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
  return superCliUI.requestJSON(url, opts);
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

var activeAppDialog = null;
function showAppDialog(options) {
  options = options || {};
  if (activeAppDialog) activeAppDialog(null);
  return new Promise(function (resolve) {
    var previousFocus = document.activeElement;
    var overlay = el("div", "app-dialog-overlay");
    overlay.setAttribute("role", "presentation");
    var panel = el("form", "app-dialog" + (options.danger ? " danger" : ""));
    panel.setAttribute("role", "dialog");
    panel.setAttribute("aria-modal", "true");
    var titleID = "app-dialog-title-" + Date.now();
    panel.setAttribute("aria-labelledby", titleID);
    panel.appendChild(el("div", "app-dialog-kicker", options.danger ? t("dialog.caution") : t("dialog.action")));
    var title = el("h2", "app-dialog-title", options.title || t("dialog.confirmTitle"));
    title.id = titleID;
    panel.appendChild(title);
    if (options.message) panel.appendChild(el("p", "app-dialog-message", options.message));

    var input = null;
    var error = null;
    if (options.input) {
      input = document.createElement(options.multiline ? "textarea" : "input");
      input.className = "app-dialog-input" + (options.multiline ? " multiline" : "");
      if (options.multiline) input.rows = options.rows || 6;
      else input.type = "text";
      input.value = options.value || "";
      input.maxLength = options.maxLength || (options.multiline ? 100000 : 160);
      input.autocomplete = "off";
      input.spellcheck = true;
      input.setAttribute("aria-label", options.title || t("dialog.editTitle"));
      panel.appendChild(input);
      error = el("div", "app-dialog-error");
      error.setAttribute("aria-live", "polite");
      panel.appendChild(error);
    }

    var actions = el("div", "app-dialog-actions");
    var cancel = el("button", "btn", options.cancelLabel || t("common.cancel"));
    cancel.type = "button";
    var confirm = el("button", "btn " + (options.danger ? "danger app-dialog-danger" : "primary"),
      options.confirmLabel || (options.input ? t("common.save") : t("dialog.confirm")));
    confirm.type = "submit";
    actions.appendChild(cancel);
    actions.appendChild(confirm);
    panel.appendChild(actions);
    overlay.appendChild(panel);
    document.body.appendChild(overlay);

    var settled = false;
    function finish(value) {
      if (settled) return;
      settled = true;
      document.removeEventListener("keydown", onKeyDown, true);
      overlay.classList.add("closing");
      setTimeout(function () { overlay.remove(); }, 120);
      activeAppDialog = null;
      if (previousFocus && previousFocus.isConnected) previousFocus.focus();
      resolve(value);
    }
    function onKeyDown(event) {
      if (event.key === "Escape") {
        event.preventDefault();
        finish(null);
      }
    }
    activeAppDialog = finish;
    document.addEventListener("keydown", onKeyDown, true);
    cancel.addEventListener("click", function () { finish(null); });
    overlay.addEventListener("mousedown", function (event) { if (event.target === overlay) finish(null); });
    panel.addEventListener("submit", function (event) {
      event.preventDefault();
      if (!input) { finish(true); return; }
      var value = input.value.trim();
      if (!value) {
        error.textContent = t("dialog.required");
        input.focus();
        return;
      }
      finish(value);
    });
    requestAnimationFrame(function () {
      if (input) { input.focus(); input.select(); }
      else confirm.focus();
    });
  });
}
function appConfirm(message, options) {
  options = Object.assign({}, options || {}, { message: message });
  return showAppDialog(options);
}
function appPrompt(title, value, options) {
  options = Object.assign({}, options || {}, { title: title, value: value, input: true });
  return showAppDialog(options);
}
