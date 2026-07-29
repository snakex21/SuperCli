"use strict";

sections.providers = function () { renderProvidersList(); };

function providerModelCount(models) {
  return Array.isArray(models) ? models.length : (Number(models) || 0);
}

// providerSlug normalizes a provider name to a filename-safe icon key.
function providerSlug(name) {
  return String(name || "").toLowerCase().replace(/[^a-z0-9-]/g, "");
}

// providerIconEl builds a small provider badge. It renders a colored monogram
// immediately, then tries to swap in a bundled SVG from icons/providers/.
// Regional variants (xiaomi-tokenplan-global) fall back to the family icon
// (xiaomi) after the slug, so one logo covers the whole family. Anything with
// no bundled icon keeps the monogram — a new provider is never left blank.
function providerIconEl(name) {
  var slug = providerSlug(name);
  var family = slug.split("-")[0];
  var wrap = el("span", "prov-icon");
  var letter = (family.charAt(0) || "?").toUpperCase();
  var mono = el("span", "prov-mono", letter);
  var h = 0;
  for (var i = 0; i < slug.length; i++) h = ((h * 31) + slug.charCodeAt(i)) >>> 0;
  mono.style.background = "hsl(" + (h % 360) + " 52% 42%)";
  wrap.appendChild(mono);
  var candidates = [];
  if (slug) candidates.push("icons/providers/" + slug + ".svg");
  if (family && family !== slug) candidates.push("icons/providers/" + family + ".svg");
  if (candidates.length) {
    var idx = 0;
    var img = new Image();
    img.className = "prov-img";
    img.alt = "";
    img.addEventListener("load", function () { mono.style.display = "none"; wrap.appendChild(img); });
    img.addEventListener("error", function () { idx++; if (idx < candidates.length) img.src = candidates[idx]; });
    img.src = candidates[0];
  }
  return wrap;
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
    var row = el("div", "list-row" + (p.Disabled ? " provider-disabled" : ""));
    row.appendChild(providerIconEl(p.Name));
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(p.Name) + "</strong> <span class='note'>" + escHtml(p.Type || "") + "</span>";
    main.appendChild(title);
    var sub = (p.BaseURL || "") + " · " + (p.HasKey ? t("prov.key") : t("prov.noKey")) +
      ((p.Models || []).length ? " · " + p.Models.length + " " + t("prov.models") : "");
    if (p.Disabled) sub = (ui.lang === "pl" ? "wyłączony · " : "disabled · ") + sub;
    main.appendChild(el("div", "lr-sub", sub));
    row.appendChild(main);
    var act = el("div", "lr-act");
    var bt = el("button", "", p.Disabled ? (ui.lang === "pl" ? "włącz" : "enable") : (ui.lang === "pl" ? "wyłącz" : "disable"));
    bt.addEventListener("click", async function () {
      try {
        await j("/api/providers", { method:"PUT", headers:{"Content-Type":"application/json"}, body:JSON.stringify({name:p.Name, disabled:!p.Disabled}) });
        await loadModels(); await renderProvidersList();
      } catch (e) { toast(e.message); }
    });
    act.appendChild(bt);
    var bs = el("button", "", t("common.scan"));
    bs.disabled = !!p.Disabled;
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
        // A provider kept despite an unfinished scan reports why, so the
        // empty model list does not look like a silent failure.
        if (res2.warning) toast(res2.warning);
        else toast(t("prov.added") + " " + providerModelCount(res2.models) + " " + t("prov.models"));
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
      b.appendChild(providerIconEl(tpl.Name));
      var txt = el("span", "tpl-txt");
      txt.appendChild(el("span", "tt", tpl.Name));
      txt.appendChild(el("span", "ts", tpl.Desc || tpl.BaseURL || ""));
      b.appendChild(txt);
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
