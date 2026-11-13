"use strict";

/* Host approval card. A risky tool operation (thunderbird_mail writes, Outlook
   trash/purge, trash outside the workspace) waits for the user's answer here.
   The card content comes from the trusted server-side approval request, never
   from model text; option values are the server's own labels. */

function approvalText(key, fallback) {
  var value = t(key);
  return value === key ? fallback : value;
}

function approvalRow(list, labelKey, value) {
  if (value === undefined || value === null || value === "") return;
  var row = el("div", "approval-row");
  row.appendChild(el("dt", "", t(labelKey)));
  var dd = el("dd", "");
  if (value instanceof Node) dd.appendChild(value); else dd.textContent = String(value);
  row.appendChild(dd);
  list.appendChild(row);
}

function showApprovalCard(q) {
  var a = q.approval || {};
  closeQuestionOverlay(q.id);
  var overlay = el("div", "question-inline approval-inline");
  var panel = el("section", "question-panel approval-card" + (a.reversible ? "" : " irreversible"));
  var titleId = "approval-title-" + q.id, descId = "approval-desc-" + q.id;
  panel.setAttribute("role", "alertdialog");
  panel.setAttribute("aria-labelledby", titleId);
  panel.setAttribute("aria-describedby", descId);
  panel.tabIndex = -1;

  var head = el("div", "question-head");
  head.appendChild(i18nEl("span", "question-kicker", "approval.title"));
  var title = el("h2", "question-text approval-op");
  title.id = titleId;
  function refreshTitle() {
    title.textContent = approvalText("approval.op." + a.operation, a.summary || q.question || "");
  }
  registerLanguageRefresh(title, refreshTitle);
  refreshTitle();
  head.appendChild(title);
  panel.appendChild(head);

  var summary = el("p", "approval-summary", a.summary || "");
  summary.id = descId;
  panel.appendChild(summary);

  var badges = el("div", "approval-badges");
  badges.appendChild(i18nEl("span", "approval-badge risk-" + (a.risk || "unknown"), "approval.risk." + (a.risk || "write_reversible")));
  badges.appendChild(i18nEl("span", "approval-badge " + (a.reversible ? "reversible" : "irreversible"),
    a.reversible ? "approval.reversible.yes" : "approval.reversible.no"));
  panel.appendChild(badges);

  var list = el("dl", "approval-details");
  approvalRow(list, "approval.field.tool", (a.tool || "") + (a.operation ? " · " + a.operation : ""));
  var account = a.account_name && a.account && a.account_name.toLowerCase() !== a.account.toLowerCase()
    ? a.account_name + " (" + a.account + ")" : (a.account_name || a.account);
  approvalRow(list, "approval.field.account", account);
  approvalRow(list, "approval.field.folder", a.folder);
  approvalRow(list, "approval.field.destination", a.destination);
  approvalRow(list, "approval.field.target", a.target);
  approvalRow(list, "approval.field.count", typeof a.count === "number" && a.count >= 0 ? a.count : "");
  if (a.samples && a.samples.length) {
    var samples = el("ul", "approval-samples");
    a.samples.forEach(function (s) {
      var item = el("li", "");
      if (s.date) item.appendChild(el("span", "approval-sample-date", s.date));
      if (s.from) item.appendChild(el("span", "approval-sample-from", s.from));
      item.appendChild(el("span", "approval-sample-subject", s.subject || "—"));
      samples.appendChild(item);
    });
    approvalRow(list, "approval.field.samples", samples);
  }
  panel.appendChild(list);

  var error = el("div", "question-error");
  panel.appendChild(error);
  var actions = el("div", "question-actions approval-actions");
  var deny = i18nEl("button", "btn approval-deny", "approval.deny"); deny.type = "button";
  var approve = i18nEl("button", "btn primary approval-approve", "approval.approve"); approve.type = "button";
  actions.appendChild(deny);
  var similar = null;
  if (a.similar_label) {
    similar = el("button", "btn approval-similar");
    similar.type = "button";
    function refreshSimilar() {
      similar.textContent = t("approval.similar").replace("{minutes}", String(a.grant_minutes || 30));
    }
    registerLanguageRefresh(similar, refreshSimilar);
    refreshSimilar();
    actions.appendChild(similar);
  }
  actions.appendChild(approve);
  panel.appendChild(actions);
  overlay.appendChild(panel);
  appendStream(overlay);
  questionOverlays[q.id] = overlay; activeQuestionOverlay = overlay; smartScroll(true);

  var buttons = [deny, approve].concat(similar ? [similar] : []);
  function answer(kind) {
    buttons.forEach(function (b) { b.disabled = true; });
    error.textContent = "";
    var body = { id: q.id, selected: [], custom: "", cancelled: false };
    if (kind === "approve") body.selected = [a.approve_label];
    else if (kind === "similar") body.selected = [a.similar_label];
    else body.cancelled = true;
    jpost("/api/question/answer", body).then(function () {
      delete questionOverlays[q.id];
      var remaining = Object.keys(questionOverlays);
      activeQuestionOverlay = remaining.length ? questionOverlays[remaining[remaining.length - 1]] : null;
      actions.remove();
      panel.classList.add("answered", kind === "deny" ? "denied" : "approved");
      panel.removeAttribute("role");
      panel.appendChild(i18nEl("div", "approval-receipt",
        kind === "deny" ? "approval.receipt.denied" : kind === "similar" ? "approval.receipt.similar" : "approval.receipt.approved"));
      setRunState("running", t(activeQuestionOverlay ? "question.waiting" : "composer.working"));
    }).catch(function (e) {
      error.textContent = e.message;
      buttons.forEach(function (b) { b.disabled = false; });
    });
  }
  deny.addEventListener("click", function () { answer("deny"); });
  approve.addEventListener("click", function () { answer("approve"); });
  if (similar) similar.addEventListener("click", function () { answer("similar"); });
  panel.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && !deny.disabled) { e.preventDefault(); answer("deny"); }
  });
  // Focus the safe choice first; Tab reaches approve, Enter/Space activates.
  setTimeout(function () { deny.focus(); }, 0);
}
