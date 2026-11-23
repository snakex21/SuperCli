"use strict";

sections.providers = function () { return renderProvidersList(); };

function providerModelCount(models) {
  return Array.isArray(models) ? models.length : (Number(models) || 0);
}

// providerSlug normalizes a provider name for the letter badge colour.
function providerSlug(name) {
  return String(name || "").toLowerCase().replace(/[^a-z0-9-]/g, "");
}

// Built-in logos (icons/providers/<key>.svg): key -> true for single-colour
// logos, which are drawn as a mask in the theme's text colour so they read on
// a dark background; colour logos are shown as images.
var providerIconMono = null;
async function loadProviderIconCatalog() {
  if (providerIconMono) return providerIconMono;
  try {
    var list = await j("/api/provider/icons");
    providerIconMono = {};
    (list || []).forEach(function (icon) { providerIconMono[icon.key] = !!icon.mono; });
  } catch (e) { providerIconMono = {}; }
  return providerIconMono;
}
function providerGlyph(key, onReady) {
  var url = "icons/providers/" + encodeURIComponent(key) + ".svg";
  if (providerIconMono && providerIconMono[key]) {
    var glyph = el("span", "prov-glyph");
    glyph.style.webkitMaskImage = glyph.style.maskImage = "url(" + url + ")";
    onReady();
    return glyph;
  }
  var img = new Image();
  img.className = "prov-img";
  img.alt = "";
  img.addEventListener("load", onReady);
  img.src = url;
  return img;
}

// providerIconEl is a provider's badge: the user's own logo, else the
// built-in logo the server matched from type and address (p.Icon), else a
// coloured letter. The name the user typed never picks the logo.
function providerIconEl(p) {
  if (typeof p === "string") p = {Name: p};
  p = p || {};
  var slug = providerSlug(p.Name);
  var wrap = el("span", "prov-icon");
  var mono = el("span", "prov-mono", (slug.charAt(0) || "?").toUpperCase());
  var h = 0;
  for (var i = 0; i < slug.length; i++) h = ((h * 31) + slug.charCodeAt(i)) >>> 0;
  mono.style.background = "hsl(" + (h % 360) + " 52% 42%)";
  wrap.appendChild(mono);
  var logo = p.Logo || {};
  function ready(node) { return function () { mono.style.display = "none"; node.style.display = ""; }; }
  if (logo.url) {
    var own = new Image();
    own.className = "prov-img prov-own";
    own.alt = "";
    own.style.display = "none";
    own.addEventListener("load", ready(own));
    own.src = logo.url;
    wrap.appendChild(own);
  } else if (logo.builtin || p.Icon) {
    var holder = el("span", "prov-tile");
    holder.style.display = "none";
    holder.appendChild(providerGlyph(logo.builtin || p.Icon, ready(holder)));
    wrap.appendChild(holder);
  }
  return wrap;
}

