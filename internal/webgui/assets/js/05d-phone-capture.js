"use strict";

// Native capture is a browser hint, not a camera permission request. Browsers
// without capture support simply offer their normal file picker.
(function () {
  var button = document.getElementById("attach-btn");
  var menu = document.getElementById("phone-attachment-menu");
  if (!button || !menu || button.dataset.phoneCaptureBound) return;
  button.dataset.phoneCaptureBound = "true";
  var narrow = window.matchMedia("(max-width:560px)");
  // Capture actions follow the responsive phone layout, not remote transport:
  // a wide desktop reached through a LAN/hostname keeps its existing picker.
  var busy = false, selection = null;
  function phoneMode() { return narrow.matches; }
  function scope() { return {draft: composerDraftStore.scope(), epoch: projectEpoch}; }
  function current(saved) {
    return saved && saved.draft === composerDraftStore.scope() && saved.epoch === projectEpoch && !queueEdit;
  }
  function available() {
    return !busy && !button.disabled && !queueEdit && pendingAttachments.length < 8;
  }
  function close(restoreFocus) {
    menu.hidden = true;
    button.setAttribute("aria-expanded", "false");
    if (restoreFocus && !button.disabled) button.focus({preventScroll:true});
  }
  function refreshMode() {
    if (phoneMode()) {
      button.setAttribute("aria-haspopup", "dialog");
      button.setAttribute("aria-controls", menu.id);
      if (!button.hasAttribute("aria-expanded")) button.setAttribute("aria-expanded", "false");
    } else {
      close(menu.contains(document.activeElement));
      button.removeAttribute("aria-haspopup");
      button.removeAttribute("aria-controls");
      button.removeAttribute("aria-expanded");
    }
  }
  refreshMode();
  narrow.addEventListener("change", refreshMode);
  // Capture phase prevents the existing desktop pickAttachments listener from
  // opening a second chooser. Desktop clicks are otherwise left untouched.
  button.addEventListener("click", function (event) {
    if (!phoneMode()) return;
    event.preventDefault(); event.stopImmediatePropagation();
    if (!menu.hidden) { close(true); return; }
    if (!available()) return;
    selection = null;
    menu.hidden = false;
    button.setAttribute("aria-expanded", "true");
    document.getElementById("phone-attachment-files").focus({preventScroll:true});
  }, true);
  ["files", "photo", "video"].forEach(function (kind) {
    var input = document.getElementById("phone-" + kind + "-input");
    document.getElementById("phone-attachment-" + kind).addEventListener("click", function () {
      if (!available()) { close(true); return; }
      selection = {input: input, scope: scope()};
      input.value = "";
      close(true);
      // Must remain synchronous within this user gesture (especially on iOS).
      input.click();
    });
    input.addEventListener("cancel", function () {
      input.value = "";
      if (selection && selection.input === input) selection = null;
    });
    input.addEventListener("change", async function () {
      var files = Array.prototype.slice.call(input.files || []);
      var selected = selection;
      input.value = ""; // Allow the same captured/picked file again.
      selection = null;
      if (!files.length || !selected || selected.input !== input || !current(selected.scope) || !available()) return;
      busy = true; button.disabled = true; button.setAttribute("aria-busy", "true");
      try {
        // Shared upload owns staging, media processing and attachment rendering.
        await uploadAttachmentFiles(files, "composer.attach");
      } catch (error) {
        if (current(selected.scope)) toast(t("composer.attachFailed") + ": " + error.message);
      } finally {
        busy = false; button.disabled = false; button.removeAttribute("aria-busy");
      }
    });
  });
  document.getElementById("phone-attachment-close").addEventListener("click", function () { close(true); });
  menu.addEventListener("keydown", function (event) {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(true); }
    if (event.key === "Tab") {
      var items = menu.querySelectorAll("button");
      if ((event.shiftKey && document.activeElement === items[0]) || (!event.shiftKey && document.activeElement === items[items.length - 1])) {
        event.preventDefault(); close(true);
      }
    }
  });
  document.addEventListener("click", function (event) {
    if (!menu.hidden && !menu.contains(event.target) && !button.contains(event.target)) close(false);
  });
  document.addEventListener("focusin", function (event) {
    if (!menu.hidden && !menu.contains(event.target) && event.target !== button) close(false);
  });
})();
