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
// Guide / E       programme guide
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
  async function tune(channel, force) {
    if (!channel) return;
    state.current = channel;
    store.set("last", channel.id);
    showBanner(channel, false);
    stopPlayer();
    if (await tuneTimeshift(channel, force)) return;
    let url;
    try {
      url = (await api("POST", "/api/v1/live/" + channel.id + "/ticket", { viewer })).url;
    } catch (e) {
      flash(e.message);
      return;
    }
    if (state.current !== channel) return; // zapped meanwhile
    if (!window.mpegts || !mpegts.isSupported()) {
      flash(t("Questo dispositivo non supporta la riproduzione MPEG-TS nel browser"));
      return;
    }
    const player = window.VodMax.tsPlayer($("video"), url + (force ? "?force=1" : ""), {
      live: true,
      onStart: () => { delete state.retries[channel.id]; },
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
        state.timers.retry = setTimeout(() => state.current === channel && tune(channel), 1500 * attempt);
      },
    });
    state.player = player;
    showControls();
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
  // Returns false when the direct stream must be used instead.
  async function tuneTimeshift(channel, force) {
    if (!liveSettings) liveSettings = await api("GET", "/api/v1/settings/live").catch(() => ({ mode: "direct" }));
    if (liveSettings.mode !== "timeshift" || !canHLS()) return false;
    flash(t("Sintonizzazione…"));
    let buf;
    try {
      buf = await api("POST", "/api/v1/live/" + channel.id + "/timeshift", { viewer, force: Boolean(force) });
    } catch (e) {
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
    if (state.current !== channel) { navigator.sendBeacon(buf.stop); return true; } // zapped meanwhile
    $("status").hidden = true;
    state.buffer = buf;
    const player = window.VodMax.hlsPlayer($("video"), buf.url, {
      onError: (type) => {
        if (state.player !== player) return;
        if (type === "ended") {
          // The server dropped the buffer (another device took the
          // connection, or a recording): open the channel again.
          stopPlayer();
          flash(t("Diretta interrotta: riapro il canale…"));
          state.timers.retry = setTimeout(() => state.current === channel && tune(channel), 2000);
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
    const rows = touch.matches ? o.channels.length : Math.max(5, Math.floor((window.innerHeight * 0.8) / 64));
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
  }

  function closeList() {
    state.overlay = null;
    $("list").hidden = true;
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
    clearTimeout(state.timers.controls);
    // They stay on screen only while the viewer has paused (the way back
    // to playing); a video still loading counts as playing.
    state.timers.controls = setTimeout(function hide() {
      if (state.player && state.player.userPaused) { state.timers.controls = setTimeout(hide, 1000); return; }
      hideControls();
    }, 5000);
  }
  function hideControls() {
    clearTimeout(state.timers.controls);
    $("controls").hidden = true;
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
    fav: () => toggleFavorite(state.current).then(showControls),
    rec: () => record(state.current),
    multiview: () => { stopPlayer(); location.href = "/multiview.html" + (state.current ? "?channel=" + state.current.id : ""); },
  };
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
    if (state.overlay) return;
    if (lastPointer === "mouse") actions.toggle();
    else if ($("controls").hidden) showControls();
    else hideControls();
  });
  // Keep the "behind live" label current while visible.
  setInterval(() => { if (!$("controls").hidden) {
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
  function flash(text) {
    const el = $("status");
    el.textContent = text;
    el.hidden = false;
    clearTimeout(state.timers.status);
    state.timers.status = setTimeout(() => { el.hidden = true; }, 3500);
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

  // ---- Keys: browser, LG webOS and Android TV ----
  function keyOf(e) {
    const byCode = { 33: "ChannelUp", 34: "ChannelDown", 403: "Red", 404: "Green", 405: "Yellow", 406: "Blue",
      457: "Info", 458: "Guide", 461: "Back", 427: "ChannelUp", 428: "ChannelDown" };
    if (byCode[e.keyCode]) return byCode[e.keyCode];
    const byKey = { PageUp: "ChannelUp", PageDown: "ChannelDown", "+": "ChannelUp", "-": "ChannelDown",
      ColorF0Red: "Red", ColorF1Green: "Green", ColorF2Yellow: "Yellow", ColorF3Blue: "Blue",
      Escape: "Back", Backspace: "Back", BrowserBack: "Back", GoBack: "Back", Enter: "OK",
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

    if (/^[0-9]$/.test(key)) { digit(key); e.preventDefault(); return; }
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
        case "ArrowUp": browseStep(-1); break;
        case "ArrowDown": browseStep(1); break;
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
        case "Guide": stopPlayer(); location.href = "/guide.html"; break;
        case "Blue": record(state.browse !== null ? state.channels[state.browse] : state.current); break;
        case "Info": showBanner(state.current, false); showControls(); break;
        case " ": case "PlayPause": case "Play": case "Pause": actions.toggle(); break;
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

  // Android "Back" goes through the page's own Back handling.
  window.VodMaxNativeBack = () => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    return true;
  };
})();
