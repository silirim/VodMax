// VodMax home: films, series, recordings and the way to TV, browsed with a
// remote control; playback with resume and "continue watching".
"use strict";

(() => {
  const { store, viewer, api, keyOf, clock, hhmm } = window.VodMax;
  const t = window.VodMaxI18n.t;
  const locale = window.VodMaxI18n.locale;
  const $ = (id) => document.getElementById(id);
  const el = (tag, props = {}, ...children) => {
    const e = Object.assign(document.createElement(tag), props);
    e.append(...children.filter((c) => c !== null && c !== undefined));
    return e;
  };

  const askedView = new URLSearchParams(location.search).get("view");
  const state = { view: ["home", "movie", "series", "mylist", "recordings", "downloads", "tv"].includes(askedView) ? askedView : store.get("vodView", "home"), query: "", detail: null, play: null };
  if (askedView) history.replaceState(null, "", "/");
  let statusTimer;
  function flash(text) {
    $("status").textContent = text;
    $("status").hidden = false;
    clearTimeout(statusTimer);
    statusTimer = setTimeout(() => { $("status").hidden = true; }, 4000);
  }

  // ---- Cards ----
  function poster(item, title) {
    if (item.logo) {
      const img = el("img", { className: "poster", src: item.logo, alt: "", loading: "lazy" });
      img.onerror = () => img.replaceWith(el("div", { className: "poster" }, el("span", { className: "poster-title" }, title)));
      return img;
    }
    return el("div", { className: "poster" }, el("span", { className: "poster-title" }, title));
  }

  // "Nuovo" only where it singles a few titles out: a first import, a new
  // subscription or a list of the newest would put it on every card.
  const isFresh = (item) => item.first_seen_at && Date.now() / 1000 - item.first_seen_at < 7 * 86400;
  function itemCards(items) {
    const few = items.filter(isFresh).length <= items.length * 0.3;
    return items.map((item) => itemCard(item, few));
  }

  function itemCard(item, showNew) {
    const title = item.display_title;
    const card = el("button", { className: "card focusable" }, poster(item, title),
      el("span", { className: "label" }, title),
      el("span", { className: "sub" }, [item.year, item.variant_count > 1 ? t("{n} versioni", { n: item.variant_count }) : ""].filter(Boolean).join(" · ")));
    // Badges from what the catalogue already says: new this week on the
    // left; 4K, else the rating, else the quality on the right.
    const fresh = showNew && isFresh(item);
    const q = { 4: "4K", 3: "FHD", 2: "HD" }[item.quality];
    const right = item.quality === 4 || (!item.rating && q) ? el("span", { className: "badge hd right" }, q)
      : item.rating ? el("span", { className: "badge rating right" }, rating(item.rating)) : null;
    if (fresh || right) card.append(el("span", { className: "card-badges" }, fresh ? el("span", { className: "badge new" }, t("Nuovo")) : null, right));
    card.onclick = () => openDetail(item);
    card.hero = { ...heroFromItem(item), eyebrow: state.view === "movie" ? t("Film") : state.view === "series" ? t("Serie") : "" };
    return card;
  }

  function continueCard(c) {
    const episode = c.media === "episode";
    const title = episode ? c.series_title : c.title;
    const ep = "S" + c.season + "E" + c.number + " · " + c.title;
    const sub = [c.next ? t("Prossima puntata") : "", episode ? c.title : "",
      c.next ? "" : t("Mancano {time}", { time: clock(c.progress.duration - c.progress.position) })].filter(Boolean).join(" · ");
    const pct = c.next ? 0 : c.progress.duration ? (c.progress.position / c.progress.duration) * 100 : 0;
    const art = poster({ logo: c.image }, title);
    const card = el("button", { className: "card wide focusable" }, art,
      episode ? el("span", { className: "card-badges" }, el("span", { className: "badge" }, "S" + c.season + " · E" + c.number)) : null,
      pct ? el("div", { className: "bar" }, el("div", { style: "width:" + pct + "%" })) : null,
      el("span", { className: "label" }, title), el("span", { className: "sub tnum" }, sub));
    // The title's backdrop fits a wide card better than its poster.
    detailsOf(episode ? c.series_id : c.id).then((d) => {
      if (!d.backdrop) return;
      const img = el("img", { className: "poster", alt: "" });
      img.onload = () => { if (art.isConnected) art.replaceWith(img); };
      img.src = d.backdrop;
    });
    card.onclick = () => play(c.media, c.id, title + (c.media === "episode" ? " · " + ep : ""), c.next ? 0 : c.progress.position);
    card.hero = heroFromContinue(c);
    // Off the row: the × on the card, the red key (Canc), or a long press.
    const itemID = c.media === "episode" ? c.series_id : c.id;
    card.dataset.continue = itemID;
    const off = el("button", { className: "card-x focusable" });
    window.VodMax.label(off, "x", t("Togli da Continua a guardare"));
    off.title = t("Togli da Continua a guardare");
    off.onclick = () => hideContinue(card);
    card.addEventListener("contextmenu", async (e) => {
      e.preventDefault();
      if (await choose(title, [["off", t("Togli da Continua a guardare"), "x"]]) === "off") hideContinue(card);
    });
    return el("div", { className: "card-wrap wide" }, card, off);
  }

  // Takes a title off "continue watching" (its position stays: the detail
  // page still resumes it); the focus moves to the next card.
  async function hideContinue(card) {
    const wrap = card.closest(".card-wrap");
    try { await api("DELETE", "/api/v1/continue/" + card.dataset.continue); }
    catch (e) { flash(e.message); return; }
    const next = wrap.nextElementSibling || wrap.previousElementSibling;
    const row = wrap.parentElement;
    wrap.remove();
    flash(t("Tolto da Continua a guardare"));
    if (next) next.querySelector(".card").focus();
    else if (row) {
      row.replaceWith(el("p", { className: "empty" }, t("Qui compaiono i film e gli episodi iniziati e la prossima puntata delle serie che stai seguendo.")));
      focusFirst($("content"));
    }
  }

  // ---- Views ----
  function setTabs() {
    for (const tab of document.querySelectorAll(".tab[data-view]")) tab.classList.toggle("active", tab.dataset.view === state.view);
    window.VodMax.bottomNav(["recordings", "downloads", "mylist"].includes(state.view) ? "more" : state.view);
  }

  // Every render() has a number; a view's answer that arrives after a newer
  // render started (a quick tab switch, more typing in the search) is
  // dropped instead of being drawn over the newer page.
  let renderSeq = 0;
  async function render() {
    const my = ++renderSeq;
    // Typing in the search box: the results change under it, the focus
    // stays there (on TVs moving it closes the on-screen keyboard).
    const typing = document.activeElement === $("search");
    setTabs();
    $("search").placeholder = state.view === "tv" ? t("Cerca un canale (nome o numero)") : t("Cerca film e serie");
    store.set("vodView", state.view);
    const main = $("content");
    main.replaceChildren(loading(state.view));
    heroTop();
    try {
      if (state.view === "tv") await renderTV(main);
      else if (state.query) await renderSearch(main);
      else if (state.view === "home") await renderHome(main);
      else if (state.view === "recordings") await renderRecordings(main);
      else if (state.view === "downloads") await renderDownloads(main);
      else if (state.view === "mylist") await renderMyList(main);
      else await renderGrid(main, state.view);
    } catch (e) {
      if (my === renderSeq) main.replaceChildren(el("p", { className: "empty" }, e.message));
    }
    if (my !== renderSeq) return;
    heroTop();
    if (!typing || document.activeElement !== $("search")) focusFirst(main);
  }

  // While a page loads: grey shapes with the radii of what is coming.
  function loading(view) {
    const box = ["mylist", "movie", "series"].includes(view) || state.query
      ? el("div", { className: "grid" }, ...Array.from({ length: 12 }, () => el("div", { className: "card" }, el("div", { className: "poster skeleton" }))))
      : el("div", { className: "skeleton-list" }, ...Array.from({ length: 5 }, () => el("div", { className: "skeleton" })));
    box.setAttribute("role", "status");
    box.setAttribute("aria-label", t("Caricamento…"));
    return box;
  }

  // An empty list: an icon, one sentence, the way to fill it.
  function emptyState(icon, text, action) {
    const box = el("div", { className: "empty-state" }, window.VodMaxIcons ? window.VodMaxIcons.icon(icon) : null, el("p", {}, text));
    if (action) {
      const b = el("button", { className: "button focusable", type: "button" });
      window.VodMax.label(b, action[0], action[1]);
      b.onclick = action[2];
      box.append(b);
    }
    return box;
  }
  const goView = (view) => () => { state.view = view; state.query = ""; $("search").value = ""; render(); };

  // ---- TV: the channel list, then full screen ----
  const TV_LISTS = [["", t("Tutti")], ["tv", "TV"], ["sport", t("Sport")], ["radio", t("Radio")], ["favorites", t("Preferiti")]];
  async function renderTV(main) {
    const query = state.query.toLowerCase();
    const list = store.get("tvList", "");
    // A search looks in every channel, whatever the list.
    const q = query ? "" : list === "favorites" ? "?list=favorites" : list ? "?section=" + list : "";
    const my = renderSeq;
    let channels = await api("GET", "/api/v1/tv/channels" + q);
    if (my !== renderSeq) return;
    if (query) channels = channels.filter((c) => c.name.toLowerCase().includes(query) || String(c.number) === query);
    const now = Date.now() / 1000;
    const chips = TV_LISTS.map(([id, label]) => {
      const b = el("button", { className: "chip focusable" + (id === list && !query ? " active" : "") }, label);
      // Focus stays on the chosen list, not back on the first one.
      b.onclick = () => { store.set("tvList", id); state.query = ""; $("search").value = ""; render().then(() => {
        const on = document.querySelector(".tv-tools .chip.active");
        if (on) on.focus();
      }); };
      return b;
    });
    const guide = el("a", { className: "chip focusable", href: "/guide.html" });
    window.VodMax.label(guide, "calendar-days", t("Guida TV"));
    const item = (c) => {
      const p = c.now;
      const pct = p ? Math.min(100, Math.max(0, ((now - p.start) / (p.stop - p.start)) * 100)) : 0;
      const b = el("button", { className: "focusable" }, el("span", { className: "num" }, c.number || ""),
        window.VodMax.channelArt(c),
        el("span", { className: "what" }, el("strong", {}, (c.favorite ? "★ " : "") + c.name),
          el("span", {}, p ? hhmm(p.start) + " " + p.title : t("Nessuna informazione")),
          c.next ? el("span", {}, t("Dopo: {time} {title}", { time: hhmm(c.next.start), title: c.next.title })) : null,
          p ? el("div", { className: "bar" }, el("div", { style: "width:" + pct + "%" })) : null));
      b.onclick = () => { location.href = "/tv.html?channel=" + c.id; };
      b.hero = channelHero(c);
      return el("li", {}, b);
    };
    // 600 at a time (a long list is slow to draw on a TV); the rest on
    // request, like the films' grid, so no channel is out of reach.
    const PAGE = 600;
    const ol = el("ol", { className: "channels" }, ...channels.slice(0, PAGE).map(item));
    let more = null;
    if (channels.length > PAGE) {
      more = el("button", { className: "button more focusable" }, t("Altri canali"));
      more.onclick = () => {
        const n = ol.children.length;
        ol.append(...channels.slice(n, n + PAGE).map(item));
        if (ol.children.length >= channels.length) more.remove();
        focusFirst(ol.children[n]);
      };
    }
    const hero = el("section", { className: "hero compact" });
    main.replaceChildren(hero, el("div", { className: "tv-tools" }, ...chips, guide),
      query ? el("p", { className: "empty" }, t("{n} canali per “{query}” (nome o numero)", { n: channels.length, query: state.query })) : "",
      channels.length ? ol : el("p", { className: "empty" }, list === "favorites"
        ? t("Nessun preferito: in TV premi il tasto giallo (F) su un canale.") : t("Nessun canale.")), more || "");
    if (channels.length) showHero(hero, channelHero(channels[0]));
    else hero.remove();
  }

  // The TV list's header: what the focused channel shows now, over a glow
  // of the channel's colour (the guide rarely has programme pictures).
  function channelHero(c) {
    const p = c.now;
    return { title: p ? p.title : c.name, eyebrow: t("In onda adesso"), mark: el("span", { className: "hero-mark" }, window.VodMax.channelArt(c)),
      text: [(c.number ? c.number + " · " : "") + c.name, p ? hhmm(p.start) + " – " + hhmm(p.stop) : "", c.next ? t("A seguire") + " · " + c.next.title : ""],
      art: () => {
        const glow = el("div", { className: "hero-art ch-glow" });
        glow.style.setProperty("--tint", "hsla(" + channelHue(String(c.name || "")) + ", 50%, 40%, 0.55)");
        return glow;
      } };
  }

  // ---- Home: a featured title on top (it follows the focused card),
  // then rows. ----
  const detailCache = new Map();
  function detailsOf(id) {
    if (!detailCache.has(id)) detailCache.set(id, api("GET", "/api/v1/items/" + id + "/details").catch(() => ({})));
    return detailCache.get(id);
  }

  function skeletonRow(wide) {
    return el("div", { className: "row" }, ...Array.from({ length: 7 }, () => el("div", { className: "card" + (wide ? " wide" : "") },
      el("div", { className: "poster skeleton" }))));
  }

  // A row's title, with "Vedi tutti ›" when the row has a page of its own.
  function section(title, view, before) {
    const h = el("h2", { className: "section" }, el("span", {}, title));
    if (view) {
      const all = el("button", { className: "see-all focusable", type: "button" }, t("Vedi tutti"));
      all.setAttribute("aria-label", t("Vedi tutti") + ": " + title);
      all.onclick = () => { if (before) before(); state.view = view; state.query = ""; $("search").value = ""; window.scrollTo(0, 0); render(); };
      h.append(all);
    }
    return h;
  }
  const newest = (kind) => () => store.set("browse." + kind, { ...browseState(kind), sort: "recent" });

  async function renderHome(main) {
    main.replaceChildren(el("section", { className: "hero under" }), el("h2", { className: "section" }, t("Continua a guardare")), skeletonRow(true),
      el("h2", { className: "section" }, t("Nuovi film")), skeletonRow(false));
    heroTop();
    const my = renderSeq;
    const [cont, films, series, favorites, recordings, mine, trending] = await Promise.all([
      api("GET", "/api/v1/continue"),
      api("GET", "/api/v1/catalog/movie?sort=recent&limit=24"),
      api("GET", "/api/v1/catalog/series?sort=recent&limit=24"),
      api("GET", "/api/v1/tv/channels?list=favorites").catch(() => []),
      api("GET", "/api/v1/recordings").catch(() => []),
      api("GET", "/api/v1/mylist").catch(() => []),
      api("GET", "/api/v1/trending").catch(() => []),
    ]);
    const hero = el("section", { className: "hero under" });
    const parts = [hero];
    const tonight = await assistant() ? tonightBox() : null;
    if (my !== renderSeq) return;
    parts.push(el("h2", { className: "section" }, t("Continua a guardare")));
    parts.push(cont.length ? el("div", { className: "row" }, ...cont.map(continueCard))
      : el("p", { className: "empty" }, t("Qui compaiono i film e gli episodi iniziati e la prossima puntata delle serie che stai seguendo.")));
    if (mine.length) parts.push(section(t("La mia lista"), "mylist"), el("div", { className: "row" }, ...itemCards(mine.slice(0, 24))));
    // TMDB's week, among the titles you have (with a TMDB key).
    if (trending.length) parts.push(section(t("Di tendenza questa settimana")), el("div", { className: "row" }, ...itemCards(trending)));
    if (favorites.length) parts.push(section(t("In onda sui tuoi canali"), "tv", () => store.set("tvList", "favorites")), el("div", { className: "row" }, ...favorites.slice(0, 20).map(channelCard)));
    parts.push(section(t("Nuovi film"), "movie", newest("movie")), el("div", { className: "row" }, ...itemCards(films.items)));
    parts.push(section(t("Nuove serie"), "series", newest("series")), el("div", { className: "row" }, ...itemCards(series.items)));
    const done = recordings.filter((r) => r.status === "completed").slice(0, 12);
    if (done.length) parts.push(section(t("Registrazioni recenti"), "recordings"), el("div", { className: "row" }, ...done.map(recordingCard)));
    // "Stasera" after the first two rows: what to resume and what is new
    // come first, the question to the assistant just below.
    if (tonight) parts.splice(Math.min(5, parts.length), 0, tonight);
    main.replaceChildren(...parts);
    // The featured title: the first thing to continue, else the newest film.
    const first = cont[0] ? heroFromContinue(cont[0]) : films.items[0] ? heroFromItem(films.items[0]) : null;
    if (first) showHero(hero, first);
    heroTop();
  }
  // Added once, not at every visit of Home: the hero follows the focused
  // card only on the page that has one (a card of Film or a search sets
  // card.hero too, and a listener left from Home fetched its details).
  let heroTimer;
  $("content").addEventListener("focusin", (e) => {
    const card = e.target.closest(".card, .channels button");
    const hero = $("content").querySelector(".hero");
    if (!card || !card.hero || !hero) return;
    clearTimeout(heroTimer);
    // Only if the focus is still on that card (moving on quickly with
    // the remote must not redraw the hero under the new focus).
    heroTimer = setTimeout(() => { if (document.activeElement === card && hero.isConnected) showHero(hero, card.hero); }, 350);
  });

  const QUALITY = { 4: "4K", 3: "Full HD", 2: "HD", 1: "SD" };
  const rating = (r) => Number(r).toLocaleString(locale, { maximumFractionDigits: 1 });
  function heroFromItem(item) {
    return { id: item.id, title: item.display_title, poster: item.logo, year: item.year, quality: item.quality, rating: item.rating,
      actions: [["primary", "play", t("Guarda"), () => openDetail(item)], ["", "info", t("Dettagli"), () => openDetail(item)]] };
  }
  function heroFromContinue(c) {
    const episode = c.media === "episode";
    const title = episode ? c.series_title : c.title;
    const ep = episode ? "S" + c.season + "E" + c.number + " · " + c.title : "";
    const item = episode ? { id: c.series_id, kind: "series", display_title: c.series_title, logo: c.image }
      : { id: c.id, kind: "movie", display_title: c.title, year: c.year, logo: c.image };
    return { id: item.id, title, poster: c.image, year: c.year,
      text: [c.next ? t("Prossima puntata") : episode ? t("Continua") : t("Mancano {time}", { time: clock(c.progress.duration - c.progress.position) }), ep],
      actions: [["primary", "play", c.next ? t("Guarda la puntata") : t("Riprendi"), () => play(c.media, c.id, title + (episode ? " · " + ep : ""), c.next ? 0 : c.progress.position)],
        ["", "info", t("Dettagli"), () => openDetail(item)]] };
  }

  // The meta line of a hero or a detail page: year, duration and genre as
  // text, the rating and the quality as pills.
  function metaLine(m) {
    const text = [m.year ? el("strong", { className: "m" }, String(m.year)) : null,
      ...(m.text || []).filter(Boolean).map((s) => el("span", { className: "m" }, s))].filter(Boolean);
    return [...text, m.rating ? el("span", { className: "badge rating" }, rating(m.rating)) : null,
      QUALITY[m.quality] ? el("span", { className: "badge hd" }, QUALITY[m.quality]) : null].filter(Boolean);
  }
  const detailText = (d) => [d.duration ? Math.round(d.duration / 60) + " min" : "", d.genre ? d.genre.split(",")[0].trim() : ""];

  // The title's logo (TMDB) in place of its name, once it has loaded; the
  // name stays as its alternative text for screen readers.
  function titleLogo(h1, url, title) {
    const img = el("img", { className: "title-logo", alt: title });
    img.onload = () => { if (h1.isConnected) h1.replaceChildren(img); };
    img.src = url;
  }

  // A backdrop that fades into the page. Drawn on a canvas so the bottom of
  // the picture turns transparent over the ambient (common.js): no seam
  // where it ends. CSS could only do it with mask-image, which webOS 22 has
  // only prefixed (and old WebViews not at all). The shade behind the text
  // and the header is in the picture too, so it fades with it. A detail
  // page has more text, wider and longer, than a hero: its shade is deeper.
  function fadedArt(url, box, fadeTop, detail) {
    return new Promise((resolve) => {
      const img = new Image();
      img.onload = () => {
        const k = Math.min(window.devicePixelRatio || 1, 2);
        const w = Math.round(box.clientWidth * k), h = Math.round(box.clientHeight * k);
        if (!w || !h) { resolve(null); return; }
        const c = el("canvas", { className: "hero-art", width: w, height: h });
        const g = c.getContext("2d");
        const s = Math.max(w / img.naturalWidth, h / img.naturalHeight);
        g.drawImage(img, (w - img.naturalWidth * s) / 2, (h - img.naturalHeight * s) * 0.2, img.naturalWidth * s, img.naturalHeight * s);
        const bg = getComputedStyle(document.documentElement).getPropertyValue("--bg-rgb").trim() || "11, 12, 16";
        const shade = (x1, y1, a, hold) => {
          const gr = g.createLinearGradient(0, 0, x1, y1);
          gr.addColorStop(0, "rgba(" + bg + "," + a + ")");
          if (hold) gr.addColorStop(hold, "rgba(" + bg + "," + a + ")");
          gr.addColorStop(1, "rgba(" + bg + ",0)");
          g.fillStyle = gr;
          g.fillRect(0, 0, w, h);
        };
        if (detail) shade(w * 0.85, 0, 0.92, 0.55);
        else shade(w * 0.72, 0, 0.9);
        shade(0, h * 0.3, 0.55);
        if (w < h * 1.3) { // a phone: the text spans the width, so the shade goes down, not across
          const down = detail ? g.createLinearGradient(0, h * 0.12, 0, h * 0.42) : g.createLinearGradient(0, h * 0.2, 0, h * 0.65);
          down.addColorStop(0, "rgba(" + bg + ",0)");
          down.addColorStop(1, "rgba(" + bg + "," + (detail ? 0.88 : 0.75) + ")");
          g.fillStyle = down;
          g.fillRect(0, 0, w, h);
        }
        g.globalCompositeOperation = "destination-in";
        const fade = g.createLinearGradient(0, h * 0.5, 0, h);
        fade.addColorStop(0, "rgba(0,0,0,1)");
        fade.addColorStop(0.55, "rgba(0,0,0,0.5)");
        fade.addColorStop(1, "rgba(0,0,0,0)");
        g.fillStyle = fade;
        g.fillRect(0, 0, w, h);
        // Below the header (not under it): the top fades in too.
        if (fadeTop) {
          const top = g.createLinearGradient(0, 0, 0, h * 0.3);
          top.addColorStop(0, "rgba(0,0,0,0)");
          top.addColorStop(1, "rgba(0,0,0,1)");
          g.fillStyle = top;
          g.fillRect(0, 0, w, h); // the whole canvas: destination-in clears what it does not cover
        }
        resolve(c);
      };
      img.onerror = () => resolve(null);
      img.src = url;
    });
  }
  // The new picture fades in over the old one, which then goes.
  function setArt(box, art) {
    const old = [...box.querySelectorAll(":scope > .hero-art")];
    if (art) box.insertBefore(art, box.querySelector(".hero-text"));
    setTimeout(() => old.forEach((o) => o.remove()), art ? 700 : 0);
  }

  let heroShown = null;
  async function showHero(hero, h) {
    heroShown = h;
    const compact = hero.classList.contains("compact");
    const buttons = (compact ? [] : h.actions || []).map(([kind, icon, text, fn]) => {
      const b = el("button", { className: "button focusable " + kind });
      window.VodMax.label(b, icon, text);
      b.onclick = fn;
      return b;
    });
    const plot = el("p", { className: "hero-plot" });
    const title = el("h1", {}, h.title);
    const meta = el("div", { className: "hero-meta" }, ...metaLine(h));
    // A focused hero button keeps the focus on the new one in its place.
    const had = hero.contains(document.activeElement) ? [...hero.querySelectorAll("button")].indexOf(document.activeElement) : -1;
    // The eyebrow names the page (Film, Serie, In onda adesso): a heading.
    const text = el("div", { className: "hero-text" }, h.eyebrow ? el("h2", { className: "eyebrow" }, h.eyebrow) : null, h.mark || null, title, meta,
      compact ? null : plot, buttons.length ? el("div", { className: "hero-actions" }, ...buttons) : null);
    const old = hero.querySelector(".hero-text");
    if (old) old.replaceWith(text); else hero.append(text);
    heroTop();
    if (had >= 0 && buttons.length) buttons[Math.min(had, buttons.length - 1)].focus();
    if (h.art) { setArt(hero, h.art()); return; } // a channel: its glow, no details
    const d = h.id ? await detailsOf(h.id) : {};
    if (heroShown !== h) return;
    plot.textContent = d.plot || "";
    meta.replaceChildren(...metaLine({ ...h, rating: h.rating || d.rating, text: [...(h.text || []), ...detailText(d)] }));
    if (d.logo) titleLogo(title, d.logo, h.title);
    const art = d.backdrop ? await fadedArt(d.backdrop, hero, !hero.classList.contains("under")) : null;
    if (heroShown !== h) return;
    setArt(hero, art);
    window.VodMax.setAmbient(d.backdrop || h.poster || "");
  }

  // Home's hero lies under the header: the bar is clear while it is over
  // the picture and gets its dark band back once the page scrolls on.
  const header = document.querySelector("header.top");
  function heroTop() {
    document.documentElement.style.setProperty("--top-h", header.offsetHeight + "px");
    // Clear until the title and its buttons would pass under the bar.
    const hero = $("content").querySelector(".hero.under");
    const text = hero && hero.querySelector(".hero-text");
    document.body.classList.toggle("over-hero", Boolean(hero) && (text ? text.getBoundingClientRect().top > header.offsetHeight : window.scrollY < 8));
  }
  window.addEventListener("scroll", heroTop, { passive: true });
  window.addEventListener("resize", heroTop);

  // The hue of a channel's initials tile (common.js channelArt), to tint
  // its tile on Home whether it shows the logo or the initials.
  function channelHue(name) {
    let h = 0;
    for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
    return (Math.imul(h, 2654435761) >>> 0) % 360;
  }
  const onAir = (p, now = Date.now() / 1000) => (p ? Math.min(100, Math.max(0, ((now - p.start) / (p.stop - p.start)) * 100)) : 0);

  // "In onda sui tuoi canali": a dark tile tinted with the channel's colour,
  // its logo, what is on with its progress, what follows.
  function channelCard(c) {
    const p = c.now;
    const tile = el("div", { className: "poster ch-tile" },
      el("span", { className: "ch-head" }, window.VodMax.channelArt(c), el("span", { className: "ch-num tnum" }, c.number || "")),
      el("span", { className: "eyebrow tnum" }, p ? t("In onda") + " · " + hhmm(p.start) + " – " + hhmm(p.stop) : c.name),
      el("strong", { className: "ch-now" }, p ? p.title : t("Nessuna informazione")),
      p ? el("span", { className: "ch-bar" }, el("span", { style: "width:" + onAir(p) + "%" })) : null,
      c.next ? el("span", { className: "ch-next" }, t("A seguire") + " · " + c.next.title) : null);
    tile.style.setProperty("--tint", "hsla(" + channelHue(String(c.name || "")) + ", 45%, 34%, 0.35)");
    const card = el("button", { className: "card wide focusable" }, tile, el("span", { className: "label" }, c.name));
    card.onclick = () => { location.href = "/tv.html?channel=" + c.id; };
    return card;
  }

  // ---- The assistant (when the admin set one up) ----
  let assistantOn = null;
  const assistant = () => assistantOn || (assistantOn = api("GET", "/api/v1/assistant").then((r) => r.enabled, () => false));

  // "What shall I watch tonight?": the model picks among tonight's
  // programmes and the library; a card per pick, with the reason.
  function tonightBox() {
    const wish = el("input", { className: "focusable", type: "text", maxLength: 200, placeholder: t("Hai voglia di… (facoltativo)") });
    wish.setAttribute("aria-label", t("Hai voglia di…"));
    // Android keyboards show ✓ ("done") otherwise, which closes the keyboard
    // without sending the form.
    wish.setAttribute("enterkeyhint", "go");
    const ask = el("button", { className: "small primary focusable", type: "submit" }, t("Cosa guardo stasera?"));
    const result = el("div", { className: "tonight-result" });
    const form = el("form", { className: "tonight-form" }, wish, ask);
    form.onsubmit = async (e) => {
      e.preventDefault();
      wish.blur(); // closes the phone keyboard over the answer
      ask.disabled = true;
      result.replaceChildren(el("p", { className: "empty" }, t("L'assistente sta scegliendo… (con un modello sul processore anche un minuto)")));
      try {
        // "Registra il film delle 21 su…": a recording, not a suggestion.
        if (RECORD.test(wish.value) && await recordWish(wish.value, result)) { ask.disabled = false; return; }
        const r = await api("POST", "/api/v1/assistant/tonight", { wish: wish.value });
        result.replaceChildren(r.intro ? el("p", { className: "tonight-intro" }, r.intro) : "", (r.picks || []).length ? el("div", { className: "row" }, ...r.picks.map(pickCard)) : "");
        focusFirst(result.querySelector(".row"));
      } catch (err) { result.replaceChildren(el("p", { className: "empty" }, err.message)); }
      ask.disabled = false;
    };
    return el("section", { className: "tonight" }, el("h2", { className: "section" }, t("Stasera")), form, result);
  }

  const RECORD = /(^|[^\p{L}])(registr|record)/iu;
  let pendingRule = ""; // the sentence the rule form starts from

  // A request to record, asked in the "tonight" box: the programmes the
  // guide has at that time, to record once, or a rule for every airing.
  // False when the assistant for rules cannot answer (a viewer, a kids
  // profile): the box then suggests as usual. Any other error (the model
  // still loading, a timeout) is shown, not swapped for suggestions.
  async function recordWish(text, result) {
    const who = await window.VodMax.me().catch(() => null);
    if (who && who.profile && who.profile.kind === "kids") return false;
    let p;
    try { p = await api("POST", "/api/v1/assistant/rule", { text }); } catch (e) { if (e.status === 403) return false; throw e; }
    let once = p.once || [], fromRule = false;
    // No day or time ("registra Feyenoord Inter"): the next airings of the
    // rule, the live one first, are what to record once.
    if (!once.length && p.rule && (p.rule.all || []).length) {
      try {
        const r = p.rule;
        const m = await api("POST", "/api/v1/recording-rules/preview", { name: r.name || r.all.join(" "), all: r.all, any: r.any || [], none: r.none || [],
          section: r.section || "", live_only: !!r.live_only, channel_id: r.channel_id || 0, keep: 0, enabled: true });
        const live = (x) => (x.airing && x.airing.kind === "live" ? 0 : 1);
        once = m.filter((x) => x.state === "new")
          // At the same time, the title with least besides what was asked
          // ("LIVE: Feyenoord - Inter" before "Radiocronaca - Feyenoord-Inter UCL").
          .sort((a, b) => live(a) - live(b) || a.start - b.start || a.title.length - b.title.length).slice(0, 3);
        fromRule = once.length > 0;
      } catch (e) { /* the rule button below still works */ }
    }
    const every = el("button", { className: "small focusable", type: "button" }, t("Registra ogni volta che va in onda"));
    every.onclick = () => { pendingRule = text; state.view = "recordings"; render(); };
    result.replaceChildren(
      el("p", { className: "tonight-intro" }, fromRule ? t("Nella guida ho trovato questi programmi: tocca quello da registrare.") : once.length === 1 ? t("Nella guida a quell'ora c'è questo programma. È quello che vuoi registrare?")
        : once.length ? t("Nella guida a quell'ora ci sono questi programmi. È uno di questi?")
        : t("Nella guida non ho trovato un programma a quell'ora: scrivi il canale e l'ora d'inizio, oppure crea una regola.")),
      once.length ? el("div", { className: "row" }, ...once.map((m) => pickCard({ kind: "tv", title: m.title, channel_id: m.channel_id, channel: m.channel,
        start: m.start, stop: m.stop, why: t("Solo questa volta") }, true))) : "",
      el("div", { className: "actions" }, every));
    focusFirst(result.querySelector(".row") || result);
    return true;
  }

  // Every pick is a wide card, TV or library alike. A film or series shows
  // its backdrop when it has one, otherwise the whole poster over a blurred
  // copy of itself.
  function pickCard(p, record) {
    const why = el("span", { className: "why" }, p.why);
    if (p.kind !== "tv") {
      const item = { id: p.item_id, kind: p.kind, display_title: p.title, logo: p.image };
      const art = el("div", { className: "poster pick-art" });
      if (p.image) {
        art.style.setProperty("--art", "url(" + JSON.stringify(p.image) + ")");
        art.append(el("img", { src: p.image, alt: "", loading: "lazy" }));
      } else art.append(el("span", {}, p.title));
      detailsOf(p.item_id).then((d) => {
        if (!d.backdrop) return;
        const img = new Image();
        img.onload = () => { art.replaceChildren(); art.style.setProperty("--art", "url(" + JSON.stringify(d.backdrop) + ")"); art.classList.add("backdrop"); };
        img.src = d.backdrop;
      });
      const card = el("button", { className: "card wide focusable pick" }, art, el("span", { className: "label" }, p.title),
        el("span", { className: "sub" }, p.kind === "series" ? t("Serie") : t("Film")), why);
      card.onclick = () => openDetail(item);
      return card;
    }
    const now = Date.now() / 1000, onAir = p.start <= now && now < p.stop;
    const art = el("div", { className: "poster channel-art" }, p.image ? el("img", { src: p.image, alt: "", loading: "lazy" }) : el("span", {}, p.channel));
    const card = el("button", { className: "card wide focusable pick" }, art,
      el("span", { className: "label" }, p.title),
      el("span", { className: "sub" }, (onAir ? t("In onda ora") : hhmm(p.start)) + " · " + p.channel), why);
    card.onclick = async () => {
      if (onAir && !record) { location.href = "/tv.html?channel=" + p.channel_id; return; }
      if (!confirm(t("Registrare “{title}” alle {time} su {channel}?", { title: p.title, time: hhmm(p.start), channel: p.channel }))) return;
      try {
        await api("POST", "/api/v1/recordings", { channel_id: p.channel_id, start: p.start, stop: p.stop, title: p.title, from_guide: true });
        flash(t("Registrazione programmata"));
      } catch (e) { flash(e.message); }
    };
    return card;
  }

  function recordingCard(r) {
    const card = el("button", { className: "card wide focusable" }, el("div", { className: "poster rec-art" }, el("span", {}, r.channel)),
      el("span", { className: "label" }, r.title),
      el("span", { className: "sub" }, new Date(r.start * 1000).toLocaleDateString(locale, { weekday: "short", day: "numeric", month: "short" }) + " " + hhmm(r.start)));
    card.onclick = () => playRecording(r);
    return card;
  }


  // ---- Films and series: filters (order, rating, category), then a
  // grid that loads more as asked. ----
  const SORTS = [["recent", t("Più recenti")], ["title", "A–Z"], ["rating", t("Più votati")]];
  const RATINGS = [[0, t("Tutti")], [6, "6+"], [7, "7+"], [8, "8+"]];
  function browseState(kind) {
    return { sort: "recent", minRating: 0, category: "", categorySub: 0, categoryName: "", ...store.get("browse." + kind, {}) };
  }

  async function renderGrid(main, kind, cursor, grid) {
    const b = browseState(kind);
    const q = new URLSearchParams({ limit: 60, sort: b.sort });
    if (b.minRating) q.set("min_rating", b.minRating);
    if (b.category) q.set("category", b.category);
    // Two providers (or two local collections) can use the same category id.
    if (b.category && b.categorySub) q.set("category_subscription", b.categorySub);
    if (cursor) q.set("cursor", cursor);
    if (!grid) main.replaceChildren(el("section", { className: "hero compact" }), filterBar(kind, b), skeletonRow(false));
    const my = renderSeq; // "Altri titoli" too: dropped if the page changed meanwhile
    const page = await api("GET", "/api/v1/catalog/" + kind + "?" + q);
    if (my !== renderSeq) return;
    if (!grid) {
      grid = el("div", { className: "grid" });
      main.lastElementChild.replaceWith(page.items.length ? grid : el("p", { className: "empty" }, t("Nessun titolo con questi filtri.")));
      // The header shows the first title, then the focused one.
      const hero = main.querySelector(".hero");
      if (page.items.length) showHero(hero, { ...heroFromItem(page.items[0]), eyebrow: kind === "movie" ? t("Film") : t("Serie") });
      else hero.remove();
    }
    grid.append(...itemCards(page.items));
    const oldMore = main.querySelector(".more");
    if (oldMore) oldMore.remove();
    if (page.next) {
      const more = el("button", { className: "button more focusable" }, t("Altri titoli"));
      more.onclick = async () => { more.remove(); const n = grid.children.length; await renderGrid(main, kind, page.next, grid); focusFirst(grid.children[n]); };
      main.append(more);
    }
  }

  function filterBar(kind, b) {
    // After a choice the focus stays on the chip that was pressed.
    const set = (change) => {
      const pressed = document.activeElement && document.activeElement.closest(".filters .chip");
      const index = pressed ? [...pressed.parentElement.querySelectorAll(".chip")].indexOf(pressed) : -1;
      store.set("browse." + kind, { ...b, ...change });
      render().then(() => {
        const chips = $("content").querySelectorAll(".filters .chip");
        if (index >= 0 && chips[index]) chips[index].focus();
      });
    };
    const chip = (text, active, fn, icon) => {
      const c = el("button", { className: "chip focusable" + (active ? " active" : "") });
      window.VodMax.label(c, icon, text);
      c.onclick = fn;
      return c;
    };
    return el("div", { className: "chips filters" },
      el("span", { className: "chip-label" }, t("Ordina")),
      ...SORTS.map(([v, text]) => chip(text, b.sort === v, () => set({ sort: v }))),
      el("span", { className: "chip-label" }, t("Voto")),
      ...RATINGS.map(([v, text]) => chip(text, b.minRating === v, () => set({ minRating: v }))),
      el("span", { className: "chip-label" }, t("Categoria")),
      chip(b.category ? b.categoryName : t("Tutte"), Boolean(b.category), () => pickCategory(kind, b, set), "list"),
      b.category ? chip(t("Togli"), false, () => set({ category: "", categorySub: 0, categoryName: "" }), "x") : null);
  }

  // A panel to choose among hundreds of provider categories: type to
  // filter, arrows and OK to choose, Back to close.
  async function pickCategory(kind, b, set) {
    const back = document.activeElement;
    const list = await api("GET", "/api/v1/browse/" + kind + "/categories");
    const filter = el("input", { type: "search", className: "focusable", placeholder: t("Cerca una categoria"), autocomplete: "off" });
    const options = el("div", { className: "options" });
    const scrim = el("div", { className: "scrim" });
    const panel = el("section", { className: "picker", id: "picker" }, el("h2", {}, t("Categoria")), filter, options);
    const close = () => { panel.remove(); scrim.remove(); if (back) back.focus(); };
    scrim.onclick = close;
    panel.close = close;
    const draw = () => {
      const f = filter.value.trim().toLowerCase();
      const all = el("button", { className: "chip focusable" + (b.category ? "" : " active") }, el("span", { className: "name" }, t("Tutte")));
      all.onclick = () => { close(); set({ category: "", categorySub: 0, categoryName: "" }); };
      // Names that start with the text first ("azione" before "animazione").
      const found = list.filter((c) => !f || c.name.toLowerCase().includes(f))
        .sort((x, y) => (f ? Number(!x.name.toLowerCase().startsWith(f)) - Number(!y.name.toLowerCase().startsWith(f)) : 0));
      options.replaceChildren(...(f ? [] : [all]), ...found.map((c) => {
        const active = c.id === b.category && (!b.categorySub || c.subscription === b.categorySub);
        const o = el("button", { className: "chip focusable" + (active ? " active" : ""), title: c.name }, el("span", { className: "name" }, c.name), el("small", {}, c.titles));
        o.onclick = () => { close(); set({ category: c.id, categorySub: c.subscription, categoryName: c.name }); };
        return o;
      }));
    };
    filter.oninput = draw;
    draw();
    document.body.append(scrim, panel);
    (options.querySelector(".active") || options.firstElementChild || filter).focus();
  }


  async function renderSearch(main) {
    const q = encodeURIComponent(state.query);
    const my = renderSeq;
    const [films, series] = await Promise.all([
      api("GET", "/api/v1/catalog/movie?limit=40&q=" + q), api("GET", "/api/v1/catalog/series?limit=40&q=" + q)]);
    if (my !== renderSeq) return;
    const sections = [[t("Film"), films.items], [t("Serie"), series.items]];
    if (state.view === "series") sections.reverse(); // the current tab's kind first
    const parts = [];
    for (const [label, items] of sections) {
      if (items.length) parts.push(el("h2", { className: "section" }, label), el("div", { className: "grid" }, ...itemCards(items)));
    }
    if (!parts.length) parts.push(el("p", { className: "empty" }, t("Nessun risultato per “{query}”.", { query: state.query })));
    main.replaceChildren(...parts);
  }

  const recState = { scheduled: t("In programma"), recording: t("● In registrazione"), completed: t("Registrata"), failed: t("Non riuscita") };
  async function renderRecordings(main) {
    const my = renderSeq;
    const [list, space, who] = await Promise.all([api("GET", "/api/v1/recordings"), api("GET", "/api/v1/recordings/space"),
      window.VodMax.me().catch(() => null)]);
    const kids = who && who.profile && who.profile.kind === "kids";
    const gb = (b) => b >= 1e9 ? (b / 1e9).toLocaleString(locale, { maximumFractionDigits: 1 }) + " GB"
      : Math.round(b / 1e6).toLocaleString(locale) + " MB"; // a short recording is not "0 GB"
    const day = (ts) => new Date(ts * 1000).toLocaleDateString(locale, { weekday: "short", day: "numeric", month: "short" });
    const usage = [t("Usati {size}", { size: gb(space.used_bytes) }), space.free_bytes >= 0 ? t("liberi {size}", { size: gb(space.free_bytes) }) : "",
      space.max_gb ? t("limite {n} GB", { n: space.max_gb }) : "", space.keep_days ? t("si tengono {n} giorni", { n: space.keep_days }) : ""].filter(Boolean).join(" · ");
    const parts = [el("h2", { className: "page-title" }, t("Registrazioni")), el("p", { className: "empty" }, usage)];
    if (!list.length) parts.push(emptyState("video", t("Nessuna registrazione. In TV premi R (tasto blu) sul programma in onda, " +
      "oppure registra dalla Guida, oppure crea una regola qui sotto."), ["calendar-days", t("Guida TV"), () => { location.href = "/guide.html"; }]));
    else {
      parts.push(el("p", { className: "empty" }, t("OK guarda · Giallo (P) protegge dalla cancellazione automatica · Rosso (Canc) elimina")));
      parts.push(el("ol", { className: "recs" }, ...list.map((r) => {
        const b = el("button", { className: "rec focusable" },
          el("span", {}, day(r.start) + " " + hhmm(r.start)),
          el("span", {}, el("strong", {}, r.title), " · " + r.channel, r.rule_id ? el("small", { className: "tag" }, " " + t("regola")) : null),
          el("span", { className: "state " + r.status }, (r.protected ? "🔒 " : "") + (recState[r.status] || r.status)),
          el("span", { className: "state" }, r.bytes ? gb(r.bytes) : ""));
        b.onclick = () => r.status === "scheduled" ? flash(t("Non ancora iniziata")) : playRecording(r);
        b.dataset.recording = r.id;
        b.dataset.protected = r.protected ? "1" : "";
        b.dataset.status = r.status;
        b.title = r.error || "";
        // Visible actions, for touch and mouse too (the keys stay).
        const actions = [];
        if (!kids && r.status === "recording") {
          const stop = el("button", { className: "focusable small" });
          window.VodMax.label(stop, null, t("Ferma"));
          stop.onclick = () => stopRecording(r.id);
          actions.push(stop);
        }
        if (!kids) {
          const del = el("button", { className: "focusable small danger" });
          window.VodMax.label(del, r.status === "scheduled" ? "x" : "trash", r.status === "scheduled" ? t("Annulla") : t("Elimina"));
          del.onclick = () => deleteRecording(r.id, r.status);
          actions.push(del);
        }
        return el("li", {}, b, actions.length ? el("div", { className: "rec-actions" }, ...actions) : null);
      })));
    }
    if (!kids) parts.push(...await rulesSection());
    if (my !== renderSeq) return;
    main.replaceChildren(...parts);
  }

  // ---- The downloads page; refreshed while one is running ----
  // ---- My list: films and series kept for later, per profile ----
  async function renderMyList(main) {
    const my = renderSeq;
    const list = await api("GET", "/api/v1/mylist");
    if (my !== renderSeq) return;
    main.replaceChildren(el("h2", { className: "page-title" }, t("La mia lista")),
      list.length ? el("div", { className: "grid" }, ...itemCards(list))
        : emptyState("bookmark", t("La lista è vuota. Nella scheda di un film o di una serie premi “La mia lista” per ritrovarlo qui."), ["film", t("Vai ai film"), goView("movie")]));
  }

  // The "My list" button of a detail page.
  async function addListButton(item) {
    const b = el("button", { className: "button focusable mylist" });
    let on = false;
    const draw = () => window.VodMax.label(b, on ? "bookmark-check" : "bookmark", on ? t("Nella mia lista") : t("La mia lista"));
    draw();
    b.onclick = async () => {
      try { on = (await api("PUT", "/api/v1/mylist/" + item.id, { on: !on })).on; draw(); flash(on ? t("Aggiunto alla tua lista") : t("Tolto dalla tua lista")); }
      catch (e) { flash(e.message); }
    };
    const actions = $("detail-actions");
    actions.insertBefore(b, actions.querySelector(".download, .dl-state, .trailer")); // same order whatever arrives first
    try { on = (await api("GET", "/api/v1/mylist/" + item.id)).on; draw(); } catch { /* the button still works */ }
  }

  async function renderDownloads(main) {
    const my = renderSeq;
    const dl = await downloadList(true);
    if (my !== renderSeq) return;
    if (!dl) { main.replaceChildren(el("p", { className: "empty" }, t("I download non sono disponibili su questo dispositivo."))); return; }
    main.replaceChildren(downloadsSection(dl));
    watchDownloads(dl);
  }
  // The Download tab appears where downloads exist (not for kids).
  function showDownloadsTab() {
    window.VodMax.me().then((who) => {
      if (who && who.profile && who.profile.kind === "kids") return;
      downloadList().then((list) => { const tab = document.querySelector('.tab[data-view="downloads"]'); if (list && tab) tab.hidden = false; });
    }).catch(() => {});
  }

  function downloadsSection(list) {
    const gb = (b) => b >= 1e9 ? (b / 1e9).toLocaleString(locale, { maximumFractionDigits: 2 }) + " GB"
      : Math.round(b / 1e6).toLocaleString(locale) + " MB";
    const box = el("section", { id: "downloads" }, el("h2", { className: "page-title" }, t("Download")));
    if (!list.length) {
      box.append(emptyState("download", t("Nessun download. Nella scheda di un film o di una serie premi “Scarica”: il server lo copia sul suo disco, " +
        "senza togliere la connessione a chi guarda, e lo trovi nelle raccolte «Scaricati»."), ["film", t("Vai ai film"), goView("movie")]));
      return box;
    }
    box.append(el("ol", { className: "downloads" }, ...list.map((d) => {
      const pct = d.total ? Math.min(100, (d.bytes / d.total) * 100) : 0;
      const size = d.total ? gb(d.bytes) + " / " + gb(d.total) : d.bytes ? gb(d.bytes) : "";
      const row = el("li", {}, el("div", { className: "dl-main" }, el("strong", {}, d.title),
        el("span", { className: "state " + d.status }, (dlState[d.status] || d.status) + (size ? " · " + size : "")),
        d.error ? el("small", { className: "empty" }, d.error) : null,
        d.status !== "done" ? el("span", { className: "bar" }, el("div", { style: "width:" + pct + "%" })) : null));
      const buttons = el("div", { className: "dl-buttons" });
      const act = (label, method, path) => {
        const b = el("button", { className: "focusable small" + (method === "DELETE" ? " danger" : "") }, label);
        b.onclick = async () => {
          if (method === "DELETE" && !confirm(t("Eliminare “{title}” e il suo file?", { title: d.title }))) return;
          try { await api(method, path); render(); } catch (e) { flash(e.message); }
        };
        buttons.append(b);
      };
      if (d.status === "downloading" || d.status === "queued") act(t("Pausa"), "POST", "/api/v1/downloads/" + d.id + "/pause");
      if (d.status === "paused" || d.status === "failed") act(t("Riprendi"), "POST", "/api/v1/downloads/" + d.id + "/resume");
      act(t("Elimina"), "DELETE", "/api/v1/downloads/" + d.id);
      row.append(buttons);
      return row;
    })));
    return box;
  }
  let dlTimer;
  function watchDownloads(list) {
    clearTimeout(dlTimer);
    if (!list || !list.some((d) => d.status === "downloading" || d.status === "queued")) return;
    dlTimer = setTimeout(async () => {
      if (state.view !== "downloads" || !$("downloads")) return;
      const fresh = await downloadList(true);
      if (!fresh || !$("downloads")) return;
      // Redrawn in place; the focused button (remote) stays focused.
      const old = $("downloads"), buttons = [...old.querySelectorAll("button")];
      const at = buttons.indexOf(document.activeElement);
      const next = downloadsSection(fresh);
      old.replaceWith(next);
      if (at >= 0) {
        const again = next.querySelectorAll("button");
        (again[Math.min(at, again.length - 1)] || next).focus({ preventScroll: true });
      }
      watchDownloads(fresh);
    }, 3000);
  }

  async function toggleProtected(id, now) {
    try { await api("PUT", "/api/v1/recordings/" + id + "/protected", { protected: !now }); flash(now ? t("Protezione tolta") : t("Protetta dalla cancellazione automatica")); render(); }
    catch (e) { flash(e.message); }
  }

  // ---- Recording rules ----
  const words = (v) => v.split(",").map((w) => w.trim()).filter(Boolean);
  const MATCH = { new: t("da registrare"), scheduled: t("in programma"), cancelled: t("annullata a mano"), conflict: t("non registrabile"), too_long: t("troppo lunga"),
    rerun: t("replica, non la registra") };

  async function rulesSection() {
    const rules = await api("GET", "/api/v1/recording-rules");
    const out = [el("h2", { className: "section" }, t("Regole automatiche")),
      el("p", { className: "empty" }, t("Una regola registra da sola i programmi della guida che contengono certe parole: per esempio tutte le partite " +
        "della Juve, o gara e qualifiche di Formula 1. Si applica ogni 10 minuti ai prossimi giorni di guida."))];
    for (const r of rules) {
      const desc = [t("tutte: {words}", { words: r.spec.all.join(", ") }), r.spec.any && r.spec.any.length ? t("almeno una: {words}", { words: r.spec.any.join(", ") }) : "",
        r.spec.none && r.spec.none.length ? t("escluse: {words}", { words: r.spec.none.join(", ") }) : "", r.channel_name ? t("solo {channel}", { channel: r.channel_name }) : "",
        r.spec.section ? t("sezione {section}", { section: r.spec.section }) : "", r.spec.live_only ? t("solo dirette") : "", r.keep ? t("tiene le ultime {n}", { n: r.keep }) : ""].filter(Boolean).join(" · ");
      const box = el("div", { className: "rule" }, el("strong", {}, r.enabled ? r.name : t("{name} (sospesa)", { name: r.name })), el("p", { className: "empty" }, desc),
        r.last_note ? el("p", { className: "empty" }, t("Ultimo controllo: {note}", { note: r.last_note })) : null);
      const edit = el("button", { className: "small focusable" }, t("Modifica"));
      edit.onclick = () => { edit.disabled = true; box.append(ruleForm(r)); };
      const toggle = el("button", { className: "small focusable" }, r.enabled ? t("Sospendi") : t("Riattiva"));
      toggle.onclick = async () => {
        try { await api("PUT", "/api/v1/recording-rules/" + r.id, ruleInput(r, { enabled: !r.enabled })); render(); } catch (e) { flash(e.message); }
      };
      const del = el("button", { className: "small danger focusable" }, t("Elimina"));
      del.onclick = async () => {
        if (!confirm(t("Eliminare la regola “{name}”? Le registrazioni già fatte restano; quelle in programma vengono tolte.", { name: r.name }))) return;
        try { await api("DELETE", "/api/v1/recording-rules/" + r.id); render(); } catch (e) { flash(e.message); }
      };
      box.append(el("div", { className: "actions" }, edit, toggle, del));
      out.push(box);
    }
    out.push(el("section", { className: "form-card" }, el("h2", { className: "section" }, t("Nuova regola")), ruleForm()));
    return out;
  }

  function ruleInput(r, extra) {
    return { name: r.name, all: r.spec.all, any: r.spec.any || [], none: r.spec.none || [], section: r.spec.section || "", live_only: !!r.spec.live_only, keep: r.keep,
      channel_id: 0, channel: r.spec.channel || "", enabled: r.enabled, ...extra };
  }

  let channelsCache;
  function ruleForm(existing) {
    const f = (label, input) => el("label", { className: "rule-field" }, el("span", {}, label), input);
    const name = el("input", { className: "focusable", value: existing ? existing.name : "", placeholder: t("es. Juventus") });
    const all = el("input", { className: "focusable", value: existing ? existing.spec.all.join(", ") : "", placeholder: t("se vuoto, il nome della regola") });
    const any = el("input", { className: "focusable", value: existing && existing.spec.any ? existing.spec.any.join(", ") : "", placeholder: t("es. gara, qualifiche (facoltativo)") });
    const none = el("input", { className: "focusable", value: existing && existing.spec.none ? existing.spec.none.join(", ") : "", placeholder: t("es. replica, highlights") });
    const section = el("select", { className: "focusable" }, el("option", { value: "" }, t("Tutti i canali")), el("option", { value: "sport" }, t("Solo Sport")), el("option", { value: "tv" }, t("Solo TV")));
    section.value = existing ? existing.spec.section || "" : "";
    const channel = el("select", { className: "focusable" }, el("option", { value: "0" }, t("Qualsiasi canale")));
    if (!channelsCache) channelsCache = api("GET", "/api/v1/tv/channels");
    channelsCache.then((list) => {
      channel.append(...list.map((c) => el("option", { value: c.id }, c.number + " " + c.name)));
      if (existing && existing.channel_name) {
        const o = [...channel.options].find((o) => o.textContent.endsWith(" " + existing.channel_name));
        if (o) channel.value = o.value;
      }
    }).catch(() => {});
    const liveOnly = el("input", { className: "focusable", type: "checkbox", checked: !!(existing && existing.spec.live_only) });
    const keep = el("input", { className: "focusable", type: "number", min: 0, value: existing ? existing.keep || "" : "", placeholder: t("tutte") });
    const result = el("ol", { className: "matches" });
    // Without words, the name is what to look for ("Formula 1").
    const allWords = () => words(all.value).length ? words(all.value) : words(name.value.replace(/,/g, " "));
    const read = () => ({ name: name.value || "Anteprima", all: allWords(), any: words(any.value), none: words(none.value),
      section: section.value, live_only: liveOnly.checked, channel_id: Number(channel.value), keep: Number(keep.value) || 0, enabled: existing ? existing.enabled : true });
    let timer;
    const refresh = () => {
      clearTimeout(timer);
      timer = setTimeout(async () => {
        if (!allWords().length) { result.replaceChildren(); return; }
        try {
          const m = await api("POST", "/api/v1/recording-rules/preview", read());
          result.replaceChildren(...(m.length ? m.map((x) => el("li", { className: "match " + x.state },
            el("span", { className: "when" }, day(x.start) + " " + hhmm(x.start) + " · " + x.channel),
            el("strong", {}, x.title),
            el("span", { className: "what" }, (MATCH[x.state] || x.state) + (x.note ? ": " + x.note : ""),
              x.airing && x.airing.kind === "live" ? el("small", { className: "live" }, " · " + t("diretta: {why}", { why: x.airing.why })) : null)))
            : [el("li", { className: "empty" }, t("Nessun programma corrispondente nella guida dei prossimi giorni"))]));
        } catch (e) { result.replaceChildren(el("li", { className: "empty" }, e.message)); }
      }, 400);
    };
    const day = (ts) => new Date(ts * 1000).toLocaleDateString(locale, { weekday: "short", day: "numeric" });
    const save = el("button", { className: "small primary focusable", type: "submit" }, existing ? t("Salva la regola") : t("Crea la regola"));
    const form = el("form", { className: "rule-form" }, f(t("Nome"), name), f(t("Deve contenere (tutte)"), all), f(t("E almeno una di"), any),
      f(t("Escludi se contiene"), none), f(t("Sezione"), section), f(t("Canale"), channel),
      f(t("Solo dirette"), el("span", { className: "check" }, liveOnly, t("salta repliche, differite e sintesi"))), f(t("Tieni le ultime"), keep),
      el("p", { className: "empty" }, t("Anteprima di cosa registrerebbe:")), result, save);
    form.addEventListener("input", refresh);
    form.addEventListener("change", refresh);
    if (!existing) assistant().then((on) => {
      if (!on) return;
      const text = el("input", { className: "focusable", type: "text", maxLength: 500, placeholder: t("es. registra le gare di Formula 1 in diretta, non le qualifiche; oppure: il film di stasera alle 21 su Rai 1") });
      const go = el("button", { className: "small focusable", type: "button" }, t("Compila con l'assistente"));
      const said = el("p", { className: "empty" });
      const once = el("div", { className: "rule-once" });
      once.hidden = true;
      // A day or a time in the sentence: the programme then may be the one
      // meant, to record once, rather than every airing of the title.
      const showOnce = (list) => {
        once.replaceChildren();
        once.hidden = !list.length;
        if (!list.length) return;
        once.append(el("p", {}, list.length === 1 ? t("Nella guida a quell'ora c'è questo programma. È quello che vuoi registrare?")
          : t("Nella guida a quell'ora ci sono questi programmi. È uno di questi?")));
        for (const m of list) {
          const just = el("button", { className: "small primary focusable", type: "button" }, t("Solo questa volta"));
          just.onclick = async () => {
            just.disabled = true;
            try {
              await api("POST", "/api/v1/recordings", { channel_id: m.channel_id, start: m.start, stop: m.stop, title: m.title, from_guide: true });
              flash(t("Registrazione programmata"));
              render();
            } catch (e) { flash(e.message); just.disabled = false; }
          };
          const every = el("button", { className: "small focusable", type: "button" }, t("Ogni volta che va in onda"));
          every.onclick = () => {
            // The rule for this title: its name is what to look for.
            name.value = m.title;
            all.value = m.title.replace(/,/g, " ");
            any.value = "";
            none.value = "";
            once.hidden = true;
            said.textContent = t("Regola per “{title}”: controlla l'anteprima e salva.", { title: m.title });
            refresh();
            name.focus();
          };
          once.append(el("div", { className: "once-item" },
            el("span", { className: "when" }, day(m.start) + " " + hhmm(m.start) + "–" + hhmm(m.stop) + " · " + m.channel),
            el("strong", {}, m.title), el("div", { className: "buttons" }, just, every)));
        }
        once.append(el("p", { className: "empty" }, t("Se non è nessuno di questi, la regola qui sotto registra ogni programma che corrisponde.")));
      };
      go.onclick = async () => {
        if (!text.value.trim()) return;
        go.disabled = true;
        showOnce([]);
        said.textContent = t("L'assistente sta scrivendo la regola…");
        try {
          const p = await api("POST", "/api/v1/assistant/rule", { text: text.value });
          const r = p.rule;
          name.value = r.name;
          all.value = (r.all || []).join(", ");
          any.value = (r.any || []).join(", ");
          none.value = (r.none || []).join(", ");
          section.value = r.section || "";
          await channelsCache.catch(() => {});
          channel.value = String(r.channel_id || 0);
          liveOnly.checked = !!r.live_only;
          keep.value = r.keep || "";
          said.textContent = (p.explanation ? p.explanation + " " : "") + t("Controlla l'anteprima e salva.");
          refresh();
          showOnce(p.once || []);
          const first = once.querySelector("button");
          if (first) first.focus();
        } catch (e) { said.textContent = e.message; }
        go.disabled = false;
      };
      text.setAttribute("enterkeyhint", "go");
      text.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); go.click(); } });
      form.prepend(el("div", { className: "rule-ask" }, f(t("Descrivi la regola"), text), go, said, once));
      if (pendingRule) { text.value = pendingRule; pendingRule = ""; text.scrollIntoView({ block: "center" }); go.click(); }
    });
    form.onsubmit = async (e) => {
      e.preventDefault();
      try {
        const body = { ...read(), name: name.value };
        const r = existing ? await api("PUT", "/api/v1/recording-rules/" + existing.id, body) : await api("POST", "/api/v1/recording-rules", body);
        flash(t("Regola salvata: {note}", { note: r.last_note || "" }));
        render();
      } catch (err) { flash(err.message); }
    };
    if (existing) refresh();
    return form;
  }

  async function deleteRecording(id, status) {
    const ask = status === "recording" ? t("Interrompere la registrazione ed eliminare quanto registrato finora?")
      : status === "scheduled" ? t("Annullare la registrazione in programma?") : t("Eliminare la registrazione?");
    if (!confirm(ask)) return;
    try { await api("DELETE", "/api/v1/recordings/" + id); flash(status === "scheduled" ? t("Registrazione annullata") : t("Registrazione eliminata")); render(); }
    catch (e) { flash(e.message); }
  }

  // Ends a recording in progress now; what has been recorded stays.
  async function stopRecording(id) {
    if (!confirm(t("Fermare la registrazione? Quanto registrato finora resta da guardare."))) return;
    flash(t("Fermo la registrazione…"));
    try { await api("POST", "/api/v1/recordings/" + id + "/stop"); flash(t("Registrazione fermata")); render(); }
    catch (e) { flash(e.message); }
  }

  // ---- Detail ----
  // Plot, credits and backdrop: from the provider (and TMDB when
  // configured), loaded after the page opens so it is never slower.
  async function showDetails(item) {
    const panel = $("detail");
    $("detail-plot").textContent = "";
    $("detail-credits").replaceChildren();
    $("cast").hidden = true;
    $("detail-image").hidden = true;
    setArt($("detail-art"), null);
    let d;
    try { d = await api("GET", "/api/v1/items/" + item.id + "/details"); } catch { $("detail-image").hidden = !item.logo; return; }
    if (state.detail !== item) return;
    if (d.title && d.title.toLowerCase() !== item.display_title.toLowerCase()) $("detail-title").textContent = d.title;
    if (d.logo) titleLogo($("detail-title"), d.logo, $("detail-title").textContent);
    $("detail-meta").replaceChildren(...detailMeta(item, d));
    $("detail-plot").textContent = d.plot || "";
    const people = (d.people || []).slice(0, 12);
    const rows = [[t("Regia"), d.director], [t("Cast"), people.length ? "" : d.cast], [t("Uscita"), released(d.released)], [t("Paese"), d.country],
      [t("Titolo originale"), d.original_title && d.original_title !== item.display_title ? d.original_title : ""]];
    for (const [k, v] of rows) if (v) $("detail-credits").append(el("dt", {}, k), el("dd", {}, v));
    // The cast with TMDB's photos, initials without one. Not focusable:
    // there is no search by person to open.
    $("cast-list").replaceChildren(...people.map((p) => {
      const initials = el("span", { className: "cast-photo" }, p.name.split(/\s+/).slice(0, 2).map((w) => w[0] || "").join("").toUpperCase());
      const face = p.photo ? el("img", { className: "cast-photo", src: p.photo, alt: "", loading: "lazy" }) : initials;
      if (p.photo) face.onerror = () => face.replaceWith(initials);
      return el("li", {}, face, el("strong", {}, p.name), p.role ? el("span", {}, p.role) : null);
    }));
    $("cast").hidden = !people.length;
    if (!d.local && item.kind === "movie") addDownloadButtons(item);
    if (d.trailer) {
      const a = el("a", { className: "button focusable trailer", href: "https://www.youtube.com/watch?v=" + encodeURIComponent(d.trailer),
        target: "_blank", rel: "noopener noreferrer" });
      window.VodMax.label(a, "circle-play", "Trailer");
      $("detail-actions").append(a);
    }
    // The backdrop over the whole top of the page, fading into the ambient;
    // the poster only when there is none.
    $("detail-image").hidden = Boolean(d.backdrop) || !item.logo;
    panel.classList.toggle("with-backdrop", Boolean(d.backdrop));
    if (d.backdrop) {
      window.VodMax.setAmbient(d.backdrop);
      const art = await fadedArt(d.backdrop, $("detail-art"), false, true);
      if (state.detail === item) setArt($("detail-art"), art);
    }
  }

  // TMDB's ISO date in words ("1 gennaio 2010"); a provider's free text as it is.
  const released = (s) => /^\d{4}-\d{2}-\d{2}$/.test(s || "")
    ? new Date(s + "T00:00").toLocaleDateString(locale, { day: "numeric", month: "long", year: "numeric" }) : s;

  // Year, duration, genres, versions as text; rating and quality as pills.
  function detailMeta(item, d = {}) {
    return metaLine({ year: item.year, quality: item.quality, rating: item.rating || d.rating,
      text: [d.duration ? Math.round(d.duration / 60) + " min" : "", d.genre, item.variant_count > 1 ? t("{n} versioni", { n: item.variant_count }) : ""] });
  }

  // "Titoli simili": titles of the catalogue sharing genres, as cards.
  async function showSimilar(item) {
    $("similar").hidden = true;
    $("similar-row").replaceChildren();
    const list = await api("GET", "/api/v1/items/" + item.id + "/similar").catch(() => []);
    if (state.detail !== item || !list.length) return;
    $("similar-row").replaceChildren(...itemCards(list));
    $("similar").hidden = false;
  }

  // ---- Downloads to the server's disk ----
  // The list is read once per page to know whether downloads exist here
  // (not on a device running alone) and what is already downloaded.
  let downloads = null;
  async function downloadList(fresh) {
    if (!downloads || fresh) downloads = await api("GET", "/api/v1/downloads").catch(() => false);
    return downloads;
  }
  const dlState = { queued: t("In coda"), downloading: t("In download"), paused: t("In pausa"), done: t("Scaricato"), failed: t("Non riuscito") };
  async function addDownloadButtons(item, season) {
    const who = await window.VodMax.me().catch(() => null);
    const list = await downloadList();
    if (!list || state.detail !== item || (who && who.profile && who.profile.kind === "kids")) return;
    if (item.kind === "series") {
      // A series of a collection is already on disk.
      const d = await api("GET", "/api/v1/items/" + item.id + "/details").catch(() => ({}));
      if (d.local || state.detail !== item) return;
    }
    const actions = $("detail-actions");
    const add = (label, body) => {
      const b = el("button", { className: "button focusable download" });
      window.VodMax.label(b, "download", label);
      b.onclick = async () => {
        try {
          const r = await api("POST", "/api/v1/downloads", body);
          flash(r.added ? t("Aggiunti ai download: {n}", { n: r.added }) + (r.already ? " · " + t("già presenti: {n}", { n: r.already }) : "")
            : t("Già nei download"));
          downloadList(true);
        } catch (e) { flash(e.message); }
      };
      actions.insertBefore(b, actions.querySelector(".trailer"));
    };
    if (item.kind === "movie") {
      const d = list.find((x) => x.media === "movie" && x.media_id === item.id);
      if (d) {
        const s = el("span", { className: "empty dl-state" }, "⬇ " + (dlState[d.status] || d.status));
        actions.insertBefore(s, actions.querySelector(".trailer"));
      } else add(t("Scarica"), { media: "movie", id: item.id });
    } else {
      if (season) add(t("Scarica la stagione {n}", { n: season }), { media: "season", id: item.id, season });
      add(t("Scarica la serie"), { media: "series", id: item.id });
    }
  }

  async function openDetail(item) {
    // Back returns to what opened it (the card in a long grid, a pick).
    if (!state.detail) state.detailFrom = document.activeElement;
    state.detail = item;
    $("detail-title").textContent = item.display_title;
    $("detail-meta").replaceChildren(...detailMeta(item));
    if (item.logo) $("detail-image").src = item.logo;
    $("detail").scrollTop = 0; // a similar title opens at its top
    // A poster the server cannot fetch (404, 502) leaves no broken-image box.
    $("detail-image").onerror = () => { $("detail-image").hidden = true; };
    showDetails(item);
    showSimilar(item);
    $("seasons").replaceChildren();
    $("episodes").replaceChildren();
    $("episodes-title").hidden = item.kind !== "series";
    const actions = $("detail-actions");
    actions.replaceChildren();
    $("detail").hidden = false;
    if (item.kind === "series") {
      actions.append(el("p", { className: "empty" }, t("Caricamento episodi…")));
      let eps;
      try { eps = await api("GET", "/api/v1/series/" + item.id + "/episodes"); }
      catch (e) { if (state.detail === item) actions.replaceChildren(el("p", { className: "empty" }, e.message)); return; }
      // Another title opened meanwhile (its episodes came from the cache):
      // this answer must not draw its seasons on that page.
      if (state.detail !== item) return;
      actions.replaceChildren();
      renderSeasons(item, eps);
      addListButton(item);
      return;
    }
    // Only the saved position: a playback session is created on "play",
    // because preparing it may already use the provider connection.
    const saved = await api("GET", "/api/v1/progress/movie/" + item.id).catch(() => null);
    if (state.detail !== item) return;
    const resume = saved && !saved.completed && saved.position > 30 ? saved : null;
    // Play comes first: the download button may have arrived meanwhile.
    const main = [];
    if (resume) {
      const r = el("button", { className: "button primary focusable" });
      window.VodMax.label(r, "play", t("Riprendi da {time}", { time: clock(resume.position) }));
      r.onclick = () => play("movie", item.id, item.display_title, resume.position);
      main.push(r);
    }
    const start = el("button", { className: "button focusable" + (resume ? "" : " primary") });
    window.VodMax.label(start, resume ? "undo-2" : "play", resume ? t("Dall'inizio") : t("Guarda"));
    start.onclick = () => play("movie", item.id, item.display_title, 0);
    main.push(start);
    if (item.variant_count > 1) {
      const v = el("button", { className: "button focusable" });
      v.dataset.variants = "1";
      window.VodMax.label(v, "layers", t("Altre versioni"));
      v.onclick = () => showVariants(item);
      main.push(v);
    }
    actions.prepend(...main);
    addListButton(item);
    // The trailer (added when the details arrive) always comes last.
    const trailer = actions.querySelector(".trailer");
    if (trailer) actions.append(trailer);
    focusFirst(actions);
  }

  async function showVariants(item) {
    const list = await api("GET", "/api/v1/items/" + item.id + "/variants");
    $("episodes").className = "episodes";
    $("episodes").replaceChildren(...list.map((v) => {
      const b = el("button", { className: "focusable" }, el("span", { className: "n" }, { 4: "4K", 3: "FHD", 2: "HD", 1: "SD" }[v.quality] || ""),
        el("span", {}, v.title), el("span", {}));
      b.onclick = () => play("movie", v.id, v.display_title, 0);
      return el("li", {}, b);
    }));
    focusFirst($("episodes"));
  }

  function renderSeasons(item, eps) {
    const seasons = [...new Set(eps.map((e) => e.season))];
    const next = eps.find((e) => e.progress && !e.progress.completed) || eps.find((e) => !e.progress) || eps[0];
    const showSeason = (season) => {
      for (const b of $("seasons").children) b.classList.toggle("active", Number(b.dataset.season) === season);
      // One card per episode, side by side: picture, number and title,
      // plot. Without a picture, the number on the card.
      $("episodes").className = "episodes ep-row";
      $("episodes").replaceChildren(...eps.filter((e) => e.season === season).map((e) => {
        const pct = e.progress ? (e.progress.completed ? 100 : (e.progress.position / (e.progress.duration || 1)) * 100) : 0;
        const thumb = el("span", { className: "ep-thumb" }, el("span", { className: "ep-num" }, e.number));
        // The badge only over a picture: without one the big number says it.
        if (e.thumb) {
          const img = el("img", { src: e.thumb, alt: "", loading: "lazy" });
          const badge = el("span", { className: "badge" }, "E" + e.number);
          img.onerror = () => { img.remove(); badge.remove(); };
          thumb.append(img, badge);
        }
        if (pct) thumb.append(el("span", { className: "bar" }, el("div", { style: "width:" + pct + "%" })));
        const p = e.progress;
        const info = [e.duration ? Math.round(e.duration / 60) + " min" : "",
          p && p.completed ? t("Visto") : p && p.duration ? t("Mancano {time}", { time: clock(p.duration - p.position) }) : ""].filter(Boolean).join(" · ");
        const b = el("button", { className: "focusable ep-card" + (e === next ? " next" : "") }, thumb,
          el("span", { className: "ep-text" }, el("strong", {}, e.title || t("Episodio {n}", { n: e.number })),
            info ? el("span", { className: "ep-info tnum" }, info) : null,
            e.plot ? el("span", { className: "ep-plot" }, e.plot) : null));
        b.title = e.plot || "";
        b.onclick = () => play("episode", e.id, item.display_title + " · S" + e.season + "E" + e.number + " " + e.title,
          e.progress && !e.progress.completed ? e.progress.position : 0);
        return el("li", {}, b);
      }));
      // The episode to watch in view, measured within the row (the first
      // one leaves the row where it starts).
      const row = $("episodes"), cur = row.querySelector(".ep-card.next");
      row.scrollLeft = 0;
      if (cur) row.scrollLeft = Math.max(0, cur.parentElement.getBoundingClientRect().left - row.getBoundingClientRect().left - 8);
    };
    $("seasons").replaceChildren(...seasons.map((s) => {
      const b = el("button", { className: "chip focusable" }, t("Stagione {n}", { n: s }));
      b.dataset.season = s;
      b.onclick = () => showSeason(s);
      return b;
    }));
    if (next) {
      const code = { episode: "S" + next.season + "E" + next.number };
      const go = el("button", { className: "button primary focusable" });
      window.VodMax.label(go, "play", next.progress && !next.progress.completed ? t("Riprendi {episode}", code) : t("Guarda {episode}", code));
      go.onclick = () => play("episode", next.id, item.display_title + " · S" + next.season + "E" + next.number + " " + next.title,
        next.progress && !next.progress.completed ? next.progress.position : 0);
      $("detail-actions").prepend(go);
      showSeason(next.season);
    }
    addDownloadButtons(item, next ? next.season : 0);
    focusFirst($("detail-actions"));
  }

  $("detail-back").onclick = () => closeDetail();
  function closeDetail() {
    $("detail").hidden = true;
    document.body.classList.remove("with-detail"); // the page below is hidden until then, and hidden things take no focus
    state.detail = null;
    const from = state.detailFrom;
    state.detailFrom = null;
    if (from && document.body.contains(from) && from.offsetParent !== null) {
      from.focus({ preventScroll: true });
      from.scrollIntoView({ block: "nearest" });
    } else focusFirst($("content"));
  }

  // ---- Player ----
  const video = $("video");
  let saveTimer, idleTimer;

  async function play(media, id, title, startAt, mode, keep) {
    diagCount = 0;
    if (!state.play) state.playFrom = document.activeElement;
    let sess, forced = false;
    flash(mode === "remux" ? t("Preparazione della conversione…") : t("Avvio…"));
    const native = window.VodMax.native;
    // The Android player reads MKV, AC-3, HEVC: the file as it is.
    const ask = { viewer, mode: native ? "direct" : mode || "" };
    const vodSession = (body) => api("POST", "/api/v1/vod/" + media + "/" + id + "/session", body);
    try {
      sess = media === "catchup"
        ? await api("POST", "/api/v1/catchup/" + id + "/session", { viewer, ...state.catchup })
        // The only connection is busy on another device (409): asked
        // first, then taken over, once.
        : await vodSession(ask).catch((e) => {
          if (e.status !== 409 || !confirm(t("La connessione dell'abbonamento è in uso su un altro dispositivo. Prendere il controllo?"))) throw e;
          forced = true;
          return vodSession({ ...ask, force: true });
        });
    } catch (e) {
      flash(e.message);
      // The next episode did not start: its card goes, the error stays read.
      if (state.play) { state.play.nextCancelled = true; state.play.nextStarted = false; updateEpisodeExtras(); }
      return;
    }
    if (native) {
      // Taken over: the player's file takes the connection too (a direct
      // file has no query of its own).
      if (forced) sess = { ...sess, url: sess.url + "?force=" + Date.now() };
      playNative({ media, id, title: title || sess.title, sess, startAt: startAt || 0 });
      return;
    }
    stopPlayback(false);
    const tracks = chooseTracks(sess, keep);
    state.play = { media, id, title, sess, offset: 0, duration: sess.duration || 0, fellBack: mode === "remux", ...tracks };
    $("player-title").textContent = title || sess.title;
    $("player-live").hidden = true;
    $("player-audio").hidden = sess.audio.length < 2;
    $("player-subs").hidden = sess.subtitles.length === 0;
    $("player-next").hidden = !sess.next;
    $("skip-intro").hidden = $("next-card").hidden = true;
    $("player").hidden = false;
    // The remembered language needs another track than the direct file plays.
    if (sess.mode === "direct" && (tracks.audio !== 0 || tracks.sub >= 0)) {
      play(media, id, title, startAt, "remux", tracks);
      return;
    }
    // Taken over: the video too, or the other device, reopening in the
    // moment the analysis let the connection go, would get it back.
    load(startAt || 0, forced);
    saveTimer = setInterval(saveProgress, 15000);
  }

  // ---- The Android app: its native player plays, the position comes
  // back here and is saved like in the browser. ----
  let nativeCtx = null;
  function playNative(ctx) {
    nativeCtx = ctx;
    let url = ctx.sess.url, start = ctx.startAt;
    if (ctx.sess.mode === "remux") { // a converted stream starts where asked
      url += "?t=" + Math.floor(start) + "&audio=" + (ctx.sess.default_audio || 0) + "&sub=-1";
      ctx.offset = Math.floor(start);
      start = 0;
    }
    $("status").hidden = true;
    // The language chosen for this profile, as in the browser; the player
    // also saves the position itself while playing (the TV may be switched
    // off with the player open).
    const audio = ctx.sess.audio && ctx.sess.audio.find((tr) => tr.index === chooseTracks(ctx.sess).audio);
    const progress = (ctx.media === "movie" || ctx.media === "episode") ? {
      url: "/api/v1/progress/" + ctx.media + "/" + ctx.id, token: window.VodMax.token(),
      profile: window.VodMax.store.get("profile", ""), offset: ctx.offset || 0, duration: ctx.sess.duration || 0,
    } : null;
    // A converted stream cannot seek: the player reopens it from the new time.
    const restart = ctx.restart || (ctx.sess.mode === "remux"
      ? { url: ctx.sess.url + "?audio=" + (ctx.sess.default_audio || 0) + "&sub=-1&t=", offset: ctx.offset || 0, duration: ctx.sess.duration || 0 } : null);
    window.VodMax.native.play(JSON.stringify({ url, title: ctx.title, start, live: Boolean(ctx.live),
      episode: ctx.media === "episode", next: ctx.sess.next ? ctx.sess.next.title : "", markers: ctx.sess.markers || null,
      audio_language: audio && audio.language ? audio.language : "", progress, restart }));
  }
  window.VodMaxNativeDone = (r) => {
    const c = nativeCtx;
    nativeCtx = null;
    if (!c) return;
    if (r.error) flash(t("Riproduzione non riuscita: {error}", { error: r.error }));
    const position = (c.offset || 0) + r.position;
    const total = (c.sess && c.sess.duration) || r.duration;
    // r.position 0: no answer from the player (closed by Android): the
    // position it saved while playing stands.
    if ((c.media === "movie" || c.media === "episode") && total > 0 && position > 5 && r.position > 0) {
      api("PUT", "/api/v1/progress/" + c.media + "/" + c.id, { position: Math.floor(position), duration: Math.floor(total) })
        .then(() => {
          if (state.detail && !$("detail").hidden) openDetail(state.detail); // the new position on the page
          else if (state.view === "home") render();
        }).catch(() => {});
    }
    // At the end of the archive of a programme still on air, from where the
    // player stopped (apps before 0.6.7 do not say it ended: live TV).
    if (c.media === "catchup" && r.ended && goOnCatchup(c, (c.offset || 0) + r.position,
      (at) => playNative({ ...c, startAt: at, sess: { ...c.sess, duration: c.duration } }))) return;
    if (c.media === "catchup" && state.catchupBack) location.href = state.catchupBack;
    if (c.media === "episode" && c.sess.series_id) {
      // The same care as in the browser (guessMarker): two episodes agree.
      const p = { sess: c.sess, id: c.id };
      if (r.intro_end && r.intro_end <= Math.min(600, total * 0.25) && guessMarker(p, "intro", r.intro_end, 10)) {
        saveMarkers(c.sess, { intro_start: r.intro_start || 0, intro_end: r.intro_end });
      }
      if (r.credits && r.credits <= Math.min(300, total * 0.15) && guessMarker(p, "credits", r.credits, 20)) saveMarkers(c.sess, { credits: r.credits });
      if (r.next && c.sess.next) play("episode", c.sess.next.id, c.sess.next.title, 0);
    }
  };
  window.VodMaxNativeError = (m) => flash(m);
  // Android "Back": close what is open, then the menu's home; false lets
  // the app go to the background.
  window.VodMaxNativeBack = () => {
    if (window.VodMax.sheetOpen()) { window.VodMax.closeSheet(); return true; }
    // Panels with a Back of their own (my profile, the update notice, the
    // PIN of "Chi guarda?"); not the profile choice or pairing, where Back
    // still sends the app to the background.
    const panel = document.querySelector("#my-profile, #update-banner") || [...document.querySelectorAll(".setup-panel")].find((p) => p.back);
    if (panel || $("picker") || !$("player").hidden || !$("detail").hidden || state.query) {
      // From the focused element, so that the panels' capture handlers on
      // document stop it before the menu's own handler sees it.
      (document.activeElement || document.body).dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      return true;
    }
    if (state.view !== "home") { state.view = "home"; render(); return true; }
    return false;
  };

  // ---- Audio and subtitles ----
  // The chosen languages are remembered per profile; a file without them
  // falls back to the server's suggestion (Italian audio, no subtitles).
  const langKey = () => "lang." + (store.get("profile", "") || "").split(".")[0];
  function chooseTracks(sess, keep) {
    if (keep) return keep;
    const pref = store.get(langKey(), {});
    let audio = sess.default_audio || 0, sub = -1;
    const a = pref.audio && sess.audio.find((tr) => tr.language === pref.audio);
    if (a) audio = a.index;
    const st = pref.sub && sess.subtitles.find((tr) => tr.language === pref.sub && tr.supported);
    if (st) sub = st.index;
    return { audio, sub };
  }

  const LANG = { ita: t("Italiano"), it: t("Italiano"), eng: t("Inglese"), en: t("Inglese"), spa: t("Spagnolo"), fre: t("Francese"), fra: t("Francese"),
    ger: t("Tedesco"), deu: t("Tedesco"), por: t("Portoghese"), jpn: t("Giapponese"), kor: t("Coreano"), rus: t("Russo"), ara: t("Arabo"), alb: t("Albanese") };
  function trackLabel(tr, kind) {
    const lang = LANG[tr.language] || (tr.language ? tr.language.toUpperCase() : t("Traccia {n}", { n: tr.index + 1 }));
    const extra = [tr.title && tr.title !== lang ? tr.title : "", kind === "audio" && tr.channels > 2 ? tr.channels === 6 ? "5.1" : t("{n} canali", { n: tr.channels }) : "",
      tr.forced ? t("forzati") : "", kind === "sub" && !tr.supported ? t("immagine, non nel browser") : ""].filter(Boolean);
    return lang + (extra.length ? " (" + extra.join(", ") + ")" : "");
  }

  function openMenu(kind) {
    const p = state.play;
    if (!p) return;
    const menu = $("player-menu");
    if (!menu.hidden && menu.dataset.kind === kind) { closeMenu(); return; }
    menu.dataset.kind = kind;
    const items = kind === "audio"
      ? p.sess.audio.map((tr) => ({ label: trackLabel(tr, "audio"), on: tr.index === p.audio, ok: true, pick: () => switchTracks(tr.index, p.sub, { audio: tr.language }) }))
      : [{ label: t("Nessuno"), on: p.sub < 0, ok: true, pick: () => switchTracks(p.audio, -1, { sub: "" }) },
        ...p.sess.subtitles.map((tr) => ({ label: trackLabel(tr, "sub"), on: tr.index === p.sub, ok: tr.supported, pick: () => switchTracks(p.audio, tr.index, { sub: tr.language }) }))];
    const buttons = items.map((it) => {
      const b = document.createElement("button");
      b.className = "chip focusable" + (it.on ? " active" : "");
      b.textContent = (it.on ? "✓ " : "") + it.label;
      b.disabled = !it.ok;
      b.onclick = () => { closeMenu(); it.pick(); };
      return b;
    });
    const title = Object.assign(document.createElement("div"), { className: "menu-title", textContent: kind === "audio" ? t("Lingua dell'audio") : t("Sottotitoli") });
    menu.replaceChildren(title, ...buttons);
    menu.hidden = false;
    showBar();
    (buttons.find((b) => b.classList.contains("active")) || buttons[0]).focus();
  }
  function closeMenu() { $("player-menu").hidden = true; }

  function switchTracks(audio, sub, remember) {
    const p = state.play;
    if (!p || (audio === p.audio && sub === p.sub)) return;
    store.set(langKey(), { ...store.get(langKey(), {}), ...remember });
    const at = position();
    if (p.sess.mode === "direct") {
      // Another audio track or subtitles: the server converts the file.
      play(p.media, p.id, p.title, at, "remux", { audio, sub });
      return;
    }
    p.audio = audio;
    p.sub = sub;
    load(at);
  }

  // Subtitles: the remux writes WebVTT as it goes; new cues are added to
  // a text track every two seconds. Cue times start with the remux, like
  // the video element's clock.
  let subTimer, subTrack;
  function startSubtitles() {
    clearInterval(subTimer);
    if (!subTrack) { subTrack = video.addTextTrack("subtitles", t("Sottotitoli"), "it"); }
    while (subTrack.cues && subTrack.cues.length) subTrack.removeCue(subTrack.cues[0]);
    const p = state.play;
    if (!p || p.sub < 0 || p.sess.mode !== "remux") { subTrack.mode = "disabled"; return; }
    subTrack.mode = "showing";
    let added = 0;
    const url = p.sess.subtitles_url;
    const pull = async () => {
      if (state.play !== p) { clearInterval(subTimer); return; }
      const text = await fetch(url, { cache: "no-store" }).then((r) => (r.ok ? r.text() : "")).catch(() => "");
      const cues = parseVTT(text);
      for (; added < cues.length; added++) {
        const c = cues[added];
        try { subTrack.addCue(new VTTCue(c.start, c.end, c.text)); } catch { /* bad cue */ }
      }
    };
    pull();
    subTimer = setInterval(pull, 2000);
  }

  function parseVTT(text) {
    const out = [];
    const time = (v) => { const p = v.trim().split(":").map(Number); return p.length === 3 ? p[0] * 3600 + p[1] * 60 + p[2] : p[0] * 60 + p[1]; };
    for (const block of text.replace(/\r/g, "").split(/\n\n+/)) {
      const lines = block.split("\n");
      const i = lines.findIndex((l) => l.includes("-->"));
      if (i < 0) continue;
      const [a, b] = lines[i].split("-->");
      const end = time(b.trim().split(/\s+/)[0]);
      const textLines = lines.slice(i + 1).join("\n").trim();
      // The last block may still be being written.
      if (!textLines || !isFinite(end)) continue;
      out.push({ start: time(a), end, text: textLines.replace(/<[^>]+>/g, "") });
    }
    return out;
  }

  function load(position, force) {
    const p = state.play;
    // A new value per "take control": the server honours each one once, so
    // the browser's retries of this address cannot take the connection back.
    const extra = force ? "force=" + Date.now() : "";
    if (p.sess.mode === "remux") {
      p.offset = position;
      video.src = p.sess.url + "?t=" + Math.floor(position) + "&audio=" + p.audio + "&sub=" + p.sub + (extra ? "&" + extra : "");
      startSubtitles();
    } else {
      p.offset = 0;
      // Until the metadata arrives the video's clock says 0: the position
      // asked for is where the conversion or "take control" start.
      p.startAt = position;
      p.loaded = false;
      video.src = p.sess.url + (extra ? "?" + extra : "");
      // Only for this player: one closed before its metadata arrived must
      // not move the next video (another film, a recording) to its time.
      video.addEventListener("loadedmetadata", () => {
        if (state.play !== p) return;
        p.loaded = true;
        if (position > 0) video.currentTime = position;
      }, { once: true });
    }
    video.play().catch(() => flash(t("Premi OK per avviare")));
    showBar();
  }

  function position() { return state.play ? state.play.offset + (video.currentTime || 0) : 0; }
  function duration() {
    const p = state.play;
    if (!p) return 0;
    if (p.growing) return p.growing.recorded + (performance.now() - p.growing.at) / 1000;
    if (p.reopen) return p.duration; // the video holds only what follows the offset
    return p.sess.mode === "direct" && isFinite(video.duration) ? video.duration : p.duration;
  }

  function toggle() {
    const p = state.play;
    if (!p) return;
    if (p.ts) p.ts.toggle(); else if (video.paused) video.play(); else video.pause();
    updateToggle();
    showBar();
  }

  function updateToggle() {
    const p = state.play;
    const paused = p && (p.ts ? p.ts.paused : video.paused);
    window.VodMax.label($("player-toggle"), paused ? "play" : "pause", paused ? t("Riprendi") : t("Pausa"));
  }
  video.addEventListener("play", updateToggle);
  video.addEventListener("pause", updateToggle);

  function seek(delta) {
    const p = state.play;
    if (!p) return;
    if (p.reopen) { seekTS(position() + delta); return; }
    const target = Math.max(0, Math.min(position() + delta, (duration() || Infinity) - 5));
    learnIntro(position(), target);
    if (p.sess.mode === "direct") video.currentTime = target;
    else load(target);
    showBar();
  }

  // ---- Seek bar: click or drag ----
  function seekTo(target) {
    const p = state.play;
    if (!p) return;
    const d = duration();
    if (p.reopen) { seekTS(target); return; }
    target = Math.max(0, Math.min(target, (d || Infinity) - 3));
    learnIntro(position(), target);
    if (p.sess.mode === "direct") video.currentTime = target;
    else load(target);
    showBar();
  }
  {
    const bar = $("player-seek");
    let dragging = false;
    const at = (e) => {
      const r = bar.getBoundingClientRect();
      return Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
    };
    const preview = (e) => {
      const d = duration();
      if (!d) return;
      const f = at(e);
      $("player-hover").textContent = clock(f * d);
      $("player-hover").style.left = f * 100 + "%";
      $("player-hover").hidden = false;
      if (dragging) $("player-fill").style.width = f * 100 + "%";
      showBar();
    };
    bar.addEventListener("pointerdown", (e) => {
      if (!duration()) return;
      dragging = true;
      bar.classList.add("dragging");
      bar.setPointerCapture(e.pointerId);
      preview(e);
    });
    bar.addEventListener("pointermove", preview);
    bar.addEventListener("pointerleave", () => { if (!dragging) $("player-hover").hidden = true; });
    const finish = (e) => {
      if (!dragging) return;
      dragging = false;
      bar.classList.remove("dragging");
      $("player-hover").hidden = true;
      seekTo(at(e) * duration());
    };
    bar.addEventListener("pointerup", finish);
    bar.addEventListener("pointercancel", () => { dragging = false; bar.classList.remove("dragging"); $("player-hover").hidden = true; });
    state.seekDragging = () => dragging;
  }

  video.addEventListener("timeupdate", updateTime);
  function updateTime() {
    if (!state.play) return;
    const d = duration();
    $("player-pos").textContent = clock(position());
    $("player-dur").textContent = d ? clock(d) : "";
    if (!(state.seekDragging && state.seekDragging())) $("player-fill").style.width = d ? Math.min(100, (position() / d) * 100) + "%" : "0";
  }

  video.addEventListener("error", async () => {
    const p = state.play;
    if (!p || p.recording) { if (p && p.recording) flash(t("Registrazione non riproducibile in questo browser")); return; }
    // Find out why: a busy connection answers 409, one held by a recording 423.
    const status = await fetch(p.sess.url + (p.sess.mode === "remux" ? "?t=0" : ""), { headers: { Range: "bytes=0-0" } })
      .then((r) => { r.body && r.body.cancel(); return r.status; }).catch(() => 0);
    if (state.play !== p) return; // closed meanwhile
    const at = p.sess.mode === "direct" && !p.loaded ? p.startAt : position();
    if (status === 423) {
      flash(t("Tutte le connessioni dell'abbonamento sono occupate da una registrazione in corso"));
      stopPlayback(true);
      return;
    }
    if (status === 409) {
      if (confirm(t("La connessione dell'abbonamento è in uso su un altro dispositivo. Prendere il controllo?"))) load(at, true);
      else stopPlayback(true);
      return;
    }
    if (p.sess.mode === "direct" && !p.fellBack) {
      flash(t("Formato non leggibile dal browser: conversione sul server…"));
      play(p.media, p.id, p.title, at, "remux");
      return;
    }
    flash(t("Riproduzione non riuscita"));
  });

  video.addEventListener("ended", () => {
    const p = state.play;
    if (p && p.sess.next && !p.nextCancelled) { playNext(false); return; }
    if (p && p.media === "catchup" && goOnCatchup(p)) return;
    saveProgress();
    stopPlayback(true);
  });

  // "From the start" of a programme still on air: each opening holds the
  // archive up to when it was made, so at its end the stream is opened
  // again from there with what aired meanwhile. Live TV takes over only
  // near the edge, where an opening would hold a minute at most; an
  // opening that brought nothing new (an archive shorter than the guide)
  // ends too. The Android player reopens the same way (reopen).
  function goOnCatchup(p, at = position(), reopen = load) {
    const c = state.catchup;
    const edge = Math.min(c.stop, Date.now() / 1000) - c.start;
    if (at >= edge - 120 || (p.wentOn !== undefined && at < p.wentOn + 30)) return false;
    p.wentOn = at;
    p.duration = edge;
    reopen(at);
    return true;
  }

  // ---- Episodes: skip the opening, go on to the next one ----
  // Where the opening ends and how long the credits are is learned from
  // the people watching: skipping ahead in the first minutes marks the
  // opening, "Next episode" in the last minutes marks the credits. One gesture is
  // not enough (skipping a boring scene, leaving an episode early): two
  // episodes must agree before the server keeps it, per series, for every
  // device.
  function saveMarkers(sess, m) {
    sess.markers = { ...(sess.markers || {}), ...m };
    api("PUT", "/api/v1/series/" + sess.series_id + "/markers", m).catch(() => {});
  }

  // guessMarker remembers a guess on this device; it returns true when a
  // guess of another episode of the series is within tolerance.
  function guessMarker(p, kind, value, tolerance) {
    let all = {};
    try { all = JSON.parse(localStorage.getItem("vodmax.markerGuess") || "{}") || {}; } catch { /* private mode */ }
    const key = p.sess.series_id + ":" + kind, prev = all[key];
    const agreed = !!prev && prev.episode !== p.id && Math.abs(prev.value - value) <= tolerance;
    if (agreed) delete all[key];
    else all[key] = { episode: p.id, value };
    try { localStorage.setItem("vodmax.markerGuess", JSON.stringify(all)); } catch { /* private mode */ }
    return agreed;
  }

  // Forward seeks close together are one skip; it marks the opening when
  // it starts in the first 5 minutes and jumps 20 s to 10 minutes.
  function learnIntro(from, to) {
    const p = state.play;
    if (!p || p.media !== "episode" || p.skipping) return;
    clearTimeout(p.chainTimer);
    if (to <= from) { p.chain = null; return; }
    if (!p.chain) p.chain = { start: from };
    p.chain.end = to;
    p.chainTimer = setTimeout(() => {
      const c = p.chain;
      p.chain = null;
      if (state.play !== p || !c || c.start > 300 || c.end - c.start < 20 || c.end > Math.min(600, duration() * 0.25)) return;
      if (guessMarker(p, "intro", c.end, 10)) saveMarkers(p.sess, { intro_start: Math.round(c.start), intro_end: Math.round(c.end) });
    }, 8000);
  }

  function skipIntro() {
    const p = state.play;
    if (!p || !p.sess.markers) return;
    p.skipping = true;
    seekTo(p.sess.markers.intro_end);
    p.skipping = false;
    $("skip-intro").hidden = true;
  }

  // byPerson: chosen near the end, maybe during the credits (see
  // guessMarker). Once only: if the next episode does not start, the
  // error stays on screen instead of trying again at every frame.
  function playNext(byPerson) {
    const p = state.play;
    if (!p || !p.sess.next || p.nextStarted) return;
    p.nextStarted = true;
    const left = duration() - position();
    // Credits are the last minutes: not a skip from the middle.
    const credits = byPerson && left >= 15 && left <= Math.min(300, duration() * 0.15);
    if (credits && guessMarker(p, "credits", left, 20)) saveMarkers(p.sess, { credits: Math.round(left) });
    saveProgress();
    play("episode", p.sess.next.id, p.sess.next.title, 0);
  }

  // Focus goes to what appears, so that OK on the remote presses it.
  function reveal(el) {
    if (!el.hidden) return;
    el.hidden = false;
    const a = document.activeElement;
    if (!a || !($("player-bar").contains(a) || $("player-extra").contains(a))) (el.querySelector("button") || el).focus();
  }
  function conceal(el) {
    if (el.hidden) return;
    if (el.contains(document.activeElement)) document.activeElement.blur();
    el.hidden = true;
  }

  function updateEpisodeExtras() {
    const p = state.play;
    if (!p || p.media !== "episode") return;
    const pos = position(), d = duration(), m = p.sess.markers || {};
    if (m.intro_end > 0 && pos >= (m.intro_start || 0) && pos < m.intro_end - 2) reveal($("skip-intro"));
    else conceal($("skip-intro"));
    if (!p.sess.next || !d || p.nextCancelled) { conceal($("next-card")); return; }
    // With the credits known the card comes when they start and plays
    // the next one 10 s later; otherwise it comes 15 s before the end.
    const left = d - pos;
    if (left > (m.credits > 0 ? m.credits : 15)) { p.nextAt = 0; conceal($("next-card")); return; }
    if (!p.nextAt) p.nextAt = m.credits > 0 ? Math.min(d, pos + 10) : d;
    $("next-title").textContent = p.sess.next.title;
    $("next-count").textContent = t("tra {n} s", { n: Math.max(0, Math.ceil(p.nextAt - pos)) });
    reveal($("next-card"));
    if (pos >= p.nextAt && p.nextAt < d) playNext(false);
  }
  video.addEventListener("timeupdate", updateEpisodeExtras);

  // Advertising breaks found by Comskip: a button while inside one.
  function adBreak() {
    const p = state.play;
    if (!p || !p.breaks || !p.breaks.length) return null;
    const pos = position();
    return p.breaks.find((b) => pos >= b.start - 0.5 && pos < b.end - 2) || null;
  }
  video.addEventListener("timeupdate", () => { if (adBreak()) reveal($("skip-ad")); else conceal($("skip-ad")); });
  $("skip-ad").onclick = () => {
    const b = adBreak();
    if (b) seekTo(b.end);
    conceal($("skip-ad"));
  };
  $("skip-intro").onclick = skipIntro;
  $("next-now").onclick = () => playNext(true);
  $("next-cancel").onclick = () => { if (state.play) state.play.nextCancelled = true; conceal($("next-card")); };

  // ---- Diagnostics: player errors, sent to the server log ----
  // Only errors: every event plus a tick every 10 s wrote up to 300 lines
  // per film in the server log and hid real problems. localStorage
  // "vodmax.playerDebug" = true sends them all again from one browser, to
  // chase a playback problem on a device.
  const diagAll = store.get("playerDebug", false);
  let diagCount = 0;
  function diag(event, extra) {
    const p = state.play;
    if (!p || diagCount++ > 300) return;
    const b = video.buffered;
    const ranges = [];
    for (let i = 0; i < b.length && i < 4; i++) ranges.push(b.start(i).toFixed(1) + "-" + b.end(i).toFixed(1));
    api("POST", "/api/v1/debug/player", { event, detail: {
      media: p.media, id: p.id, mode: p.sess && p.sess.mode, t: Number(video.currentTime.toFixed(2)), ready: video.readyState,
      net: video.networkState, paused: video.paused, buffered: ranges.join(" "),
      error: video.error ? video.error.code + " " + (video.error.message || "") : "", ...extra } }).catch(() => {});
  }
  for (const ev of diagAll ? ["loadstart", "loadedmetadata", "canplay", "playing", "waiting", "stalled", "pause", "play", "seeking", "seeked", "error", "emptied", "abort"] : ["error"]) {
    video.addEventListener(ev, () => diag(ev));
  }
  if (diagAll) setInterval(() => { if (state.play && !video.paused) diag("tick"); }, 10000);

  function saveProgress() {
    const p = state.play;
    if (!p || p.recording || p.media === "catchup" || !duration()) return Promise.resolve();
    return api("PUT", "/api/v1/progress/" + p.media + "/" + p.id, { position: Math.floor(position()), duration: Math.floor(duration()) })
      .catch(() => {});
  }

  function stopPlayback(save) {
    const saved = save ? saveProgress() : Promise.resolve();
    // An archive opened from the TV or the guide: closing goes back there.
    if (save && state.play && state.play.media === "catchup" && state.catchupBack) {
      location.href = state.catchupBack;
      return;
    }
    clearInterval(saveTimer);
    clearInterval(subTimer);
    if (subTrack) subTrack.mode = "disabled";
    closeMenu();
    // Only when the player closes: play() also passes here (save false) for
    // the next episode, another audio track or the server's conversion,
    // and full screen must stay.
    if (save && document.fullscreenElement) document.exitFullscreen().catch(() => {});
    if (state.play && state.play.ts) state.play.ts.destroy();
    video.pause();
    video.removeAttribute("src");
    video.load();
    state.play = null;
    $("player").hidden = true;
    // Closed by the person: the detail page shows the new position
    // ("Riprendi da…", the next episode), and the focus goes back where it
    // was instead of being lost.
    if (save) {
      const from = state.playFrom;
      saved.then(() => {
        if (state.play) return;
        if (state.detail && !$("detail").hidden) openDetail(state.detail);
        else if (from && document.body.contains(from)) from.focus();
        else focusFirst($("content"));
      });
    }
  }

  function hideBar() {
    clearTimeout(idleTimer);
    closeMenu();
    $("player-bar").classList.add("idle");
    $("player").classList.add("cursor-hidden");
  }

  function showBar() {
    updateTime();
    $("player-bar").classList.remove("idle");
    $("player").classList.remove("cursor-hidden");
    updateToggle();
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      const p = state.play;
      if (p && (p.ts ? p.ts.paused : video.paused)) return; // stays visible while paused
      $("player-bar").classList.add("idle");
      $("player").classList.add("cursor-hidden");
    }, 4000);
  }

  // A small panel with a few choices: resolves to the chosen value, or
  // null when closed (Back, a click outside).
  function choose(title, options) {
    return new Promise((resolve) => {
      const back = document.activeElement;
      const scrim = el("div", { className: "scrim" });
      const buttons = options.map(([value, text, icon]) => {
        const b = el("button", { className: "chip focusable" }, el("span", { className: "name" }, text));
        if (icon) window.VodMax.label(b, icon, text);
        b.onclick = () => close(value);
        return b;
      });
      const panel = el("section", { className: "picker choice", id: "picker" }, el("h2", {}, title), el("div", { className: "options" }, ...buttons));
      const close = (value) => { panel.remove(); scrim.remove(); if (back && value === null) back.focus(); resolve(value); };
      scrim.onclick = () => close(null);
      panel.close = () => close(null);
      document.body.append(scrim, panel);
      buttons[0].focus();
    });
  }

  // Recordings in progress start from the beginning or from the live
  // edge, and can be moved in up to what has been recorded so far: the
  // server reopens the stream from the chosen second (?t=).
  async function playRecording(r) {
    let from = 0;
    if (r.status === "recording") {
      from = await choose(t("Registrazione in corso"), [[0, t("Dall'inizio"), "undo-2"], ["live", t("In diretta"), "radio"]]);
      if (from === null) return;
    }
    let url, breaks, recorded, ts, length;
    try { ({ url, commercials: breaks, recorded, ts, duration: length } = await api("POST", "/api/v1/recordings/" + r.id + "/ticket")); } catch (e) { flash(e.message); return; }
    const growing = typeof recorded === "number";
    if (!growing) from = 0; // finished in the meantime: a normal file
    // MPEG-TS (in progress, or left so when the conversion to MP4 failed):
    // players cannot seek far in it, so the server reopens it at the
    // chosen second (?t=). Finished MP4 files are seekable as they are.
    const isTS = growing || Boolean(ts);
    const startSec = from === "live" ? Math.max(0, recorded - 4) : 0;
    if (window.VodMax.native) {
      // Apps before 0.6.7 show a dead time bar on a reopened stream: a
      // finished TS goes to them as a plain file, as it did then.
      const reopen = growing || (isTS && !window.VodMax.newerVersion("0.6.7", window.VodMax.native.version()));
      playNative({ media: "recording", id: r.id, title: r.title + " · " + r.channel, startAt: 0, live: growing,
        sess: { mode: "direct", url: reopen ? url + "?t=" + from : url },
        restart: reopen ? { url: url + "?t=", offset: startSec, duration: Math.max(1, growing ? recorded : length || 0), grows: growing } : null });
      return;
    }
    stopPlayback(false);
    // The real length from the server: a recording stopped early is
    // shorter than its scheduled slot.
    state.play = { recording: r, title: r.title, sess: { mode: "direct", url }, offset: 0, duration: length || r.stop - r.start, breaks: breaks || [] };
    if (growing) state.play.growing = { recorded, at: performance.now() };
    $("player-audio").hidden = $("player-subs").hidden = true;
    $("player-title").textContent = r.title + " · " + r.channel;
    $("player").hidden = false;
    if (isTS && window.mpegts && mpegts.isSupported()) {
      state.play.reopen = true;
      openTS(from);
      $("player-live").hidden = !growing;
    } else {
      video.src = url;
      video.play().catch(() => flash(t("Premi OK per avviare")));
    }
    showBar();
  }

  function recordingError(type, detail) {
    const p = state.play;
    const fail = () => flash(t("Registrazione non riproducibile ({detail})", { detail }));
    if (!p || !p.recording || !p.reopen || p.swapped) { fail(); return; }
    // The conversion to MP4 may have ended while it played: the TS is gone
    // and ?t= now answers the MP4. Once, ask again and play that file from
    // the same second.
    p.swapped = true;
    const at = position();
    api("POST", "/api/v1/recordings/" + p.recording.id + "/ticket").then((tk) => {
      if (state.play !== p) return;
      if (tk.ts || typeof tk.recorded === "number") { fail(); return; }
      if (p.ts) { p.ts.destroy(); p.ts = null; }
      p.reopen = false;
      p.growing = null;
      p.offset = 0;
      p.sess.url = tk.url;
      if (tk.duration) p.duration = tk.duration;
      $("player-live").hidden = true;
      video.src = tk.url;
      video.addEventListener("loadedmetadata", () => { if (state.play === p) video.currentTime = at; }, { once: true });
      video.play().catch(() => flash(t("Premi OK per avviare")));
      showBar();
    }).catch(fail);
  }

  // Opens a TS recording at a second (or "live" while it is recorded).
  // The video's clock starts again at 0: position() adds the offset.
  function openTS(from) {
    const p = state.play;
    if (!p || !p.reopen) return;
    if (p.ts) p.ts.destroy();
    p.offset = from === "live" ? Math.max(0, duration() - 4) : Math.max(0, from);
    p.ts = window.VodMax.tsPlayer(video, p.sess.url + "?t=" + (from === "live" ? "live" : Math.floor(p.offset)), { live: true, onError: recordingError });
    showBar();
  }

  // In a TS recording: inside what the browser holds, a plain seek;
  // elsewhere the stream is reopened there.
  function seekTS(target) {
    const p = state.play;
    const d = duration();
    if (p.growing && target >= d - 8) { openTS("live"); return; }
    target = Math.max(0, d > 5 ? Math.min(target, d - 5) : target);
    const b = video.buffered, at = target - p.offset;
    for (let i = 0; i < b.length; i++) {
      if (at >= b.start(i) && at <= b.end(i) - 1) { p.ts.seek(target - position()); showBar(); return; }
    }
    openTS(target);
  }

  // ---- Remote control navigation ----
  function focusables(root = document) {
    const layer = $("picker") || (!$("player").hidden ? $("player") : !$("detail").hidden ? $("detail") : document);
    return [...(root === document ? layer : root).querySelectorAll(".focusable")].filter((e) => e.offsetParent !== null);
  }

  function focusFirst(root) {
    const list = root ? [...root.querySelectorAll(".focusable")].filter((e) => e.offsetParent !== null) : [];
    const target = root && root.classList && root.classList.contains("focusable") ? root : list[0];
    if (target) target.focus({ preventScroll: false });
  }

  // Moves focus to the nearest element in the arrow's direction.
  function moveFocus(dir) {
    const current = document.activeElement;
    const all = focusables();
    if (!current || !all.includes(current)) { all[0] && all[0].focus(); return; }
    const a = current.getBoundingClientRect();
    const ax = a.left + a.width / 2, ay = a.top + a.height / 2;
    const horizontal = dir === "ArrowLeft" || dir === "ArrowRight";
    let found = [];
    for (const e of all) {
      if (e === current) continue;
      const b = e.getBoundingClientRect();
      const bx = b.left + b.width / 2, by = b.top + b.height / 2;
      const dx = bx - ax, dy = by - ay;
      const ok = { ArrowLeft: dx < -4, ArrowRight: dx > 4, ArrowUp: dy < -4, ArrowDown: dy > 4 }[dir];
      if (!ok) continue;
      // Up and Down go from row to row and keep the column: "Vedi tutti"
      // is reached with Right from the row's last card (below).
      if (!horizontal && e.classList.contains("see-all")) continue;
      // Across: the gap between the two boxes (0 when they line up), so a
      // field to the side of its label is not skipped for a button below.
      const along = horizontal ? Math.abs(dx) : Math.abs(dy);
      const across = horizontal ? Math.max(0, Math.max(a.top, b.top) - Math.min(a.bottom, b.bottom))
        : Math.max(0, Math.max(a.left, b.left) - Math.min(a.right, b.right));
      // Left and Right stay on the line: in a row of wide episode cards the
      // season tab above is nearer than the next card, and at the end of a
      // row the nearest thing to the right is a card rows below.
      if (horizontal && across > 0) continue;
      found.push({ e, b, score: along + across * 3 });
    }
    // The header stays on top while the page scrolls under it: a row gone
    // up under it is still the row above, and nothing is above the header.
    const top = document.querySelector("header.top");
    if (dir === "ArrowUp" && top) {
      const inTop = (c) => top.contains(c.e);
      if (top.contains(current)) found = found.filter(inTop);
      else if (found.some((c) => !inTop(c))) found = found.filter((c) => !inTop(c));
    }
    // Up and Down go to the nearest row: the element whose edge is closest
    // and those level with it. Scored on centres alone, a button straight
    // above but far away beat the row just above it, offset sideways (from
    // the season tab Up jumped to "Indietro" over the action buttons).
    if (!horizontal && found.length) {
      const gap = (b) => dir === "ArrowUp" ? a.top - b.bottom : b.top - a.bottom;
      const n = found.reduce((x, y) => gap(y.b) < gap(x.b) ? y : x).b;
      found = found.filter((c) => c.b === n || (c.b.bottom > n.top && c.b.top < n.bottom));
    }
    let best = null, bestScore = Infinity;
    for (const c of found) if (c.score < bestScore) { bestScore = c.score; best = c.e; }
    const row = current.closest(".row");
    if (!best && dir === "ArrowRight" && row && row.previousElementSibling) best = row.previousElementSibling.querySelector(".see-all");
    if (!best) return;
    // Scrolled by the least that shows it whole (scroll-margin keeps the
    // focus ring in view): focus() alone centres a card that is off screen,
    // so quick presses made a row jump back and forth.
    best.focus({ preventScroll: true });
    best.scrollIntoView({ block: "nearest", inline: "nearest" });
  }

  document.addEventListener("keydown", (e) => {
    const key = keyOf(e);
    if (!$("player").hidden) {
      // Moving among the on-screen buttons and track menu.
      const inBar = document.activeElement && ($("player-bar").contains(document.activeElement) || $("player-extra").contains(document.activeElement));
      if (inBar) {
        if (key.startsWith("Arrow")) { moveFocus(key); showBar(); e.preventDefault(); return; }
        if (key === "OK") { document.activeElement.click(); showBar(); e.preventDefault(); return; }
        if (key === "Back" || key === "Backspace") {
          if (!$("player-menu").hidden) { closeMenu(); $("player-toggle").focus(); }
          else if ($("player-extra").contains(document.activeElement)) stopPlayback(true); // a card has the focus by itself
          else document.activeElement.blur();
          e.preventDefault();
          return;
        }
      }
      if (key === "ArrowDown") { showBar(); $("player-toggle").focus(); e.preventDefault(); return; }
      const handled = {
        OK: toggle, PlayPause: toggle, " ": toggle,
        Play: () => { if (state.play && (state.play.ts ? state.play.ts.paused : video.paused)) toggle(); },
        Pause: () => { if (state.play && !(state.play.ts ? state.play.ts.paused : video.paused)) toggle(); },
        ArrowLeft: () => seek(-10), ArrowRight: () => seek(30), ArrowUp: showBar, ArrowDown: showBar,
        Back: () => stopPlayback(true), Stop: () => stopPlayback(true), Backspace: () => stopPlayback(true),
      }[key];
      if (handled) { handled(); e.preventDefault(); }
      return;
    }
    if ($("picker")) {
      const typing = document.activeElement && document.activeElement.tagName === "INPUT";
      if (key === "Back" || (key === "Backspace" && !typing)) { $("picker").close(); e.preventDefault(); return; }
      if (key.startsWith("Arrow") && !(typing && (key === "ArrowLeft" || key === "ArrowRight"))) { moveFocus(key); e.preventDefault(); }
      return;
    }
    const active = document.activeElement;
    if (key === "OK" && active && active.type === "checkbox") { active.click(); e.preventDefault(); return; }
    // Text fields (rule form, "Stasera"): Up and Down leave them, ◀ ▶ leave
    // them at the start or the end of the text, Back leaves them; lists
    // (select) open with OK.
    const field = active && active !== $("search") && (active.tagName === "INPUT" || active.tagName === "SELECT" || active.tagName === "TEXTAREA");
    if (field && !active.closest(".setup-panel")) {
      const text = active.tagName !== "SELECT" && typeof active.selectionStart === "number";
      const atStart = !text || (active.selectionStart === 0 && active.selectionEnd === 0);
      const atEnd = !text || active.selectionEnd === active.value.length;
      if (key === "ArrowUp" || key === "ArrowDown" || (key === "ArrowLeft" && atStart) || (key === "ArrowRight" && atEnd)) {
        moveFocus(key);
        e.preventDefault();
      } else if (key === "Back") {
        moveFocus("ArrowDown");
        if (document.activeElement === active) moveFocus("ArrowUp");
        e.preventDefault();
      }
      return;
    }
    if (field) return; // a full-screen panel's form (token, PIN)
    const typing = active === $("search");
    if (typing && !["ArrowDown", "Back", "OK"].includes(key)) return;
    if (key.startsWith("Arrow")) { moveFocus(key); e.preventDefault(); return; }
    if (key === "OK" && typing) { $("search").blur(); focusFirst($("content")); e.preventDefault(); return; }
    if (key === "Back" || (key === "Backspace" && !typing)) {
      if (!$("detail").hidden) closeDetail();
      else if (state.query) { state.query = ""; $("search").value = ""; render(); }
      // As on Android: Back goes to Home, and from Home leaves the TV app
      // (webOS hands Back to the page and closes the app on window.close).
      else if (!window.VodMax.native && state.view !== "home") { state.view = "home"; render(); }
      else if (/Web0S|webOS/.test(navigator.userAgent) && state.view === "home") window.close();
      e.preventDefault();
      return;
    }
    if (key === "Green" || key === "g") { location.href = "/tv.html"; return; }
    if ((key === "Yellow" || key === "p") && document.activeElement && document.activeElement.dataset.recording) {
      toggleProtected(document.activeElement.dataset.recording, document.activeElement.dataset.protected === "1");
      e.preventDefault();
      return;
    }
    if ((key === "Red" || key === "Delete") && document.activeElement && document.activeElement.dataset.continue) {
      hideContinue(document.activeElement);
      e.preventDefault();
      return;
    }
    if ((key === "Red" || key === "Delete") && document.activeElement && document.activeElement.dataset.recording) {
      deleteRecording(document.activeElement.dataset.recording, document.activeElement.dataset.status);
      e.preventDefault();
    }
  });

  for (const tab of document.querySelectorAll(".tab[data-view]")) {
    tab.onclick = () => { state.view = tab.dataset.view; state.query = ""; $("search").value = ""; closeDetailSilently(); render(); };
  }
  function closeDetailSilently() { $("detail").hidden = true; state.detail = null; state.detailFrom = null; }

  let searchTimer;
  $("search").addEventListener("input", () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => { state.query = $("search").value.trim(); render(); }, 350);
  });

  // Mouse and touch: buttons in the bar, click on the picture to pause.
  $("player-bar").addEventListener("click", (e) => {
    const b = e.target.closest("button[data-action]");
    if (!b) return;
    ({
      close: () => stopPlayback(true), back10: () => seek(-10), fwd30: () => seek(30), toggle,
      audio: () => openMenu("audio"), subs: () => openMenu("subs"),
      live: () => {
        const p = state.play;
        if (p && p.growing && duration() - position() > 15) openTS("live");
        else if (p && p.ts) { p.ts.goLive(); showBar(); }
      },
      next: () => playNext(true),
      fullscreen: toggleFullscreen,
    })[b.dataset.action]();
  });
  // Mouse: moving shows the bar, a click pauses. Touch: a tap shows or
  // hides the bar (pausing is its button), as on the TV page.
  let lastPointer = "mouse";
  $("player").addEventListener("pointerdown", (e) => { lastPointer = e.pointerType; }, true);
  video.addEventListener("click", () => {
    if (lastPointer === "mouse") toggle();
    else if ($("player-bar").classList.contains("idle")) showBar();
    else hideBar();
  });
  $("player").addEventListener("pointermove", (e) => { if (e.pointerType === "mouse") showBar(); });

  // Full screen, where the browser allows it (phones, PCs). On the TV the
  // page already fills the screen. A phone turns to landscape.
  const canFullscreen = Boolean(document.fullscreenEnabled && $("player").requestFullscreen) && !/Web0S|webOS/.test(navigator.userAgent);
  $("player-full").hidden = !canFullscreen;
  function toggleFullscreen() {
    if (document.fullscreenElement) { document.exitFullscreen().catch(() => {}); return; }
    $("player").requestFullscreen({ navigationUI: "hide" }).then(() => {
      if (screen.orientation && screen.orientation.lock && matchMedia("(pointer: coarse)").matches) screen.orientation.lock("landscape").catch(() => {});
    }).catch(() => {});
  }
  document.addEventListener("fullscreenchange", () => {
    const full = Boolean(document.fullscreenElement);
    window.VodMax.label($("player-full"), full ? "minimize" : "maximize", full ? t("Esci dallo schermo intero") : t("Schermo intero"));
  });

  addEventListener("pagehide", () => saveProgress());
  // From the TV ("from the start") or the guide ("watch again"):
  // /?catchup=CHANNEL&start=…&stop=…&title=…&back=…
  const asked = new URLSearchParams(location.search);
  window.VodMax.withToken(() => {
    render();
    showDownloadsTab();
    if (asked.get("catchup")) {
      state.catchup = { start: Number(asked.get("start")), stop: Number(asked.get("stop")), title: asked.get("title") || "" };
      // Only a page of this server: browsers read "/\host" and "/\t/host"
      // as another host, so the check is the parsed origin, not the text.
      let back = null;
      try { back = asked.get("back") ? new URL(asked.get("back"), location.origin) : null; } catch { /* not a URL */ }
      state.catchupBack = back && back.origin === location.origin ? back.pathname + back.search + back.hash : "";
      history.replaceState(null, "", "/");
      play("catchup", Number(asked.get("catchup")), state.catchup.title, 0, "remux");
    }
  });
})();
