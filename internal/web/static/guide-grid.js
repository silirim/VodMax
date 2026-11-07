// VodMax programme grid, shared by the guide page and the guide over the
// live video (decision 0053): one grid, two homes.
//
// Arrows move between programmes (Left/Right in time, Up/Down between
// channels), OK opens a programme (watch, from the start, record), Back
// closes it. Past the edge of the window the window moves.
//
//   const grid = GuideGrid.create(container, {
//     toolbar,   // element with the [data-list] tabs, [data-move] buttons,
//                // a .guide-when label and an optional [data-close]
//     panel,     // the element that shows a programme (its actions)
//     hours,     // window width: 3 hours, 1.5 on phones
//     list,      // "all" | "favorites" | "sport" (default: the last used)
//     onWatch(channel, programme), onFromStart(channel, programme),
//     onRecord(channel, programme),  // optional: replaces the built-in recording
//     onClose(),                     // Back with nothing open, [data-close]
//     flash(text),                   // messages
//   });
//   grid.load()           the window as it is
//   grid.show(channelId)  the window at now, the channel's programme on air in focus
//   grid.key(e)           true when the key was for the grid (arrows, OK, Back)
"use strict";

window.GuideGrid = (() => {
  const { store, api, keyOf, hhmm } = window.VodMax;
  const t = window.VodMaxI18n.t;
  const el = (tag, props = {}, ...children) => {
    const e = Object.assign(document.createElement(tag), props);
    e.append(...children.filter((c) => c !== null && c !== undefined && c !== false));
    return e;
  };
  // Windows start on the half hour.
  const slot = (ts) => Math.floor(ts / 1800) * 1800;
  const dayName = (ts) => {
    const d = new Date(ts * 1000), today = new Date();
    const diff = Math.round((new Date(d.getFullYear(), d.getMonth(), d.getDate()) - new Date(today.getFullYear(), today.getMonth(), today.getDate())) / 864e5);
    return diff === 0 ? t("Oggi") : diff === 1 ? t("Domani") : d.toLocaleDateString(window.VodMaxI18n.locale, { weekday: "long", day: "numeric", month: "short" });
  };
  // Icons from the bundled set: the symbols ⏺ ⏮ are missing from the
  // fonts of some TVs (an empty box).
  function iconButton(className, icon, text, onclick) {
    const b = el("button", { className, onclick });
    window.VodMax.label(b, icon, text);
    return b;
  }

  function create(container, o) {
    const HOURS = o.hours || (window.matchMedia("(max-width: 600px)").matches ? 1.5 : 3);
    const flash = o.flash || (() => {});
    const toolbar = o.toolbar || null;
    const panel = o.panel;
    // seq: windows asked, done: the last one answered (they can arrive out of order).
    const state = { from: slot(Date.now() / 1000), list: o.list || store.get("guideList", "all"), rows: [], kids: false, seq: 0, done: 0 };
    window.VodMax.me().then((me) => { state.kids = Boolean(me && me.profile && me.profile.kind === "kids"); }).catch(() => {});
    const $$ = (sel) => [...container.querySelectorAll(sel)];
    const inToolbar = (node) => Boolean(toolbar && node && toolbar.contains(node));

    async function load(focusKey) {
      const seq = ++state.seq;
      const q = new URLSearchParams({ from: state.from, hours: HOURS });
      if (state.list === "favorites") q.set("list", "favorites");
      if (state.list === "sport") q.set("section", "sport");
      if (toolbar) {
        for (const b of toolbar.querySelectorAll("[data-list]")) b.classList.toggle("active", b.dataset.list === state.list);
        const when = toolbar.querySelector(".guide-when");
        if (when) when.textContent = dayName(state.from) + " " + hhmm(state.from) + "–" + hhmm(state.from + HOURS * 3600);
      }
      let rows;
      try { rows = (await api("GET", "/api/v1/guide?" + q)).filter((r) => r.programmes.length > 0); }
      catch (e) { if (!e.handled && seq === state.seq) flash(e.message); }
      if (seq !== state.seq) return; // a newer window was asked meanwhile
      state.done = seq;
      if (!rows) return;
      state.rows = rows;
      render(focusKey);
    }

    function render(focusKey) {
      const from = state.from, span = HOURS * 3600, now = Date.now() / 1000;
      const pct = (ts) => Math.max(0, Math.min(100, ((ts - from) / span) * 100));
      const scale = el("div", { className: "scale" });
      for (let ts = from; ts < from + span; ts += 1800) {
        const s = el("span", {}, hhmm(ts));
        s.style.left = pct(ts) + "%";
        scale.append(s);
      }
      const head = el("div", { className: "guide-times" }, el("div", {}), scale);
      const rows = state.rows.map((r, ri) => {
        const line = el("div", { className: "line" });
        r.programmes.forEach((p, pi) => {
          const replay = p.stop <= now && r.channel.catchup_days;
          const b = el("button", { className: "prog focusable" + (p.start <= now && p.stop > now ? " on-air" : "") + (p.stop <= now ? " past" : "") +
            (p.recording ? " rec" : "") + (replay ? " replay" : "") },
            p.title, el("small", {}, hhmm(p.start) + "–" + hhmm(p.stop) + (replay ? " · ↺ " + t("riguarda") : "")));
          // Provider guides overlap at times: drawn up to the next start, so
          // no programme hides another.
          const next = r.programmes[pi + 1];
          const end = next && next.start < p.stop && next.start > p.start ? next.start : p.stop;
          b.style.left = pct(p.start) + "%";
          b.style.width = "calc(" + (pct(end) - pct(p.start)) + "% - 3px)";
          b.dataset.row = ri;
          b.dataset.start = p.start;
          b.dataset.stop = p.stop;
          b.dataset.channel = r.channel.id;
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
      container.replaceChildren(head, ...(rows.length ? rows : [el("p", { className: "empty" },
        state.list === "favorites" ? t("Nessun preferito con la guida in questo orario.") : t("Nessun programma in guida in questo orario."))]));
      // focusKey: a programme ("channel:start"), a channel ({channel}: its
      // programme on air), or the row and the side the window moved to
      // ({row, dir}: the same channel, at the new edge).
      let target = typeof focusKey === "string" && container.querySelector('.prog[data-key="' + focusKey + '"]');
      if (focusKey && typeof focusKey === "object" && focusKey.channel) target = container.querySelector('.prog.on-air[data-channel="' + focusKey.channel + '"]');
      if (focusKey && typeof focusKey === "object" && focusKey.dir) {
        const list = $$('.prog[data-row="' + Math.min(focusKey.row, state.rows.length - 1) + '"]');
        const inside = list.filter((b) => Number(b.dataset.start) < from + span && Number(b.dataset.stop) > from);
        target = focusKey.dir === "ArrowRight" ? inside[0] : inside[inside.length - 1];
      }
      // A toolbar button pressed by remote keeps the focus: "2 ore ▶" repeats.
      if (!target && inToolbar(document.activeElement)) return;
      // No programme in this window: "Adesso", so the remote is not stuck.
      target = target || firstCell() || (toolbar && toolbar.querySelector('[data-move="now"]'));
      if (target) { target.focus({ preventScroll: true }); target.scrollIntoView({ block: "nearest" }); }
    }
    const firstCell = () => container.querySelector(".prog.on-air") || container.querySelector(".prog");

    // ---- A programme ----
    function openProgramme(row, p) {
      const now = Date.now() / 1000;
      const buttons = [];
      const archive = row.channel.catchup_days && p.start >= now - row.channel.catchup_days * 86400 && p.start < now;
      const replay = () => o.onFromStart(row.channel, p);
      if (p.start <= now && p.stop > now) {
        buttons.push(iconButton("button focusable", "play", t("Guarda"), () => o.onWatch(row.channel, p)));
        if (archive) buttons.push(iconButton("button secondary focusable", "undo-2", t("Dall'inizio"), replay));
      } else if (archive) {
        buttons.push(iconButton("button focusable", "play", t("Riguarda"), replay));
      }
      if (p.recording && !state.kids) {
        buttons.push(el("button", { className: "button secondary focusable", onclick: () => record(row, p) }, t("Annulla la registrazione")));
      } else if (p.stop > now && !state.kids) {
        buttons.push(iconButton("button focusable", "circle-dot", t("Registra"), () => record(row, p)));
      }
      buttons.push(el("button", { className: "button secondary focusable", onclick: () => close() }, t("Chiudi")));
      panel.replaceChildren(el("h2", {}, p.title),
        el("p", { className: "meta" }, row.channel.name + " · " + dayName(p.start) + " " + hhmm(p.start) + "–" + hhmm(p.stop) +
          (p.category ? " · " + p.category : "") + (p.recording ? " · ● " + t("in registrazione") : "")),
        ...[p.subtitle, p.description].filter(Boolean).map((text) => el("p", {}, text)),
        el("div", { className: "buttons" }, ...buttons));
      panel.hidden = false;
      panel.dataset.back = row.channel.id + ":" + p.start;
      buttons[0].focus();
    }
    function close() {
      panel.hidden = true;
      const b = container.querySelector('.prog[data-key="' + panel.dataset.back + '"]');
      if (b) b.focus();
    }
    async function record(row, p) {
      if (o.onRecord) { o.onRecord(row.channel, p); return; }
      try {
        if (p.recording) {
          await api("DELETE", "/api/v1/recordings/" + p.recording);
          flash(t("Registrazione annullata"));
        } else {
          await api("POST", "/api/v1/recordings", { channel_id: row.channel.id, start: p.start, stop: p.stop, title: p.title, from_guide: true });
          flash(t("Registrazione programmata: {title}", { title: p.title }));
        }
        close();
        load(row.channel.id + ":" + p.start);
      } catch (e) { flash(e.message); }
    }

    // ---- Remote control ----
    function move(dir) {
      const cur = document.activeElement;
      if (!cur || !cur.classList.contains("prog")) { const f = firstCell(); if (f) f.focus(); return; }
      const row = Number(cur.dataset.row), start = Number(cur.dataset.start);
      const inRow = (r) => $$('.prog[data-row="' + r + '"]');
      if (dir === "ArrowLeft" || dir === "ArrowRight") {
        const list = inRow(row);
        const i = list.indexOf(cur) + (dir === "ArrowRight" ? 1 : -1);
        if (i >= 0 && i < list.length) { list[i].focus(); return; }
        // Past the edge of the window: move it, staying on the channel. Not
        // while the last move is loading: a held key would stack hours.
        if (state.done !== state.seq) return;
        state.from += (dir === "ArrowRight" ? 2 : -2) * 3600;
        load({ row, dir });
        return;
      }
      const target = row + (dir === "ArrowDown" ? 1 : -1);
      const list = inRow(target);
      if (!list.length) {
        if (dir === "ArrowUp" && toolbar) { const b = toolbar.querySelector("[data-move]") || toolbar.querySelector(".focusable"); if (b) b.focus(); }
        return;
      }
      // The programme on air at the same moment in the other channel.
      const best = list.find((b, i) => Number(b.dataset.start) <= start && (!list[i + 1] || Number(list[i + 1].dataset.start) > start)) || list[0];
      best.focus();
      best.scrollIntoView({ block: "nearest" });
    }

    // True when the key was for the grid: the caller leaves it alone.
    function key(e) {
      const k = keyOf(e);
      const active = document.activeElement;
      if (!panel.hidden) {
        if (k === "Back") { close(); return true; }
        // The panel's buttons in a row: the arrows go along them.
        const buttons = [...panel.querySelectorAll("button")];
        const i = buttons.indexOf(active);
        if (k.startsWith("Arrow") && buttons.length) {
          const d = k === "ArrowRight" || k === "ArrowDown" ? 1 : -1;
          buttons[Math.max(0, Math.min(buttons.length - 1, (i < 0 ? 0 : i + d)))].focus();
          return true;
        }
        if (k === "OK" && active) { active.click(); return true; }
        return false;
      }
      if (active && active.tagName === "INPUT") return false;
      // The toolbar: ◀ ▶ along its buttons, Down to the grid.
      if (inToolbar(active) && k.startsWith("Arrow")) {
        const items = [...toolbar.querySelectorAll(".focusable")].filter((b) => b.offsetParent);
        const i = items.indexOf(active);
        if (k === "ArrowLeft" && i > 0) items[i - 1].focus();
        else if (k === "ArrowRight" && i < items.length - 1) items[i + 1].focus();
        else if (k === "ArrowDown") { const f = firstCell(); if (f) f.focus(); }
        return true;
      }
      // Focus lost (the grid was redrawn): any arrow brings it back.
      const lost = !active || active === document.body;
      const inGrid = Boolean(active && active.classList.contains("prog"));
      if (k.startsWith("Arrow") && (lost || inGrid || k === "ArrowDown")) {
        if (!inGrid) { const f = firstCell() || (toolbar && toolbar.querySelector('[data-move="now"]')); if (f) f.focus(); }
        else move(k);
        return true;
      }
      if (k === "OK" && (inGrid || inToolbar(active))) { active.click(); return true; }
      if (k === "Back" && o.onClose) { o.onClose(); return true; }
      return false;
    }

    function setList(id) { state.list = id; store.set("guideList", id); load(); }
    function jump(m) {
      if (m === "now") state.from = slot(Date.now() / 1000);
      else if (m === "day" || m === "yesterday") {
        const d = new Date();
        d.setDate(d.getDate() + (m === "day" ? 1 : -1));
        d.setHours(20, 30, 0, 0);
        state.from = d.getTime() / 1000;
      } else state.from += Number(m) * 3600;
      load();
    }
    if (toolbar) {
      for (const b of toolbar.querySelectorAll("[data-list]")) b.onclick = () => setList(b.dataset.list);
      for (const b of toolbar.querySelectorAll("[data-move]")) b.onclick = () => jump(b.dataset.move);
      for (const b of toolbar.querySelectorAll("[data-close]")) b.onclick = () => o.onClose && o.onClose();
    }

    return {
      load,
      key,
      close,
      setList,
      jump,
      show(channelId) { state.from = slot(Date.now() / 1000); return load({ channel: channelId }); },
      get open() { return !panel.hidden; },
    };
  }

  return { create };
})();
