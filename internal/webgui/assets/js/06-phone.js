"use strict";

(function () {
  var button = document.getElementById("open-phone");
  if (!button) return;
  var local = /^(localhost|127(?:\.[0-9]+){3}|\[?::1\]?)$/i.test(location.hostname);
  button.hidden = !local;
  if (!local) return;
  var panel = document.getElementById("phone-panel"), consent = document.getElementById("phone-consent");
  var toggle = document.getElementById("phone-enabled"), status = document.getElementById("phone-status");
  var progress = document.getElementById("phone-progress"), qr = document.getElementById("phone-qr");
  var link = document.getElementById("phone-link"), expires = document.getElementById("phone-expires");
  var copy = document.getElementById("phone-copy");
  var lastState = "", busy = false, timer;
  function clearCode() { qr.replaceChildren(); link.value = ""; copy.disabled = true; }
  async function copyLink() {
    var url = link.value;
    if (!url) return;
    try {
      if (!navigator.clipboard || !navigator.clipboard.writeText) throw new Error("Clipboard unavailable");
      await navigator.clipboard.writeText(url);
    } catch (_) {
      var textarea = document.createElement("textarea"), active = document.activeElement;
      textarea.value = url;
      textarea.readOnly = true;
      textarea.style.cssText = "position:fixed;left:-9999px;top:0;opacity:0";
      document.body.appendChild(textarea);
      try {
        textarea.focus(); textarea.select();
        if (!document.execCommand("copy")) throw new Error("Copy failed");
      } catch (_) {
        toast(t("phone.copyFailed"));
        return;
      } finally {
        textarea.remove();
        if (active && active.focus) active.focus({preventScroll:true});
      }
    }
    toast(t("phone.copied"));
  }
  async function pair() {
    var result = await jpost("/api/phone/pair", {});
    // SVG comes only from the bundled, server-side QR encoder.
    qr.innerHTML = result.qr_svg;
    link.value = result.url;
    copy.disabled = !result.url;
  }
  async function refresh() {
    try {
      var st = await j("/api/phone/status");
      toggle.checked = st.state === "on" || st.state === "starting" || st.state === "downloading";
      toggle.disabled = busy || st.state === "starting" || st.state === "downloading";
      button.classList.toggle("phone-on", st.state === "on");
      status.textContent = st.error || t("phone.state." + st.state);
      progress.hidden = st.state !== "downloading";
      progress.max = st.total || st.download_size; progress.value = st.downloaded;
      document.getElementById("phone-new-code").disabled = st.state !== "on";
      document.getElementById("phone-disconnect").disabled = st.state === "off" || st.state === "error";
      if (st.state === "on") {
        expires.textContent = t("phone.expires", {minutes: Math.max(0, Math.ceil((Date.parse(st.expires_at) - Date.now()) / 60000))});
        if (!panel.hidden && (lastState !== "on" || !link.value)) await pair();
      } else { expires.textContent = ""; clearCode(); }
      lastState = st.state;
      return st;
    } catch (error) { status.textContent = error.message; }
  }
  async function start(allowDownload) {
    busy = true; toggle.disabled = true;
    try { await jpost("/api/phone/start", {allow_download:allowDownload}); }
    catch (error) { status.textContent = error.message; toast(error.message); }
    finally { busy = false; await refresh(); }
  }
  async function stop() {
    busy = true;
    try { await jpost("/api/phone/stop", {}); clearCode(); }
    catch (error) { toast(error.message); }
    finally { busy = false; await refresh(); }
  }
  button.addEventListener("click", async function () {
    panel.hidden = !panel.hidden;
    button.setAttribute("aria-expanded", String(!panel.hidden));
    if (!panel.hidden) { clearCode(); await refresh(); toggle.focus(); }
  });
  document.getElementById("phone-close").addEventListener("click", function () { panel.hidden = true; button.setAttribute("aria-expanded", "false"); button.focus(); });
  panel.addEventListener("keydown", function (event) { if (event.key === "Escape") document.getElementById("phone-close").click(); });
  toggle.addEventListener("change", async function () {
    if (!toggle.checked) { await stop(); return; }
    var st = await refresh(); if (!st) return;
    if (!st.available) { consent.showModal(); } else await start(false);
  });
  document.getElementById("phone-download").addEventListener("click", async function () { consent.close(); await start(true); });
  document.getElementById("phone-cancel-download").addEventListener("click", function () { consent.close(); refresh(); });
  consent.addEventListener("cancel", function () { refresh(); });
  document.getElementById("phone-new-code").addEventListener("click", function () { pair().catch(function (error) { toast(error.message); }); });
  document.getElementById("phone-disconnect").addEventListener("click", stop);
  copy.addEventListener("click", copyLink);
  timer = setInterval(function () { if (!document.hidden) refresh(); }, 2000);
  window.addEventListener("pagehide", function () { clearInterval(timer); });
  refresh();
})();
