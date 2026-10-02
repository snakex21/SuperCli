"use strict";
// Shared by the chat pane and the lightweight TUI preview window. No frame or
// network request exists until the user opens a URL. Closing removes the frame.
window.SuperCliPreview = {
  errorMessage: function (text, error) {
    var detail = error && error.message ? String(error.message).trim().slice(0, 512) : "";
    return text("preview.failed") + (detail ? "\n" + detail : "");
  },
  create: function (root, options) {
    var disposed = false, generation = 0, currentURL = "", frame = null, noticeKey = "", noticeDetail = "";
    var text = options.text, expanded = !!options.expanded;
    function node(tag, cls, content) {
      var item = document.createElement(tag);
      item.className = cls || "";
      if (content != null) item.textContent = content;
      return item;
    }
    function button(symbol, key, action) {
      var item = node("button", "preview-button", symbol);
      item.type = "button";
      item._copyKey = key;
      item.addEventListener("click", action);
      return item;
    }
    function icon(item, path) {
      item.innerHTML = '<svg viewBox="0 0 20 20" aria-hidden="true"><path d="' + path + '"/></svg>';
    }
    root.classList.add("site-preview");
    var head = node("div", "preview-heading");
    var title = node("strong", "preview-title");
    var close = options.close ? button("×", "preview.close", options.close) : null;
    var expand = options.expand ? button("", "preview.expand", options.expand) : null;
    var actions = node("div", "preview-heading-actions");
    if (expand) actions.appendChild(expand); if (close) actions.appendChild(close);
    head.appendChild(title); head.appendChild(actions);
    var form = node("form", "preview-addressbar");
    var address = node("input", "preview-address");
    address.type = "text"; address.maxLength = 4096; address.autocomplete = "off";
    address.spellcheck = false; address.placeholder = "http://localhost:5173";
    address.value = options.url || "";
    var go = button("→", "preview.open", function () { navigate(); });
    var reload = button("↻", "preview.reload", function () {
      if (currentURL && frame) frame.src = currentURL;
      else navigate();
    });
    var clear = button("", "preview.clear", function () { reset(); });
    icon(clear, "M3 12l7-8 7 6-7 8H7zM7 18h10M6 9l7 6");
    var external = button("↗", "preview.browser", function () { openExternal(); });
    form.appendChild(address); form.appendChild(go); form.appendChild(reload); form.appendChild(clear); form.appendChild(external);
    form.addEventListener("submit", function (event) { event.preventDefault(); navigate(); });
    var hint = node("p", "preview-hint");
    var status = node("p", "preview-status"); status.setAttribute("role", "status");
    var view = node("div", "preview-view");
    root.appendChild(head); root.appendChild(form); root.appendChild(hint); root.appendChild(status); root.appendChild(view);
    function notice(key, detail) {
      noticeKey = key; noticeDetail = detail || "";
      status.textContent = key ? text(key) + (noticeDetail ? "\n" + noticeDetail : "") : "";
    }
    function refresh() {
      title.textContent = text("preview.title"); hint.textContent = text("preview.hint");
      address.setAttribute("aria-label", text("preview.address"));
      if (expand) {
        expand._copyKey = expanded ? "preview.restore" : "preview.expand";
        expand.setAttribute("aria-pressed", String(expanded));
        icon(expand, expanded ? "M7 3h10v10M3 7h10v10H3z" : "M7 3H3v4M13 3h4v4M17 13v4h-4M7 17H3v-4");
      }
      [close, expand, go, reload, clear, external].filter(Boolean).forEach(function (item) {
        item.title = text(item._copyKey); item.setAttribute("aria-label", item.title);
      });
      if (frame) frame.title = text("preview.title");
      notice(noticeKey, noticeDetail);
    }
    function reset() {
      if (disposed) return;
      generation++; currentURL = ""; address.value = ""; notice("");
      if (frame) { frame.remove(); frame = null; }
      if (options.remember) options.remember("");
      address.focus();
    }
    async function navigate(url) {
      if (disposed) return;
      if (typeof url === "string") address.value = url;
      var ticket = ++generation, raw = address.value;
      notice("");
      try {
        var result = await options.request("/api/browser/resolve", { url: raw });
        if (disposed || ticket !== generation) return;
        currentURL = result.url; address.value = currentURL;
        if (!frame) {
          frame = node("iframe", "preview-frame");
          // Dev sites can run scripts/forms/HMR, but cannot navigate the parent,
          // open popups or invoke native bindings. The app refuses framing itself.
          frame.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms allow-modals");
          frame.setAttribute("referrerpolicy", "no-referrer");
          frame.title = text("preview.title"); view.appendChild(frame);
        }
        frame.src = currentURL;
        if (options.remember) options.remember(currentURL);
      } catch (error) {
        if (!disposed && ticket === generation) notice("preview.invalid");
      }
    }
    async function openExternal() {
      if (disposed || external.disabled) return;
      var ticket = generation;
      external.disabled = true; notice("");
      try {
        await options.request("/api/browser/open", { url: address.value });
        if (!disposed && ticket === generation) notice("preview.opened");
      } catch (error) {
        if (!disposed && ticket === generation) notice("preview.failed", error && error.message ? String(error.message).trim().slice(0, 512) : "");
      } finally { if (!disposed) external.disabled = false; }
    }
    refresh();
    if (options.url) navigate();
    return {
      refresh: refresh,
      navigate: navigate,
      reset: reset,
      setExpanded: function (value) { expanded = !!value; refresh(); },
      focus: function () { address.focus(); address.select(); },
      destroy: function () {
        disposed = true; generation++;
        if (frame) { frame.remove(); frame = null; }
        root.remove();
      }
    };
  }
};
