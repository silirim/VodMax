// VodMax programme grid, shared by the guide page and the guide over the
// live video (decision 0053): one grid, two homes.
//
// Arrows move between programmes (Left/Right in time, Up/Down between
// channels); the programme in focus is described in the panel above the
// time scale (title, channel, time, plot, image when the guide has one).
// OK opens its actions in the same panel (watch, from the start, record),
// Back returns to the cell. Past the edge of the window the window moves.
//
//   const grid = GuideGrid.create(container, {
//     toolbar,   // element with the [data-list] tabs, [data-move] buttons,
//                // a .guide-when label and an optional [data-close]
//     panel,     // where the programme is described (outside the
//                // container, which scrolls)
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
//   grid.close()          the actions, back to the cell
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
  // A tint per genre (data-genre on the cell), from the words of the
  // category: XMLTV guides write it in any language, so only the broad
  // families are recognised.
  const GENRES = [
    ["film", /film|movie|cinema|fiction|serie|series|drama|comed|thriller/i],
    ["sport", /sport|calcio|football|soccer|tennis|basket|motor|rugby|golf|cicl|cycl|volley|formula/i],
    ["news", /news|notiz|telegiornale|\btg\b|informazion|attualit|current affairs|weather|meteo/i],
    ["kids", /kids|bambin|cartoon|cartoni|children|ragazzi|animazion|anime/i],
  ];
  const genre = (category) => { const g = GENRES.find(([, re]) => re.test(category)); return g ? g[0] : "other"; };
  // Rows of rounded placeholders while the first window loads.
  const skeleton = () => el("div", { className: "guide-skeleton" }, ...Array.from({ length: 8 }, () =>
    el("div", { className: "guide-row" }, el("div", { className: "name" }, el("span", { className: "skeleton" })),
      el("div", { className: "line" }, el("span", { className: "skeleton" }), el("span", { className: "skeleton" }), el("span", { className: "skeleton" })))));
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
    // Dark glass over the video, the stronger light glass on the guide page.
    panel.classList.add(container.closest(".guide-overlay") ? "glass-dark" : "glass-strong");
    // seq: windows asked, done: the last one answered (they can arrive out of
    // order). shown: the programme described in the panel, open: with its actions.
    const state = { from: slot(Date.now() / 1000), list: o.list || store.get("guideList", "all"), rows: [], kids: false, seq: 0, done: 0, shown: null, open: false };
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
      if (!container.firstElementChild) container.replaceChildren(skeleton());
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
        const s = el("span", { className: "tick tnum" }, hhmm(ts));
        s.style.left = pct(ts) + "%";
        scale.append(s);
      }
      // "Now" as a pill on the scale, above the line that crosses the rows.
      if (now >= from && now < from + span) {
        const pill = el("span", { className: "now-pill tnum" }, hhmm(now));
        pill.style.left = pct(now) + "%";
        pill.style.left = "max(" + pct(now) + "%, 1.8rem)"; // not cut by the channel column (ignored where max() is unknown)
        scale.append(pill);
      }
      const times = el("div", { className: "guide-times" }, el("div", { className: "corner" }), scale);
      // Phones scroll the grid sideways: its width follows the half hours.
      container.style.setProperty("--slots", span / 1800);
      const rows = state.rows.map((r, ri) => {
        const line = el("div", { className: "line" });
        r.programmes.forEach((p, pi) => {
          // The states are classes for the theme: .on-air, .past, .can-replay
          // (in the provider's archive), .recording; .focus follows the focus.
          const replay = p.stop <= now && r.channel.catchup_days && p.start >= now - r.channel.catchup_days * 86400;
          // Title and time each on one line; ↺ (an icon: the glyph is missing
          // from some TV fonts) leads the time of a programme to watch again.
          const b = el("button", { className: "prog focusable" + (p.start <= now && p.stop > now ? " on-air" : "") + (p.stop <= now ? " past" : "") +
            (p.recording ? " recording" : "") + (replay ? " can-replay" : "") },
            el("span", { className: "t" }, p.title),
            el("small", { className: "tnum" }, replay ? el("span", { className: "mark" }, window.VodMaxIcons.icon("rotate-ccw")) : null,
              hhmm(p.start) + "–" + hhmm(p.stop) + (replay ? " · " + t("riguarda") : "")));
          if (p.category) b.dataset.genre = genre(p.category);
          // Provider guides overlap at times: drawn up to the next start, so
          // no programme hides another.
          const next = r.programmes[pi + 1];
          const end = next && next.start < p.stop && next.start > p.start ? next.start : p.stop;
          b.style.left = pct(p.start) + "%";
          b.style.width = "calc(" + (pct(end) - pct(p.start)) + "% - 0.2rem)";
          b.dataset.row = ri;
          b.dataset.i = pi;
          b.dataset.start = p.start;
          b.dataset.stop = p.stop;
          b.dataset.channel = r.channel.id;
          b.dataset.key = r.channel.id + ":" + p.start;
          b.onclick = () => openProgramme(r, p);
          // The mouse reads a programme by resting on it, like the remote.
          b.onpointerenter = () => { if (!state.open) b.focus({ preventScroll: true }); };
          line.append(b);
        });
        // Under the cells: it shows through them without crossing their titles.
        if (now >= from && now < from + span) {
          const n = el("div", { className: "now-line" });
          n.style.left = pct(now) + "%";
          line.prepend(n);
        }
        const name = el("div", { className: "name" }, el("b", { className: "tnum" }, r.channel.number || ""), window.VodMax.channelArt(r.channel),
          el("span", { className: "ch-name" }, r.channel.name),
          r.channel.catchup_days ? el("small", { className: "archive", title: t("Archivio di {days} giorni", { days: r.channel.catchup_days }) }, "↺") : null);
        return el("div", { className: "guide-row" }, name, line);
      });
      // The time scale stays at the top; the rows scroll under it.
      // Nothing in this window: a sentence and the way back to now.
      container.replaceChildren(el("div", { className: "guide-head" }, times), ...(rows.length ? rows : [el("div", { className: "empty guide-empty" },
        window.VodMaxIcons.icon("calendar-days"),
        el("p", {}, state.list === "favorites" ? t("Nessun preferito con la guida in questo orario.") : t("Nessun programma in guida in questo orario.")),
        el("button", { className: "button focusable", onclick: () => jump("now") }, t("Adesso")))]));
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
    // A programme, or in an empty window its "Adesso".
    const firstCell = () => container.querySelector(".prog.on-air") || container.querySelector(".prog") || container.querySelector(".guide-empty .button");
    const cellOf = (row, p) => container.querySelector('.prog[data-key="' + row.channel.id + ":" + p.start + '"]');

    // ---- The programme in the panel ----
    // The cell in focus is described; OK adds its actions (open).
    container.addEventListener("focusout", (e) => { if (e.target.classList) e.target.classList.remove("focus"); });
    container.addEventListener("focusin", (e) => {
      const b = e.target.closest(".prog");
      if (!b) return;
      b.classList.add("focus");
      if (state.open) return;
      const row = state.rows[Number(b.dataset.row)];
      if (row) describe(row, row.programmes[Number(b.dataset.i)], false);
    });
    function describe(row, p, open) {
      const now = Date.now() / 1000;
      state.shown = { row, p };
      state.open = open;
      const archive = row.channel.catchup_days && p.start >= now - row.channel.catchup_days * 86400 && p.start < now;
      const onAir = p.start <= now && p.stop > now;
      // The states as pills after the time: on air, in the archive, recording.
      const pills = [onAir && el("span", { className: "badge live" }, t("In onda")),
        archive && el("span", { className: "badge replay" }, window.VodMaxIcons.icon("rotate-ccw"), t("Rivedibile")),
        p.recording && el("span", { className: "badge rec" }, t("In registrazione"))].filter(Boolean);
      const buttons = [];
      if (open) {
        const replay = () => o.onFromStart(row.channel, p);
        if (onAir) {
          buttons.push(iconButton("button primary focusable", "play", t("Guarda"), () => o.onWatch(row.channel, p)));
          if (archive) buttons.push(iconButton("button secondary focusable", "undo-2", t("Dall'inizio"), replay));
        } else if (archive) {
          buttons.push(iconButton("button primary focusable", "play", t("Riguarda"), replay));
        }
        if (p.recording && !state.kids) {
          buttons.push(el("button", { className: "button secondary focusable", onclick: () => record(row, p) }, t("Annulla la registrazione")));
        } else if (p.stop > now && !state.kids) {
          buttons.push(iconButton("button rec focusable", "circle-dot", t("Registra"), () => record(row, p)));
        }
        buttons.push(el("button", { className: "button secondary focusable", onclick: () => close() }, t("Chiudi")));
      }
      // The guide's picture, else the channel's logo on a tile.
      const tile = () => el("div", { className: "art tile" }, window.VodMax.channelArt(row.channel));
      const plot = [p.subtitle, p.description].filter(Boolean).join(" · ");
      panel.replaceChildren(
        p.icon ? el("img", { className: "art", src: p.icon, alt: "", onerror() { this.replaceWith(tile()); } }) : tile(),
        el("div", { className: "text" },
          el("h2", {}, p.title),
          el("p", { className: "meta" }, el("b", {}, row.channel.name), " · ",
            el("span", { className: "tnum" }, dayName(p.start) + " " + hhmm(p.start) + " – " + hhmm(p.stop)),
            p.category ? " · " + p.category : "", ...pills),
          plot ? el("p", { className: "plot" }, plot) : null,
          open ? el("div", { className: "buttons" }, ...buttons) : null));
      panel.classList.toggle("open", open);
      panel.hidden = false;
      if (open) buttons[0].focus();
    }
    function openProgramme(row, p) { describe(row, p, true); }
    // The actions close; the focus goes back to the cell.
    function close() {
      if (!state.open) return;
      const { row, p } = state.shown;
      describe(row, p, false);
      const b = cellOf(row, p);
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
        state.open = false;
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
        // On phones the grid scrolls sideways: the whole cell into view.
        if (i >= 0 && i < list.length) { list[i].focus(); list[i].scrollIntoView({ block: "nearest", inline: "nearest" }); return; }
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
      if (state.open) {
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
      // The empty state's button: Up goes back to the toolbar.
      if (!inGrid && !lost && container.contains(active)) {
        if (k === "OK") { active.click(); return true; }
        if (k.startsWith("Arrow")) {
          const b = k === "ArrowUp" && toolbar && (toolbar.querySelector("[data-move]") || toolbar.querySelector(".focusable"));
          if (b) b.focus();
          return true;
        }
      }
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
      // Chevrons instead of ◀ ▶: those glyphs are missing from some TV fonts.
      const back = toolbar.querySelector('[data-move="-2"]'), ahead = toolbar.querySelector('[data-move="2"]');
      if (back) { window.VodMax.label(back, "chevron-left", t("2 ore")); back.setAttribute("aria-label", t("2 ore prima")); }
      if (ahead) { window.VodMax.label(ahead, "chevron-right", t("2 ore")); ahead.append(ahead.firstChild); ahead.setAttribute("aria-label", t("2 ore dopo")); }
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
      get open() { return state.open; },
    };
  }

  return { create };
})();
