"use strict";
(async function () {
  var state = await fetch("/api/preview/state").then(function (response) { return response.json(); });
  document.documentElement.lang = state.language;
  document.title = "SuperCli · " + state.copy["preview.title"];
  var controller = window.SuperCliPreview.create(document.getElementById("site-preview"), {
    text: function (key) { return state.copy[key] || key; }, url: state.url,
    request: async function (path, body) {
      var response = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      if (!response.ok) throw new Error((await response.text()).trim() || "HTTP " + response.status);
      return response.json();
    }
  });
})();
