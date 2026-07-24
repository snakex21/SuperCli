"use strict";

sections.workflow = async function () {
  panelContent.innerHTML = "";
  panelContent.appendChild(SuperCliUI.createUserInstructionsEditor({ lang: ui.lang, className: "group" }));
  var queue = el("div", "group"); queue.appendChild(el("div", "g-label", t("workflow.queue")));
  queue.appendChild(el("div", "workflow-lead", String(promptQueue.length).padStart(2,"0") + " · " + t("composer.queued")));
  queue.appendChild(el("div", "note", ui.lang === "pl" ? "Kolejka przeżywa restart aplikacji i czeka na wznowienie." : "The queue survives app restarts and waits for you to resume it.")); panelContent.appendChild(queue);

  var profile=el("div","group");profile.appendChild(el("div","g-label",t("workflow.profile")));
  try{var p=await j("/api/prompt/profile");profile.appendChild(el("div","file-path",p.path));var openProfiles=el("button","btn",t("common.openFolder"));openProfiles.addEventListener("click",function(){openWorkspaceFolder(p.path.replace(/[\\\/][^\\\/]+$/, ""));});profile.appendChild(openProfiles);var ta=el("textarea","editor-area profile-editor");ta.value=p.content||"";ta.placeholder=ui.lang==="pl"?"Np. preferuj krótkie wywołania narzędzi.":"Example: prefer short tool calls.";profile.appendChild(ta);var save=el("button","btn primary",t("common.save"));save.addEventListener("click",async function(){try{await jpost("/api/prompt/profile",{content:ta.value});toast(t("common.save")+" ✓");}catch(e){toast(e.message);}});profile.appendChild(save);profile.appendChild(el("div","note",t("workflow.profileHint")));}catch(e){profile.appendChild(el("div","note",e.message));}
  panelContent.appendChild(profile);

  var scratch=el("div","group");scratch.appendChild(el("div","g-label",t("workflow.scratch")));try{var sc=await j("/api/scratchpad");scratch.appendChild(el("div","file-path",sc.path));var openScratch=el("button","btn",t("common.openFolder"));openScratch.addEventListener("click",function(){openWorkspaceFolder(sc.path);});scratch.appendChild(openScratch);scratch.appendChild(el("div","note",sc.notes.length?sc.notes.join(" · "):(ui.lang==="pl"?"Notatnik jest pusty.":"Scratchpad is empty.")));}catch(e){scratch.appendChild(el("div","note",e.message));}panelContent.appendChild(scratch);

  var hard=el("div","group");hard.appendChild(el("div","g-label",t("workflow.hard")));var run=el("button","btn primary",t("workflow.runHard")),output=el("pre","pre-block","");run.addEventListener("click",async function(){run.disabled=true;run.textContent=t("common.loading");output.textContent="";try{var report=await jpost("/api/test/hard",{});output.textContent=(report.ok?"PASS":"FAIL")+" · "+fmtDuration(report.duration_ms)+"\n"+(report.checks||[]).map(function(c){return(c.ok?"✓ ":"× ")+c.name+" · "+fmtDuration(c.duration_ms)+(c.ok?"":"\n"+c.output);}).join("\n");}catch(e){output.textContent=e.message;}finally{run.disabled=false;run.textContent=t("workflow.runHard");}});hard.appendChild(run);hard.appendChild(output);panelContent.appendChild(hard);
};

async function openWorkspaceFolder(path) {
  try { await jpost("/api/folder/open", { path: path }); }
  catch (e) { toast(e.message); }
}

sections.settings = async function () {
  var got;
  try { got = await j("/api/config"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  panelContent.appendChild(el("div", "note", t("set.hint")));
  var wrap = el("div", "group");
  wrap.style.marginTop = "10px";
  (got.knobs || []).forEach(function (k) { wrap.appendChild(knobRow(k)); });
  panelContent.appendChild(wrap);
  var reset = el("button", "btn danger", t("set.resetAll"));
  reset.addEventListener("click", async function () {
    await jpost("/api/config", { reset_all: true }).catch(function (e) { toast(e.message); });
    sections.settings();
  });
  panelContent.appendChild(reset);
  panelContent.appendChild(el("div", "note", " "));
  panelContent.appendChild(el("div", "note", t("set.resetAllHint")));
};

function knobRow(k) {
  var row = el("div", "knob-row");
  var copy = settingCopy(k);
  var name = el("div", "k-name", copy[0]);
  name.title = "config.toml · " + k.key;
  var d = el("small", "", copy[1]);
  name.appendChild(d);
  row.appendChild(name);
  if (k.next_session) row.appendChild(el("span", "k-next", "(" + t("set.nextSession") + ")"));

  async function post(value) {
	if (k.key === "allow_all" && value === "on" && !await appConfirm(t("sandbox.allowConfirm"), {
      title: t("sandbox.allowTitle"), danger: true, confirmLabel: t("dialog.confirm"),
    })) return;
    jpost("/api/config", { key: k.key, value: value })
      .then(function () { sections.settings(); })
      .catch(function (e) { toast(e.message); });
  }

  if (k.kind === "tri" || k.kind === "tri_auto" || k.kind === "nav") {
    var states = k.kind === "tri" ? ["default", "on", "off"] : ["auto", "on", "off"];
    var seg = el("span", "seg");
    states.forEach(function (st) {
      var label = t("set.state." + st);
      if (k.key === "orchestrator") {
        label = st === "default" ? t("stats.orchAuto") : (st === "on" ? t("stats.orchOn") : t("stats.orchOff"));
      } else if (st === "default" && k.default) {
        label += " (" + t("set.state." + k.default) + ")";
      }
      var b = el("button", "", label);
      b.type = "button";
      if (st === (k.state || "default") || (st === "auto" && (k.state || "default") === "default")) b.classList.add("on");
      b.addEventListener("click", function () { post(st === "auto" ? "default" : st); });
      seg.appendChild(b);
    });
    row.appendChild(el("span", "k-val", k.source === "default" ? k.value : ""));
    row.appendChild(seg);
  } else if (k.kind === "int" || k.kind === "text") {
    var input = el("input", "k-edit");
    input.value = k.raw || "";
    input.placeholder = k.source === "default" ? k.value : "";
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); post(input.value.trim()); }
      if (e.key === "Escape") { input.value = k.raw || ""; input.blur(); }
    });
    input.addEventListener("blur", function () {
      if (input.value.trim() !== (k.raw || "")) post(input.value.trim());
    });
    row.appendChild(input);
  } else {
    row.appendChild(el("span", "k-val", k.value));
  }
  var src = el("span", "k-src" + (k.source !== "default" ? " manual" : ""), t("set.source." + k.source));
  row.appendChild(src);
  return row;
}

