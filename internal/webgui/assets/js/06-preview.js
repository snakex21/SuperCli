"use strict";
var sitePreviewPane = null, sitePreviewController = null, lastPreviewURL = "";
var sitePreviewLayout = null, sitePreviewExpanded = false, sitePreviewWidth = 0;
function createPreviewLayout(pane) {
  var shell = $("#shell"), sidebar = $("#sidebar"), disposed = false;
  var handle = el("div", "preview-resize-handle");
  handle.tabIndex = 0; handle.setAttribute("role", "separator");
  handle.setAttribute("aria-orientation", "vertical");
  pane.appendChild(handle);
  function maximum() {
    var sideStyle = getComputedStyle(sidebar);
    var sideWidth = sideStyle.display === "none" || sideStyle.position === "fixed" || sideStyle.position === "absolute" ? 0 : sidebar.offsetWidth;
    return Math.max(300, shell.clientWidth - sideWidth - 400);
  }
  function refresh() {
    if (disposed) return;
    pane.style.setProperty("--preview-max-width", maximum() + "px");
    handle.title = t("preview.resize"); handle.setAttribute("aria-label", handle.title);
    handle.setAttribute("aria-valuemin", "300");
    handle.setAttribute("aria-valuemax", String(Math.round(maximum())));
    handle.setAttribute("aria-valuenow", String(Math.round(pane.offsetWidth)));
  }
  function width(value) {
    sitePreviewWidth = Math.max(300, Math.min(maximum(), value));
    pane.style.setProperty("--preview-width", sitePreviewWidth + "px");
    refresh();
  }
  if (sitePreviewWidth) pane.style.setProperty("--preview-width", sitePreviewWidth + "px");
  var pointer = null, startX = 0, startWidth = 0, scale = 1;
  function finishDrag() {
    if (pointer === null) return;
    var id = pointer; pointer = null; pane.classList.remove("preview-resizing");
    if (handle.hasPointerCapture(id)) handle.releasePointerCapture(id);
  }
  handle.addEventListener("pointerdown", function (event) {
    if (event.button !== 0 || event.isPrimary === false || sitePreviewExpanded || window.innerWidth <= 900) return;
    event.preventDefault(); handle.focus();
    pointer = event.pointerId; startX = event.clientX; startWidth = pane.offsetWidth;
    scale = pane.getBoundingClientRect().width / Math.max(1, startWidth);
    handle.setPointerCapture(pointer); pane.classList.add("preview-resizing");
  });
  handle.addEventListener("pointermove", function (event) {
    if (pointer === event.pointerId) width(startWidth - (event.clientX - startX) / scale);
  });
  handle.addEventListener("pointerup", finishDrag);
  handle.addEventListener("pointercancel", finishDrag);
  handle.addEventListener("lostpointercapture", finishDrag);
  handle.addEventListener("keydown", function (event) {
    if (sitePreviewExpanded || window.innerWidth <= 900 || event.ctrlKey || event.metaKey || event.altKey) return;
    var current = pane.offsetWidth, step = event.shiftKey ? 96 : 32;
    switch (event.key) {
      case "ArrowLeft": width(current + step); break;
      case "ArrowRight": width(current - step); break;
      case "Home": width(300); break;
      case "End": width(maximum()); break;
      default: return;
    }
    event.preventDefault(); event.stopPropagation();
  });
  var observer = new ResizeObserver(refresh);
  observer.observe(shell); observer.observe(sidebar); observer.observe(pane);
  refresh();
  return {
    refresh: refresh,
    destroy: function () { disposed = true; finishDrag(); observer.disconnect(); }
  };
}
function closeSitePreview() {
  if (!sitePreviewController) return;
  if (sitePreviewLayout) sitePreviewLayout.destroy();
  sitePreviewLayout = null; sitePreviewExpanded = false;
  sitePreviewController.destroy(); sitePreviewController = null; sitePreviewPane = null;
  $("#shell").classList.remove("preview-open", "preview-expanded");
  $("#open-preview").setAttribute("aria-expanded", "false");
  $("#open-preview").focus();
}
function toggleSitePreviewSize() {
  if (!sitePreviewController) return;
  sitePreviewExpanded = !sitePreviewExpanded;
  $("#shell").classList.toggle("preview-expanded", sitePreviewExpanded);
  sitePreviewController.setExpanded(sitePreviewExpanded);
  sitePreviewLayout.refresh();
}
function openSitePreview(url) {
  if (!sitePreviewController) {
    sitePreviewPane = el("section", ""); sitePreviewPane.id = "site-preview";
    sitePreviewPane.setAttribute("aria-label", t("preview.title"));
    $("#shell").insertBefore(sitePreviewPane, $("#sidebar"));
    $("#shell").classList.add("preview-open");
    sitePreviewController = window.SuperCliPreview.create(sitePreviewPane, {
      text: t, request: jpost, url: url ? "" : lastPreviewURL, close: closeSitePreview,
      expand: toggleSitePreviewSize, expanded: sitePreviewExpanded,
      remember: function (value) { lastPreviewURL = value; }
    });
    sitePreviewLayout = createPreviewLayout(sitePreviewPane);
    registerLanguageRefresh(sitePreviewPane, function () {
      sitePreviewPane.setAttribute("aria-label", t("preview.title")); sitePreviewController.refresh();
      sitePreviewLayout.refresh();
    });
    $("#open-preview").setAttribute("aria-expanded", "true");
  }
  if (url) sitePreviewController.navigate(url);
  sitePreviewController.focus();
}
function toggleSitePreview() {
  if (sitePreviewController) closeSitePreview();
  else openSitePreview();
}
$("#open-preview").addEventListener("click", toggleSitePreview);

