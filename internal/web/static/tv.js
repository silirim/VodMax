// VodMax TV: live channels driven by a remote control, like a set-top box.
//
// P+/P-           change channel
// Up/Down         browse what is on other channels without changing (OK tunes)
// OK              channel list; Left/Right switch list
// Yellow / F      add or remove favourite
// Red / P         zap through favourites only
// digits          go to channel number
// Info / I        show the banner and the on-screen controls
// Space, ◀ ▶      pause, 10 s back, 30 s forward (within the buffer)
// Green / G       menu (films, series, recordings)
// Guide / E       programme guide over the video (OK tunes, Back closes)
// Blue / R        record the programme on air
// Back            close / cancel; with nothing open, back to the menu
"use strict";

(() => {
  const $ = (id) => document.getElementById(id);
  const { store, api, viewer } = window.VodMax;
  const t = window.VodMaxI18n.t;

  // ---- State ----
  const LISTS = [
    { id: "all", label: t("Tutti"), section: "tv", favorites: false },
    { id: "favorites", label: t("Preferiti"), section: "", favorites: true },
    { id: "sport", label: t("Sport"), section: "sport", favorites: false },
    { id: "radio", label: t("Radio"), section: "radio", favorites: false },
  ];
  const state = {
    channels: [],          // the zapping list
    listIndex: store.get("list", 0),
    favoritesOnly: store.get("favoritesOnly", false),
    current: null,         // channel being watched
    browse: null,          // index shown in the banner while browsing
    overlay: null,         // { list, channels, selected }
    player: null,
    digits: "",
    timers: {},
    retries: {},           // network retries per channel
    endedAt: {},           // when the server last dropped a channel's buffer
    tuneGen: 0,            // bumped by every tune: older answers are ignored
  };

  function zapList() { return state.favoritesOnly ? LISTS[1] : LISTS[state.listIndex === 1 ? 0 : state.listIndex]; }

  async function load(list) {
    const q = new URLSearchParams();
    if (list.section) q.set("section", list.section);
    if (list.favorites) q.set("list", "favorites");
    return api("GET", "/api/v1/tv/channels?" + q);
  }

  async function refreshChannels() {
    state.channels = await load(zapList());
    if (state.favoritesOnly && state.channels.length === 0) {
      state.favoritesOnly = false;
      store.set("favoritesOnly", false);
      flash(t("Nessun preferito: elenco completo"));
      state.channels = await load(zapList());
    }
    if (state.current) {
      const fresh = state.channels.find((c) => c.id === state.current.id);
      if (fresh) state.current = fresh;
    }
  }

  // ---- Playback ----
  // One provider connection per zap: the player opens the relay directly
  // and a refused stream (409, connection busy) is reported as an error.
  // Every tune has a generation: an answer or an error that arrives after a
  // newer zap belongs to a channel already left, and is dropped. Comparing
  // channel objects is not enough, the 60 s refresh replaces them.
  async function tune(channel, force) {
    if (!channel) return;
    const gen = ++state.tuneGen;
    state.current = channel;
    store.set("last", channel.id);
    showBanner(channel, false);
    stopPlayer();
    if (await tuneTimeshift(channel, force, gen)) return;
    let url;
    try {
      url = (await api("POST", "/api/v1/live/" + channel.id + "/ticket", { viewer })).url;
    } catch (e) {
      if (gen === state.tuneGen) flash(e.message);
      return;
    }
    if (gen !== state.tuneGen) return; // zapped meanwhile
    if (!window.mpegts || !mpegts.isSupported()) {
      flash(t("Questo dispositivo non supporta la riproduzione MPEG-TS nel browser"));
      return;
    }
    const player = window.VodMax.tsPlayer($("video"), url + (force ? "?force=1" : ""), {
      live: true,
      onStart: () => { delete state.retries[channel.id]; },
      // The stream closed: another device took the connection, or the
      // provider stopped it. Tuning again says which (it asks to take the
      // connection back when it is busy).
      onEnd: () => {
        if (state.player !== player) return;
        stopPlayer();
        const attempt = (state.retries[channel.id] || 0) + 1;
        state.retries[channel.id] = attempt;
        if (attempt > 2) { flash(t("La diretta si è interrotta")); return; }
        flash(t("La diretta si è interrotta: la riapro…"));
        retune(gen, 1500);
      },
      onSilent: () => {
        if (state.player === player) flash(t("Il canale non manda immagini: riprova o scegli un altro canale"), true);
      },
      onError: (type, detail, info) => {
        if (state.player !== player) return;
        stopPlayer();
        const code = info && info.code;
        if (code === 423) { flash(t("Tutte le connessioni dell'abbonamento sono occupate da una registrazione in corso")); return; }
        if (code === 409) {
          confirmDialog(t("Tutte le connessioni dell'abbonamento sono in uso su un altro dispositivo.\n\nPrendere il controllo?"),
            () => tune(channel, true));
          return;
        }
        // Only network failures are worth retrying; a format the device
        // cannot decode would just reopen the channel at the provider.
        const attempt = (state.retries[channel.id] || 0) + 1;
        if (type !== mpegts.ErrorTypes.NETWORK_ERROR || attempt > 3) {
          flash(type === mpegts.ErrorTypes.MEDIA_ERROR
            ? t("Questo dispositivo non riesce a decodificare il canale ({detail})", { detail })
            : t("Canale non disponibile ({code})", { code: code || detail }));
          return;
        }
        state.retries[channel.id] = attempt;
        flash(t("Flusso interrotto: nuovo tentativo {n} di 3…", { n: attempt }));
        retune(gen, 1500 * attempt);
      },
    });
    state.player = player;
    showControls();
  }

  // The same channel again after a while, unless the viewer zapped.
  function retune(gen, ms) {
    state.timers.retry = setTimeout(() => gen === state.tuneGen && tune(state.current), ms);
  }

  function stopPlayer() {
    clearTimeout(state.timers.retry);
    clearInterval(state.timers.keepalive);
    if (state.player) {
      state.player.destroy();
      state.player = null;
    }
    if (state.buffer) {
      navigator.sendBeacon(state.buffer.stop);
      state.buffer = null;
    }
  }

  // ---- Live with pause and rewind (server buffer, HLS) ----
  let liveSettings;
  const canHLS = () => (window.Hls && Hls.isSupported()) || $("video").canPlayType("application/vnd.apple.mpegurl") !== "";
  // Returns false when the direct stream must be used instead, true when
  // handled (or superseded by a newer tune).
  async function tuneTimeshift(channel, force, gen) {
    if (!liveSettings) liveSettings = await api("GET", "/api/v1/settings/live").catch(() => ({ mode: "direct" }));
    if (gen !== state.tuneGen) return true;
    if (liveSettings.mode !== "timeshift" || !canHLS()) return false;
    flash(t("Sintonizzazione…"));
    let buf;
    try {
      buf = await api("POST", "/api/v1/live/" + channel.id + "/timeshift", { viewer, force: Boolean(force) });
    } catch (e) {
      // A newer zap replaced this open (the server says so with an error).
      if (gen !== state.tuneGen) return true;
      if (e.status === 423) { flash(e.message); return true; }
      if (e.status === 409) {
        confirmDialog(t("Tutte le connessioni dell'abbonamento sono in uso su un altro dispositivo.\n\nPrendere il controllo?"),
          () => tune(channel, true));
        return true;
      }
      if (e.status === 503) return false; // no buffer on this server now: direct stream
      flash(e.message);
      return true;
    }
    if (gen !== state.tuneGen) { navigator.sendBeacon(buf.stop); return true; } // zapped meanwhile
    $("status").hidden = true;
    state.buffer = buf;
    const player = window.VodMax.hlsPlayer($("video"), buf.url, {
      onError: (type) => {
        if (state.player !== player) return;
        if (type === "ended") {
          // The server dropped the buffer (another device took the
          // connection, or a recording): open the channel again. Not from
          // a tab in the background (a film in another tab of this browser
          // took the connection: it would take it back), and not again
          // within 30 s, or two tabs would keep taking it from each other.
          stopPlayer();
          const last = state.endedAt[channel.id] || 0;
          state.endedAt[channel.id] = Date.now();
          if (document.hidden) {
            flash(t("La diretta si è interrotta"));
            addEventListener("visibilitychange", () => { if (gen === state.tuneGen && !document.hidden) tune(state.current); }, { once: true });
            return;
          }
          if (Date.now() - last < 30000) { flash(t("La diretta si è interrotta")); return; }
          flash(t("Diretta interrotta: riapro il canale…"));
          retune(gen, 2000);
          return;
        }
        flash(t("Riproduzione non riuscita ({type})", { type }));
      },
    });
    state.player = player;
    state.timers.keepalive = setInterval(() => { fetch(buf.keepalive, { method: "POST" }).catch(() => {}); }, 30000);
    showControls();
    return true;
  }
  addEventListener("pagehide", () => { if (state.buffer) navigator.sendBeacon(state.buffer.stop); });

  function step(delta) {
    const list = state.channels;
    if (list.length === 0) return;
    let i = list.findIndex((c) => state.current && c.id === state.current.id);
    if (i < 0 && state.current) {
      // A channel typed by number from another list: the nearest number.
      const n = state.current.number;
      let j = -1; // findLastIndex is too new for webOS 22
      if (delta > 0) j = list.findIndex((c) => c.number > n);
      else for (let k = list.length - 1; k >= 0 && j < 0; k--) if (list[k].number < n) j = k;
      tune(list[j >= 0 ? j : delta > 0 ? 0 : list.length - 1]);
      return;
    }
    i = i < 0 ? 0 : (i + delta + list.length) % list.length;
    tune(list[i]);
  }

  // ---- Banner and browsing ----
  const pad = (n) => String(n).padStart(2, "0");
  const hhmm = (t) => { const d = new Date(t * 1000); return pad(d.getHours()) + ":" + pad(d.getMinutes()); };

  function showBanner(channel, browsing) {
    if (!channel) return;
    const banner = $("banner");
    banner.classList.toggle("browsing", browsing);
    $("banner-number").textContent = channel.number;
    $("banner-name").textContent = channel.name;
    const logo = $("banner-logo");
    logo.hidden = !channel.logo;
    if (channel.logo) logo.src = channel.logo;
    const badges = $("banner-badges");
    badges.replaceChildren();
    const quality = { 4: "4K", 3: "FHD", 2: "HD", 1: "SD" }[channel.quality];
    for (const [text, cls] of [[quality, ""], [channel.favorite ? "★ " + t("Preferito") : "", "fav"],
      [channel.catchup_days ? t("Replay {days}g", { days: channel.catchup_days }) : "", ""]]) {
      if (!text) continue;
      const span = document.createElement("span");
      span.textContent = text;
      if (cls) span.className = cls;
      badges.append(span);
    }
    $("banner-mode").textContent = state.favoritesOnly ? t("Solo preferiti") : "";
    $("banner-mode").hidden = !state.favoritesOnly;
    const now = channel.now, next = channel.next;
    $("banner-now").textContent = now ? now.title : t("Nessuna informazione sul programma");
    $("banner-now-time").textContent = now ? hhmm(now.start) + " – " + hhmm(now.stop) : "";
    const pct = now ? Math.min(100, Math.max(0, (Date.now() / 1000 - now.start) / (now.stop - now.start) * 100)) : 0;
    $("banner-progress").style.width = pct + "%";
    $("banner-next").textContent = next ? next.title : "";
    $("banner-next-time").textContent = next ? hhmm(next.start) : "";
    $("banner-hint").textContent = browsing ? t("OK per guardare · Indietro per tornare a {name}", { name: state.current.name }) : "";
    banner.hidden = false;
    clearTimeout(state.timers.banner);
    state.timers.banner = setTimeout(endBrowse, browsing ? 8000 : 5000);
  }

  function endBrowse() {
    state.browse = null;
    $("banner").hidden = true;
  }

  function browseStep(delta) {
    const list = state.channels;
    if (list.length === 0) return;
    if (state.browse === null) {
      const i = list.findIndex((c) => state.current && c.id === state.current.id);
      state.browse = i < 0 ? 0 : i;
    }
    state.browse = (state.browse + delta + list.length) % list.length;
    showBanner(list[state.browse], true);
  }

  // ---- Channel list overlay ----
  // With the remote a window of rows moves with the selection; on touch
  // screens the whole list scrolls and a tap picks the channel.
  const touch = window.matchMedia("(pointer: coarse), (max-width: 800px)");
  async function openList(listIndex) {
    hideControls();
    const list = LISTS[listIndex];
    let channels;
    try { channels = await load(list); } catch (e) { flash(e.message); return; }
    let selected = channels.findIndex((c) => state.current && c.id === state.current.id);
    state.overlay = { listIndex, channels, selected: selected < 0 ? 0 : selected };
    renderList();
    $("list").hidden = false;
    $("banner").hidden = true;
    const sel = $("list-items").querySelector(".selected");
    if (touch.matches && sel) sel.scrollIntoView({ block: "center" });
  }

  // The selected channel of the list: watch it, and zap in that list.
  function pickFromList() {
    const o = state.overlay;
    const c = o && o.channels[o.selected];
    if (!c) return;
    if (o.listIndex === 1) { state.favoritesOnly = true; }
    else { state.favoritesOnly = false; state.listIndex = o.listIndex; }
    store.set("favoritesOnly", state.favoritesOnly);
    store.set("list", state.listIndex);
    closeList();
    refreshChannels().then(() => tune(c));
  }

  function renderList() {
    const o = state.overlay;
    const tabs = $("list-tabs");
    tabs.replaceChildren(...LISTS.map((l, i) => {
      const s = document.createElement("span");
      s.textContent = l.label;
      if (i === o.listIndex) s.className = "active";
      s.onclick = () => { if (i !== o.listIndex) openList(i); };
      return s;
    }));
    if (touch.matches) {
      const close = document.createElement("span");
      close.className = "close";
      close.textContent = "✕";
      close.setAttribute("aria-label", t("Chiudi"));
      close.onclick = closeList;
      tabs.append(close);
    }
    const ol = $("list-items");
    if (o.channels.length === 0) {
      const li = document.createElement("li");
      li.textContent = o.listIndex === 1 ? t("Nessun preferito: premi Giallo su un canale per aggiungerlo.") : t("Nessun canale.");
      ol.replaceChildren(li);
      return;
    }
    // Remote: a window around the selection (lists can hold hundreds of
    // channels). Touch: all of them, the browser scrolls.
    const rows = touch.matches ? o.channels.length : o.rows || Math.max(5, Math.floor((window.innerHeight * 0.8) / 64));
    const start = Math.max(0, Math.min(o.selected - Math.floor(rows / 2), o.channels.length - rows));
    const items = o.channels.slice(start, start + rows).map((c, k) => {
      const li = document.createElement("li");
      li.onclick = () => { o.selected = start + k; pickFromList(); };
      if (start + k === o.selected) li.classList.add("selected");
      if (state.current && c.id === state.current.id) li.classList.add("current");
      const n = document.createElement("span"); n.className = "n"; n.textContent = c.number;
      const logo = document.createElement("img"); logo.className = "l"; logo.alt = ""; logo.loading = "lazy";
      if (c.logo) { logo.src = c.logo; logo.onerror = () => { logo.style.visibility = "hidden"; }; } else logo.style.visibility = "hidden";
      const name = document.createElement("span"); name.className = "c"; name.textContent = c.name;
      if (c.favorite) { const st = document.createElement("span"); st.className = "star"; st.textContent = "★"; name.append(st); }
      const p = document.createElement("span"); p.className = "p";
      p.textContent = c.now ? hhmm(c.now.start) + " " + c.now.title + (c.next ? "  ·  " + hhmm(c.next.start) + " " + c.next.title : "") : "";
      li.append(n, logo, name, p);
      return li;
    });
    ol.replaceChildren(...items);
    // Rows are taller on big screens than the first guess: as many as fit
    // above the hint, so the selection is never hidden behind it.
    if (!touch.matches && !o.rows && items.length) {
      const hint = $("list").querySelector(".hint");
      const bottom = hint && hint.offsetParent ? hint.getBoundingClientRect().top : $("list").getBoundingClientRect().bottom;
      const top = ol.getBoundingClientRect().top, h = items[0].getBoundingClientRect().height;
      if (h > 0) {
        o.rows = Math.max(3, Math.floor((bottom - top) / h));
        if (o.rows < rows) renderList();
      }
    }
  }

  function closeList() {
    state.overlay = null;
    $("list").hidden = true;
  }

  // ---- The guide over the video (decision 0053) ----
  // The grid of guide-grid.js covers the screen; the video shrinks into a
  // window at the top right (tv.css, body.guide-on) and keeps playing. OK
  // on a programme on air tunes its channel; Back closes and gives the
  // focus back to what had it (the controls, or nothing).
  let guide = null, guideBack = null;
  const guideOpen = () => !$("guide-overlay").hidden;
  function openGuide() {
    if (!guide) {
      const bar = $("guide-bar");
      const close = document.createElement("button");
      close.className = "tab focusable guide-close";
      close.textContent = "✕";
      close.setAttribute("aria-label", t("Chiudi"));
      close.dataset.close = "";
      bar.append(close);
      guide = window.GuideGrid.create($("guide-grid"), {
        toolbar: bar, panel: $("guide-panel"), flash,
        onWatch: (c) => {
          closeGuide();
          if (!state.current || c.id !== state.current.id) tune(state.channels.find((x) => x.id === c.id) || c);
        },
        onFromStart: (c, p) => {
          stopPlayer();
          location.href = "/?" + new URLSearchParams({ catchup: c.id, start: p.start, stop: p.stop, title: p.title, back: "/tv.html?channel=" + c.id });
        },
        onClose: closeGuide,
      });
    }
    guideBack = { focus: document.activeElement, controls: !$("controls").hidden };
    closeList();
    endBrowse();
    hideControls();
    $("guide-overlay").hidden = false;
    document.body.classList.add("guide-on");
    guide.show(state.current && state.current.id);
  }
  function closeGuide() {
    if (!guideOpen()) return;
    guide.close();
    $("guide-overlay").hidden = true;
    document.body.classList.remove("guide-on");
    const back = guideBack;
    guideBack = null;
    if (document.activeElement) document.activeElement.blur();
    if (back && back.controls) {
      showControls();
      if (back.focus && $("controls").contains(back.focus)) back.focus.focus();
    }
  }

  // ---- Favourites ----
  async function toggleFavorite(channel) {
    if (!channel) return;
    try {
      await api("PUT", "/api/v1/tv/favorites/" + channel.id, { favorite: !channel.favorite });
    } catch (e) { flash(e.message); return; }
    channel.favorite = !channel.favorite;
    flash(channel.favorite ? t("Aggiunto ai preferiti: {name}", { name: channel.name }) : t("Tolto dai preferiti: {name}", { name: channel.name }));
    await refreshChannels();
    if (state.overlay) {
      const keep = state.overlay.channels[state.overlay.selected];
      state.overlay.channels = await load(LISTS[state.overlay.listIndex]);
      const i = state.overlay.channels.findIndex((c) => keep && c.id === keep.id);
      state.overlay.selected = Math.max(0, Math.min(i < 0 ? state.overlay.selected : i, state.overlay.channels.length - 1));
      renderList();
    }
  }

  async function toggleFavoritesOnly() {
    state.favoritesOnly = !state.favoritesOnly;
    store.set("favoritesOnly", state.favoritesOnly);
    await refreshChannels();
    flash(state.favoritesOnly ? t("P+/P− scorrono solo i preferiti") : t("P+/P− scorrono tutti i canali"));
  }

  // From the start of the programme on air: in the buffer if it is
  // there, otherwise from the provider's archive.
  function startOver() {
    const c = state.current;
    const p = c && c.now;
    if (!p) { flash(t("Nessun programma in guida per questo canale")); return; }
    if (state.player && state.player.seekToTime && state.player.seekToTime(p.start - 30)) { showControls(); return; }
    if (c.catchup_days) {
      stopPlayer();
      location.href = "/?" + new URLSearchParams({ catchup: c.id, start: p.start, stop: p.stop, title: p.title, back: "/tv.html?channel=" + c.id });
      return;
    }
    flash(t("L'inizio non è più nel buffer e questo canale non ha l'archivio del provider"));
  }

  // ---- On-screen controls ----
  function showControls() {
    if (guideOpen()) return; // the guide covers them
    const ctl = $("controls");
    const p = state.player;
    const c = state.current;
    VodMax.label($("ctl-toggle"), p && p.paused ? "play" : "pause", p && p.paused ? t("Riprendi") : t("Pausa"));
    const behind = p ? p.behind() : 0;
    const live = $("ctl-live");
    live.classList.toggle("behind", behind > 12);
    VodMax.label(live, behind > 12 ? "skip-forward" : "radio", behind > 12 ? t("Torna in diretta (−{time})", { time: VodMax.clock(behind) }) : t("In diretta"));
    const fav = $("ctl-fav");
    fav.classList.toggle("on", Boolean(c && c.favorite));
    VodMax.label(fav, "star", c && c.favorite ? t("Preferito") + " ✓" : t("Preferito"));
    ctl.hidden = false;
    document.body.classList.add("controls-on");
    updateTimeline();
    clearTimeout(state.timers.controls);
    // They stay on screen only while the viewer has paused (the way back
    // to playing); a video still loading counts as playing.
    state.timers.controls = setTimeout(function hide() {
      if (state.player && state.player.userPaused) { state.timers.controls = setTimeout(hide, 1000); return; }
      hideControls();
    }, 5000);
  }
  // The bar with the keyboard focus on Pause / Riprendi.
  function focusControls() {
    showControls();
    $("ctl-toggle").focus();
  }
  function hideControls() {
    clearTimeout(state.timers.controls);
    $("controls").hidden = true;
    $("timeline").hidden = true;
    document.body.classList.remove("controls-on");
  }

  const actions = {
    menu: () => { stopPlayer(); location.href = "/"; },
    list: () => { hideControls(); openList(state.favoritesOnly ? 1 : state.listIndex); },
    back10: () => { state.player && state.player.seek(-10); showControls(); },
    fwd30: () => { state.player && state.player.seek(30); showControls(); },
    toggle: () => { state.player && state.player.toggle(); showControls(); },
    live: () => { state.player && state.player.goLive(); showControls(); },
    startOver: () => startOver(),
    guide: () => openGuide(),
    fav: () => toggleFavorite(state.current).then(showControls),
    rec: () => record(state.current),
    multiview: () => { stopPlayer(); location.href = "/multiview.html" + (state.current ? "?channel=" + state.current.id : ""); },
    fullscreen: () => {
      if (document.fullscreenElement) { document.exitFullscreen().catch(() => {}); return; }
      document.documentElement.requestFullscreen({ navigationUI: "hide" }).then(() => {
        if (screen.orientation && screen.orientation.lock && matchMedia("(pointer: coarse)").matches) screen.orientation.lock("landscape").catch(() => {});
      }).catch(() => {});
    },
  };
  // Full screen where the browser allows it (phones, PCs); the TV app
  // already fills the screen.
  $("ctl-full").hidden = !(document.fullscreenEnabled && document.documentElement.requestFullscreen) || /Web0S|webOS/.test(navigator.userAgent);
  document.addEventListener("fullscreenchange", () => {
    const full = Boolean(document.fullscreenElement);
    const text = full ? t("Esci dallo schermo intero") : t("Schermo intero");
    const span = document.createElement("span");
    span.className = "txt";
    span.textContent = text;
    $("ctl-full").replaceChildren(VodMaxIcons.icon(full ? "minimize" : "maximize"), span);
    $("ctl-full").title = text;
    $("ctl-full").setAttribute("aria-label", text);
  });
  $("controls").addEventListener("click", (e) => {
    const b = e.target.closest("button[data-action]");
    if (b) actions[b.dataset.action]();
    else if (e.target === $("controls")) hideControls(); // a tap on the picture
  });
  // Mouse: moving shows the controls, a click pauses. Touch: a tap shows
  // or hides them (pausing is the button in the middle).
  let lastPointer = "mouse";
  document.addEventListener("pointerdown", (e) => { lastPointer = e.pointerType; }, true);
  $("screen").addEventListener("pointermove", (e) => { if (e.pointerType === "mouse" && !state.overlay) showControls(); });
  $("video").addEventListener("click", () => {
    if (guideOpen()) { closeGuide(); return; } // the small window: back to full screen
    if (state.overlay) return;
    if (lastPointer === "mouse") actions.toggle();
    else if ($("controls").hidden) showControls();
    else hideControls();
  });
  // ---- Timeline: the programme on air, from its start to its end ----
  // Where the picture is (wall clock), how far back the buffer goes, how
  // much has aired. A click moves there: in the buffer, or from the
  // provider's archive before it, or back to live after it.
  function timelineState() {
    const c = state.current, p = c && c.now, player = state.player;
    const now = Date.now() / 1000;
    if (!p || p.stop <= now || p.stop <= p.start) return null;
    const behind = player ? player.behind() : 0;
    const clock = player && player.clockTime && player.clockTime();
    const at = clock ? Math.min(now, clock.getTime() / 1000) : now - behind;
    let back = 0;
    if (player && player.back) back = player.back();
    else {
      const v = $("video"), b = v.buffered;
      if (b.length) back = Math.max(0, v.currentTime - b.start(0));
    }
    return { c, p, now, at, behind: Math.max(0, now - at), from: at - back };
  }
  function updateTimeline() {
    const tl = $("timeline"), st = timelineState();
    if (!st || $("controls").hidden) { tl.hidden = true; return; }
    const { p, now, at, behind, from } = st;
    const span = p.stop - p.start;
    const f = (t) => Math.min(100, Math.max(0, (t - p.start) / span * 100));
    $("tl-title").textContent = p.title;
    $("tl-start").textContent = hhmm(p.start);
    $("tl-end").textContent = hhmm(p.stop);
    $("tl-aired").style.width = f(now) + "%";
    $("tl-buffer").style.left = f(from) + "%";
    $("tl-buffer").style.width = Math.max(0, f(now) - f(from)) + "%";
    $("tl-fill").style.width = f(at) + "%";
    $("tl-pos").style.left = f(at) + "%";
    const late = behind > 12;
    $("tl-state").classList.toggle("behind", late);
    $("tl-state").textContent = late ? t("−{time} dalla diretta", { time: VodMax.clock(behind) }) : t("In diretta");
    tl.hidden = false;
  }
  {
    const bar = $("tl-bar");
    const timeAt = (e) => {
      const st = timelineState();
      if (!st) return null;
      const r = bar.getBoundingClientRect();
      const x = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
      return { st, x, time: st.p.start + x * (st.p.stop - st.p.start) };
    };
    bar.addEventListener("pointermove", (e) => {
      const h = timeAt(e), tip = $("tl-hover");
      if (!h) return;
      tip.textContent = h.time > h.st.now ? t("In diretta") : hhmm(h.time);
      tip.style.left = h.x * 100 + "%";
      tip.hidden = false;
      showControls();
    });
    bar.addEventListener("pointerleave", () => { $("tl-hover").hidden = true; });
    bar.addEventListener("click", (e) => {
      const h = timeAt(e), player = state.player;
      if (!h || !player) return;
      const { st, time } = h;
      if (time >= st.now - 8) player.goLive();
      else if (player.seekToTime && player.seekToTime(time)) { /* in the buffer */ }
      else if (time >= st.from) player.seek(time - st.at);
      else if (st.c.catchup_days) {
        stopPlayer();
        location.href = "/?" + new URLSearchParams({ catchup: st.c.id, start: Math.floor(time), stop: st.p.stop, title: st.p.title, back: "/tv.html?channel=" + st.c.id });
        return;
      } else flash(t("Questo punto non è più nel buffer e il canale non ha l'archivio del provider"));
      showControls();
    });
  }

  // Keep the "behind live" label current while visible.
  setInterval(() => { if (!$("controls").hidden) {
    updateTimeline();
    const p = state.player, behind = p ? p.behind() : 0;
    $("ctl-live").classList.toggle("behind", behind > 12);
    VodMax.label($("ctl-live"), behind > 12 ? "skip-forward" : "radio", behind > 12 ? t("Torna in diretta (−{time})", { time: VodMax.clock(behind) }) : t("In diretta"));
  } }, 1000);

  // ---- Recording ----
  async function record(channel) {
    if (!channel) return;
    try {
      const r = await api("POST", "/api/v1/recordings", { channel_id: channel.id });
      flash(t("Registrazione di “{title}” fino alle {time} (Verde → Registrazioni)", { title: r.title, time: hhmm(r.stop) }));
    } catch (e) { flash(e.message); }
  }

  // ---- Direct number entry ----
  function digit(d) {
    state.digits = (state.digits + d).slice(-4);
    $("digits").textContent = state.digits;
    $("digits").hidden = false;
    clearTimeout(state.timers.digits);
    state.timers.digits = setTimeout(commitDigits, 1500);
  }

  async function commitDigits() {
    const n = parseInt(state.digits, 10);
    state.digits = "";
    $("digits").hidden = true;
    clearTimeout(state.timers.digits);
    if (!n) return;
    let channel = state.channels.find((c) => c.number === n);
    if (!channel) {
      const all = await load({ section: "", favorites: false }).catch(() => []);
      channel = all.find((c) => c.number === n);
    }
    if (channel) tune(channel); else flash(t("Canale {n} non presente", { n }));
  }

  // ---- Messages ----
  // keep: stays until the next message (nothing else explains a black screen).
  function flash(text, keep) {
    const el = $("status");
    el.textContent = text;
    el.hidden = false;
    clearTimeout(state.timers.status);
    if (!keep) state.timers.status = setTimeout(() => { el.hidden = true; }, 3500);
  }

  let dialogAction = null;
  function confirmDialog(text, action) {
    $("dialog-text").textContent = text;
    $("dialog").hidden = false;
    dialogAction = action;
  }
  function closeDialog(confirmed) {
    $("dialog").hidden = true;
    const a = dialogAction;
    dialogAction = null;
    if (confirmed && a) a();
  }
  $("dialog-yes").onclick = () => closeDialog(true);
  $("dialog-no").onclick = () => closeDialog(false);

  // Play only plays, Pause only pauses; Play/Pause and Space alternate.
  function playKey(key) {
    const paused = state.player && state.player.paused;
    if ((key === "Play" && !paused) || (key === "Pause" && paused)) { showControls(); return; }
    actions.toggle();
  }

  // ---- Keys: browser, LG webOS and Android TV ----
  function keyOf(e) {
    const byCode = { 33: "ChannelUp", 34: "ChannelDown", 403: "Red", 404: "Green", 405: "Yellow", 406: "Blue",
      457: "Info", 458: "Guide", 461: "Back", 427: "ChannelUp", 428: "ChannelDown",
      415: "Play", 19: "Pause", 10252: "PlayPause", 179: "PlayPause", 93: "Info" };
    if (byCode[e.keyCode]) return byCode[e.keyCode];
    const byKey = { PageUp: "ChannelUp", PageDown: "ChannelDown", "+": "ChannelUp", "-": "ChannelDown",
      ColorF0Red: "Red", ColorF1Green: "Green", ColorF2Yellow: "Yellow", ColorF3Blue: "Blue",
      Escape: "Back", Backspace: "Back", BrowserBack: "Back", GoBack: "Back", Enter: "OK",
      MediaPlayPause: "PlayPause", MediaPlay: "Play", MediaPause: "Pause", ContextMenu: "Info", Info: "Info",
      f: "Yellow", F: "Yellow", p: "Red", P: "Red", i: "Info", I: "Info", g: "Green", G: "Green", r: "Blue", R: "Blue", e: "Guide", E: "Guide" };
    return byKey[e.key] || e.key;
  }

  // Browsers block autoplay with sound until the user interacts: any key
  // or click resumes a paused channel.
  function resumeIfBlocked() {
    if (state.player) state.player.resumeIfBlocked();
  }
  document.addEventListener("click", resumeIfBlocked);

  document.addEventListener("keydown", (e) => {
    if (document.querySelector(".setup-panel")) return; // pairing or profile choice on screen
    resumeIfBlocked();
    const key = keyOf(e);
    let handled = true;

    if (!$("dialog").hidden) {
      if (key === "OK") closeDialog(true);
      else if (key === "Back") closeDialog(false);
      e.preventDefault();
      return;
    }

    // The guide over the video: the grid takes the arrows, OK and Back;
    // P+/P- and the digits zap the small window; Guide closes it again.
    if (guideOpen()) {
      if (guide.key(e)) { e.preventDefault(); return; }
      if (/^[0-9]$/.test(key)) digit(key);
      else if (state.digits && key === "OK") commitDigits();
      else switch (key) {
        case "ChannelUp": step(1); break;
        case "ChannelDown": step(-1); break;
        case "Guide": case "Back": closeGuide(); break;
        case "OK": if (document.activeElement) document.activeElement.click(); break;
        default: handled = false;
      }
      if (handled) e.preventDefault();
      return;
    }

    // In the controls bar (at the top): the arrows move along it, OK
    // presses, Down and Back leave it.
    const bar = $("controls");
    let passOn = false;
    if (!bar.hidden && bar.contains(document.activeElement)) {
      const buttons = [...bar.querySelectorAll("button")].filter((b) => b.offsetParent);
      const i = buttons.indexOf(document.activeElement);
      if (key === "ArrowLeft" || key === "ArrowRight") {
        const next = buttons[Math.min(buttons.length - 1, Math.max(0, i + (key === "ArrowRight" ? 1 : -1)))];
        if (next) next.focus();
        showControls();
      } else if (key === "OK") {
        document.activeElement.click();
        if (!bar.hidden && !bar.contains(document.activeElement) && buttons[i]) buttons[i].focus();
      } else if (key === "Back" || key === "ArrowDown") {
        document.activeElement.blur();
        // While paused it stays: it is the way back to playing.
        if (!(state.player && state.player.userPaused)) hideControls();
      } else if (key === "ArrowUp") {
        // already at the top
      } else if (key === " " || key === "PlayPause" || key === "Play" || key === "Pause") {
        playKey(key);
      } else if (["Tab", "Shift", "Control", "Alt", "Meta"].includes(key)) {
        return;
      } else {
        // Any other key (P+, numbers, colours) does its job: the bar closes.
        document.activeElement.blur();
        hideControls();
        passOn = true;
      }
      if (!passOn) { e.preventDefault(); return; }
    }

    if (/^[0-9]$/.test(key)) { if (state.overlay) closeList(); digit(key); e.preventDefault(); return; }
    if (state.digits && key === "OK") { commitDigits(); e.preventDefault(); return; }

    const o = state.overlay;
    if (o) {
      switch (key) {
        case "ArrowUp": o.selected = Math.max(0, o.selected - 1); renderList(); break;
        case "ArrowDown": o.selected = Math.min(o.channels.length - 1, o.selected + 1); renderList(); break;
        case "ChannelUp": o.selected = Math.max(0, o.selected - 10); renderList(); break;
        case "ChannelDown": o.selected = Math.min(o.channels.length - 1, o.selected + 10); renderList(); break;
        case "ArrowLeft": openList((o.listIndex + LISTS.length - 1) % LISTS.length); break;
        case "ArrowRight": openList((o.listIndex + 1) % LISTS.length); break;
        case "OK": pickFromList(); break;
        case "Yellow": toggleFavorite(o.channels[o.selected]); break;
        case "Blue": record(o.channels[o.selected]); break;
        case "Back": closeList(); break;
        default: handled = false;
      }
    } else {
      switch (key) {
        // P+ goes to the next number up, as on a set-top box.
        case "ChannelUp": endBrowse(); step(1); break;
        case "ChannelDown": endBrowse(); step(-1); break;
        // With the controls on screen (after a pause or a skip), Up and
        // Down go into them; otherwise they browse the channels.
        case "ArrowUp":
        case "ArrowDown":
          if (!$("controls").hidden && state.browse === null) focusControls();
          else browseStep(key === "ArrowUp" ? -1 : 1);
          break;
        case "OK":
          if (state.browse !== null) { const c = state.channels[state.browse]; endBrowse(); tune(c); }
          else openList(state.favoritesOnly ? 1 : state.listIndex);
          break;
        case "Back":
          // Closes the banner first; with nothing open, back to the menu.
          if (state.browse !== null || !$("banner").hidden) endBrowse();
          else { stopPlayer(); location.href = "/"; }
          break;
        case "Yellow": toggleFavorite(state.browse !== null ? state.channels[state.browse] : state.current); break;
        case "Red": toggleFavoritesOnly(); break;
        case "Green": stopPlayer(); location.href = "/"; break;
        case "Guide": openGuide(); break;
        case "Blue": record(state.browse !== null ? state.channels[state.browse] : state.current); break;
        case "Info": showBanner(state.current, false); focusControls(); break;
        case " ": case "PlayPause": case "Play": case "Pause": playKey(key); break;
        case "ArrowLeft": actions.back10(); break;
        case "ArrowRight": actions.fwd30(); break;
        default: handled = false;
      }
    }
    if (handled) e.preventDefault();
  });

  $("banner-logo").onerror = () => { $("banner-logo").hidden = true; };

  // Keep "now / next" fresh.
  setInterval(() => { refreshChannels().catch(() => {}); }, 60000);

  // ---- Start ----
  async function start() {
    // No radio channels: no Radio list (nothing behind it).
    const radio = await load({ section: "radio" }).catch(() => []);
    if (!radio.length) {
      LISTS.splice(LISTS.findIndex((l) => l.id === "radio"), 1);
      if (state.listIndex >= LISTS.length) { state.listIndex = 0; store.set("list", 0); }
    }
    try { await refreshChannels(); } catch (e) { if (!e.handled) flash(e.message); return; }
    if (state.channels.length === 0) { flash(t("Nessun canale: sincronizza un abbonamento")); return; }
    // From the guide: /tv.html?channel=ID.
    const asked = Number(new URLSearchParams(location.search).get("channel"));
    const last = asked || store.get("last", null);
    let channel = state.channels.find((c) => c.id === last);
    if (!channel && last) {
      // A sport or radio channel: zap in its own list from now on.
      const all = await load({ section: "" }).catch(() => []);
      const found = all.find((c) => c.id === last);
      const i = found ? LISTS.findIndex((l) => l.section === found.section) : -1;
      if (i >= 0) {
        state.listIndex = i;
        store.set("list", i);
        if (state.favoritesOnly && !found.favorite) { state.favoritesOnly = false; store.set("favoritesOnly", false); }
        await refreshChannels();
        channel = state.channels.find((c) => c.id === last);
      }
    }
    tune(channel || state.channels[0]);
  }

  window.VodMax.withToken(start);

  // The Android app's small window: it opens on leaving the app while the
  // video plays, and shows the picture alone.
  if (window.VodMax.native && window.VodMax.native.pip) {
    const video = $("video");
    const report = () => window.VodMax.native.pip(!video.paused && !video.ended, video.videoWidth, video.videoHeight);
    for (const e of ["playing", "pause", "ended", "emptied", "resize"]) video.addEventListener(e, report);
    window.VodMaxNativePip = (on) => document.body.classList.toggle("pip", on);
    window.VodMaxNativePipToggle = () => actions.toggle();
  }

  // Android "Back" goes through the page's own Back handling.
  window.VodMaxNativeBack = () => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    return true;
  };
})();