/* ── Appearance ── */

sections.appearance = function () {
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", t("app.theme")));
  var seg = el("span", "seg");
  [["dark", t("app.dark")], ["midnight", t("app.midnight")], ["light", t("app.light")]].forEach(function (pair) {
    var b = el("button", ui.theme === pair[0] ? "on" : "", pair[1]);
    b.type = "button";
    b.addEventListener("click", function () {
      ui.theme = pair[0]; applyUI(); saveUI(); sections.appearance();
    });
    seg.appendChild(b);
  });
  g.appendChild(seg);
  panelContent.appendChild(g);

  function selectRow(label, key, options) {
    var gg = el("div", "group");
    var tr = el("label", "toggle-row");
    tr.appendChild(el("span", "", label));
    var sel = el("select", "field-select");
    options.forEach(function (o) {
      var opt = el("option", "", o[1]);
      opt.value = o[0];
      sel.appendChild(opt);
    });
    sel.value = ui[key];
    sel.addEventListener("change", function () {
      ui[key] = sel.value;
      applyUI();
      saveUI();
      if (key === "lang") {
        $("#panel-title").textContent = t("panel." + currentSection);
        renderStats();
        sections.appearance();
      }
    });
    tr.appendChild(sel);
    gg.appendChild(tr);
    return gg;
  }
  panelContent.appendChild(selectRow(t("app.lang"), "lang", [["en", "English"], ["pl", "Polski"]]));
  panelContent.appendChild(selectRow(t("app.uiFont"), "uiFont",
    [["system", "System"], ["segoe", "Segoe UI"], ["aptos", "Aptos"], ["arial", "Arial"], ["tahoma", "Tahoma"], ["verdana", "Verdana"], ["trebuchet", "Trebuchet MS"], ["georgia", "Georgia"]]));
  panelContent.appendChild(selectRow(t("app.codeFont"), "codeFont",
    [["system", "System monospace"], ["cascadia", "Cascadia Mono"], ["jetbrains", "JetBrains Mono"], ["fira", "Fira Code"], ["consolas", "Consolas"], ["courier", "Courier New"], ["lucida", "Lucida Console"]]));
  panelContent.appendChild(selectRow(t("app.scale"), "uiScale", [["auto", t("app.auto")], ["compact", "90%"], ["normal", "100%"], ["large", "110%"], ["xlarge", "125%"], ["huge", "140%"]]));

  var gn = el("div", "group");
  gn.appendChild(el("div", "g-label", t("app.notify")));
  [["notifySound", t("app.sound")], ["notifyDesktop", t("app.desktop")], ["appBadge", t("app.badge")]].forEach(function (pair) {
    var tr = el("label", "toggle-row");
    tr.appendChild(el("span", "", pair[1]));
    var cb = document.createElement("input");
    cb.type = "checkbox";
    cb.checked = !!ui[pair[0]];
    cb.addEventListener("change", function () {
      ui[pair[0]] = cb.checked;
      saveUI();
      if (pair[0] === "appBadge") updateAppBadge();
      if (pair[0] === "notifyDesktop" && cb.checked && window.Notification && Notification.permission === "default") {
        Notification.requestPermission();
      }
    });
    tr.appendChild(cb);
    gn.appendChild(tr);
  });
  panelContent.appendChild(gn);
};

function sessionRuntimePreferenceGroup() {
  var gs = el("div", "group");
  gs.appendChild(el("div", "g-label", t("session.runtime")));
  var sr = el("label", "toggle-row");
  var sc = el("span");
  sc.appendChild(el("span", "", t("session.runtime")));
  sc.appendChild(el("span", "toggle-hint", t("session.runtimeHint")));
  sr.appendChild(sc);
  var sessionRuntime = document.createElement("input");
  sessionRuntime.type = "checkbox";
  sessionRuntime.checked = !!ui.rememberSessionRuntime;
  sessionRuntime.addEventListener("change", function () {
    ui.rememberSessionRuntime = sessionRuntime.checked;
    saveUI();
  });
  sr.appendChild(sessionRuntime);
  gs.appendChild(sr);
  return gs;
}

/* ── Models (visibility management) ── */

var modelProviderTab = "";
var modelSettingsSearch = "";
