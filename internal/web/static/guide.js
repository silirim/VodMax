// VodMax TV guide page: the shared grid (guide-grid.js) with the tabs and
// the time buttons of the header. Watching goes to tv.html, a programme
// from the archive to the player of the home page.
"use strict";

(() => {
  const { keyOf } = window.VodMax;
  const $ = (id) => document.getElementById(id);
  let statusTimer;
  function flash(text) {
    $("status").textContent = text;
    $("status").hidden = false;
    clearTimeout(statusTimer);
    statusTimer = setTimeout(() => { $("status").hidden = true; }, 5000);
  }

  const grid = window.GuideGrid.create($("guide"), {
    toolbar: document.querySelector("header.top"),
    panel: $("guide-panel"),
    flash,
    onWatch: (channel) => { location.href = "/tv.html?channel=" + channel.id; },
    onFromStart: (channel, p) => {
      location.href = "/?" + new URLSearchParams({ catchup: channel.id, start: p.start, stop: p.stop, title: p.title, back: "/guide.html" });
    },
    onClose: () => { location.href = "/"; },
  });

  document.addEventListener("keydown", (e) => {
    if (grid.key(e)) { e.preventDefault(); return; }
    if (keyOf(e) === "OK" && document.activeElement) { document.activeElement.click(); e.preventDefault(); }
  });

  window.VodMax.bottomNav("more");
  window.VodMax.withToken(() => grid.load());

  // Android "Back" goes through the page's own Back handling.
  window.VodMaxNativeBack = () => {
    if (window.VodMax.sheetOpen()) { window.VodMax.closeSheet(); return true; }
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    return true;
  };
})();