async function renderProvidersList() {
  var got;
  try {
    await loadProviderIconCatalog();
    got = await j("/api/providers?lang=" + encodeURIComponent(ui.lang));
  } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var lbl = i18nEl("div", "g-label", "prov.configured");
  var addB = el("button", "g-act", "＋ " + t("prov.addNew"));
  addB.addEventListener("click", function () { renderProviderForm(got.templates || [], null); });
  lbl.appendChild(addB);
  g.appendChild(lbl);
  var provs = got.providers || [];
  if (!provs.length) g.appendChild(i18nEl("div", "note", "prov.none"));
  provs.forEach(function (p) {
    var row = el("div", "list-row" + (p.Disabled ? " provider-disabled" : ""));
    row.appendChild(providerIconEl(p));
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(p.Name) + "</strong> <span class='note'>" + escHtml(p.Type || "") + "</span>";
    main.appendChild(title);
    var sub = (p.BaseURL || "") + " · " + (p.Type === "claude" ? t("claude.account") : (p.HasKey ? t("prov.key") : t("prov.noKey"))) +
      ((p.Models || []).length ? " · " + p.Models.length + " " + t("prov.models") : "");
    if (p.Disabled) sub = (t("prov.disabledPrefix")) + sub;
    main.appendChild(el("div", "lr-sub", sub));
    row.appendChild(main);
    var act = el("div", "lr-act");
    var bt = el("button", "", p.Disabled ? (t("runtime.enable")) : (t("runtime.disable")));
    bt.addEventListener("click", async function () {
      try {
        await j("/api/providers", { method:"PUT", headers:{"Content-Type":"application/json"}, body:JSON.stringify({name:p.Name, disabled:!p.Disabled}) });
        await loadModels(); await renderProvidersList();
      } catch (e) { toast(e.message); }
    });
    act.appendChild(bt);
    var bs = i18nEl("button", "", "common.scan");
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
    var br = i18nEl("button", "", "prov.rename");
    br.addEventListener("click", function () { startProviderRename(row, main, act, p); });
    act.appendChild(br);
    var be = i18nEl("button", "", "common.edit");
    be.addEventListener("click", function () {
      if (p.Type === "claude") renderClaudeLogin(got.templates || []);
      else renderProviderForm(got.templates || [], p);
    });
    act.appendChild(be);
    var bx = i18nEl("button", "danger", "common.remove");
    bx.addEventListener("click", function () { confirmProviderRemove(row, main, act, p); });
    act.appendChild(bx);
    row.appendChild(act);
    g.appendChild(row);
  });
  panelContent.appendChild(g);
}

// renameProvider asks the backend to rename a provider and rewrite every
// reference (settings, "provider/model" choices, saved sessions). The API key
// stays inside the provider entry; it is never sent or returned here.
async function renameProvider(oldName, newName) {
  var res = await j("/api/provider/rename", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: oldName, new_name: newName }),
  });
  if (res.warnings && res.warnings.length) toast(res.warnings.join(" · "));
  return res;
}

// startProviderRename swaps a provider row's title for an inline name editor.
function startProviderRename(row, main, act, p) {
  if (row.querySelector(".provider-rename")) return;
  var box = el("form", "provider-rename");
  box.style.display = "flex";
  box.style.gap = "6px";
  box.style.alignItems = "center";
  box.style.flexWrap = "wrap";
  var inp = document.createElement("input");
  inp.className = "field-input";
  inp.type = "text";
  inp.value = p.Name;
  inp.maxLength = 64;
  inp.setAttribute("aria-label", t("prov.renameLabel"));
  inp.title = t("prov.renameHint");
  var ok = i18nEl("button", "btn primary", "common.save");
  ok.type = "submit";
  var cancel = i18nEl("button", "btn", "common.cancel");
  cancel.type = "button";
  var note = el("div", "note", t("prov.renameHint"));
  note.style.flexBasis = "100%";
  box.appendChild(inp); box.appendChild(ok); box.appendChild(cancel); box.appendChild(note);
  main.style.display = "none";
  act.style.display = "none";
  row.insertBefore(box, act);
  function close() { box.remove(); main.style.display = ""; act.style.display = ""; }
  cancel.addEventListener("click", close);
  inp.addEventListener("keydown", function (e) { if (e.key === "Escape") close(); });
  box.addEventListener("submit", async function (e) {
    e.preventDefault();
    var next = inp.value.trim();
    if (!next || next === p.Name) { close(); return; }
    ok.disabled = true;
    try {
      await renameProvider(p.Name, next);
      toast(t("prov.renamed").replace("{n}", next));
      await loadModels();
      await renderProvidersList();
    } catch (err) {
      ok.disabled = false;
      note.textContent = t("prov.renameFailed") + ": " + err.message;
    }
  });
  inp.focus();
  inp.select();
}

