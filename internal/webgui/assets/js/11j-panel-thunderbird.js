"use strict";

// Thunderbird AI Bridge pairing: status, one-time code, unpair. The code is
// shown only here and never stored by the page.
var thunderbirdPairTimer = null;

sections.thunderbird = async function () {
  clearTimeout(thunderbirdPairTimer);
  panelContent.innerHTML = "";
  panelContent.appendChild(i18nEl("div", "note", "tb.hint"));
  var info;
  try { info = await j("/api/integrations/thunderbird"); }
  catch (e) { panelContent.appendChild(el("div", "note", e.message)); return; }
  var bridge = info.bridge || {};

  var facts = el("div", "data-facts");
  [[bridge.paired ? "tb.paired" : "tb.notPaired"], [bridge.connected ? "tb.connected" : "tb.disconnected"]].forEach(function (item) {
    var fact = el("div", "data-fact");
    fact.appendChild(el("span", "", "Thunderbird"));
    fact.appendChild(el("strong", "", t(item[0])));
    facts.appendChild(fact);
  });
  panelContent.appendChild(facts);

  var detail = [];
  if (bridge.message && bridge.state !== "connected") detail.push(bridge.message);
  if (bridge.connected && info.status && info.status.extensionVersion) detail.push("Thunderbird AI Bridge " + info.status.extensionVersion);
  else if (bridge.extension_version) detail.push("Thunderbird AI Bridge " + bridge.extension_version);
  if (bridge.host_id) detail.push("host " + String(bridge.host_id).slice(0, 8) + " · " + (bridge.addr || ""));
  detail.forEach(function (line) { panelContent.appendChild(el("div", "note", line)); });

  var group = el("div", "group data-group");
  var code = el("div", "tb-code");
  var row = el("div", "data-action");
  var pairBtn = i18nEl("button", "btn", "tb.pair"); pairBtn.type = "button";
  pairBtn.addEventListener("click", async function () {
    pairBtn.disabled = true;
    try {
      var got = await j("/api/integrations/thunderbird/pair", { method: "POST" });
      if (!got.ok) throw new Error(got.error || "pairing failed");
      code.innerHTML = "";
      var value = el("strong", "", got.code);
      value.style.cssText = "font:600 22px var(--mono);letter-spacing:.08em;user-select:all";
      code.appendChild(value);
      var until = new Date(got.expires_at);
      code.appendChild(el("div", "note", t("tb.codeHelp").replace("{time}", until.toLocaleTimeString())));
      // Refresh until the extension finished pairing or the code expired.
      var poll = async function () {
        if (currentSection !== "thunderbird" || Date.now() > until.getTime()) return;
        try {
          var now = await j("/api/integrations/thunderbird");
          if (now.bridge && !now.bridge.pairing_active) { sections.thunderbird(); return; }
        } catch (_) {}
        thunderbirdPairTimer = setTimeout(poll, 2000);
      };
      thunderbirdPairTimer = setTimeout(poll, 2000);
    } catch (e) { toast(e.message); }
    finally { pairBtn.disabled = false; }
  });
  row.appendChild(el("div", "data-action-copy")).appendChild(el("strong", "", t("tb.pair")));
  row.appendChild(pairBtn);
  group.appendChild(row);
  group.appendChild(code);

  if (bridge.paired) {
    var unpairRow = el("div", "data-action");
    var unpairBtn = i18nEl("button", "btn", "tb.unpair"); unpairBtn.type = "button";
    unpairBtn.addEventListener("click", async function () {
      if (!await appConfirm(t("tb.unpairConfirm"), { danger: true, confirmLabel: t("tb.unpair") })) return;
      unpairBtn.disabled = true;
      try { await j("/api/integrations/thunderbird/unpair", { method: "POST" }); }
      catch (e) { toast(e.message); }
      sections.thunderbird();
    });
    unpairRow.appendChild(el("div", "data-action-copy")).appendChild(el("strong", "", t("tb.unpair")));
    unpairRow.appendChild(unpairBtn);
    group.appendChild(unpairRow);
  }
  panelContent.appendChild(group);
};
