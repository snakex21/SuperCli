"use strict";

// Darwin (best-of-N) result card. The darwin tool result carries a
// `darwin_run_id:` marker; the card loads that run from /api/darwin and shows
// every candidate (model, diff, answer) with the judge's verdict. The user
// applies any one candidate into the current branch or discards them all;
// either way the candidate branches are deleted afterwards.

function darwinRunID(text) {
  var m = /darwin_run_id:\s*(bon-[0-9a-f]{12})/.exec(String(text || ""));
  return m ? m[1] : "";
}

function appendDarwinCard(row, text, isError) {
  if (!row || !row.parentNode || row._darwinCard || isError) return;
  var id = darwinRunID(text);
  if (!id) return;
  var card = el("div", "darwin-card");
  if (row.classList.contains("transcript-live")) card.classList.add("transcript-live");
  card.appendChild(el("div", "darwin-card-status", t("darwin.loading")));
  row.parentNode.insertBefore(card, row.nextSibling);
  row._darwinCard = card;
  j("/api/darwin?id=" + encodeURIComponent(id)).then(function (run) {
    renderDarwinCard(card, run);
  }).catch(function () {
    card.innerHTML = "";
    card.appendChild(el("div", "darwin-card-status", t("darwin.loadFailed")));
  });
}

function renderDarwinCard(card, run) {
  card.innerHTML = "";
  var cands = Array.isArray(run.candidates) ? run.candidates : [];
  var pending = run.status === "pending";
  var head = el("div", "darwin-card-head");
  head.appendChild(el("span", "darwin-card-title", t("darwin.title").replace("{n}", cands.length)));
  if (run.score > 0) head.appendChild(el("span", "darwin-card-score", t("darwin.score").replace("{score}", Number(run.score).toFixed(2))));
  head.appendChild(el("span", "darwin-card-tok", fmtTok(run.total_tokens) + " tok"));
  card.appendChild(head);
  if (run.reason) card.appendChild(el("div", "darwin-card-reason", run.reason));
  if (run.note) card.appendChild(el("div", "darwin-card-note", run.note));
  cands.forEach(function (c, pos) { card.appendChild(darwinCandidateRow(card, run, c, pos, pending)); });
  var foot = el("div", "darwin-card-foot");
  if (run.status === "applied") {
    foot.appendChild(el("span", "darwin-card-done", t("darwin.applied").replace("{n}", run.applied_index + 1)));
  } else if (run.status === "discarded") {
    foot.appendChild(el("span", "darwin-card-done", t("darwin.discarded")));
  } else if (cands.some(function (c) { return c.kept; })) {
    foot.appendChild(el("span", "", t("darwin.pending")));
    var discard = el("button", "btn danger", t("darwin.discardAll"));
    discard.type = "button";
    discard.addEventListener("click", function () { darwinAction(card, run, "discard", -1); });
    foot.appendChild(discard);
  }
  if (foot.firstChild) card.appendChild(foot);
}

function darwinCandidateRow(card, run, c, pos, pending) {
  var winner = pos === run.winner_index;
  var applied = run.status === "applied" && run.applied_index === c.index;
  var box = el("div", "darwin-cand" + (winner ? " winner" : "") + (c.error ? " failed" : "") + (applied ? " applied" : ""));
  var head = el("div", "darwin-cand-head");
  head.appendChild(el("span", "darwin-cand-num", "#" + (c.index + 1)));
  if (c.model) head.appendChild(el("span", "darwin-cand-model", c.model));
  if (winner) head.appendChild(el("span", "darwin-cand-badge", "★ " + t("darwin.winner")));
  var stat = darwinStatLine(c);
  if (stat) head.appendChild(el("span", "darwin-cand-stat", stat));
  head.appendChild(el("span", "darwin-cand-tok", fmtTok(c.tokens) + " tok"));
  if (pending && c.kept) {
    var apply = el("button", "btn" + (winner ? " primary" : ""), t("darwin.apply"));
    apply.type = "button";
    apply.addEventListener("click", function () { darwinAction(card, run, "apply", c.index); });
    head.appendChild(apply);
  }
  box.appendChild(head);
  if (c.error) {
    box.appendChild(el("div", "darwin-cand-error", c.error));
    return box;
  }
  if (c.diff) {
    box.appendChild(darwinDetails(t("darwin.diff"), function (d) { d.appendChild(darwinDiffView(c.diff, c.diff_truncated)); }));
  }
  if (c.text) {
    box.appendChild(darwinDetails(t("darwin.answer"), function (d) { d.appendChild(el("div", "darwin-cand-text", c.text)); }));
  }
  return box;
}

// darwinDetails builds the viewer only on first open, so a run with large
// diffs does not grow the transcript DOM until the user looks at it.
function darwinDetails(label, fill) {
  var d = el("details", "darwin-cand-details");
  d.appendChild(el("summary", "", label));
  d.addEventListener("toggle", function () {
    if (d.open && !d._filled) { d._filled = true; fill(d); }
  });
  return d;
}

function darwinDiffView(diff, truncated) {
  var view = el("div", "tool-diff");
  String(diff).split(/\r?\n/).forEach(function (line) {
    var cls = "diff-line";
    if (/^\+(?!\+\+)/.test(line)) cls += " added";
    else if (/^-(?!--)/.test(line)) cls += " removed";
    else cls += " context";
    view.appendChild(el("div", cls, line || " "));
  });
  if (truncated) view.appendChild(el("div", "diff-line context", "…"));
  return view;
}

function darwinStatLine(c) {
  if (c.error) return t("tool.error");
  if (!c.branch) return "";
  var lines = String(c.diffstat || "").trim().split(/\r?\n/);
  return lines[lines.length - 1].trim() || t("darwin.noChanges");
}

async function darwinAction(card, run, action, index) {
  var applying = action === "apply";
  var ok = await appConfirm(applying ? t("darwin.applyConfirm").replace("{n}", index + 1) : t("darwin.discardConfirm"), {
    danger: !applying,
    confirmLabel: applying ? t("darwin.apply") : t("darwin.discardAll"),
  });
  if (!ok) return;
  var buttons = card.querySelectorAll("button");
  buttons.forEach(function (b) { b.disabled = true; });
  try {
    var next = await jpost("/api/darwin", { id: run.id, action: action, index: index });
    renderDarwinCard(card, next);
    toast(applying ? t("darwin.applied").replace("{n}", index + 1) : t("darwin.discarded"));
  } catch (e) {
    buttons.forEach(function (b) { b.disabled = false; });
    toast(String((e && e.message) || e));
  }
}