// confirmProviderRemove asks inside the row before deleting, and says when
// the selected model or saved choices (modes, roles, settings) use it.
async function confirmProviderRemove(row, main, act, p) {
  if (row.querySelector(".provider-remove-confirm")) return;
  var box = el("div", "provider-remove-confirm");
  box.appendChild(el("div", "", t("prov.removeConfirm").replace("{n}", p.Name)));
  var warn = el("div", "note provider-remove-warn");
  warn.hidden = true;
  box.appendChild(warn);
  var buttons = el("div", "provider-remove-buttons");
  var yes = i18nEl("button", "btn danger", "prov.removeYes");
  var no = i18nEl("button", "btn", "common.cancel");
  buttons.appendChild(yes); buttons.appendChild(no);
  box.appendChild(buttons);
  main.style.display = "none";
  act.style.display = "none";
  row.insertBefore(box, act);
  function close() { box.remove(); main.style.display = ""; act.style.display = ""; }
  no.addEventListener("click", close);
  yes.addEventListener("click", async function () {
    yes.disabled = no.disabled = true;
    try {
      var resp = await fetch("/api/providers?name=" + encodeURIComponent(p.Name), { method: "DELETE" });
      if (!resp.ok) throw new Error((await resp.text()) || ("HTTP " + resp.status));
      toast(t("prov.removed").replace("{n}", p.Name));
      if (typeof loadModels === "function") loadModels();
      renderProvidersList();
    } catch (e) {
      yes.disabled = no.disabled = false;
      toast(e.message);
    }
  });
  no.focus();
  try {
    var usage = await j("/api/provider/usage?name=" + encodeURIComponent(p.Name));
    var lines = [];
    if (usage && usage.active) lines.push(t("prov.removeActive"));
    if (usage && usage.refs) lines.push(t("prov.removeRefs"));
    warn.textContent = lines.join(" ");
    warn.hidden = !lines.length;
  } catch (e) { /* the question still stands without the warning */ }
}

