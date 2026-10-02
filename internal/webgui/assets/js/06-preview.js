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
$("#stream").addEventListener("click", async function (event) {
  if (event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
  var link = event.target.closest("a[href]");
  if (!link || !$("#stream").contains(link)) return;
  var target;
  try { target = new URL(link.href); } catch (error) { return; }
  if (target.protocol !== "http:" && target.protocol !== "https:") return;
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
