"use strict";

// One viewer for composer, queued and transcript images. Native modal dialog
// supplies background inertness; explicit key handling also keeps focus contained.
var imageLightbox = null;
function openImageLightbox(paths, selected, trigger, previewPath) {
  if (!paths || !paths.length) return;
  if (imageLightbox) imageLightbox.close();
  var dialog = el("dialog", "image-lightbox");
  dialog.setAttribute("role", "dialog");
  dialog.setAttribute("aria-modal", "true");
  dialog.setAttribute("aria-label", t("attachment.preview"));
  dialog.tabIndex = -1;
  var toolbar = el("div", "image-lightbox-toolbar");
  var title = el("span", "image-lightbox-title");
  var counter = el("span", "image-lightbox-counter");
  counter.setAttribute("aria-live", "polite");
  var stage = el("div", "image-lightbox-stage");
  var image = document.createElement("img");
  image.draggable = false;
  var failure = el("div", "image-lightbox-error", t("attachment.imageLoadFailed")); failure.hidden = true;
  stage.appendChild(image); stage.appendChild(failure);
  var controls = [];
  var state = { index: Math.max(0, paths.indexOf(selected)), scale: 1, fit: 1, rotation: 0, x: 0, y: 0, ready: false, close: close };
  imageLightbox = state;
  var returnFocus = trigger || document.activeElement;
  var pointers = new Map(), moved = false, backdropDown = false, closed = false, dragging = false;
  function button(label, text, action) {
    var b = el("button", "image-lightbox-button", text); b.type = "button";
    b.title = t(label); b.setAttribute("aria-label", t(label));
    b.addEventListener("click", action); toolbar.appendChild(b); controls.push(b); return b;
  }
  var prev = button("attachment.previousImage", "‹", function () { navigate(-1); });
  toolbar.appendChild(counter);
  var next = button("attachment.nextImage", "›", function () { navigate(1); });
  toolbar.appendChild(title);
  button("attachment.zoomOut", "−", function () { zoom(state.scale / 1.25, 0, 0); });
  button("attachment.zoomIn", "+", function () { zoom(state.scale * 1.25, 0, 0); });
  var fitButton = button("attachment.fitImage", "⤢", fit);
  button("attachment.rotateImage", "↻", rotate);
  var closeButton = button("common.close", "×", close);
  prev.disabled = next.disabled = paths.length < 2;
  dialog.appendChild(toolbar); dialog.appendChild(stage); document.body.appendChild(dialog);
  function bounds() { return stage.getBoundingClientRect(); }
  function dimensions() {
    return state.rotation % 180 ? { width: image.naturalHeight, height: image.naturalWidth } : { width: image.naturalWidth, height: image.naturalHeight };
  }
  function fitScale() {
    var r = bounds(), size = dimensions();
    return Math.min(1, r.width / size.width, r.height / size.height);
  }
  // Pan limit for one axis: an overflowing image may scroll to its edge. A
  // zoomed image that still fits (small screenshots: fit is 100%) may move
  // inside the stage, so the grab cursor never promises a dead drag.
  function panLimit(extent, room, zoomed) {
    var overflow = extent - room;
    return overflow > 0 ? overflow / 2 : (zoomed ? -overflow / 2 : 0);
  }
  function paint() {
    var r = bounds(), size = dimensions(), zoomed = state.scale > state.fit + 0.001;
    var maxX = panLimit(size.width * state.scale, r.width, zoomed);
    var maxY = panLimit(size.height * state.scale, r.height, zoomed);
    state.x = Math.max(-maxX, Math.min(maxX, state.x));
    state.y = Math.max(-maxY, Math.min(maxY, state.y));
    image.style.transform = "translate(-50%, -50%) translate(" + state.x + "px," + state.y + "px) rotate(" + state.rotation + "deg) scale(" + state.scale + ")";
    stage.classList.toggle("zoomed", zoomed);
    fitButton.title = t("attachment.fitImage") + " · " + Math.round(state.scale * 100) + "%";
  }
  function fit() {
    if (!state.ready) return;
    state.fit = fitScale();
    state.scale = state.fit; state.x = state.y = 0; paint();
  }
  function rotate() {
    if (!state.ready) return;
    var fitted = state.scale <= state.fit + 0.001, x = state.x;
    state.rotation = (state.rotation + 90) % 360;
    state.fit = fitScale();
    state.scale = fitted ? state.fit : Math.max(state.fit, state.scale);
    state.x = -state.y; state.y = x; paint();
  }
  function zoom(value, x, y) {
    if (!state.ready) return;
    var scale = Math.max(state.fit, Math.min(8, value));
    var factor = scale / state.scale;
    state.x = x - (x - state.x) * factor; state.y = y - (y - state.y) * factor;
    state.scale = scale; paint();
  }
  function navigate(delta) {
    state.index = (state.index + delta + paths.length) % paths.length;
    state.ready = false; state.scale = 1; state.rotation = 0; state.x = state.y = 0;
    endGesture(); backdropDown = false; image.hidden = true; failure.hidden = true;
    var path = paths[state.index]; title.textContent = attachmentName(path);
    image.alt = attachmentName(path);
    counter.textContent = (state.index + 1) + "/" + paths.length;
    image.src = attachmentPreviewSource(previewPath && path === selected ? previewPath : path);
  }
  image.addEventListener("load", function () {
    if (!image.naturalWidth || !image.naturalHeight) return;
    image.style.width = image.naturalWidth + "px"; image.style.height = image.naturalHeight + "px";
    image.hidden = false; state.ready = true; fit();
  });
  image.addEventListener("error", function () { failure.hidden = false; });
  stage.addEventListener("wheel", function (event) {
    event.preventDefault();
    var r = bounds(); zoom(state.scale * Math.exp(-event.deltaY * 0.002), event.clientX - r.left - r.width / 2, event.clientY - r.top - r.height / 2);
  }, { passive: false });
  stage.addEventListener("dblclick", function (event) {
    event.preventDefault();
    if (state.scale > state.fit + 0.001) fit(); else zoom(1, 0, 0);
  });
  function geometry() {
    var p = Array.from(pointers.values());
    if (p.length < 2) return { x: p[0].x, y: p[0].y, distance: 0 };
    return { x: (p[0].x + p[1].x) / 2, y: (p[0].y + p[1].y) / 2, distance: Math.hypot(p[0].x - p[1].x, p[0].y - p[1].y) };
  }
  // Cancel native selection/drag before it can steal a captured pointer. This
  // does not cancel click/dblclick, which still handle zoom and backdrop close.
  function preventNative(event) { event.preventDefault(); }
  stage.addEventListener("mousedown", preventNative);
  stage.addEventListener("dragstart", preventNative);
  dialog.addEventListener("selectstart", preventNative);
  // Pointer capture is only a hint. Moves and releases are tracked on window
  // (capture phase) for the whole gesture, so a refused, dropped or early-lost
  // capture (lostpointercapture) can no longer end a drag before it moves.
  function capture(id) { try { stage.setPointerCapture(id); } catch (e) { /* window listeners still track the drag */ } }
  function releaseCapture(id) { try { if (stage.hasPointerCapture(id)) stage.releasePointerCapture(id); } catch (e) { /* already gone */ } }
  function listenDrag(on) {
    if (dragging === on) return;
    dragging = on;
    var method = on ? "addEventListener" : "removeEventListener";
    window[method]("pointermove", movePointer, true);
    window[method]("pointerup", release, true);
    window[method]("pointercancel", release, true);
    window[method]("blur", abortGesture);
  }
  function endGesture() {
    pointers.forEach(function (_, id) { releaseCapture(id); });
    pointers.clear(); listenDrag(false); stage.classList.remove("interacting");
  }
  function abortGesture() { moved = true; backdropDown = false; endGesture(); }
  stage.addEventListener("pointerdown", function (event) {
    if (event.button && event.pointerType !== "touch") return;
    event.preventDefault();
    // Preventing pointerdown also prevents native focus changes. Move focus off
    // the auto-focused close button so its keyboard ring cannot linger on clicks.
    dialog.focus({ preventScroll: true });
    if (!pointers.size) { moved = false; backdropDown = event.target === stage; }
    else moved = true; // A pinch is never a backdrop click.
    pointers.set(event.pointerId, { x: event.clientX, y: event.clientY, startX: event.clientX, startY: event.clientY });
    listenDrag(true); capture(event.pointerId); stage.classList.add("interacting");
  });
  function movePointer(event) {
    if (!pointers.has(event.pointerId)) return;
    // A mouse button released outside the window never reports pointerup here.
    if (event.pointerType === "mouse" && !event.buttons) { release(event); return; }
    event.preventDefault();
    var old = geometry(), pointer = pointers.get(event.pointerId);
    pointer.x = event.clientX; pointer.y = event.clientY;
    var current = geometry();
    var dx = current.x - old.x, dy = current.y - old.y;
    // Accumulate from pointerdown: many small mouse/touch moves are still a drag.
    if (Math.hypot(pointer.x - pointer.startX, pointer.y - pointer.startY) > 3) moved = true;
    if (old.distance && current.distance) {
      var r = bounds(); zoom(state.scale * current.distance / old.distance, old.x - r.left - r.width / 2, old.y - r.top - r.height / 2);
      moved = true;
    }
    state.x += dx; state.y += dy; paint();
  }
  function release(event) {
    if (!pointers.has(event.pointerId)) return;
    if (event.type !== "pointerup") { moved = true; backdropDown = false; }
    pointers.delete(event.pointerId); releaseCapture(event.pointerId);
    if (!pointers.size) { listenDrag(false); stage.classList.remove("interacting"); }
  }
  stage.addEventListener("click", function (event) { if (event.target === stage && backdropDown && !moved) close(); });
  dialog.addEventListener("click", function (event) { if (event.target === dialog) close(); });
  dialog.addEventListener("cancel", function (event) { event.preventDefault(); close(); });
  dialog.addEventListener("keydown", function (event) {
    event.stopPropagation();
    if (event.key === "Escape") { event.preventDefault(); close(); }
    else if (event.key === "ArrowLeft" || event.key === "ArrowRight") { event.preventDefault(); navigate(event.key === "ArrowLeft" ? -1 : 1); }
    else if (event.key === "+" || event.key === "=") { event.preventDefault(); zoom(state.scale * 1.25, 0, 0); }
    else if (event.key === "-") { event.preventDefault(); zoom(state.scale / 1.25, 0, 0); }
    else if (event.key === "0") { event.preventDefault(); fit(); }
    else if (event.key === "r" || event.key === "R") { event.preventDefault(); rotate(); }
    else if (event.key === "Tab") {
      var available = controls.filter(function (b) { return !b.disabled; });
      var index = available.indexOf(document.activeElement);
      var nextIndex = index < 0 ? (event.shiftKey ? available.length - 1 : 0) : (index + (event.shiftKey ? -1 : 1) + available.length) % available.length;
      event.preventDefault(); available[nextIndex].focus();
    }
  });
  function close() {
    if (closed) return;
    closed = true; window.removeEventListener("resize", fit);
    image.removeAttribute("src"); endGesture();
    if (dialog.open) dialog.close(); dialog.remove();
    if (imageLightbox === state) imageLightbox = null;
    if (returnFocus && returnFocus.isConnected) returnFocus.focus();
  }
  dialog.addEventListener("close", close);
  window.addEventListener("resize", fit);
  dialog.showModal(); navigate(0); closeButton.focus();
}

function appendQueuedImageThumbnails(row, item) {
  var paths = (item.attachments || []).filter(imageAttachment);
  var gallery = el("div", "queue-image-thumbnails");
  paths.forEach(function (path) {
    var button = el("button", "queue-image-thumbnail"); button.type = "button";
    button.setAttribute("aria-label", t("attachment.preview") + ": " + attachmentName(path));
    var image = document.createElement("img"); image.alt = attachmentName(path); image.loading = "lazy"; image.decoding = "async";
    image.src = attachmentThumbnailSource(path, "composer"); button.appendChild(image);
    button.addEventListener("click", function () { openImageLightbox(paths, path, button); });
    gallery.appendChild(button);
  });
  row.appendChild(gallery);
}
