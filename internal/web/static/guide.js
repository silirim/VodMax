// VodMax TV guide: a programme grid browsed with the remote control.
// Arrows move between programmes (Left/Right in time, Up/Down between
// channels), OK opens a programme (record, watch, cancel), Back closes.
"use strict";

(() => {
  const { store, api, keyOf, hhmm } = window.VodMax;
  const t = window.VodMaxI18n.t;
  const $ = (id) => document.getElementById(id);
  const el = (tag, props = {}, ...children) => {
    const e = Object.assign(document.createElement(tag), props);
    e.append(...children.filter((c) => c !== null && c !== undefined && c !== false));
    return e;
  };
  // A narrower window on phones, so programmes stay readable.
  const HOURS = window.matchMedia("(max-width: 600px)").matches ? 1.5 : 3;
  const state = { from: slot(Date.now() / 1000), list: store.get("guideList", "all"), rows: [], me: null };
  let statusTimer;
  function flash(text) {
    $("status").textContent = text;
    $("status").hidden = false;
    clearTimeout(statusTimer);
    statusTimer = setTimeout(() => { $("status").hidden = true; }, 5000);
  }
  // Windows start on the half hour.
  function slot(t) { return Math.floor(t / 1800) * 1800; }
  const dayName = (ts) => {
    const d = new Date(ts * 1000), today = new Date();
    const diff = Math.round((new Date(d.getFullYear(), d.getMonth(), d.getDate()) - new Date(today.getFullYear(), today.getMonth(), today.getDate())) / 864e5);
    return diff === 0 ? t("Oggi") : diff === 1 ? t("Domani") : d.toLocaleDateString(window.VodMaxI18n.locale, { weekday: "long", day: "numeric", month: "short" });
  };

  async function load(focusKey) {
    const q = new URLSearchParams({ from: state.from, hours: HOURS });
    if (state.list === "favorites") q.set("list", "favorites");
    if (state.list === "sport") q.set("section", "sport");
    for (const t of document.querySelectorAll("[data-list]")) t.classList.toggle("active", t.dataset.list === state.list);
    $("guide-when").textContent = dayName(state.from) + " " + hhmm(state.from) + "–" + hhmm(state.from + HOURS * 3600);
    try { state.rows = (await api("GET", "/api/v1/guide?" + q)).filter((r) => r.programmes.length > 0); }
    catch (e) { if (!e.handled) flash(e.message); return; }
    render(focusKey);
  }

  function render(focusKey) {
    const from = state.from, span = HOURS * 3600, now = Date.now() / 1000;
    const pct = (t) => Math.max(0, Math.min(100, ((t - from) / span) * 100));
    const scale = el("div", { className: "scale" });
    for (let t = from; t < from + span; t += 1800) scale.append(Object.assign(el("span", {}, hhmm(t)), { style: "" }));
    [...scale.children].forEach((s, i) => { s.style.left = pct(from + i * 1800) + "%"; });
    const head = el("div", { className: "guide-times" }, el("div", {}), scale);
    const rows = state.rows.map((r, ri) => {
      const line = el("div", { className: "line" });
      r.programmes.forEach((p, pi) => {
        const b = el("button", { className: "prog focusable" + (p.start <= now && p.stop > now ? " on-air" : "") + (p.stop <= now ? " past" : "") +
          (p.recording ? " rec" : "") + (p.stop <= now && r.channel.catchup_days ? " replay" : "") },
          p.title, el("small", {}, hhmm(p.start) + "–" + hhmm(p.stop) + (p.stop <= now && r.channel.catchup_days ? " · ↺ " + t("riguarda") : "")));
        // Provider guides overlap at times: drawn up to the next start, so
        // no programme hides another.
        const next = r.programmes[pi + 1];
        const end = next && next.start < p.stop && next.start > p.start ? next.start : p.stop;
        b.style.left = pct(p.start) + "%";
        b.style.width = "calc(" + (pct(end) - pct(p.start)) + "% - 3px)";
        b.dataset.row = ri;
        b.dataset.start = p.start;
        b.dataset.stop = p.stop;
        b.dataset.key = r.channel.id + ":" + p.start;
        b.onclick = () => openProgramme(r, p);
        line.append(b);
      });
      if (now >= from && now < from + span) {
        const n = el("div", { className: "now-line" });
        n.style.left = pct(now) + "%";
        line.append(n);
      }
      const name = el("div", { className: "name" + (r.channel.logo ? "" : " no-logo") }, el("b", {}, r.channel.number || ""),
        r.channel.logo ? el("img", { src: r.channel.logo, alt: "", loading: "lazy" }) : null, r.channel.name,
        r.channel.catchup_days ? el("small", { className: "archive", title: t("Archivio di {days} giorni", { days: r.channel.catchup_days }) }, "↺") : null);
      return el("div", { className: "guide-row" }, name, line);
    });
    $("guide").replaceChildren(head, ...(rows.length ? rows : [el("p", { className: "empty" },
      state.list === "favorites" ? t("Nessun preferito con la guida in questo orario.") : t("Nessun programma in guida in questo orario."))]));
    // focusKey: a programme ("channel:start"), or the row and the side the
    // window moved to (the same channel, at the new edge).
    let target = typeof focusKey === "string" && document.querySelector('.prog[data-key="' + focusKey + '"]');
    if (focusKey && typeof focusKey === "object") {
      const list = [...document.querySelectorAll('.prog[data-row="' + Math.min(focusKey.row, state.rows.length - 1) + '"]')];
      const inside = list.filter((b) => Number(b.dataset.start) < from + span && Number(b.dataset.stop) > from);
      target = focusKey.dir === "ArrowRight" ? inside[0] : inside[inside.length - 1];
    }
    // A header button pressed by remote keeps the focus: "2 ore ▶" repeats.
    if (!target && document.activeElement && document.querySelector("header.top").contains(document.activeElement)) return;
    target = target || document.querySelector(".prog.on-air") || document.querySelector(".prog");
    if (target) { target.focus({ preventScroll: true }); target.scrollIntoView({ block: "nearest" }); }
  }

  // Icons from the bundled set: the symbols ⏺ ⏮ are missing from the
  // fonts of some TVs (an empty box).
  function iconButton(className, icon, text, onclick) {
    const b = el("button", { className, onclick });
    window.VodMax.label(b, icon, text);
    return b;
  }

  // ---- A programme ----
  function openProgramme(row, p) {
    const panel = $("programme");
    const now = Date.now() / 1000;
    const kids = state.me && state.me.profile && state.me.profile.kind === "kids";
    const buttons = [];
    const archive = row.channel.catchup_days && p.start >= now - row.channel.catchup_days * 86400 && p.start < now;
    const replay = () => {
      location.href = "/?" + new URLSearchParams({ catchup: row.channel.id, start: p.start, stop: p.stop, title: p.title, back: "/guide.html" });
    };
    if (p.start <= now && p.stop > now) {
      buttons.push(iconButton("button focusable", "play", t("Guarda"), () => { location.href = "/tv.html?channel=" + row.channel.id; }));
      if (archive) buttons.push(iconButton("button secondary focusable", "undo-2", t("Dall'inizio"), replay));
    } else if (archive) {
      buttons.push(iconButton("button focusable", "play", t("Riguarda"), replay));
    }
    if (p.recording && !kids) {
      buttons.push(el("button", { className: "button secondary focusable", onclick: async () => {
        try { await api("DELETE", "/api/v1/recordings/" + p.recording); flash(t("Registrazione annullata")); close(); load(row.channel.id + ":" + p.start); }
        catch (e) { flash(e.message); }
      } }, t("Annulla la registrazione")));
    } else if (p.stop > now && !kids) {
      buttons.push(el("button", { className: "button focusable", onclick: async () => {
        try {
          await api("POST", "/api/v1/recordings", { channel_id: row.channel.id, start: p.start, stop: p.stop, title: p.title, from_guide: true });
          flash(t("Registrazione programmata: {title}", { title: p.title }));
          close();
          load(row.channel.id + ":" + p.start);
        } catch (e) { flash(e.message); }
      } }, t("Registra")));
      window.VodMax.label(buttons[buttons.length - 1], "circle-dot", t("Registra"));
    }
    buttons.push(el("button", { className: "button secondary focusable", onclick: () => close() }, t("Chiudi")));
    panel.replaceChildren(...[el("h2", {}, p.title),
      el("p", { className: "meta" }, row.channel.name + " · " + dayName(p.start) + " " + hhmm(p.start) + "–" + hhmm(p.stop) +
        (p.category ? " · " + p.category : "") + (p.recording ? " · ● " + t("in registrazione") : "")),
      p.subtitle ? el("p", {}, p.subtitle) : null,
      p.description ? el("p", {}, p.description) : null,
      el("div", { className: "buttons" }, ...buttons)].filter(Boolean));
    panel.hidden = false;
    panel.dataset.back = row.channel.id + ":" + p.start;
    buttons[0].focus();
  }
  function close() {
    const panel = $("programme");
    panel.hidden = true;
    const b = document.querySelector('.prog[data-key="' + panel.dataset.back + '"]');
    if (b) b.focus();
  }

  // ---- Remote control ----
  function move(dir) {
    const cur = document.activeElement;
    if (!cur || !cur.classList.contains("prog")) { const f = document.querySelector(".prog.on-air") || document.querySelector(".prog"); if (f) f.focus(); return; }
    const row = Number(cur.dataset.row), start = Number(cur.dataset.start);
    const inRow = (r) => [...document.querySelectorAll('.prog[data-row="' + r + '"]')];
    if (dir === "ArrowLeft" || dir === "ArrowRight") {
      const list = inRow(row);
      const i = list.indexOf(cur) + (dir === "ArrowRight" ? 1 : -1);
      if (i >= 0 && i < list.length) { list[i].focus(); return; }
      // Past the edge of the window: move it, staying on the channel.
      state.from += (dir === "ArrowRight" ? 2 : -2) * 3600;
      load({ row, dir });
      return;
    }
    const target = row + (dir === "ArrowDown" ? 1 : -1);
    const list = inRow(target);
    if (!list.length) { if (dir === "ArrowUp") document.querySelector("[data-move]").focus(); return; }
    // The programme on air at the same moment in the other channel.
    const best = list.find((b, i) => Number(b.dataset.start) <= start && (!list[i + 1] || Number(list[i + 1].dataset.start) > start)) || list[0];
    best.focus();
    best.scrollIntoView({ block: "nearest" });
  }

  document.addEventListener("keydown", (e) => {
    const key = keyOf(e);
    if (!$("programme").hidden) {
      if (key === "Back") { close(); e.preventDefault(); }
      // The panel's buttons in a row: the arrows go along them.
      const buttons = [...$("programme").querySelectorAll("button")];
      const i = buttons.indexOf(document.activeElement);
      if (key.startsWith("Arrow") && buttons.length) {
        const d = key === "ArrowRight" || key === "ArrowDown" ? 1 : -1;
        buttons[Math.max(0, Math.min(buttons.length - 1, (i < 0 ? 0 : i + d)))].focus();
        e.preventDefault();
      }
      return;
    }
    if (document.activeElement && document.activeElement.tagName === "INPUT") return;
    // The header: ◀ ▶ along its buttons, Down to the grid.
    const header = document.querySelector("header.top");
    if (header.contains(document.activeElement) && key.startsWith("Arrow")) {
      const items = [...header.querySelectorAll(".focusable")].filter((b) => b.offsetParent);
      const i = items.indexOf(document.activeElement);
      if (key === "ArrowLeft" && i > 0) items[i - 1].focus();
      else if (key === "ArrowRight" && i < items.length - 1) items[i + 1].focus();
      else if (key === "ArrowDown") { const f = document.querySelector(".prog.on-air") || document.querySelector(".prog"); if (f) f.focus(); }
      e.preventDefault();
      return;
    }
    if (key.startsWith("Arrow") && (!document.activeElement || document.activeElement.classList.contains("prog") || key === "ArrowDown")) {
      if (!document.activeElement || !document.activeElement.classList.contains("prog")) {
        const f = document.querySelector(".prog.on-air") || document.querySelector(".prog");
        if (f) f.focus();
      } else move(key);
      e.preventDefault();
      return;
    }
    if (key === "OK" && document.activeElement) { document.activeElement.click(); e.preventDefault(); return; }
    if (key === "Back") { location.href = "/"; e.preventDefault(); }
  });

  for (const b of document.querySelectorAll("[data-list]")) b.onclick = () => { state.list = b.dataset.list; store.set("guideList", state.list); load(); };
  for (const b of document.querySelectorAll("[data-move]")) {
    b.onclick = () => {
      const m = b.dataset.move;
      if (m === "now") state.from = slot(Date.now() / 1000);
      else if (m === "day" || m === "yesterday") {
        const d = new Date();
        d.setDate(d.getDate() + (m === "day" ? 1 : -1));
        d.setHours(20, 30, 0, 0);
        state.from = d.getTime() / 1000;
      }
      else state.from += Number(m) * 3600;
      load();
    };
  }
  window.VodMax.bottomNav("more");
  window.VodMax.withToken(async () => {
    state.me = await window.VodMax.me().catch(() => null);
    load();
  });

  // Android "Back" goes through the page's own Back handling.
  window.VodMaxNativeBack = () => {
    if (window.VodMax.sheetOpen()) { window.VodMax.closeSheet(); return true; }
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    return true;
  };
})();
