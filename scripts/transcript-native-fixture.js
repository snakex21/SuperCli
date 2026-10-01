/* Paste into DevTools only in the isolated --echo profile described in
 * docs/transcript-performance.md. This changes the test window's display;
 * it does not call a model, run a tool, or write a transcript to the server. */
(function () {
  "use strict";
  var paragraph = "Synthetic **paragraph**, `code`, Unicode 🧪 ąćę and a [link](https://example.com).\n\n";
  var lastResult = null, running = false;
  function clear() {
    if (streaming || running) throw new Error("Stop the actual run before using the fixture");
    settleOpenTools();
    stream.textContent = "";
    loadedTranscriptMessages = [];
    toolRows = {}; workerRows = {}; openToolOrder = [];
    transcriptHasMore = false; transcriptBeforeSeq = 0;
    hideWelcome();
  }
  function history() {
    clear();
    loadedTranscriptMessages = Array.from({length: 20}, function (_, i) {
      return {seq: i + 1, role: "assistant", content: "<thinking>" + paragraph.repeat(100) + "</thinking>Answer " + i};
    });
    var start = performance.now();
    renderLoadedTranscript(false);
    lastResult = {mode: "history", renderMs: performance.now() - start,
      elements: stream.querySelectorAll("*").length, messages: loadedTranscriptMessages.length};
    console.log(lastResult);
    return lastResult;
  }
  async function streamFixture() {
    clear();
    running = true;
    transcriptLiveAppend = true;
    var text = "<thinking>" + paragraph.repeat(80) + "</thinking>\n" + paragraph.repeat(800);
    var encoder = new TextEncoder(), cursor = 0, current = null, pending = [], latencies = [], renders = 0, renderMs = 0;
    var original = renderAssistant, longTasks = [], observer = null;
    if (window.PerformanceObserver && PerformanceObserver.supportedEntryTypes.indexOf("longtask") >= 0) {
      observer = new PerformanceObserver(function (list) { list.getEntries().forEach(function (e) { longTasks.push(e.duration); }); });
      observer.observe({entryTypes: ["longtask"]});
    }
    renderAssistant = function (node) {
      var start = performance.now();
      original(node); renders++; renderMs += performance.now() - start;
      var now = performance.now();
      pending.splice(0).forEach(function (arrival) { latencies.push(now - arrival); });
    };
    var body = new ReadableStream({
      async pull(controller) {
        if (cursor >= text.length) {
          controller.enqueue(encoder.encode('data: {"type":"done"}\n\n'));
          controller.close(); return;
        }
        await new Promise(function (resolve) { setTimeout(resolve, 8); });
        var chunk = text.slice(cursor, cursor + 256); cursor += chunk.length;
        controller.enqueue(encoder.encode("data: " + JSON.stringify({type: "message", text: chunk}) + "\n\n"));
      },
    });
    var started = performance.now();
    try {
      await superCliUI.readSSE(body, function (ev) {
        if (ev.type === "done") { sealAssistantSegment(current); return; }
        pending.push(performance.now());
        current = handleEvent(ev, current);
      });
      sealAssistantSegment(current);
      if (!current || current._raw !== text) throw new Error("Fixture lost or reordered source text");
      latencies.sort(function (a, b) { return a - b; });
      lastResult = {mode: "stream", sourceChars: text.length, chunks: latencies.length, elapsedMs: performance.now() - started,
        renders: renders, rendererMs: renderMs, domCommitP50Ms: latencies[Math.floor(latencies.length * .5)],
        domCommitP95Ms: latencies[Math.floor(latencies.length * .95)], domCommitMaxMs: latencies[latencies.length - 1],
        longTasks: observer ? longTasks.length : null,
        longestTaskMs: observer ? Math.max.apply(null, [0].concat(longTasks)) : null,
        elements: stream.querySelectorAll("*").length};
      console.log(lastResult);
      return lastResult;
    } finally {
      if (current) flushAssistantRender(current);
      renderAssistant = original;
      releaseLiveTranscriptBlocks();
      if (observer) observer.disconnect();
      running = false;
    }
  }
  window.supercliTranscriptFixture = {history: history, stream: streamFixture, clear: clear, result: function () { return lastResult; }};
  console.log("Synthetic fixture ready: supercliTranscriptFixture.history(), .stream(), .clear(), .result()");
}());
