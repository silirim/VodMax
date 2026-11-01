// VodMax Multiview: two or four live channels at once, each with its own
// provider connection. The audio is the selected box's.
//
// Arrows          choose the box (the audio follows)
// OK              change the channel of the box
// Red / Delete    empty the box (frees its connection)
// Back            the selected channel full screen
// Green           menu
"use strict";

(() => {
  const $ = (id) => document.getElementById(id);
  const { store, api, viewer, keyOf, hhmm } = window.VodMax;
  const t = window.VodMaxI18n.t;
  const icon = (name) => window.VodMaxIcons.icon(name);

  const state = {
    layout: store.get("mvLayout", 4) === 2 ? 2 : 4,
    ids: store.get("multiview", []),   // channel id per box
    active: 0,
    channels: [],
    cells: [],                          // { box, video, player, label }
    pick: null,                         // { cell, selected }
  };

  function el(tag, props = {}, ...children) {
    const e = Object.assign(document.createElement(tag), props);
    e.append(...children.filter((c) => c !== null && c !== undefined));
    return e;
  }
  const channel = (id) => state.channels.find((c) => c.id === id);

  let statusTimer;
  function flash(text) {
    $("status").textContent = text;
    $("status").hidden = false;
    clearTimeout(statusTimer);
    statusTimer = setTimeout(() => { $("status").hidden = true; }, 4000);
  }

  // ---- Boxes ----
  function build() {
    for (const c of state.cells) stop(c);
    const mosaic = $("mosaic");
    mosaic.className = "mosaic n" + state.layout;
    state.cells = [];
    mosaic.replaceChildren();
    for (let i = 0; i < state.layout; i++) {
      const video = el("video", { autoplay: true, muted: true, playsInline: true });
      const box = el("div", { className: "mv-cell" }, video);
      box.onclick = () => { if (state.active === i) openPick(i); else activate(i); };
      box.ondblclick = () => fullScreen(i);
      const cell = { box, video, player: null, i };
      state.cells.push(cell);
      mosaic.append(box);
      render(cell);
      play(cell);
    }
    if (state.active >= state.layout) state.active = 0;
    activate(state.active);
    $("mv-layout").lastChild.textContent = state.layout === 4 ? t("2 canali") : t("4 canali");
  }

  // A box's message stays until its channel changes or plays again
  // (moving the focus redraws every box).
  function render(cell, message) {
    if (message !== undefined) cell.message = message;
    message = cell.message;
    for (const old of cell.box.querySelectorAll(".mv-label, .mv-empty")) old.remove();
    const c = channel(state.ids[cell.i]);
    if (!c) {
      cell.box.append(el("div", { className: "mv-empty" }, icon("plus"), el("span", {}, message || t("Scegli un canale"))));
      return;
    }
    const label = el("div", { className: "mv-label" }, el("span", { className: "number" }, String(c.number)),
      el("span", { className: "name" }, c.name), el("span", { className: "now" }, c.now ? c.now.title : ""));
    if (cell.i === state.active) label.append(icon("volume-2"));
    cell.box.append(label);
    if (message) cell.box.append(el("div", { className: "mv-empty" }, icon("triangle-alert"), el("span", {}, message)));
  }

  function stop(cell) {
    if (cell.player) { cell.player.destroy(); cell.player = null; }
  }

  // Each box is its own viewer: two boxes take two connections, and a box
  // emptied frees its own.
  async function play(cell) {
    stop(cell);
    cell.message = "";
    const c = channel(state.ids[cell.i]);
    if (!c) return;
    let url;
    try {
      url = (await api("POST", "/api/v1/live/" + c.id + "/ticket", { viewer: viewer + "-mv" + cell.i })).url;
    } catch (e) { render(cell, e.message); return; }
    if (channel(state.ids[cell.i]) !== c) return; // changed meanwhile
    if (!window.mpegts || !mpegts.isSupported()) { render(cell, t("Questo dispositivo non supporta la riproduzione MPEG-TS nel browser")); return; }
    const player = window.VodMax.tsPlayer(cell.video, url, {
      live: true,
      onError: (type, detail, info) => {
        if (cell.player !== player) return;
        stop(cell);
        const code = info && info.code;
        render(cell, code === 423 ? t("Tutte le connessioni dell'abbonamento sono occupate da una registrazione in corso")
          : code === 409 ? t("Nessuna connessione libera: l'abbonamento ne ha già in uso il massimo")
          : type === mpegts.ErrorTypes.MEDIA_ERROR ? t("Questo dispositivo non riesce a decodificare il canale ({detail})", { detail })
            : t("Canale non disponibile ({code})", { code: code || detail }));
      },
      onEnd: () => { if (cell.player === player) { stop(cell); render(cell, t("La diretta si è interrotta")); } },
      onSilent: () => { if (cell.player === player) render(cell, t("Il canale non manda immagini: riprova o scegli un altro canale")); },
    });
    cell.player = player;
    cell.video.muted = cell.i !== state.active;
  }

  function activate(i) {
    state.active = i;
    for (const c of state.cells) {
      c.box.classList.toggle("active", c.i === i);
      c.video.muted = c.i !== i;
      render(c);
    }
    const cur = state.cells[i];
    if (cur && cur.player) cur.player.resumeIfBlocked();
  }

  function setChannel(i, id) {
    state.ids[i] = id;
    state.cells[i].message = "";
    store.set("multiview", state.ids);
    render(state.cells[i]);
    play(state.cells[i]);
  }

  function fullScreen(i) {
    const id = state.ids[i];
    leave(id ? "/tv.html?channel=" + id : "/tv.html");
  }

  function leave(to) {
    for (const c of state.cells) stop(c);
    location.href = to;
  }

  // ---- Channel choice ----
  function openPick(i) {
    const cur = state.channels.findIndex((c) => c.id === state.ids[i]);
    state.pick = { cell: i, selected: Math.max(0, cur) };
    const close = el("button", { type: "button", className: "mv-pick-close", onclick: closePick }, "✕");
    close.setAttribute("aria-label", t("Chiudi"));
    $("mv-pick-title").replaceChildren(close, t("Canale per il riquadro {n}", { n: i + 1 }));
    $("mv-pick").hidden = false;
    renderPick();
  }
  function closePick() { state.pick = null; $("mv-pick").hidden = true; }
  function renderPick() {
    const p = state.pick;
    const rows = Math.max(6, Math.floor((window.innerHeight * 0.8) / 44));
    const start = Math.max(0, Math.min(p.selected - Math.floor(rows / 2), state.channels.length - rows));
    $("mv-pick-items").replaceChildren(...state.channels.slice(start, start + rows).map((c, k) => {
      const li = el("li", { className: start + k === p.selected ? "selected" : "" }, el("span", { className: "number" }, String(c.number)),
        el("span", {}, c.name), el("span", { className: "now" }, c.now ? c.now.title : ""));
      li.onclick = (e) => { e.stopPropagation(); p.selected = start + k; choose(); };
      return li;
    }));
  }
  function choose() {
    const c = state.channels[state.pick.selected];
    const i = state.pick.cell;
    closePick();
    if (c) setChannel(i, c.id);
  }

  // ---- Keys ----
  function move(key) {
    const n = state.layout, i = state.active;
    const cols = matchMedia("(max-width: 800px)").matches ? 1 : 2;
    let j = i;
    if (key === "ArrowLeft" && i % cols > 0) j = i - 1;
    if (key === "ArrowRight" && i % cols < cols - 1 && i + 1 < n) j = i + 1;
    if (key === "ArrowUp" && i - cols >= 0) j = i - cols;
    if (key === "ArrowDown" && i + cols < n) j = i + cols;
    if (j !== i) activate(j);
  }

  let idleTimer;
  function wake() {
    $("mv-bar").classList.remove("idle");
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => { if (!state.pick) $("mv-bar").classList.add("idle"); }, 5000);
  }
  document.addEventListener("mousemove", wake);

  document.addEventListener("keydown", (e) => {
    if (document.querySelector(".setup-panel")) return;
    const key = keyOf(e);
    wake();
    const cur = state.cells[state.active];
    if (cur && cur.player) cur.player.resumeIfBlocked();
    let handled = true;
    if (e.target.closest && e.target.closest("#mv-bar") && key === "OK") return; // the focused button
    if (state.pick) {
      const p = state.pick, last = state.channels.length - 1;
      switch (key) {
        case "ArrowUp": p.selected = Math.max(0, p.selected - 1); renderPick(); break;
        case "ArrowDown": p.selected = Math.min(last, p.selected + 1); renderPick(); break;
        case "ChannelUp": p.selected = Math.max(0, p.selected - 10); renderPick(); break;
        case "ChannelDown": p.selected = Math.min(last, p.selected + 10); renderPick(); break;
        case "OK": choose(); break;
        case "Back": closePick(); break;
        default: handled = false;
      }
    } else {
      switch (key) {
        case "ArrowLeft": case "ArrowRight": case "ArrowUp": case "ArrowDown": move(key); break;
        case "OK": openPick(state.active); break;
        case "Red": case "Delete":
          state.ids[state.active] = null;
          store.set("multiview", state.ids);
          stop(cur);
          render(cur);
          break;
        case "Back": fullScreen(state.active); break;
        case "Green": leave("/"); break;
        default: handled = false;
      }
    }
    if (handled) e.preventDefault();
  });

  $("mv-bar").addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    if (b.dataset.action === "back") leave("/tv.html");
    if (b.dataset.action === "layout") {
      state.layout = state.layout === 4 ? 2 : 4;
      store.set("mvLayout", state.layout);
      build();
    }
  });
  addEventListener("pagehide", () => { for (const c of state.cells) stop(c); });

  // Keep "now" fresh.
  setInterval(async () => {
    try { state.channels = (await api("GET", "/api/v1/tv/channels")).filter((c) => c.section !== "radio"); } catch { return; }
    for (const c of state.cells) if (!c.box.querySelector(".mv-empty")) render(c);
  }, 60000);

  async function start() {
    try { state.channels = (await api("GET", "/api/v1/tv/channels")).filter((c) => c.section !== "radio"); }
    catch (e) { if (!e.handled) flash(e.message); return; }
    if (!state.channels.length) { flash(t("Nessun canale: sincronizza un abbonamento")); return; }
    // From the TV: /multiview.html?channel=ID goes in the first box.
    const asked = Number(new URLSearchParams(location.search).get("channel"));
    if (asked && !state.ids.includes(asked)) state.ids[0] = asked;
    state.ids = Array.from({ length: 4 }, (_, i) => (channel(state.ids[i]) ? state.ids[i] : null));
    store.set("multiview", state.ids);
    $("mv-layout").append(document.createTextNode(""));
    build();
    wake();
    if (!state.ids.some(Boolean)) openPick(0);
  }

  window.VodMax.withToken(start);

  window.VodMaxNativeBack = () => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    return true;
  };
})();