// One delegated listener also covers restored sessions and streamed answers.
// Choosing a destination does not create a frame or launch a browser.
// classifyChatLink tells a web link (preview dialog) from a file on this
// computer: a relative path, C:\... or C:/..., file://..., a path with a
// ":line" suffix, or a link that only resolved to this GUI's own address.
// Returns {kind: "web", url} | {kind: "file", target} | {kind: "other"}.
function classifyChatLink(raw, origin) {
  raw = String(raw || "").trim();
  if (!raw || raw.charAt(0) === "#") return {kind: "other"};
  if (/^[a-zA-Z]:[\\/]/.test(raw) || /^\\\\/.test(raw)) return {kind: "file", target: raw};
  if (/^file:/i.test(raw)) return {kind: "file", target: raw};
  var scheme = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(raw);
  if (scheme && !/^https?$/i.test(scheme[1])) return {kind: "other"};
  if (!scheme) return {kind: "file", target: raw.replace(/^\.\//, "")};
  var url;
  try { url = new URL(raw); } catch (error) { return {kind: "other"}; }
  if (origin && url.origin === origin) {
    // The GUI's own pages and API are not files the chat talks about.
    if (url.pathname === "/" || /^\/(api|\.__supercli)\//.test(url.pathname)) return {kind: "other"};
    return {kind: "file", target: decodeURIComponent(url.pathname.replace(/^\//, "")) + (url.hash || "")};
  }
  return {kind: "web", url: url.href};
}

// openChatFileLink shows a file named in the chat on this computer: a
// folder opens, a text or code file in the project opens in SuperCli's file
// view at its line, other files can be opened with their app; programs and
// scripts are only ever shown in their folder.
async function openChatFileLink(target) {
  var info;
  try { info = await jpost("/api/file-link", { target: target }); }
  catch (error) { toast(t("filelink.missing").replace("{path}", target)); return; }
  if (!info.exists) { toast(t("filelink.missing").replace("{path}", info.path || target)); return; }
  var act = async function (action) {
    try { await jpost("/api/file-link", { target: info.path, action: action }); }
    catch (error) { toast(error.message); }
  };
  if (info.kind === "dir") { act("open"); return; }
  var choices = [];
  if (info.kind === "text" && info.in_workspace) choices.push({ value: "view", label: t("filelink.open"), primary: true });
  else if (!info.executable) choices.push({ value: "open", label: t("filelink.open"), primary: true });
  choices.push({ value: "reveal", label: t("filelink.reveal"), primary: !choices.length });
  var choice = await showAppDialog({
    title: t("filelink.title"),
    message: info.path + (info.line ? ":" + info.line : "") + (info.executable && info.kind !== "text" ? " — " + t("filelink.noRun") : ""),
    choices: choices
  });
  if (choice === "view") openFileInPanel(info.path, info.line);
  else if (choice === "open" || choice === "reveal") act(choice);
}

$("#stream").addEventListener("click", async function (event) {
  if (event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
  var link = event.target.closest("a[href]");
  if (!link || !$("#stream").contains(link)) return;
  var kind = classifyChatLink(link.getAttribute("href"), location.origin);
  if (kind.kind === "file") {
    event.preventDefault();
    openChatFileLink(kind.target);
    return;
  }
  if (kind.kind !== "web") return;
  var target = new URL(kind.url);
  event.preventDefault();
  var destination = await showAppDialog({
    title: t("preview.title"), message: target.href,
    choices: [
      { value: "preview", label: t("preview.open"), primary: true },
      { value: "browser", label: t("preview.browser") }
    ]
  });
  if (destination === "preview") openSitePreview(target.href);
  else if (destination === "browser") {
    try { await jpost("/api/browser/open", { url: target.href }); }
    catch (error) { toast(window.SuperCliPreview.errorMessage(t, error)); }
  }
});