// renderProviderLogoPicker: the edit form's logo row. Choices apply at once:
// automatic (matched from type and address), any built-in logo, or an
// uploaded image (PNG, JPG, SVG, WebP; up to 512 KB; SVG is sanitized).
function renderProviderLogoPicker(parent, existing) {
  var box = el("div", "fw provider-logo-picker");
  var head = el("div", "provider-logo-head");
  head.appendChild(el("span", "", t("prov.logo")));
  var preview = el("span", "provider-logo-preview");
  head.appendChild(preview);
  box.appendChild(head);
  box.appendChild(el("div", "note", t("prov.logoHint")));
  var grid = el("div", "provider-logo-grid");
  box.appendChild(grid);
  var tools = el("div", "provider-logo-tools");
  var file = document.createElement("input");
  file.type = "file";
  file.accept = ".png,.jpg,.jpeg,.svg,.webp,image/png,image/jpeg,image/svg+xml,image/webp";
  file.hidden = true;
  var upload = i18nEl("button", "btn", "prov.logoUpload");
  upload.type = "button";
  var reset = i18nEl("button", "btn", "prov.logoReset");
  reset.type = "button";
  var status = el("span", "note provider-logo-status");
  tools.appendChild(upload); tools.appendChild(reset); tools.appendChild(file); tools.appendChild(status);
  box.appendChild(tools);
  parent.appendChild(box);

  function paint() {
    preview.replaceChildren(providerIconEl(existing));
    var current = (existing.Logo && existing.Logo.builtin) || (!existing.Logo ? "auto" : "");
    grid.querySelectorAll("button").forEach(function (b) {
      b.setAttribute("aria-pressed", b.dataset.key === current ? "true" : "false");
    });
    reset.disabled = !existing.Logo;
  }
  async function save(body) {
    status.textContent = "…";
    body.name = existing.Name;
    try {
      var resp = await fetch("/api/provider/logo", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      var data = await resp.json().catch(function () { return {}; });
      if (!resp.ok) throw new Error(data.code ? t(data.code) : (data.error || ("HTTP " + resp.status)));
      existing.Logo = data.logo || null;
      status.textContent = t("prov.logoSaved");
      paint();
    } catch (e) { status.textContent = e.message; }
  }
  var auto = el("button", "provider-logo-choice provider-logo-auto", t("prov.logoAuto"));
  auto.type = "button";
  auto.dataset.key = "auto";
  auto.title = t("prov.logoAuto");
  auto.addEventListener("click", function () { save({ reset: true }); });
  grid.appendChild(auto);
  loadProviderIconCatalog().then(function (icons) {
    Object.keys(icons).sort().forEach(function (key) {
      var b = el("button", "provider-logo-choice");
      b.type = "button";
      b.dataset.key = key;
      b.title = key;
      b.setAttribute("aria-label", key);
      b.appendChild(providerIconEl({ Name: key, Icon: key }));
      b.addEventListener("click", function () { save({ builtin: key }); });
      grid.appendChild(b);
    });
    paint();
  });
  upload.addEventListener("click", function () { file.click(); });
  reset.addEventListener("click", function () { save({ reset: true }); });
  file.addEventListener("change", function () {
    var f = file.files && file.files[0];
    file.value = "";
    if (!f) return;
    if (f.size > 512 * 1024) { status.textContent = t("prov.logoTooBig"); return; }
    var reader = new FileReader();
    reader.onload = function () {
      var text = String(reader.result || "");
      save({ filename: f.name, data: text.slice(text.indexOf(",") + 1) });
    };
    reader.onerror = function () { status.textContent = t("prov.logoInvalid"); };
    reader.readAsDataURL(f);
  });
  paint();
}

function normalizeProviderSearch(value) {
  var text = String(value || "").toLowerCase();
  return text.normalize ? text.normalize("NFD").replace(/[\u0300-\u036f]/g, "") : text;
}

function renderProviderChooser(templates) {
  panelContent.innerHTML = "";
  var chooser = el("div", "provider-chooser");
  var head = el("div", "provider-chooser-head");
  var copy = el("div", "provider-chooser-copy");
  copy.appendChild(el("strong", "", t("prov.choose")));
  copy.appendChild(el("span", "note", t("prov.chooseHint")));
  head.appendChild(copy);
  var back = el("button", "g-act", "‹ " + t("common.back"));
  back.addEventListener("click", renderProvidersList);
  head.appendChild(back);
  chooser.appendChild(head);

  var templateSearch = document.createElement("input");
  templateSearch.type = "search";
  templateSearch.className = "field-input provider-template-search";
  templateSearch.placeholder = t("prov.searchPlaceholder");
  templateSearch.setAttribute("aria-label", t("prov.search"));
  templateSearch.autocomplete = "off";
  templateSearch.spellcheck = false;
  chooser.appendChild(templateSearch);

  var grid = el("div", "tpl-grid provider-chooser-grid");
  var choices = [{
    Name: "custom",
    DisplayName: t("prov.custom"),
    Desc: t("prov.customHint"),
    Type: "auto",
    BaseURL: "",
    Custom: true,
  }].concat(templates || []);
  choices.forEach(function (tpl) {
    var displayName = tpl.DisplayName || tpl.Name;
    var b = el("button");
    b.type = "button";
    b.dataset.search = normalizeProviderSearch([displayName, tpl.Name, tpl.Desc, tpl.BaseURL].filter(Boolean).join(" "));
    b.appendChild(providerIconEl(tpl.Custom ? {Name: "custom"} : tpl));
    var txt = el("span", "tpl-txt");
    txt.appendChild(el("span", "tt", displayName));
    txt.appendChild(el("span", "ts", tpl.Desc || tpl.BaseURL || ""));
    b.appendChild(txt);
    b.addEventListener("click", function () { renderProviderForm(templates, null, tpl); });
    grid.appendChild(b);
  });
  chooser.appendChild(grid);

  var emptySearch = el("div", "note provider-template-empty", t("prov.noMatches"));
  emptySearch.hidden = true;
  chooser.appendChild(emptySearch);
  templateSearch.addEventListener("input", function () {
    var q = normalizeProviderSearch(templateSearch.value.trim());
    var visible = 0;
    grid.querySelectorAll("button").forEach(function (button) {
      var show = !q || button.dataset.search.indexOf(q) >= 0;
      button.classList.toggle("provider-template-filtered", !show);
      if (show) visible++;
    });
    emptySearch.hidden = visible !== 0;
  });

  panelContent.appendChild(chooser);
  templateSearch.focus();
}

function renderProviderForm(templates, existing, selectedTemplate) {
  if ((selectedTemplate && selectedTemplate.Type === "claude") || (existing && existing.Type === "claude")) {
    renderClaudeLogin(templates);
    return;
  }
  if (!existing && !selectedTemplate) {
    renderProviderChooser(templates);
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var selectedName = selectedTemplate && !selectedTemplate.Custom ? selectedTemplate.Name : "";
  var lbl = el("div", "g-label", existing
    ? t("prov.save") + ": " + existing.Name
    : ((selectedTemplate && selectedTemplate.DisplayName) || selectedName || (t("prov.custom"))));
  var back = el("button", "g-act", "‹ " + t("common.back"));
  back.addEventListener("click", function () {
    if (existing) renderProvidersList();
    else renderProviderChooser(templates);
  });
  lbl.appendChild(back);
  g.appendChild(lbl);
  if (selectedTemplate && selectedTemplate.Desc) g.appendChild(el("div", "note", selectedTemplate.Desc));

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
  [["auto", t("prov.autoDetect")], ["openai", "OpenAI Chat Completions"], ["responses", "OpenAI Responses API"], ["anthropic", "Anthropic Messages"], ["opencode", "OpenCode gateway"]].forEach(function (o) {
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
  var keyToggle = i18nEl("button", "secret-toggle", "prov.showKey");
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

  var modelI = field(t("prov.model"), "model", "text", t("prov.modelPlaceholder"), true);
  var clearI = null;
  if (existing) {
    var lab = el("label", "fw");
    lab.style.flexDirection = "row";
    lab.style.alignItems = "center";
    lab.style.gap = "8px";
    clearI = document.createElement("input");
    clearI.type = "checkbox";
    lab.appendChild(clearI);
    lab.appendChild(i18nEl("span", "", "prov.clearKey"));
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
    nameI.value = existing.Name;
    nameI.maxLength = 64;
    nameI.title = t("prov.renameHint");
    typeI.value = existing.Type || "openai";
    urlI.value = existing.BaseURL || "";
    modelI.value = existing.Model || "";
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

  if (!existing) {
    if (selectedTemplate) {
      nameI.value = selectedTemplate.Custom ? "" : (selectedTemplate.Name || "");
      typeI.value = selectedTemplate.Type || "auto";
      urlI.value = selectedTemplate.BaseURL || "";
    } else {
      typeI.value = "auto";
    }
    status.textContent = typeI.value === "auto"
      ? (t("prov.autoDetectHint"))
      : "";
  }

  form.addEventListener("submit", async function (e) {
    e.preventDefault();
    status.textContent = "…";
    try {
      if (existing) {
        var newName = nameI.value.trim();
        if (newName && newName !== existing.Name) {
          // Rename first so the field update below targets the new name.
          await renameProvider(existing.Name, newName);
          existing.Name = newName;
          lbl.firstChild.textContent = t("prov.save") + ": " + newName;
        }
        var body = { name: existing.Name, type: typeI.value, base_url: urlI.value.trim(), model: modelI.value.trim() };
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
        status.textContent = res.scan_error ? "scan: " + res.scan_error : t("common.ok") + " · " + (providerModelCount(res.models) + " " + t("prov.models"));
      } else {
        var res2 = await jpost("/api/providers", {
          name: nameI.value.trim(), type: typeI.value, base_url: urlI.value.trim(), api_key: keyI.value, model: modelI.value.trim(),
        });
        // A provider kept despite an unfinished scan reports why, so the
        // empty model list does not look like a silent failure.
        if (res2.warnings && res2.warnings.length) toast(uiWarningText(res2.warnings));
        else toast((res2.type ? res2.type + " · " : "") + t("prov.added") + " " + providerModelCount(res2.models) + " " + t("prov.models"));
        await loadModels();
        await renderProvidersList();
      }
    } catch (err) {
      status.textContent = existing ? err.message : t("prov.addFailed") + ": " + err.message;
    }
  });
  if (existing) renderProviderLogoPicker(form, existing);
  g.appendChild(form);
  panelContent.appendChild(g);
  if (existing) loadStoredKey();

}

/* ── Accounts (Codex) ── */

var codexPollTimer = null;
