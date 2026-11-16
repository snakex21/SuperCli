"use strict";

/* Host approval card. A risky tool operation (thunderbird_mail writes, Outlook
   trash/purge, trash outside the workspace) waits for the user's answer here.
   The card content comes from the trusted server-side approval request, never
   from model text; option values are the server's own labels. */

function approvalText(key, fallback) {
  var value = t(key);
  return value === key ? fallback : value;
}

/* A trusted summary_key localizes the summary in the current UI language; its
   {name} placeholders are filled in one pass from summary_args, so argument
   text is never re-expanded. summary_detail is untranslated data (exact
   arguments) shown below it. Without a catalog entry the server text, which
   already ends with the detail, stays. */
function approvalSummary(a, q) {
  var key = a.summary_key;
  if (key) {
    var text = t(key);
    if (text !== key) {
      var args = a.summary_args || {};
      text = text.replace(/\{([a-z_]+)\}/g, function (match, name) {
        return Object.prototype.hasOwnProperty.call(args, name) ? String(args[name]) : match;
      });
      return { text: text, detail: a.summary_detail || "" };
    }
  }
  return { text: a.summary || q.question || "", detail: "" };
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
  var summary = el("p", "approval-summary", "");
  summary.id = descId;
  var detail = el("pre", "approval-detail", "");
  function refreshTitle() {
    var s = approvalSummary(a, q);
    var heading = approvalText("approval.op." + a.operation, "");
    // Without an operation title the summary itself is the heading; it is
    // not repeated below it.
    title.textContent = heading || s.text;
    summary.textContent = heading ? s.text : "";
    summary.hidden = !heading;
    detail.textContent = s.detail;
    detail.hidden = !s.detail;
  }
  registerLanguageRefresh(title, refreshTitle);
  refreshTitle();
  head.appendChild(title);
  panel.appendChild(head);
  panel.appendChild(summary);
  panel.appendChild(detail);

  // Warnings come from trusted read-only checks (for example message ids that
  // are not in the stated folder); they are shown before the details.
  if (a.warnings && a.warnings.length) {
    var warnings = el("div", "approval-warnings");
    warnings.setAttribute("role", "note");
    warnings.appendChild(i18nEl("strong", "approval-warnings-label", "approval.field.warnings"));
    var warnList = el("ul", "");
    a.warnings.forEach(function (w) { warnList.appendChild(el("li", "", String(w))); });
    warnings.appendChild(warnList);
    panel.appendChild(warnings);
  }

  var badges = el("div", "approval-badges");
  badges.appendChild(i18nEl("span", "approval-badge risk-" + (a.risk || "unknown"), "approval.risk." + (a.risk || "write_reversible")));
  badges.appendChild(i18nEl("span", "approval-badge " + (a.reversible ? "reversible" : "irreversible"),
    a.reversible ? "approval.reversible.yes" : "approval.reversible.no"));
  panel.appendChild(badges);

  var list = el("dl", "approval-details");
  // Human operation name; the internal tool/operation ids stay in title=.
  var rawOp = (a.tool || "") + (a.operation ? " · " + a.operation : "");
  var opLabel = el("span", "approval-op-name", "");
  opLabel.title = rawOp;
  function refreshOpLabel() {
    var human = a.tool ? approvalText("tool." + a.tool, "") : "";
    opLabel.textContent = human ? human + " (" + a.tool + ")" : rawOp;
  }
  registerLanguageRefresh(opLabel, refreshOpLabel);
  refreshOpLabel();
  approvalRow(list, "approval.field.tool", rawOp ? opLabel : "");
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
  // Keyboard shortcuts match the TUI: N deny, Y approve, A approve similar.
  function keyed(button, key) {
    var kbd = el("kbd", "approval-key", key);
    kbd.setAttribute("aria-hidden", "true");
    button.appendChild(kbd);
    button.setAttribute("aria-keyshortcuts", key);
    return button;
  }
  var deny = keyed(i18nEl("button", "btn approval-deny", "approval.deny"), "N"); deny.type = "button";
  // An irreversible operation never gets the inviting accent fill.
  var approve = keyed(i18nEl("button", "btn approval-approve " + (a.reversible ? "primary" : "danger-outline"), "approval.approve"), "Y"); approve.type = "button";
  actions.appendChild(deny);
  var similar = null;
  if (a.similar_label) {
    similar = el("button", "btn approval-similar");
    similar.type = "button";
    var similarText = document.createTextNode("");
    similar.appendChild(similarText);
    keyed(similar, "A");
    function refreshSimilar() {
      similarText.nodeValue = t("approval.similar").replace("{minutes}", String(a.grant_minutes || 30));
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
  if (typeof setToolApprovalState === "function") setToolApprovalState(a.tool, "pending");
  setRunState("pending", t("question.waiting"));

  var buttons = [deny, approve].concat(similar ? [similar] : []);
  var followAfterAnswer = false;
  // Disabling the focused button drops focus to <body>; keep it on the card
  // while the answer posts, then hand it on (see restoreFocus).
  function answer(kind) {
    var hadFocus = panel.contains(document.activeElement);
    if (hadFocus) panel.focus({ preventScroll: true });
    buttons.forEach(function (b) { b.disabled = true; });
    error.textContent = "";
    var body = { id: q.id, selected: [], custom: "", cancelled: false };
    if (kind === "approve") body.selected = [a.approve_label];
    else if (kind === "similar") body.selected = [a.similar_label];
    else body.cancelled = true;
    // Mark the tool row before posting: the tool result can arrive over SSE
    // before this request resolves, and it must already read as denied.
    var markRow = typeof setToolApprovalState === "function" ? setToolApprovalState : function () {};
    var row = markRow(a.tool, kind === "deny" ? "denied" : "");
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
      if (hadFocus || document.activeElement === document.body) restoreFocus();
      if (followAfterAnswer) smartScroll(true);
    }).catch(function (e) {
      error.textContent = e.message;
      buttons.forEach(function (b) { b.disabled = false; });
      if (hadFocus) deny.focus();
      if (row && row.classList.contains("running")) markRow(a.tool, "pending");
    });
  }
  // After an answer the keyboard user continues where they were: the next
  // pending card's safe choice, else the composer. A touch device keeps focus
  // on the answered card so the on-screen keyboard does not pop up.
  function restoreFocus() {
    var next = activeQuestionOverlay && activeQuestionOverlay.isConnected &&
      activeQuestionOverlay.querySelector(".approval-deny:not([disabled]), input, button:not([disabled])");
    if (next) { next.focus(); return; }
    var coarse = window.matchMedia && window.matchMedia("(pointer: coarse)").matches;
    if (!coarse && typeof promptEl !== "undefined" && promptEl && !promptEl.disabled) promptEl.focus({ preventScroll: true });
    else panel.focus({ preventScroll: true });
  }
  deny.addEventListener("click", function () { answer("deny"); });
  approve.addEventListener("click", function () { answer("approve"); });
  if (similar) similar.addEventListener("click", function () { answer("similar"); });
  panel.addEventListener("keydown", function (e) {
    if (deny.disabled || e.ctrlKey || e.metaKey || e.altKey || e.repeat) return;
    var key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
    if (key === "Escape" || key === "n") { e.preventDefault(); answer("deny"); }
    else if (key === "y") { e.preventDefault(); answer("approve"); }
    else if (key === "a" && similar) { e.preventDefault(); answer("similar"); }
  });
  // Focus the safe choice first; Tab reaches approve, Enter/Space activates.
  // A card taller than the transcript (a phone) would open scrolled to its
  // buttons with the title under the top bar: what is being approved must be
  // readable first, so its top is aligned with the transcript top instead.
  setTimeout(function () {
    if (typeof flushSmartScroll === "function") flushSmartScroll();
    var scroller = typeof stage !== "undefined" && stage && stage.contains(panel) ? stage : null;
    var hidden = scroller ? scroller.getBoundingClientRect().top + 8 - panel.getBoundingClientRect().top : 0;
    if (hidden > 0) {
      scroller.scrollTop = Math.max(0, scroller.scrollTop - hidden);
      // Hold this position until the answer; output behind the card is
      // paused anyway, and answering follows the tail again.
      if (typeof transcriptFollowTail !== "undefined") transcriptFollowTail = nearBottom();
      followAfterAnswer = true;
      deny.focus({ preventScroll: true });
    } else deny.focus();
  }, 0);
}
