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
      img.onerror = () => img.replaceWith(el("div", { className: "poster" }, title));
      return img;
    }
    return el("div", { className: "poster" }, title);
  }

  function itemCard(item) {
    const title = item.display_title;
    const card = el("button", { className: "card focusable" }, poster(item, title),
      el("span", { className: "label" }, title),
      el("span", { className: "sub" }, [item.year, item.variant_count > 1 ? t("{n} versioni", { n: item.variant_count }) : ""].filter(Boolean).join(" · ")));
    const q = { 4: "4K", 3: "FHD" }[item.quality];
    if (q) card.append(el("span", { className: "badge" }, q));
    card.onclick = () => openDetail(item);
    card.hero = heroFromItem(item);
    return card;
  }

  function continueCard(c) {
    const title = c.media === "episode" ? c.series_title : c.title;
    const ep = "S" + c.season + "E" + c.number + " · " + c.title;
    const sub = c.next ? t("Prossima puntata") + " · " + ep : c.media === "episode" ? ep : t("Mancano {time}", { time: clock(c.progress.duration - c.progress.position) });
    const pct = c.next ? 0 : c.progress.duration ? (c.progress.position / c.progress.duration) * 100 : 0;
    const card = el("button", { className: "card wide focusable" }, poster({ logo: c.image }, title),
      el("div", { className: "bar" }, el("div", { style: "width:" + pct + "%" })),
      el("span", { className: "label" }, title), el("span", { className: "sub" }, sub));
    card.onclick = () => play(c.media, c.id, title + (c.media === "episode" ? " · " + ep : ""), c.next ? 0 : c.progress.position);
    card.hero = heroFromContinue(c);
    return card;
  }

  // ---- Views ----
  function setTabs() {
    for (const tab of document.querySelectorAll(".tab[data-view]")) tab.classList.toggle("active", tab.dataset.view === state.view);
    window.VodMax.bottomNav(["recordings", "downloads", "mylist"].includes(state.view) ? "more" : state.view);
  }

  async function render() {
    setTabs();
    $("search").placeholder = state.view === "tv" ? t("Cerca un canale (nome o numero)") : t("Cerca film e serie");
    store.set("vodView", state.view);
    const main = $("content");
    main.replaceChildren(el("p", { className: "empty" }, t("Caricamento…")));
    try {
      if (state.view === "tv") await renderTV(main);
      else if (state.query) await renderSearch(main);
      else if (state.view === "home") await renderHome(main);
      else if (state.view === "recordings") await renderRecordings(main);
      else if (state.view === "downloads") await renderDownloads(main);
      else if (state.view === "mylist") await renderMyList(main);
      else await renderGrid(main, state.view);
    } catch (e) {
      main.replaceChildren(el("p", { className: "empty" }, e.message));
    }
    focusFirst(main);
  }

  // ---- TV: the channel list, then full screen ----
  const TV_LISTS = [["", t("Tutti")], ["tv", "TV"], ["sport", t("Sport")], ["radio", t("Radio")], ["favorites", t("Preferiti")]];
  async function renderTV(main) {
    const query = state.query.toLowerCase();
    const list = store.get("tvList", "");
    // A search looks in every channel, whatever the list.
    const q = query ? "" : list === "favorites" ? "?list=favorites" : list ? "?section=" + list : "";
    let channels = await api("GET", "/api/v1/tv/channels" + q);
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
    const items = channels.slice(0, 600).map((c) => {
      const p = c.now;
      const pct = p ? Math.min(100, Math.max(0, ((now - p.start) / (p.stop - p.start)) * 100)) : 0;
      const b = el("button", { className: "focusable" }, el("span", { className: "num" }, c.number || ""),
        c.logo ? el("img", { src: c.logo, alt: "", loading: "lazy" }) : el("span", { className: "nologo" }),
        el("span", { className: "what" }, el("strong", {}, (c.favorite ? "★ " : "") + c.name),
          el("span", {}, p ? hhmm(p.start) + " " + p.title : t("Nessuna informazione")),
          c.next ? el("span", {}, t("Dopo: {time} {title}", { time: hhmm(c.next.start), title: c.next.title })) : null,
          p ? el("div", { className: "bar" }, el("div", { style: "width:" + pct + "%" })) : null));
      b.onclick = () => { location.href = "/tv.html?channel=" + c.id; };
      return el("li", {}, b);
    });
    main.replaceChildren(el("h2", { className: "page-title" }, "TV"), el("div", { className: "tv-tools" }, ...chips, guide),
      query ? el("p", { className: "empty" }, t("{n} canali per “{query}” (nome o numero)", { n: channels.length, query: state.query })) : "",
      items.length ? el("ol", { className: "channels" }, ...items) : el("p", { className: "empty" }, list === "favorites"
        ? t("Nessun preferito: in TV premi il tasto giallo (F) su un canale.") : t("Nessun canale.")));
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

  async function renderHome(main) {
    main.replaceChildren(el("section", { className: "hero skeleton" }), el("h2", { className: "section" }, t("Continua a guardare")), skeletonRow(true),
      el("h2", { className: "section" }, t("Nuovi film")), skeletonRow(false));
    const [cont, films, series, favorites, recordings, mine, trending] = await Promise.all([
      api("GET", "/api/v1/continue"),
      api("GET", "/api/v1/catalog/movie?sort=recent&limit=24"),
      api("GET", "/api/v1/catalog/series?sort=recent&limit=24"),
      api("GET", "/api/v1/tv/channels?list=favorites").catch(() => []),
      api("GET", "/api/v1/recordings").catch(() => []),
      api("GET", "/api/v1/mylist").catch(() => []),
      api("GET", "/api/v1/trending").catch(() => []),
    ]);
    const hero = el("section", { className: "hero" });
    const parts = [hero];
    if (await assistant()) parts.push(tonightBox());
    parts.push(el("h2", { className: "section" }, t("Continua a guardare")));
    parts.push(cont.length ? el("div", { className: "row" }, ...cont.map(continueCard))
      : el("p", { className: "empty" }, t("Qui compaiono i film e gli episodi iniziati e la prossima puntata delle serie che stai seguendo.")));
    if (mine.length) parts.push(el("h2", { className: "section" }, t("La mia lista")), el("div", { className: "row" }, ...mine.slice(0, 24).map(itemCard)));
    // TMDB's week, among the titles you have (with a TMDB key).
    if (trending.length) parts.push(el("h2", { className: "section" }, t("Di tendenza questa settimana")), el("div", { className: "row" }, ...trending.map(itemCard)));
    if (favorites.length) parts.push(el("h2", { className: "section" }, t("In onda sui tuoi canali")), el("div", { className: "row" }, ...favorites.slice(0, 20).map(channelCard)));
    parts.push(el("h2", { className: "section" }, t("Nuovi film")), el("div", { className: "row" }, ...films.items.map(itemCard)));
    parts.push(el("h2", { className: "section" }, t("Nuove serie")), el("div", { className: "row" }, ...series.items.map(itemCard)));
    const done = recordings.filter((r) => r.status === "completed").slice(0, 12);
    if (done.length) parts.push(el("h2", { className: "section" }, t("Registrazioni recenti")), el("div", { className: "row" }, ...done.map(recordingCard)));
    main.replaceChildren(...parts);
    // The featured title: the first thing to continue, else the newest film.
    const first = cont[0] ? heroFromContinue(cont[0]) : films.items[0] ? heroFromItem(films.items[0]) : null;
    if (first) showHero(hero, first);
    let heroTimer;
    main.addEventListener("focusin", (e) => {
      const card = e.target.closest(".card");
      if (!card || !card.hero) return;
      clearTimeout(heroTimer);
      heroTimer = setTimeout(() => showHero(hero, card.hero), 350);
    });
  }

  function heroFromItem(item) {
    return { id: item.id, title: item.display_title, logo: item.logo,
      meta: [item.year, { 4: "4K", 3: "Full HD" }[item.quality], item.rating ? "★ " + Number(item.rating).toLocaleString(locale, { maximumFractionDigits: 1 }) : ""],
      actions: [["primary", "play", t("Guarda"), () => openDetail(item)], ["", "info", t("Dettagli"), () => openDetail(item)]] };
  }
  function heroFromContinue(c) {
    const episode = c.media === "episode";
    const title = episode ? c.series_title : c.title;
    const ep = episode ? "S" + c.season + "E" + c.number + " · " + c.title : "";
    return { id: episode ? c.series_id : c.id, title, logo: c.image,
      meta: [c.next ? t("Prossima puntata") : episode ? t("Continua") : t("Mancano {time}", { time: clock(c.progress.duration - c.progress.position) }), ep],
      actions: [["primary", "play", c.next ? t("Guarda la puntata") : t("Riprendi"), () => play(c.media, c.id, title + (episode ? " · " + ep : ""), c.next ? 0 : c.progress.position)]] };
  }

  let heroShown = null;
  async function showHero(hero, h) {
    heroShown = h;
    const buttons = h.actions.map(([kind, icon, text, fn]) => {
      const b = el("button", { className: "button focusable " + kind });
      window.VodMax.label(b, icon, text);
      b.onclick = fn;
      return b;
    });
    const plot = el("p", { className: "hero-plot" });
    const art = el("div", { className: "hero-art" });
    hero.replaceChildren(art, el("div", { className: "hero-text" }, el("h1", {}, h.title),
      el("p", { className: "hero-meta" }, h.meta.filter(Boolean).join(" · ")), plot, el("div", { className: "hero-actions" }, ...buttons)));
    if (h.logo) art.style.backgroundImage = "url(" + JSON.stringify(h.logo) + ")";
    const d = await detailsOf(h.id);
    if (heroShown !== h) return;
    plot.textContent = d.plot || "";
    if (d.backdrop) {
      const img = new Image();
      img.onload = () => { if (heroShown === h) { art.style.backgroundImage = "url(" + JSON.stringify(d.backdrop) + ")"; art.classList.add("wide"); } };
      img.src = d.backdrop;
    }
  }

  function channelCard(c) {
    const p = c.now;
    const pct = p ? Math.min(100, Math.max(0, ((Date.now() / 1000 - p.start) / (p.stop - p.start)) * 100)) : 0;
    const art = el("div", { className: "poster channel-art" }, c.logo ? el("img", { src: c.logo, alt: "", loading: "lazy" }) : el("span", {}, c.name));
    const card = el("button", { className: "card wide focusable" }, art,
      p ? el("div", { className: "bar" }, el("div", { style: "width:" + pct + "%" })) : null,
      el("span", { className: "label" }, c.number + " · " + c.name), el("span", { className: "sub" }, p ? p.title : t("Nessuna informazione")));
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
        const r = await api("POST", "/api/v1/assistant/tonight", { wish: wish.value });
        result.replaceChildren(r.intro ? el("p", { className: "tonight-intro" }, r.intro) : "", (r.picks || []).length ? el("div", { className: "row" }, ...r.picks.map(pickCard)) : "");
        focusFirst(result.querySelector(".row"));
      } catch (err) { result.replaceChildren(el("p", { className: "empty" }, err.message)); }
      ask.disabled = false;
    };
    return el("section", { className: "tonight" }, el("h2", { className: "section" }, t("Stasera")), form, result);
  }

  // Every pick is a wide card, TV or library alike. A film or series shows
  // its backdrop when it has one, otherwise the whole poster over a blurred
  // copy of itself.
  function pickCard(p) {
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
      if (onAir) { location.href = "/tv.html?channel=" + p.channel_id; return; }
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
    return { sort: "recent", minRating: 0, category: "", categoryName: "", ...store.get("browse." + kind, {}) };
  }

  async function renderGrid(main, kind, cursor, grid) {
    const b = browseState(kind);
    const q = new URLSearchParams({ limit: 60, sort: b.sort });
    if (b.minRating) q.set("min_rating", b.minRating);
    if (b.category) q.set("category", b.category);
    if (cursor) q.set("cursor", cursor);
    if (!grid) main.replaceChildren(el("h2", { className: "page-title" }, kind === "movie" ? t("Film") : t("Serie")), filterBar(kind, b), skeletonRow(false));
    const page = await api("GET", "/api/v1/catalog/" + kind + "?" + q);
    if (!grid) {
      grid = el("div", { className: "grid" });
      main.lastElementChild.replaceWith(page.items.length ? grid : el("p", { className: "empty" }, t("Nessun titolo con questi filtri.")));
    }
    grid.append(...page.items.map(itemCard));
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
      b.category ? chip(t("Togli"), false, () => set({ category: "", categoryName: "" }), "x") : null);
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
      all.onclick = () => { close(); set({ category: "", categoryName: "" }); };
      // Names that start with the text first ("azione" before "animazione").
      const found = list.filter((c) => !f || c.name.toLowerCase().includes(f))
        .sort((x, y) => (f ? Number(!x.name.toLowerCase().startsWith(f)) - Number(!y.name.toLowerCase().startsWith(f)) : 0));
      options.replaceChildren(...(f ? [] : [all]), ...found.map((c) => {
        const o = el("button", { className: "chip focusable" + (c.id === b.category ? " active" : ""), title: c.name }, el("span", { className: "name" }, c.name), el("small", {}, c.titles));
        o.onclick = () => { close(); set({ category: c.id, categoryName: c.name }); };
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
    const [films, series] = await Promise.all([
      api("GET", "/api/v1/catalog/movie?limit=40&q=" + q), api("GET", "/api/v1/catalog/series?limit=40&q=" + q)]);
    const sections = [[t("Film"), films.items], [t("Serie"), series.items]];
    if (state.view === "series") sections.reverse(); // the current tab's kind first
    const parts = [];
    for (const [label, items] of sections) {
      if (items.length) parts.push(el("h2", { className: "section" }, label), el("div", { className: "grid" }, ...items.map(itemCard)));
    }
    if (!parts.length) parts.push(el("p", { className: "empty" }, t("Nessun risultato per “{query}”.", { query: state.query })));
    main.replaceChildren(...parts);
  }

  const recState = { scheduled: t("In programma"), recording: t("● In registrazione"), completed: t("Registrata"), failed: t("Non riuscita") };
  async function renderRecordings(main) {
    const [list, space, who] = await Promise.all([api("GET", "/api/v1/recordings"), api("GET", "/api/v1/recordings/space"),
      window.VodMax.me().catch(() => null)]);
    const kids = who && who.profile && who.profile.kind === "kids";
    const gb = (b) => (b / 1e9).toLocaleString(locale, { maximumFractionDigits: 1 }) + " GB";
    const day = (ts) => new Date(ts * 1000).toLocaleDateString(locale, { weekday: "short", day: "numeric", month: "short" });
    const usage = [t("Usati {size}", { size: gb(space.used_bytes) }), space.free_bytes >= 0 ? t("liberi {size}", { size: gb(space.free_bytes) }) : "",
      space.max_gb ? t("limite {n} GB", { n: space.max_gb }) : "", space.keep_days ? t("si tengono {n} giorni", { n: space.keep_days }) : ""].filter(Boolean).join(" · ");
    const parts = [el("h2", { className: "page-title" }, t("Registrazioni")), el("p", { className: "empty" }, usage)];
    if (!list.length) parts.push(el("p", { className: "empty" }, t("Nessuna registrazione. In TV premi R (tasto blu) sul programma in onda, " +
      "oppure registra dalla Guida, oppure crea una regola qui sotto.")));
    else {
      parts.push(el("p", { className: "empty" }, t("OK guarda · Giallo (P) protegge dalla cancellazione automatica · Rosso (Canc) elimina")));
      parts.push(el("ol", { className: "recs" }, ...list.map((r) => {
        const b = el("button", { className: "focusable" },
          el("span", {}, day(r.start) + " " + hhmm(r.start)),
          el("span", {}, el("strong", {}, r.title), " · " + r.channel, r.rule_id ? el("small", { className: "tag" }, " " + t("regola")) : null),
          el("span", { className: "state " + r.status }, (r.protected ? "🔒 " : "") + (recState[r.status] || r.status)),
          el("span", { className: "state" }, r.bytes ? gb(r.bytes) : ""));
        b.onclick = () => r.status === "scheduled" ? flash(t("Non ancora iniziata")) : playRecording(r);
        b.dataset.recording = r.id;
        b.dataset.protected = r.protected ? "1" : "";
        b.title = r.error || "";
        return el("li", {}, b);
      })));
    }
    if (!kids) parts.push(...await rulesSection());
    main.replaceChildren(...parts);
  }

  // ---- The downloads page; refreshed while one is running ----
  // ---- My list: films and series kept for later, per profile ----
  async function renderMyList(main) {
    const list = await api("GET", "/api/v1/mylist");
    main.replaceChildren(el("h2", { className: "page-title" }, t("La mia lista")),
      list.length ? el("div", { className: "grid" }, ...list.map(itemCard))
        : el("p", { className: "empty" }, t("La lista è vuota. Nella scheda di un film o di una serie premi “La mia lista” per ritrovarlo qui.")));
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
    const trailer = actions.querySelector(".trailer");
    actions.insertBefore(b, trailer || null);
    try { on = (await api("GET", "/api/v1/mylist/" + item.id)).on; draw(); } catch { /* the button still works */ }
  }

  async function renderDownloads(main) {
    const dl = await downloadList(true);
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
      box.append(el("p", { className: "empty" }, t("Nessun download. Nella scheda di un film o di una serie premi “Scarica”: il server lo copia sul suo disco, " +
        "senza togliere la connessione a chi guarda, e lo trovi nelle raccolte «Scaricati».")));
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
      const text = el("input", { className: "focusable", type: "text", maxLength: 500, placeholder: t("es. registra le gare di Formula 1 in diretta, non le qualifiche") });
      const go = el("button", { className: "small focusable", type: "button" }, t("Compila con l'assistente"));
      const said = el("p", { className: "empty" });
      go.onclick = async () => {
        if (!text.value.trim()) return;
        go.disabled = true;
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
        } catch (e) { said.textContent = e.message; }
        go.disabled = false;
      };
      text.setAttribute("enterkeyhint", "go");
      text.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); go.click(); } });
      form.prepend(el("div", { className: "rule-ask" }, f(t("Descrivi la regola"), text), go, said));
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

  async function deleteRecording(id) {
    if (!confirm(t("Eliminare la registrazione?"))) return;
    try { await api("DELETE", "/api/v1/recordings/" + id); flash(t("Registrazione eliminata")); render(); }
    catch (e) { flash(e.message); }
  }

  // ---- Detail ----
  // Plot, credits and backdrop: from the provider (and TMDB when
  // configured), loaded after the page opens so it is never slower.
  async function showDetails(item) {
    const panel = $("detail");
    $("detail-plot").textContent = "";
    $("detail-credits").replaceChildren();
    panel.classList.remove("with-backdrop");
    panel.style.backgroundImage = "";
    let d;
    try { d = await api("GET", "/api/v1/items/" + item.id + "/details"); } catch { return; }
    if (state.detail !== item) return;
    if (d.title && d.title.toLowerCase() !== item.display_title.toLowerCase()) $("detail-title").textContent = d.title;
    const meta = $("detail-meta").textContent;
    const extra = [d.duration ? Math.round(d.duration / 60) + " min" : "", d.genre].filter(Boolean).join(" · ");
    if (extra) $("detail-meta").textContent = [meta, extra].filter(Boolean).join(" · ");
    $("detail-plot").textContent = d.plot || "";
    const rows = [[t("Regia"), d.director], [t("Cast"), d.cast], [t("Uscita"), d.released], [t("Paese"), d.country],
      [t("Titolo originale"), d.original_title && d.original_title !== item.display_title ? d.original_title : ""]];
    for (const [k, v] of rows) if (v) $("detail-credits").append(el("dt", {}, k), el("dd", {}, v));
    if (!d.local && item.kind === "movie") addDownloadButtons(item);
    if (d.trailer) {
      const a = el("a", { className: "button focusable trailer", href: "https://www.youtube.com/watch?v=" + encodeURIComponent(d.trailer),
        target: "_blank", rel: "noopener noreferrer" });
      window.VodMax.label(a, "circle-play", "Trailer");
      $("detail-actions").append(a);
    }
    if (d.backdrop) {
      const img = new Image();
      img.onload = () => {
        if (state.detail !== item) return;
        panel.style.backgroundImage = "url(" + JSON.stringify(d.backdrop) + ")";
        panel.classList.add("with-backdrop");
      };
      img.src = d.backdrop;
    }
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
    state.detail = item;
    $("detail-title").textContent = item.display_title;
    $("detail-meta").textContent = [item.year, { 4: "4K", 3: "Full HD", 2: "HD", 1: "SD" }[item.quality],
      item.variant_count > 1 ? t("{n} versioni", { n: item.variant_count }) : "",
      item.rating ? "★ " + Number(item.rating).toLocaleString(locale, { maximumFractionDigits: 1 }) : ""].filter(Boolean).join(" · ");
    $("detail-image").hidden = !item.logo;
    if (item.logo) $("detail-image").src = item.logo;
    showDetails(item);
    $("seasons").replaceChildren();
    $("episodes").replaceChildren();
    const actions = $("detail-actions");
    actions.replaceChildren();
    $("detail").hidden = false;
    if (item.kind === "series") {
      actions.append(el("p", { className: "empty" }, t("Caricamento episodi…")));
      let eps;
      try { eps = await api("GET", "/api/v1/series/" + item.id + "/episodes"); }
      catch (e) { actions.replaceChildren(el("p", { className: "empty" }, e.message)); return; }
      actions.replaceChildren();
      renderSeasons(item, eps);
      addListButton(item);
      return;
    }
    // Only the saved position: a playback session is created on "play",
    // because preparing it may already use the provider connection.
    const saved = await api("GET", "/api/v1/progress/movie/" + item.id).catch(() => null);
    const resume = saved && !saved.completed && saved.position > 30 ? saved : null;
    if (resume) {
      const r = el("button", { className: "button primary focusable" });
      window.VodMax.label(r, "play", t("Riprendi da {time}", { time: clock(resume.position) }));
      r.onclick = () => play("movie", item.id, item.display_title, resume.position);
      actions.append(r);
    }
    const start = el("button", { className: "button focusable" + (resume ? "" : " primary") });
    window.VodMax.label(start, resume ? "undo-2" : "play", resume ? t("Dall'inizio") : t("Guarda"));
    start.onclick = () => play("movie", item.id, item.display_title, 0);
    actions.append(start);
    if (item.variant_count > 1) {
      const v = el("button", { className: "button focusable" });
      v.dataset.variants = "1";
      window.VodMax.label(v, "layers", t("Altre versioni"));
      v.onclick = () => showVariants(item);
      actions.append(v);
    }
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
        if (e.thumb) {
          const img = el("img", { src: e.thumb, alt: "", loading: "lazy" });
          img.onerror = () => img.remove();
          thumb.append(img);
        }
        if (e.duration) thumb.append(el("span", { className: "ep-dur" }, Math.round(e.duration / 60) + " min"));
        if (pct) thumb.append(el("span", { className: "bar" }, el("div", { style: "width:" + pct + "%" })));
        const b = el("button", { className: "focusable ep-card" + (e === next ? " next" : "") }, thumb,
          el("span", { className: "ep-text" }, el("strong", {}, e.number + ". " + (e.title || t("Episodio {n}", { n: e.number }))),
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
      const b = el("button", { className: "tab focusable" }, t("Stagione {n}", { n: s }));
      b.dataset.season = s;
      b.onclick = () => showSeason(s);
      return b;
    }));
    if (next) {
      const code = { episode: "S" + next.season + "E" + next.number };
      const go = el("button", { className: "button focusable" },
        next.progress && !next.progress.completed ? t("Riprendi {episode}", code) : t("Guarda {episode}", code));
      go.onclick = () => play("episode", next.id, item.display_title + " · S" + next.season + "E" + next.number + " " + next.title,
        next.progress && !next.progress.completed ? next.progress.position : 0);
      $("detail-actions").append(go);
      showSeason(next.season);
    }
    addDownloadButtons(item, next ? next.season : 0);
    focusFirst($("detail-actions"));
  }

  $("detail-back").onclick = () => closeDetail();
  function closeDetail() {
    $("detail").hidden = true;
    state.detail = null;
    focusFirst($("content"));
  }

  // ---- Player ----
  const video = $("video");
  let saveTimer, idleTimer;

  async function play(media, id, title, startAt, mode, keep) {
    diagCount = 0;
    let sess;
    flash(mode === "remux" ? t("Preparazione della conversione…") : t("Avvio…"));
    const native = window.VodMax.native;
    try {
      sess = media === "catchup"
        ? await api("POST", "/api/v1/catchup/" + id + "/session", { viewer, ...state.catchup })
        // The Android player reads MKV, AC-3, HEVC: the file as it is.
        : await api("POST", "/api/v1/vod/" + media + "/" + id + "/session", { viewer, mode: native ? "direct" : mode || "" });
    } catch (e) { flash(e.message); return; }
    if (native) { playNative({ media, id, title: title || sess.title, sess, startAt: startAt || 0 }); return; }
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
    load(startAt || 0);
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
    window.VodMax.native.play(JSON.stringify({ url, title: ctx.title, start, live: Boolean(ctx.live),
      episode: ctx.media === "episode", next: ctx.sess.next ? ctx.sess.next.title : "", markers: ctx.sess.markers || null }));
  }
  window.VodMaxNativeDone = (r) => {
    const c = nativeCtx;
    nativeCtx = null;
    if (!c) return;
    if (r.error) flash(t("Riproduzione non riuscita: {error}", { error: r.error }));
    const position = (c.offset || 0) + r.position;
    const total = (c.sess && c.sess.duration) || r.duration;
    if ((c.media === "movie" || c.media === "episode") && total > 0 && position > 5) {
      api("PUT", "/api/v1/progress/" + c.media + "/" + c.id, { position: Math.floor(position), duration: Math.floor(total) })
        .then(() => { if (state.view === "home" && $("detail").hidden) render(); }).catch(() => {});
    }
    if (c.media === "catchup" && state.catchupBack) location.href = state.catchupBack;
    if (c.media === "episode" && c.sess.series_id) {
      if (r.intro_end) saveMarkers(c.sess, { intro_start: r.intro_start || 0, intro_end: r.intro_end });
      if (r.credits) saveMarkers(c.sess, { credits: r.credits });
      if (r.next && c.sess.next) play("episode", c.sess.next.id, c.sess.next.title, 0);
    }
  };
  window.VodMaxNativeError = (m) => flash(m);
  // Android "Back": close what is open, then the menu's home; false lets
  // the app go to the background.
  window.VodMaxNativeBack = () => {
    if (window.VodMax.sheetOpen()) { window.VodMax.closeSheet(); return true; }
    if ($("picker") || !$("player").hidden || !$("detail").hidden || state.query) {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
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
      b.className = "focusable" + (it.on ? " on" : "");
      b.textContent = (it.on ? "✓ " : "") + it.label;
      b.disabled = !it.ok;
      b.onclick = () => { closeMenu(); it.pick(); };
      return b;
    });
    const title = Object.assign(document.createElement("div"), { className: "menu-title", textContent: kind === "audio" ? t("Lingua dell'audio") : t("Sottotitoli") });
    menu.replaceChildren(title, ...buttons);
    menu.hidden = false;
    showBar();
    (buttons.find((b) => b.classList.contains("on")) || buttons[0]).focus();
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
    const extra = force ? "force=1" : "";
    if (p.sess.mode === "remux") {
      p.offset = position;
      video.src = p.sess.url + "?t=" + Math.floor(position) + "&audio=" + p.audio + "&sub=" + p.sub + (extra ? "&" + extra : "");
      startSubtitles();
    } else {
      p.offset = 0;
      video.src = p.sess.url + (extra ? "?" + extra : "");
      if (position > 0) video.addEventListener("loadedmetadata", () => { video.currentTime = position; }, { once: true });
    }
    video.play().catch(() => flash(t("Premi OK per avviare")));
    showBar();
  }

  function position() { return state.play ? state.play.offset + (video.currentTime || 0) : 0; }
  function duration() {
    const p = state.play;
    if (!p) return 0;
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
    if (p.ts) { p.ts.seek(delta); showBar(); return; }
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
    target = Math.max(0, Math.min(target, (d || Infinity) - 3));
    learnIntro(position(), target);
    if (p.ts) p.ts.seek(target - position());
    else if (p.sess.mode === "direct") video.currentTime = target;
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

  video.addEventListener("timeupdate", () => {
    const d = duration();
    $("player-pos").textContent = clock(position());
    $("player-dur").textContent = d ? clock(d) : "";
    if (!(state.seekDragging && state.seekDragging())) $("player-fill").style.width = d ? Math.min(100, (position() / d) * 100) + "%" : "0";
  });

  video.addEventListener("error", async () => {
    const p = state.play;
    if (!p || p.recording) { if (p && p.recording) flash(t("Registrazione non riproducibile in questo browser")); return; }
    // Find out why: a busy connection answers 409.
    const status = await fetch(p.sess.url + (p.sess.mode === "remux" ? "?t=0" : ""), { headers: { Range: "bytes=0-0" } })
      .then((r) => { r.body && r.body.cancel(); return r.status; }).catch(() => 0);
    if (status === 409) {
      if (confirm(t("La connessione dell'abbonamento è in uso su un altro dispositivo. Prendere il controllo?"))) load(position(), true);
      else stopPlayback(true);
      return;
    }
    if (p.sess.mode === "direct" && !p.fellBack) {
      flash(t("Formato non leggibile dal browser: conversione sul server…"));
      play(p.media, p.id, p.title, position(), "remux");
      return;
    }
    flash(t("Riproduzione non riuscita"));
  });

  video.addEventListener("ended", () => {
    const p = state.play;
    if (p && p.sess.next && !p.nextCancelled) { playNext(false); return; }
    saveProgress();
    stopPlayback(true);
  });

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

  // ---- Diagnostics: what the player does, sent to the server log ----
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
  for (const ev of ["loadstart", "loadedmetadata", "canplay", "playing", "waiting", "stalled", "pause", "play", "seeking", "seeked", "error", "emptied", "abort"]) {
    video.addEventListener(ev, () => diag(ev));
  }
  setInterval(() => { if (state.play && !video.paused) diag("tick"); }, 10000);

  function saveProgress() {
    const p = state.play;
    if (!p || p.recording || p.media === "catchup" || !duration()) return;
    api("PUT", "/api/v1/progress/" + p.media + "/" + p.id, { position: Math.floor(position()), duration: Math.floor(duration()) })
      .catch(() => {});
  }

  function stopPlayback(save) {
    if (save) saveProgress();
    // An archive opened from the TV or the guide: closing goes back there.
    if (save && state.play && state.play.media === "catchup" && state.catchupBack) {
      location.href = state.catchupBack;
      return;
    }
    clearInterval(saveTimer);
    clearInterval(subTimer);
    if (subTrack) subTrack.mode = "disabled";
    closeMenu();
    if (state.play && state.play.ts) state.play.ts.destroy();
    video.pause();
    video.removeAttribute("src");
    video.load();
    state.play = null;
    $("player").hidden = true;
  }

  function showBar() {
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

  async function playRecording(r) {
    let url, breaks;
    try { ({ url, commercials: breaks } = await api("POST", "/api/v1/recordings/" + r.id + "/ticket")); } catch (e) { flash(e.message); return; }
    if (window.VodMax.native) {
      playNative({ media: "recording", id: r.id, title: r.title + " · " + r.channel, sess: { mode: "direct", url }, startAt: 0, live: r.status === "recording" });
      return;
    }
    stopPlayback(false);
    state.play = { recording: r, title: r.title, sess: { mode: "direct", url }, offset: 0, duration: r.stop - r.start, breaks: breaks || [] };
    $("player-audio").hidden = $("player-subs").hidden = true;
    $("player-title").textContent = r.title + " · " + r.channel;
    $("player").hidden = false;
    // Finished recordings are MP4 (seekable); recordings in progress, or
    // left in MPEG-TS, go through mpegts.js.
    const isTS = r.status === "recording" || !(await fetch(url, { headers: { Range: "bytes=0-0" } })
      .then((res) => { res.body && res.body.cancel(); return (res.headers.get("Content-Type") || "").includes("mp4"); }).catch(() => false));
    if (isTS && window.mpegts && mpegts.isSupported()) {
      state.play.ts = window.VodMax.tsPlayer(video, url, {
        live: r.status === "recording",
        onError: (type, detail) => flash(t("Registrazione non riproducibile ({detail})", { detail })),
      });
      $("player-live").hidden = r.status !== "recording";
    } else {
      video.src = url;
      video.play().catch(() => flash(t("Premi OK per avviare")));
    }
    showBar();
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
    let best = null, bestScore = Infinity;
    for (const e of all) {
      if (e === current) continue;
      const b = e.getBoundingClientRect();
      const bx = b.left + b.width / 2, by = b.top + b.height / 2;
      const dx = bx - ax, dy = by - ay;
      const ok = { ArrowLeft: dx < -4, ArrowRight: dx > 4, ArrowUp: dy < -4, ArrowDown: dy > 4 }[dir];
      if (!ok) continue;
      const along = dir === "ArrowLeft" || dir === "ArrowRight" ? Math.abs(dx) : Math.abs(dy);
      const across = dir === "ArrowLeft" || dir === "ArrowRight" ? Math.abs(dy) : Math.abs(dx);
      const score = along + across * 3;
      if (score < bestScore) { bestScore = score; best = e; }
    }
    if (best) best.focus();
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
          if (!$("player-menu").hidden) { closeMenu(); $("player-toggle").focus(); } else document.activeElement.blur();
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
    if (active && active.tagName === "INPUT" && active !== $("search")) return; // e.g. the token form
    const typing = active === $("search");
    if (typing && !["ArrowDown", "Back", "OK"].includes(key)) return;
    if (key.startsWith("Arrow")) { moveFocus(key); e.preventDefault(); return; }
    if (key === "OK" && typing) { $("search").blur(); focusFirst($("content")); e.preventDefault(); return; }
    if (key === "Back" || (key === "Backspace" && !typing)) {
      if (!$("detail").hidden) closeDetail();
      else if (state.query) { state.query = ""; $("search").value = ""; render(); }
      e.preventDefault();
      return;
    }
    if (key === "Green" || key === "g") { location.href = "/tv.html"; return; }
    if ((key === "Yellow" || key === "p") && document.activeElement && document.activeElement.dataset.recording) {
      toggleProtected(document.activeElement.dataset.recording, document.activeElement.dataset.protected === "1");
      e.preventDefault();
      return;
    }
    if ((key === "Red" || key === "Delete") && document.activeElement && document.activeElement.dataset.recording) {
      deleteRecording(document.activeElement.dataset.recording);
      e.preventDefault();
    }
  });

  for (const tab of document.querySelectorAll(".tab[data-view]")) {
    tab.onclick = () => { state.view = tab.dataset.view; state.query = ""; $("search").value = ""; closeDetailSilently(); render(); };
  }
  function closeDetailSilently() { $("detail").hidden = true; state.detail = null; }

  let searchTimer;
  $("search").addEventListener("input", () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => { state.query = $("search").value.trim(); render().then(() => $("search").focus()); }, 350);
  });

  // Mouse and touch: buttons in the bar, click on the picture to pause.
  $("player-bar").addEventListener("click", (e) => {
    const b = e.target.closest("button[data-action]");
    if (!b) return;
    ({
      close: () => stopPlayback(true), back10: () => seek(-10), fwd30: () => seek(30), toggle,
      audio: () => openMenu("audio"), subs: () => openMenu("subs"),
      live: () => { if (state.play && state.play.ts) { state.play.ts.goLive(); showBar(); } },
      next: () => playNext(true),
    })[b.dataset.action]();
  });
  video.addEventListener("click", toggle);
  $("player").addEventListener("mousemove", showBar);

  addEventListener("pagehide", () => saveProgress());
  // From the TV ("from the start") or the guide ("watch again"):
  // /?catchup=CHANNEL&start=…&stop=…&title=…&back=…
  const asked = new URLSearchParams(location.search);
  window.VodMax.withToken(() => {
    render();
    showDownloadsTab();
    if (asked.get("catchup")) {
      state.catchup = { start: Number(asked.get("start")), stop: Number(asked.get("stop")), title: asked.get("title") || "" };
      const back = asked.get("back") || "";
      state.catchupBack = back.startsWith("/") && !back.startsWith("//") ? back : "";
      history.replaceState(null, "", "/");
      play("catchup", Number(asked.get("catchup")), state.catchup.title, 0, "remux");
    }
  });
})();
