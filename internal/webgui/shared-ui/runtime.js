(function (global) {
  "use strict";

  var VERSION = 2;
  var FILE_MUTATION_TOOLS = Object.freeze({
    edit_line: true,
    edit_lines: true,
    insert_after: true,
    delete_lines: true,
    write_file: true,
    make_dir: true,
    move: true,
    copy: true,
    trash: true,
    apply_patch: true,
    patch: true,
  });

  function requestJSON(url, options) {
    return fetch(url, options).then(async function (response) {
      if (!response.ok) {
        var message = (await response.text()).trim();
        throw new Error(message || "HTTP " + response.status);
      }
      return response.json();
    });
  }

  function decodeSSEFrame(frame) {
    var data = String(frame || "")
      .split(/\r?\n/)
      .filter(function (line) { return line.indexOf("data:") === 0; })
      .map(function (line) { return line.slice(5).replace(/^ /, ""); })
      .join("\n")
      .trim();
    if (!data) return null;
    try {
      return JSON.parse(data);
    } catch (error) {
      return null;
    }
  }

  async function readSSE(body, onEvent) {
    if (!body || typeof body.getReader !== "function") {
      throw new Error("SSE response body is unavailable");
    }
    var reader = body.getReader();
    var decoder = new TextDecoder();
    var buffer = "";
    var eventCount = 0;
    var terminalSeen = false;

    function emit(frame) {
      var event = decodeSSEFrame(frame);
      if (!event) return;
      eventCount++;
      if (event.type === "done" || event.type === "error") terminalSeen = true;
      onEvent(event);
    }

    for (;;) {
      var chunk = await reader.read();
      if (chunk.done) break;
      buffer += decoder.decode(chunk.value, { stream: true });
      var frames = buffer.split(/\r?\n\r?\n/);
      buffer = frames.pop() || "";
      frames.forEach(emit);
    }
    buffer += decoder.decode();
    if (buffer.trim()) emit(buffer);
    // /api/chat promises a terminal frame. A clean HTTP EOF without one used
    // to look like success to consumers, which made an unfinished answer (or
    // an ask_user call) simply disappear when the UI unlocked the composer.
    if (eventCount > 0 && !terminalSeen) {
      throw new Error("SSE stream ended before a terminal event");
    }
  }

  function createComposerDraftStore(options) {
    options = options || {};
    var input = options.input;
    var storageKey = String(options.storageKey || "composer-drafts-v1");
    var getScope = typeof options.getScope === "function" ? options.getScope : function () { return "new"; };
    var onRestore = typeof options.onRestore === "function" ? options.onRestore : function () {};
    var drafts = Object.create(null);
    var touched = false;
    var saveTimer = 0;
    var maxEntries = 40;
    var maxTotalChars = 300000;

    function scope() {
      return String(getScope() || "new");
    }
    function normalize(raw) {
      var result = Object.create(null);
      if (!raw || typeof raw !== "object" || Array.isArray(raw)) return result;
      Object.keys(raw).forEach(function (key) {
        var item = raw[key];
        if (typeof item === "string") item = { text: item, updated: 0 };
        if (!item || typeof item.text !== "string" || !item.text) return;
        result[key] = { text: item.text.slice(0, 100000), updated: Number(item.updated) || 0 };
      });
      return compact(result);
    }
    function compact(source) {
      var entries = Object.keys(source).map(function (key) {
        return { key: key, text: String(source[key].text || ""), updated: Number(source[key].updated) || 0 };
      }).filter(function (item) { return item.text; });
      entries.sort(function (a, b) { return b.updated - a.updated; });
      var result = Object.create(null);
      var chars = 0;
      entries.slice(0, maxEntries).forEach(function (item) {
        if (chars >= maxTotalChars) return;
        var text = item.text.slice(0, Math.min(100000, maxTotalChars - chars));
        if (!text) return;
        result[item.key] = { text: text, updated: item.updated };
        chars += text.length;
      });
      return result;
    }
    function merge(base, incoming) {
      Object.keys(incoming).forEach(function (key) {
        if (!base[key] || incoming[key].updated >= base[key].updated) base[key] = incoming[key];
      });
      return compact(base);
    }
    function patchBody() {
      var patch = {};
      patch[storageKey] = drafts;
      return JSON.stringify(patch);
    }
    function saveLocal() {
      try { localStorage.setItem(storageKey, JSON.stringify(drafts)); } catch (error) {}
    }
    function persistNow() {
      clearTimeout(saveTimer);
      saveTimer = 0;
      saveLocal();
      return requestJSON("/api/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: patchBody(),
        keepalive: true,
      }).catch(function () {});
    }
    function schedulePersist() {
      saveLocal();
      clearTimeout(saveTimer);
      saveTimer = setTimeout(persistNow, 250);
    }
    function capture() {
      if (!input) return;
      touched = true;
      var key = scope();
      var text = String(input.value || "");
      if (text) drafts[key] = { text: text, updated: Date.now() };
      else delete drafts[key];
      drafts = compact(drafts);
      schedulePersist();
    }
    function restore(key) {
      if (!input) return "";
      key = String(key || scope());
      var text = drafts[key] ? drafts[key].text : "";
      input.value = text;
      onRestore(text);
      return text;
    }
    function clear(key, expectedText) {
      key = String(key || scope());
      var current = drafts[key];
      if (!current) return false;
      if (expectedText !== undefined && current.text !== String(expectedText)) return false;
      delete drafts[key];
      schedulePersist();
      return true;
    }
    async function load() {
      var local = Object.create(null);
      try { local = normalize(JSON.parse(localStorage.getItem(storageKey) || "{}")); } catch (error) {}
      drafts = merge(drafts, local);
      try {
        var response = await requestJSON("/api/settings", { cache: "no-store" });
        var server = normalize(response && response.settings ? response.settings[storageKey] : null);
        drafts = merge(server, drafts);
      } catch (error) {}
      saveLocal();
      if (!touched) restore();
      return drafts;
    }
    function flushForPageHide() {
      if (input) {
        var key = scope();
        var text = String(input.value || "");
        if (text) drafts[key] = { text: text, updated: Date.now() };
        else delete drafts[key];
        drafts = compact(drafts);
      }
      saveLocal();
      if (navigator.sendBeacon) {
        try {
          navigator.sendBeacon("/api/settings", new Blob([patchBody()], { type: "application/json" }));
          return;
        } catch (error) {}
      }
      persistNow();
    }
    if (input && typeof input.addEventListener === "function") input.addEventListener("input", capture);
    if (global && typeof global.addEventListener === "function") global.addEventListener("pagehide", flushForPageHide);
    return Object.freeze({ load: load, capture: capture, restore: restore, clear: clear, flush: persistNow, scope: scope });
  }

  function mutationKind(name, output, isError) {
    name = String(name || "").trim();
    if (isError || !FILE_MUTATION_TOOLS[name]) return "";
    var text = String(output || "");
    if (name === "write_file") return /^Created\b/i.test(text) ? "created" : "modified";
    if (name === "trash") return "deleted";
    if (name === "make_dir") return /^Created folder\b/i.test(text) ? "folder-created" : "";
    if (name === "move") return "moved";
    if (name === "copy") return "copied";
    return "modified";
  }

  function normalizeFileChanges(changes) {
    if (!Array.isArray(changes)) return [];
    var seen = Object.create(null);
    return changes.reduce(function (result, change) {
      if (!change || !change.path) return result;
      var kind = ["created", "modified", "deleted"].indexOf(change.kind) >= 0 ? change.kind : "modified";
      var path = String(change.path);
      var key = kind + "\u0000" + path;
      if (seen[key]) return result;
      seen[key] = true;
      result.push({ kind: kind, path: path });
      return result;
    }, []);
  }

  function createUserInstructionsEditor(options) {
    options = options || {};
    var polish = options.lang === "pl";
    var copy = polish ? {
      title: "Instrukcje użytkownika",
      description: "Zapisz własny sposób pracy. Instrukcje są dołączane do każdej nowej wiadomości, ale nie zastępują nadrzędnych reguł modelu ani dostawcy.",
      enabled: "Używaj instrukcji",
      enabledHint: "Wyłączenie usuwa instrukcje z następnej wiadomości i nie zużywa kontekstu.",
      preset: "Aktywny preset",
      add: "+ Nowy preset",
      name: "Nazwa presetu",
      namePlaceholder: "Np. Praca biurowa",
      content: "Instrukcje dla agenta",
      contentPlaceholder: "Np. odpowiadaj po polsku, zachowuj formatowanie dokumentów i przed usunięciem plików poproś o potwierdzenie.",
      save: "Zapisz preset",
      remove: "Usuń",
      confirmRemove: "Potwierdź usunięcie",
      noPresets: "Brak presetów",
      saved: "Zapisano",
      saving: "Zapisywanie…",
      applied: "Aktywne — preset zostanie dołączony do następnej wiadomości.",
      notApplied: "Nieaktywne — wybierz niepusty preset i włącz instrukcje.",
      tokenUnit: "szac. tokenów",
      cost: "Bez twardego limitu. Dłuższe instrukcje zajmują więcej kontekstu i mogą wydłużyć odpowiedź. Nie powodują dodatkowego wywołania modelu.",
      longCost: "To długi preset — będzie dołączany do każdej wiadomości, gdy jest włączony.",
      error: "Nie udało się zapisać: ",
    } : {
      title: "User instructions",
      description: "Save a preferred way of working. Instructions are attached to every new message, but cannot replace higher-priority model or provider rules.",
      enabled: "Use instructions",
      enabledHint: "Turning this off removes the instructions from the next message and costs no context.",
      preset: "Active preset",
      add: "+ New preset",
      name: "Preset name",
      namePlaceholder: "Example: Office work",
      content: "Instructions for the agent",
      contentPlaceholder: "Example: answer in English, preserve document formatting, and ask before deleting files.",
      save: "Save preset",
      remove: "Delete",
      confirmRemove: "Confirm delete",
      noPresets: "No presets",
      saved: "Saved",
      saving: "Saving…",
      applied: "Active — this preset will be attached to the next message.",
      notApplied: "Inactive — select a non-empty preset and enable instructions.",
      tokenUnit: "est. tokens",
      cost: "No hard limit. Longer instructions use more context and may make responses take longer. They do not trigger an extra model call.",
      longCost: "This is a long preset — it will be attached to every message while enabled.",
      error: "Could not save: ",
    };

    var root = document.createElement("section");
    root.className = "user-instructions-editor " + (options.className || "");
    root.innerHTML = '<div class="uie-loading">' + (polish ? "Ładowanie…" : "Loading…") + "</div>";

    function makeID() {
      return "preset-" + Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 8);
    }
    function estimate(content) {
      return Math.ceil(Array.from(String(content || "")).length / 3);
    }
    function button(label, className) {
      var node = document.createElement("button");
      node.type = "button";
      node.className = className || "";
      node.textContent = label;
      return node;
    }

    requestJSON("/api/user-instructions").then(function (state) {
      state.presets = Array.isArray(state.presets) ? state.presets : [];
      var head = document.createElement("div");
      head.className = "uie-head";
      head.innerHTML = "<div><h3></h3><p></p></div>";
      head.querySelector("h3").textContent = copy.title;
      head.querySelector("p").textContent = copy.description;

      var switchLabel = document.createElement("label");
      switchLabel.className = "uie-switch";
      var enabled = document.createElement("input");
      enabled.type = "checkbox";
      enabled.checked = !!state.enabled;
      var switchCopy = document.createElement("span");
      switchCopy.innerHTML = "<strong></strong><small></small>";
      switchCopy.querySelector("strong").textContent = copy.enabled;
      switchCopy.querySelector("small").textContent = copy.enabledHint;
      switchLabel.append(enabled, switchCopy);

      var effective = document.createElement("div");
      function renderEffective() {
        effective.className = "uie-effective " + (state.applied ? "is-applied" : "is-inactive");
        effective.textContent = state.applied ? copy.applied : copy.notApplied;
      }

      var toolbar = document.createElement("div");
      toolbar.className = "uie-toolbar";
      var selectWrap = document.createElement("label");
      selectWrap.innerHTML = "<span></span>";
      selectWrap.querySelector("span").textContent = copy.preset;
      var select = document.createElement("select");
      selectWrap.append(select);
      var add = button(copy.add, "uie-secondary");
      toolbar.append(selectWrap, add);

      var form = document.createElement("div");
      form.className = "uie-form";
      var nameLabel = document.createElement("label");
      nameLabel.innerHTML = "<span></span>";
      nameLabel.querySelector("span").textContent = copy.name;
      var name = document.createElement("input");
      name.type = "text";
      name.placeholder = copy.namePlaceholder;
      nameLabel.append(name);
      var contentLabel = document.createElement("label");
      contentLabel.innerHTML = "<span></span>";
      contentLabel.querySelector("span").textContent = copy.content;
      var content = document.createElement("textarea");
      content.rows = 9;
      content.placeholder = copy.contentPlaceholder;
      contentLabel.append(content);
      var meta = document.createElement("div");
      meta.className = "uie-meta";
      var count = document.createElement("span");
      var cost = document.createElement("p");
      meta.append(count, cost);
      var actions = document.createElement("div");
      actions.className = "uie-actions";
      var remove = button(copy.remove, "uie-danger");
      var status = document.createElement("span");
      status.className = "uie-status";
      var save = button(copy.save, "uie-primary");
      actions.append(remove, status, save);
      form.append(nameLabel, contentLabel, meta, actions);

      function activePreset() {
        return state.presets.find(function (preset) { return preset.id === state.active_id; }) || null;
      }
      function syncDraft() {
        var active = activePreset();
        if (!active) return;
        active.name = name.value;
        active.content = content.value;
      }
      function updateMeta() {
        var tokens = estimate(content.value);
        count.textContent = tokens.toLocaleString(polish ? "pl-PL" : "en-US") + " " + copy.tokenUnit;
        cost.textContent = tokens >= 2000 ? copy.longCost + " " + copy.cost : copy.cost;
        meta.classList.toggle("is-long", tokens >= 2000);
      }
      function renderSelect() {
        select.replaceChildren();
        if (!state.presets.length) {
          var empty = document.createElement("option");
          empty.value = "";
          empty.textContent = copy.noPresets;
          select.append(empty);
          state.active_id = "";
        } else {
          state.presets.forEach(function (preset) {
            var option = document.createElement("option");
            option.value = preset.id;
            option.textContent = preset.name || copy.namePlaceholder;
            select.append(option);
          });
          if (!activePreset()) state.active_id = state.presets[0].id;
          select.value = state.active_id;
        }
        select.disabled = !state.presets.length;
        enabled.disabled = !state.presets.length;
        form.hidden = !state.presets.length;
        var active = activePreset();
        name.value = active ? active.name : "";
        content.value = active ? active.content : "";
        remove.textContent = copy.remove;
        remove.dataset.confirm = "";
        updateMeta();
      }
      async function persist(message) {
        syncDraft();
        status.textContent = copy.saving;
        status.className = "uie-status is-saving";
        try {
          var saved = await requestJSON("/api/user-instructions", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(state),
          });
          state = saved;
          state.presets = Array.isArray(state.presets) ? state.presets : [];
          renderEffective();
          status.textContent = message || copy.saved;
          status.className = "uie-status is-saved";
          if (typeof options.onSaved === "function") options.onSaved(state);
          return true;
        } catch (error) {
          status.textContent = copy.error + error.message;
          status.className = "uie-status is-error";
          return false;
        }
      }

      enabled.addEventListener("change", async function () {
        var previous = state.enabled;
        state.enabled = enabled.checked;
        if (!(await persist())) {
          state.enabled = previous;
          enabled.checked = previous;
          renderEffective();
        }
      });
      select.addEventListener("change", async function () {
        syncDraft();
        state.active_id = select.value;
        renderSelect();
        await persist();
      });
      add.addEventListener("click", async function () {
        syncDraft();
        var preset = { id: makeID(), name: polish ? "Nowy preset" : "New preset", content: "" };
        state.presets.push(preset);
        state.active_id = preset.id;
        renderSelect();
        await persist();
        name.select();
      });
      remove.addEventListener("click", async function () {
        if (!activePreset()) return;
        if (remove.dataset.confirm !== "yes") {
          remove.dataset.confirm = "yes";
          remove.textContent = copy.confirmRemove;
          return;
        }
        state.presets = state.presets.filter(function (preset) { return preset.id !== state.active_id; });
        state.active_id = state.presets.length ? state.presets[0].id : "";
        if (!state.active_id) state.enabled = false;
        enabled.checked = state.enabled;
        renderSelect();
        await persist();
      });
      name.addEventListener("input", function () {
        var active = activePreset();
        if (active) active.name = name.value;
        var selected = select.options[select.selectedIndex];
        if (selected) selected.textContent = name.value || copy.namePlaceholder;
      });
      content.addEventListener("input", updateMeta);
      save.addEventListener("click", function () { persist(); });

      root.replaceChildren(head, switchLabel, effective, toolbar, form);
      renderEffective();
      renderSelect();
    }).catch(function (error) {
      root.innerHTML = '<div class="uie-error"></div>';
      root.firstChild.textContent = error.message;
    });
    return root;
  }

  global.SuperCliUI = Object.freeze({
    version: VERSION,
    fileMutationTools: FILE_MUTATION_TOOLS,
    requestJSON: requestJSON,
    decodeSSEFrame: decodeSSEFrame,
    readSSE: readSSE,
    createComposerDraftStore: createComposerDraftStore,
    mutationKind: mutationKind,
    normalizeFileChanges: normalizeFileChanges,
    createUserInstructionsEditor: createUserInstructionsEditor,
  });
})(window);
