// Browser-only, image-transport fallback. Never plays media or decodes audio.
(function () {
  "use strict";
  var MAX_FILE = 32 * 1024 * 1024, MAX_TOTAL = 64 * 1024 * 1024;
  var MAX_FRAMES = 6, MAX_JPEG = 512 * 1024, MAX_EDGE = 1280;
  function abortError() { return new DOMException("Attachment preparation cancelled", "AbortError"); }
  function isVideo(file) {
    return file instanceof File && (/\.(mp4|webm|mov)$/i.test(file.name) || /^(video\/mp4|video\/webm|video\/quicktime)$/i.test(file.type));
  }
  function check(signal, deadline) {
    if (signal && signal.aborted) throw abortError();
    if (performance.now() >= deadline) throw new Error("video preparation time limit");
  }
  function waitEvent(target, name, start, signal, deadline, timeout) {
    return new Promise(function (resolve, reject) {
      var timer;
      function clean() {
        clearTimeout(timer); target.removeEventListener(name, done); target.removeEventListener("error", failed);
        if (signal) signal.removeEventListener("abort", cancelled);
      }
      function done() { clean(); resolve(); }
      function failed() { clean(); reject(new Error("unsupported codec or unreadable video")); }
      function cancelled() { clean(); reject(abortError()); }
      target.addEventListener(name, done); target.addEventListener("error", failed);
      if (signal) signal.addEventListener("abort", cancelled, { once: true });
      timer = setTimeout(function () { clean(); reject(new Error("video metadata/seek timed out")); }, Math.max(1, Math.min(timeout, deadline - performance.now())));
      try { check(signal, deadline); start(); } catch (error) { clean(); reject(error); }
    });
  }
  function jpeg(canvas, quality, signal, deadline) {
    return new Promise(function (resolve, reject) {
      var timer;
      function clean() { clearTimeout(timer); if (signal) signal.removeEventListener("abort", cancelled); }
      function cancelled() { clean(); reject(abortError()); }
      if (signal) signal.addEventListener("abort", cancelled, { once: true });
      timer = setTimeout(function () { clean(); reject(new Error("JPEG encoding timed out")); }, Math.max(1, Math.min(2000, deadline - performance.now())));
      try {
        check(signal, deadline);
        canvas.toBlob(function (blob) { clean(); if (signal && signal.aborted) reject(abortError()); else if (blob && blob.type === "image/jpeg") resolve(blob); else reject(new Error("JPEG encoding unavailable")); }, "image/jpeg", quality);
      } catch (error) { clean(); reject(error); }
    });
  }
  async function extract(file, count, byteBudget, signal, deadline, sourceIndex) {
    var video = document.createElement("video"), canvas = document.createElement("canvas"), url;
    video.preload = "auto"; video.muted = true; video.playsInline = true;
    try {
      check(signal, deadline);
      // Inspect only a tiny header: do not let a disguised HLS/URL playlist
      // cause the media element to fetch remote resources from a local Blob.
      var header = new Uint8Array(await file.slice(0, 64).arrayBuffer());
      check(signal, deadline);
      var webm = header[0] === 0x1a && header[1] === 0x45 && header[2] === 0xdf && header[3] === 0xa3;
      var bmff = header[4] === 0x66 && header[5] === 0x74 && header[6] === 0x79 && header[7] === 0x70;
      if (!webm && !bmff) throw new Error("not a local MP4, WebM or QuickTime container");
      if (new TextEncoder().encode(file.name).length > 160) throw new Error("source filename is too long for timestamped snapshots");
      url = URL.createObjectURL(file);
      await waitEvent(video, "loadedmetadata", function () { video.src = url; video.load(); }, signal, deadline, 5000);
      var duration = video.duration;
      if (!Number.isFinite(duration) || duration <= 0 || !video.videoWidth || !video.videoHeight) throw new Error("video has no finite duration or dimensions");
      // Bound decoder output as well as canvas allocation. Extremely large input
      // surfaces are not safe to sample on a phone; preserve the original instead.
      if (video.videoWidth * video.videoHeight > 3840 * 2160) throw new Error("video resolution exceeds safe browser sampling limit");
      var scale = Math.min(1, MAX_EDGE / Math.max(video.videoWidth, video.videoHeight));
      canvas.width = Math.max(1, Math.round(video.videoWidth * scale)); canvas.height = Math.max(1, Math.round(video.videoHeight * scale));
      var ctx = canvas.getContext("2d", { alpha: false });
      if (!ctx) throw new Error("canvas unavailable");
      var frames = [], used = 0, margin = Math.min(0.05, duration / 10);
      for (var i = 0; i < count; i++) {
        check(signal, deadline);
        var time = count === 1 ? duration / 2 : margin + (duration - 2 * margin) * i / (count - 1);
        // A successful seek is required, not merely a currentTime assignment.
        await waitEvent(video, "seeked", function () { video.currentTime = time; }, signal, deadline, 3000);
        if (video.readyState < 2 || Math.abs(video.currentTime - time) > Math.max(0.25, duration * 0.001)) throw new Error("video is not reliably seekable");
        if (!video.seekable.length) throw new Error("video is not seekable");
        ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
        var blob;
        for (var q = 0; q < 4; q++) {
          blob = await jpeg(canvas, [0.82, 0.65, 0.45, 0.25][q], signal, deadline);
          if (blob.size <= MAX_JPEG) break;
        }
        if (blob.size > MAX_JPEG || used + blob.size > byteBudget) throw new Error("generated frames exceed attachment byte budget");
        used += blob.size;
        var source = file.name.replace(/[\\/\x00-\x1f]/g, "_");
        frames.push(new File([blob], source + ".source-" + sourceIndex + "-frame-" + (i + 1) + "-at-" + video.currentTime.toFixed(3) + "s-of-" + duration.toFixed(3) + "s.jpg", { type: "image/jpeg" }));
      }
      return frames;
    } finally {
      video.pause(); video.removeAttribute("src"); video.load(); video.remove();
      canvas.width = canvas.height = 0; canvas.remove();
      if (url) URL.revokeObjectURL(url);
    }
  }
  // prepare(File[], {slots, signal}) -> {files, warnings}. Originals always lead
  // the list and are never truncated. Abort rejects; extraction errors warn.
  async function prepare(input, options) {
    options = options || {};
    var files = Array.prototype.slice.call(input || []), slots = options.slots == null ? 8 : options.slots;
    var signal = options.signal, deadline = performance.now() + 30000;
    check(signal, deadline);
    if (files.length > slots || files.length > 8) throw new Error("Too many attachments (maximum 8 including existing attachments)");
    var total = 0;
    files.forEach(function (file) {
      if (!(file instanceof Blob) || file.size > MAX_FILE) throw new Error("Attachment exceeds 32 MiB per-file limit");
      total += file.size;
    });
    if (total > MAX_TOTAL) throw new Error("Attachments exceed 64 MiB request limit");
    var videos = files.filter(isVideo), budget = Math.min(MAX_FRAMES, slots - files.length), output = files.slice(), warnings = [];
    for (var i = 0; i < videos.length; i++) {
      check(signal, Infinity);
      var count = Math.ceil(budget / (videos.length - i));
      if (!count || total >= MAX_TOTAL || performance.now() >= deadline) {
        warnings.push(videos[i].name + ": original kept; no room/time for video snapshots. No video or audio analysis."); continue;
      }
      budget -= count;
      try {
        var frames = await extract(videos[i], count, MAX_TOTAL - total, signal, deadline, i + 1);
        frames.forEach(function (frame) { total += frame.size; output.push(frame); });
        if (count < MAX_FRAMES) warnings.push(videos[i].name + ": only " + count + " sparse snapshot(s) fit the shared attachment budget; no audio.");
      } catch (error) {
        if (error.name === "AbortError") throw error;
        warnings.push(videos[i].name + ": original kept; no snapshots (" + error.message + "). No video or audio analysis.");
      }
    }
    check(signal, Infinity);
    return { files: output, warnings: warnings };
  }
  window.SuperCliVideoAttachments = Object.freeze({ prepare: prepare, isVideo: isVideo });
})();
