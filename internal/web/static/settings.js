// VodMax settings: subscriptions, VPN connections, catalog selection and
// categories. Secrets are written, never read back.
"use strict";

const fromEntries = (pairs) => pairs.reduce((o, [k, v]) => { o[k] = v; return o; }, {});

(() => {
  const { store, api, hhmm } = window.VodMax;
  const t = window.VodMaxI18n.t;
  const locale = window.VodMaxI18n.locale;
  const $ = (id) => document.getElementById(id);
  const el = (tag, props = {}, ...children) => {
    const e = Object.assign(document.createElement(tag), props);
    e.append(...children.filter((c) => c !== null && c !== undefined && c !== false));
    return e;
  };
  let statusTimer;
  function flash(text) {
    $("status").textContent = text;
    $("status").hidden = false;
    clearTimeout(statusTimer);
    statusTimer = setTimeout(() => { $("status").hidden = true; }, 5000);
  }
  const button = (label, onclick, cls = "") => el("button", { className: "small focusable " + cls, type: "button", onclick }, label);
  const field = (label, input) => [el("label", {}, label), input];
  // Copies text: the clipboard API exists only over HTTPS, and VodMax at
  // home is usually plain HTTP, so the old way (a selected text area) too.
  function copyText(text) {
    const legacy = () => {
      const area = el("textarea", { value: text, readOnly: true });
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.append(area);
      area.select();
      let ok = false;
      try { ok = document.execCommand("copy"); } catch { /* not allowed */ }
      area.remove();
      flash(ok ? t("Copiato") : t("Copia non riuscita: seleziona il testo e copialo a mano"));
    };
    if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(text).then(() => flash(t("Copiato")), legacy);
    else legacy();
  }
  // An address or a command, wrapped on narrow screens, with its Copy button.
  const cmd = (text) => el("div", { className: "cmd-row" }, el("pre", { className: "cmd" }, text), button(t("Copia"), () => copyText(text)));
  const input = (props) => el("input", { className: "focusable", ...props });
  const when = (ts) => {
    if (!ts) return t("mai");
    const d = new Date(typeof ts === "number" ? ts * 1000 : ts);
    return d.toLocaleDateString(locale, { day: "numeric", month: "short" }) + " " + hhmm(d.getTime() / 1000);
  };

  // The sections, in groups like a system settings app.
  const SECTIONS = [
    [t("Contenuti"), [
      ["subscriptions", "layers", t("Abbonamenti"), t("Playlist Xtream, M3U e Stalker")],
      ["libraries", "folder", t("Raccolte"), t("Cartelle di film e serie")],
      ["vpn", "lock", "VPN", t("Come si raggiunge il provider")],
      ["rules", "check", t("Selezione"), t("Paesi, anni, cosa entra nel catalogo")],
      ["categories", "list", t("Categorie"), t("Includi o escludi una per una")],
      ["guide", "calendar-days", t("Guida TV"), t("Fonti XMLTV")],
    ]],
    [t("Casa"), [
      ["profiles", "user", t("Profili"), t("Chi guarda, profili bambini")],
      ["devices", "tv", t("Dispositivi"), t("TV, telefoni e PC abbinati")],
      ["language", "info", t("Lingua"), t("Lingua dell'interfaccia su questo dispositivo")],
    ]],
    ["Server", [
      ["assistant", "sparkles", t("Assistente"), t("Regole a parole, consigli per la sera")],
      ["system", "settings", t("Sistema"), t("Stato, aggiornamenti, diretta, backup")],
    ]],
  ];
  const TITLES = {};
  for (const [, items] of SECTIONS) for (const [v, , title] of items) TITLES[v] = title;
  const phone = window.matchMedia("(max-width: 800px)");

  const asked = new URLSearchParams(location.search).get("view");
  // On a phone the page opens on the list of sections.
  const state = { view: asked || (phone.matches ? null : store.get("settingsView", "subscriptions")), sub: store.get("settingsSub", 0), kind: "live" };

  function buildSections() {
    const icon = window.VodMaxIcons.icon;
    const groups = SECTIONS.map(([name, items]) => el("div", { className: "group" },
      el("div", { className: "group-title" }, name),
      el("div", { className: "group-list" }, ...items.map(([view, ic, title, sub]) => {
        const b = el("button", { className: "item focusable", type: "button" },
          el("span", { className: "item-icon" }, icon(ic)),
          el("span", { className: "item-text" }, el("span", { className: "item-title" }, title), el("span", { className: "item-sub" }, sub)),
          el("span", { className: "item-go" }, icon("chevron-right")));
        b.dataset.view = view;
        b.onclick = () => open(view);
        return b;
      }))));
    $("sections").replaceChildren(el("div", { id: "promo" }), ...groups);
    assistantPromo();
    $("pane-back").replaceChildren(icon("arrow-left"));
    $("pane-back").onclick = back;
  }

  // The assistant is optional but worth knowing: until it is on, a card at
  // the top of the list says what it does ("Non ora" makes it a plain item).
  async function assistantPromo() {
    const box = $("promo");
    const on = await api("GET", "/api/v1/assistant").then((r) => r.enabled, () => null);
    const item = document.querySelector('.item[data-view="assistant"] .item-sub');
    if (item) item.textContent = on ? t("Acceso") : t("Regole a parole, consigli per la sera");
    if (on !== false || store.get("assistantPromo", "") === "later") { box.replaceChildren(); return; }
    const icon = window.VodMaxIcons.icon;
    const go = el("button", { className: "small primary focusable", type: "button", onclick: () => open("assistant") }, t("Attiva l'assistente"));
    const later = el("button", { className: "small focusable", type: "button", onclick: () => { store.set("assistantPromo", "later"); box.replaceChildren(); } }, t("Non ora"));
    box.replaceChildren(el("section", { className: "promo" },
      el("span", { className: "promo-icon" }, icon("sparkles")),
      el("div", { className: "promo-text" },
        el("b", {}, t("Un assistente per la tua TV")),
        el("span", {}, t("Crea le regole di registrazione da una frase e ti consiglia cosa guardare stasera. Facoltativo: può funzionare tutto in casa, senza internet."))),
      el("div", { className: "buttons" }, go, later)));
  }

  function open(view) {
    if (!leaveAsked()) return;
    state.view = view;
    history.replaceState(null, "", "?view=" + view);
    render().then(() => { if (phone.matches) window.scrollTo(0, 0); });
  }

  // Phone: from a section back to the list; otherwise back to the menu.
  // Fields changed and not saved yet, in the page: a field the person
  // changed is marked; saving redraws the page with new fields.
  document.addEventListener("input", (e) => { if (e.target.closest("main")) e.target.dataset.changed = "1"; }, true);
  document.addEventListener("submit", (e) => {
    // A second press while the first is being saved is ignored (it
    // created the profile twice, then said the name was taken).
    const f = e.target;
    if (f.dataset.busy) { e.preventDefault(); e.stopImmediatePropagation(); return; }
    f.dataset.busy = "1";
    setTimeout(() => { delete f.dataset.busy; }, 1500);
    for (const i of f.querySelectorAll("[data-changed]")) delete i.dataset.changed;
  }, true);
  const unsaved = () => document.querySelector("main [data-changed]") !== null;
  const leaveAsked = () => {
    if (!unsaved() || !confirm(t("Ci sono modifiche non salvate. Uscire lo stesso?"))) return !unsaved();
    for (const i of document.querySelectorAll("main [data-changed]")) delete i.dataset.changed;
    return true;
  };
  document.addEventListener("click", (e) => { // the "Menu" link
    if (e.target.closest(".side-back") && !leaveAsked()) e.preventDefault();
  }, true);

  function back() {
    // An open folder picker closes first; changes not saved are asked about.
    const picker = document.querySelector(".picker-panel");
    if (picker && picker.close) { picker.close(); return; }
    if (!leaveAsked()) return;
    if (phone.matches && state.view) {
      const was = state.view;
      state.view = null;
      history.replaceState(null, "", location.pathname);
      render();
      const item = document.querySelector('.item[data-view="' + was + '"]');
      if (item) item.focus();
      return;
    }
    location.href = "/";
  }

  // Each h2 with what follows it becomes a card, so sections stand apart.
  // A page that opens with a list of boxes (subscriptions, profiles...)
  // keeps its heading as the page title above them.
  function cards(main) {
    const groups = [];
    for (const node of [...main.children]) {
      if (node.tagName === "H2" || !groups.length) groups.push([]);
      groups[groups.length - 1].push(node);
    }
    groups.forEach((g, i) => {
      if (g[0].tagName !== "H2") return;
      const rest = g.slice(1);
      if (i === 0 && rest.some((n) => n.classList.contains("box")) && rest.every((n) => n.classList.contains("box") || n.classList.contains("note"))) return;
      const card = el("section", { className: "card" });
      g[0].before(card);
      card.append(...g);
    });
  }

  async function render() {
    document.body.classList.toggle("at-list", !state.view);
    document.body.classList.toggle("view-system", state.view === "system");
    for (const b of document.querySelectorAll(".item[data-view]")) b.classList.toggle("active", b.dataset.view === state.view);
    if (!state.view) return;
    $("pane-title").textContent = TITLES[state.view] || "";
    store.set("settingsView", state.view);
    const main = $("content");
    main.replaceChildren(el("p", { className: "note" }, t("Caricamento…")));
    try {
      await ({ subscriptions: renderSubscriptions, libraries: renderLibraries, vpn: renderVPN, rules: renderRules, categories: renderCategories, system: renderSystem, assistant: renderAssistant, profiles: renderProfiles, devices: renderDevices, guide: renderGuideSources, language: renderLanguage })[state.view](main);
      cards(main);
    } catch (e) {
      main.replaceChildren(el("p", { className: "note bad" }, e.message));
    }
    if (!document.activeElement || document.activeElement === document.body) {
      const item = document.querySelector(".item.active");
      if (item && !phone.matches) item.focus({ preventScroll: true });
    }
  }

  // ---- Subscriptions ----
  async function renderSubscriptions(main) {
    const [all, vpns] = await Promise.all([api("GET", "/api/v1/subscriptions"), api("GET", "/api/v1/vpn")]);
    const subs = all.filter((s) => s.kind !== "local");
    const vpnName = fromEntries(vpns.map((v) => [v.id, v.name]));
    const parts = [el("h2", {}, t("Abbonamenti")),
      el("p", { className: "note" }, t("Playlist dei provider (Xtream Codes, M3U o portali Stalker). Ogni abbonamento passa dalla sua VPN: senza VPN il provider non viene mai contattato."))];
    for (const s of subs) parts.push(subscriptionBox(s, vpns, vpnName));
    parts.push(el("h2", {}, t("Aggiungi un abbonamento")));
    parts.push(vpns.length ? subscriptionForm(vpns) : el("p", { className: "note" }, t("Prima aggiungi una connessione VPN (scheda VPN).")));
    main.replaceChildren(...parts);
  }

  function subscriptionBox(s, vpns, vpnName) {
    const settings = s.settings || {};
    const a = s.account || {};
    const facts = el("div", { className: "facts" },
      el("span", {}, t("Tipo") + " ", el("b", {}, { xtream: "Xtream Codes", stalker: t("Portale Stalker") }[s.kind] || t("Playlist M3U"))),
      settings.server ? el("span", {}, "Server ", el("b", {}, settings.server)) : null,
      settings.username ? el("span", {}, t("Utente") + " ", el("b", {}, settings.username)) : null,
      settings.host ? el("span", {}, "Host ", el("b", {}, settings.host)) : null,
      el("span", {}, "VPN ", el("b", {}, vpnName[s.vpn_id] || "?")),
      a.status ? el("span", {}, "Account ", el("b", { className: a.active ? "ok" : "bad" }, a.active ? t("attivo") : a.status)) : null,
      a.expires > 0 ? el("span", {}, t("Scadenza") + " ", el("b", {}, new Date(a.expires * 1000).toLocaleDateString(locale))) : null,
      a.max_connections ? el("span", {}, t("Connessioni") + " ", el("b", {}, (a.active_cons || 0) + " / " + a.max_connections)) : null);
    const counts = s.counts || {};
    const sync = el("div", { className: "facts" },
      el("span", {}, t("Ultima sincronizzazione") + " ", el("b", { className: s.sync_status === "error" ? "bad" : "" }, when(s.synced_at) +
        (s.sync_status === "error" ? " " + t("(errore)") : ""))),
      el("span", {}, t("Canali") + " ", el("b", {}, counts.live || 0)), el("span", {}, t("Film") + " ", el("b", {}, counts.movie || 0)),
      el("span", {}, t("Serie") + " ", el("b", {}, counts.series || 0)));
    const box = el("section", { className: "box" }, el("h3", {}, s.name), facts, sync,
      s.sync_error ? el("p", { className: "note bad" }, s.sync_error) : null);
    const syncButton = button(s.syncing ? t("Sincronizzazione in corso…") : t("Sincronizza ora"), async () => {
      syncButton.disabled = true;
      syncButton.textContent = t("Sincronizzazione in corso… (anche un minuto)");
      try {
        const r = await api("POST", "/api/v1/subscriptions/" + s.id + "/sync");
        const sel = r.selection || {};
        const n = fromEntries(["live", "movie", "series"].map((k) => [k, sel[k] ? sel[k].cards : 0]));
        flash(r.guide ? t("Sincronizzato: {live} canali, {movie} film, {series} serie, guida {programmes} programmi", { ...n, programmes: r.guide.programmes })
          : t("Sincronizzato: {live} canali, {movie} film, {series} serie", n));
      } catch (e) { flash(e.message); }
      render();
    }, "primary");
    syncButton.disabled = s.syncing;
    const edit = button(t("Modifica"), () => {
      edit.disabled = true;
      box.append(subscriptionForm(vpns, s));
    });
    const del = button(t("Elimina"), async () => {
      if (!confirm(t("Eliminare l'abbonamento “{name}” e il suo catalogo? Preferiti e avanzamenti dei suoi titoli andranno persi.", { name: s.name }))) return;
      try { await api("DELETE", "/api/v1/subscriptions/" + s.id); flash(t("Abbonamento eliminato")); } catch (e) { flash(e.message); }
      render();
    }, "danger");
    const select = button(t("Selezione del catalogo"), () => { state.sub = s.id; store.set("settingsSub", s.id); open("rules"); });
    box.append(el("div", { className: "buttons" }, syncButton, select, edit, del));
    return box;
  }

  // ---- Local collections: folders of films or series ----
  const CONTENT = { movie: t("Film"), series: t("Serie") };
  async function renderLibraries(main) {
    const libs = (await api("GET", "/api/v1/subscriptions")).filter((s) => s.kind === "local");
    const parts = [el("h2", {}, t("Raccolte")),
      el("p", { className: "note" }, t("Cartelle di film o di serie che il server legge così come sono: titolo e anno dai nomi (“Il Padrino (1972).mkv”, “Twin Peaks/Stagione 1/S01E03.mkv”), " +
        "trama e locandine dai file .nfo e dalle immagini accanto ai video, se ci sono. Non passano dalla VPN. Si aggiornano ogni notte e con “Aggiorna ora”."))];
    for (const l of libs) parts.push(libraryBox(l));
    parts.push(el("h2", {}, t("Aggiungi una raccolta")), libraryForm());
    main.replaceChildren(...parts);
  }

  function libraryBox(l) {
    const set = l.settings || {}, counts = l.counts || {};
    const box = el("section", { className: "box" }, el("h3", {}, l.name),
      el("div", { className: "facts" },
        el("span", {}, t("Contenuto") + " ", el("b", {}, CONTENT[set.content] || "?")),
        el("span", {}, t("Cartella") + " ", el("b", { className: "path" }, set.path || "?"))),
      el("div", { className: "facts" },
        el("span", {}, t("Ultimo aggiornamento") + " ", el("b", { className: l.sync_status === "error" ? "bad" : "" }, when(l.synced_at) + (l.sync_status === "error" ? " " + t("(errore)") : ""))),
        el("span", {}, CONTENT[set.content] || t("Titoli"), " ", el("b", {}, counts[set.content] || 0))),
      l.sync_error ? el("p", { className: "note bad" }, l.sync_error) : null);
    const scan = button(l.syncing ? t("Aggiornamento in corso…") : t("Aggiorna ora"), async () => {
      scan.disabled = true;
      scan.textContent = t("Lettura della cartella…");
      try {
        const r = await api("POST", "/api/v1/subscriptions/" + l.id + "/sync");
        const sel = (r.selection || {})[set.content];
        const n = sel ? sel.visible : 0;
        flash(set.content === "series" ? t("Raccolta aggiornata: {n} serie", { n }) : t("Raccolta aggiornata: {n} film", { n }));
      } catch (e) { flash(e.message); }
      render();
    }, "primary");
    scan.disabled = l.syncing;
    const edit = button(t("Modifica"), () => { edit.disabled = true; box.append(libraryForm(l)); });
    const del = button(t("Elimina"), async () => {
      if (!confirm(t("Togliere la raccolta “{name}” da VodMax? I file nella cartella non vengono toccati.", { name: l.name }))) return;
      try { await api("DELETE", "/api/v1/subscriptions/" + l.id); flash(t("Raccolta tolta")); } catch (e) { flash(e.message); }
      render();
    }, "danger");
    box.append(el("div", { className: "buttons" }, scan, edit, del));
    return box;
  }

  function libraryForm(existing) {
    const set = (existing && existing.settings) || {};
    const name = input({ value: existing ? existing.name : "", placeholder: t("es. Film di casa") });
    const content = el("select", { className: "focusable" }, el("option", { value: "movie" }, t("Film")), el("option", { value: "series" }, t("Serie (una cartella per serie)")));
    content.value = set.content || "movie";
    const path = input({ value: set.path || "", placeholder: t("/percorso/della/cartella"), spellcheck: false });
    const browse = button(t("Sfoglia…"), () => folderPicker(path.value, (chosen) => { path.value = chosen; path.focus(); }, browse));
    const form = el("form", { className: "form" }, ...field(t("Nome"), name), ...field(t("Contenuto"), content),
      el("label", {}, t("Cartella")), el("div", { className: "path-field" }, path, browse));
    const save = el("button", { className: "small primary focusable full", type: "submit" }, existing ? t("Salva le modifiche") : t("Aggiungi e leggi la cartella"));
    form.append(save);
    form.onsubmit = async (e) => {
      e.preventDefault();
      save.disabled = true;
      try {
        const body = { name: name.value, path: path.value, content: content.value };
        let id = existing && existing.id;
        if (existing) await api("PATCH", "/api/v1/subscriptions/" + id, body);
        else id = (await api("POST", "/api/v1/subscriptions", { ...body, kind: "local" })).id;
        save.textContent = t("Lettura della cartella…");
        const r = await api("POST", "/api/v1/subscriptions/" + id + "/sync");
        const sel = (r.selection || {})[content.value];
        const n = sel ? sel.visible : 0;
        flash(content.value === "series" ? t("Raccolta pronta: {n} serie", { n }) : t("Raccolta pronta: {n} film", { n }));
        render();
      } catch (err) { flash(err.message); save.disabled = false; render(); }
    };
    return form;
  }

  // A panel to walk the server's folders with arrows and OK.
  function folderPicker(start, done, opener) {
    const panel = el("section", { className: "box picker-panel" });
    (opener.closest(".path-field") || opener).after(panel);
    opener.disabled = true;
    const close = () => { panel.remove(); opener.disabled = false; opener.focus(); };
    panel.close = close; // Back closes it (see back())
    const show = async (dir) => {
      let list;
      try { list = await api("GET", "/api/v1/folders?path=" + encodeURIComponent(dir || "")); }
      catch (e) { if (dir) { show(""); return; } flash(e.message); close(); return; }
      const rows = list.folders.map((f) => button(f.name, () => show(f.path), "folder-row"));
      panel.replaceChildren(
        el("div", { className: "facts" }, el("span", {}, t("Cartella") + " ", el("b", { className: "path" }, list.path)),
          list.videos ? el("span", {}, el("b", {}, list.videos), " " + t("video qui dentro")) : null),
        el("div", { className: "buttons" },
          button(t("Usa questa cartella"), () => { done(list.path); close(); }, "primary"),
          list.parent ? button("↑ " + t("Cartella superiore"), () => show(list.parent)) : null,
          button(t("Annulla"), close)),
        rows.length ? el("div", { className: "folders" }, ...rows) : el("p", { className: "note" }, t("Nessuna sottocartella.")));
      panel.querySelector("button").focus();
    };
    show(start);
  }

  // Add (existing undefined) or edit a subscription.
  function subscriptionForm(vpns, existing) {
    const kind = el("select", { className: "focusable" }, el("option", { value: "xtream" }, "Xtream Codes"), el("option", { value: "m3u" }, t("Playlist M3U")),
      el("option", { value: "stalker" }, t("Portale Stalker (MAC)")));
    const name = input({ value: existing ? existing.name : "", placeholder: t("es. Provider casa") });
    const vpn = el("select", { className: "focusable" }, ...vpns.map((v) => el("option", { value: v.id }, v.name)));
    const set = (existing && existing.settings) || {};
    const server = input({ value: set.server || "", placeholder: t("http://server:porta") });
    const user = input({ value: set.username || "", autocomplete: "off" });
    const pass = input({ type: "password", autocomplete: "new-password", placeholder: existing ? t("lascia vuoto per non cambiarla") : "" });
    const url = input({ placeholder: existing ? t("lascia vuoto per non cambiarlo") : "http://…/get.php?…" });
    const portal = input({ value: set.server || "", placeholder: "http://portale:porta/c/" });
    const mac = input({ autocomplete: "off", spellcheck: false, placeholder: existing ? t("lascia vuoto per non cambiarlo") : "00:1A:79:XX:XX:XX" });
    if (existing) { kind.value = existing.kind; kind.disabled = true; vpn.value = existing.vpn_id; }
    const xtreamRows = [...field("Server", server), ...field(t("Utente"), user), ...field("Password", pass)];
    const m3uRows = [...field(t("Indirizzo della playlist"), url)];
    const stalkerRows = [...field(t("Indirizzo del portale"), portal), ...field(t("Indirizzo MAC"), mac),
      el("p", { className: "note full" }, t("Per ora si leggono i canali in diretta; la guida si aggiunge da Guida TV → Fonti XMLTV."))];
    const form = el("form", { className: "form" }, ...field(t("Tipo"), kind), ...field(t("Nome"), name), ...field("VPN", vpn), ...xtreamRows, ...m3uRows, ...stalkerRows);
    const toggle = () => {
      for (const e of xtreamRows) e.hidden = kind.value !== "xtream";
      for (const e of m3uRows) e.hidden = kind.value !== "m3u";
      for (const e of stalkerRows) e.hidden = kind.value !== "stalker";
    };
    kind.onchange = toggle;
    toggle();
    const save = el("button", { className: "small primary focusable full", type: "submit" }, existing ? t("Salva le modifiche") : t("Aggiungi"));
    form.append(save);
    form.onsubmit = async (e) => {
      e.preventDefault();
      save.disabled = true;
      try {
        if (existing) {
          const patch = { name: name.value, vpn_id: Number(vpn.value) };
          if (existing.kind === "xtream") Object.assign(patch, { server: server.value, username: user.value, password: pass.value });
          else if (existing.kind === "stalker") Object.assign(patch, { server: portal.value, mac: mac.value });
          else patch.url = url.value;
          await api("PATCH", "/api/v1/subscriptions/" + existing.id, patch);
          flash(t("Abbonamento aggiornato"));
        } else {
          const body = { name: name.value, kind: kind.value, vpn_id: Number(vpn.value) };
          if (kind.value === "xtream") Object.assign(body, { server: server.value, username: user.value, password: pass.value });
          else if (kind.value === "stalker") Object.assign(body, { server: portal.value, mac: mac.value });
          else body.url = url.value;
          await api("POST", "/api/v1/subscriptions", body);
          flash(t("Abbonamento aggiunto: premi “Sincronizza ora” per scaricare il catalogo"));
        }
        render();
      } catch (err) { flash(err.message); save.disabled = false; }
    };
    return form;
  }

  // ---- VPN ----
  async function renderVPN(main) {
    const vpns = await api("GET", "/api/v1/vpn");
    const parts = [el("h2", {}, t("Connessioni VPN")),
      el("p", { className: "note" }, t("Il provider si contatta solo attraverso una VPN. Due modi: una WireGuard dentro VodMax (si importa il file .conf " +
        "del proprio servizio: Proton, Mullvad, NordVPN…; la chiave privata resta cifrata) oppure una VPN che hai già fuori da VodMax, " +
        "sul dispositivo o sul router."))];
    for (const v of vpns) parts.push(vpnBox(v));
    parts.push(el("h2", {}, t("Aggiungi una VPN")), vpnForm());
    main.replaceChildren(...parts);
  }

  function vpnBox(v) {
    const st = v.status || {};
    const connected = v.running && st.connected;
    const box = el("section", { className: "box" }, el("h3", {}, v.name), el("div", { className: "facts" },
      el("span", {}, v.kind === "system" ? t("Tipo") + " " : "Server ", el("b", {}, v.kind === "system" ? t("VPN esterna, non controllata da VodMax") : v.endpoint || "?")),
      el("span", {}, t("Stato") + " ", el("b", { className: connected ? "ok" : "" }, v.kind === "system" ? (v.running ? t("in uso") : t("non in uso"))
        : connected ? t("collegata") : v.running ? t("in collegamento") : t("non collegata"))),
      st.last_handshake && st.last_handshake !== "0001-01-01T00:00:00Z" ? el("span", {}, t("Ultimo handshake") + " ", el("b", {}, when(st.last_handshake))) : null,
      v.running ? el("span", {}, t("Traffico") + " ", el("b", {}, t("{mb} MB ricevuti", { mb: ((st.rx_bytes || 0) / 1e6).toFixed(1) }))) : null,
      v.health && v.health.reconnects > 0 ? el("span", {}, t("Ricollegamenti automatici") + " ", el("b", {}, v.health.reconnects)) : null),
      v.health && !v.health.ok ? el("p", { className: "note bad" }, "⚠ " + t("{error} (da {since})", { error: v.health.error, since: when(v.health.since) })) : null);
    const connect = button(v.running ? t("Scollega") : t("Collega"), async () => {
      connect.disabled = true;
      try {
        if (v.running) { await api("POST", "/api/v1/vpn/" + v.id + "/disconnect"); flash(t("VPN scollegata")); }
        else { const s = await api("POST", "/api/v1/vpn/" + v.id + "/connect"); flash(s.connected ? t("VPN collegata") : t("Nessuna risposta dal server VPN")); }
      } catch (e) { flash(e.message); }
      render();
    }, v.running ? "" : "primary");
    const edit = button(t("Modifica"), () => { edit.disabled = true; box.append(vpnForm(v)); });
    const del = button(t("Elimina"), async () => {
      if (!confirm(t("Eliminare la VPN “{name}”?", { name: v.name }))) return;
      try { await api("DELETE", "/api/v1/vpn/" + v.id); flash(t("VPN eliminata")); } catch (e) { flash(e.message); }
      render();
    }, "danger");
    box.append(el("div", { className: "buttons" }, connect, edit, del));
    return box;
  }

  function vpnForm(existing) {
    const name = input({ value: existing ? existing.name : "", placeholder: t("es. Proton Italia") });
    const kind = el("select", { className: "focusable" },
      el("option", { value: "wireguard" }, t("WireGuard dentro VodMax (consigliato)")),
      el("option", { value: "system" }, t("VPN già attiva fuori da VodMax (sul dispositivo o sul router)")));
    kind.value = existing ? existing.kind || "wireguard" : "wireguard";
    kind.disabled = Boolean(existing);
    const conf = el("textarea", { className: "focusable", spellcheck: false,
      placeholder: existing ? t("Incolla un nuovo .conf per sostituirlo, oppure lascia vuoto") : "[Interface]\nPrivateKey = …\nAddress = …\nDNS = …\n\n[Peer]\nPublicKey = …\nEndpoint = …" });
    const file = input({ type: "file", accept: ".conf,text/plain" });
    file.onchange = async () => { if (file.files[0]) conf.value = await file.files[0].text(); };
    const warning = el("p", { className: "note bad full" }, t("Con una VPN esterna VodMax contatta il provider con la normale rete del dispositivo: " +
      "è compito tuo che quella rete passi davvero dalla VPN (per esempio una VPN sul router per tutta la rete, o l'app VPN attiva sul telefono). " +
      "Se la VPN cade, VodMax non se ne accorge."));
    const confRows = [...field(t("File .conf"), file), ...field(t("Configurazione"), conf)];
    const toggle = () => {
      const system = kind.value === "system";
      for (const e of confRows) e.hidden = system;
      warning.hidden = !system;
    };
    kind.onchange = toggle;
    const save = el("button", { className: "small primary focusable full", type: "submit" }, existing ? t("Salva le modifiche") : t("Aggiungi"));
    const form = el("form", { className: "form" }, ...field(t("Nome"), name), ...field(t("Tipo"), kind), ...confRows, warning, save);
    toggle();
    form.onsubmit = async (e) => {
      e.preventDefault();
      save.disabled = true;
      try {
        if (existing) await api("PATCH", "/api/v1/vpn/" + existing.id, { name: name.value, config: kind.value === "system" ? "" : conf.value });
        else await api("POST", "/api/v1/vpn", { name: name.value, kind: kind.value, config: kind.value === "system" ? "" : conf.value });
        flash(existing ? t("VPN aggiornata") : t("VPN aggiunta"));
        render();
      } catch (err) { flash(err.message); save.disabled = false; }
    };
    return form;
  }


  // ---- Subscription picker for selection pages ----
  async function pickSubscription(main, title, note) {
    const subs = (await api("GET", "/api/v1/subscriptions")).filter((s) => s.kind !== "local");
    if (!subs.length) { main.replaceChildren(el("p", { className: "note" }, t("Nessun abbonamento: aggiungine uno nella scheda Abbonamenti."))); return null; }
    if (!subs.some((s) => s.id === state.sub)) state.sub = subs[0].id;
    const pick = el("select", { className: "pick focusable" }, ...subs.map((s) => el("option", { value: s.id }, s.name)));
    pick.value = state.sub;
    pick.onchange = () => { state.sub = Number(pick.value); store.set("settingsSub", state.sub); render(); };
    main.replaceChildren(el("h2", {}, title), el("p", { className: "note" }, note), el("div", { className: "toolbar" }, el("span", {}, t("Abbonamento")), pick));
    return subs.find((s) => s.id === state.sub);
  }

  // ---- Rules ----
  async function renderRules(main) {
    const sub = await pickSubscription(main, t("Selezione del catalogo"),
      t("Cosa tenere della lista del provider. Le regole valgono a ogni sincronizzazione; le correzioni su singole categorie (scheda Categorie) prevalgono."));
    if (!sub) return;
    const [rules, cats, current] = await Promise.all([
      api("GET", "/api/v1/subscriptions/" + sub.id + "/rules"),
      api("GET", "/api/v1/subscriptions/" + sub.id + "/categories?kind=live"),
      api("GET", "/api/v1/subscriptions/" + sub.id + "/selection")]);
    const countries = {};
    for (const c of cats) {
      const country = c.country_override != null ? c.country_override : c.country;
      if (country) countries[country] = (countries[country] || 0) + c.items;
    }
    const adult = input({ type: "checkbox", checked: rules.adult });
    const lowres = input({ type: "checkbox", checked: rules.low_res });
    const movieYear = input({ type: "number", min: 1900, max: 2100, placeholder: t("nessun limite"), value: rules.movie_min_year || "" });
    const seriesYear = input({ type: "number", min: 1900, max: 2100, placeholder: t("nessun limite"), value: rules.series_min_year || "" });
    const unknownYear = input({ type: "checkbox", checked: rules.hide_unknown_year });
    const countryBoxes = Object.entries(countries).sort((a, b) => b[1] - a[1]).map(([name, n]) => {
      const cb = input({ type: "checkbox", checked: (rules.countries || []).includes(name), value: name });
      return el("label", { className: "check" }, cb, name + " (" + n + ")");
    });
    const table = el("table", { className: "counts" });
    const readRules = () => ({
      countries: countryBoxes.map((l) => l.firstChild).filter((c) => c.checked).map((c) => c.value),
      adult: adult.checked, low_res: lowres.checked,
      movie_min_year: Number(movieYear.value) || 0, series_min_year: Number(seriesYear.value) || 0,
      hide_unknown_year: unknownYear.checked,
    });
    const showCounts = (preview) => {
      const rows = [["live", t("Canali")], ["movie", t("Film")], ["series", t("Serie")]].map(([k, label]) => {
        const now = current[k] || {}, next = (preview || current)[k] || {};
        const cell = (v, before) => el("td", { className: preview && v !== before ? "changed" : "" }, (v || 0).toLocaleString(locale));
        return el("tr", {}, el("td", {}, label), el("td", {}, (now.available || 0).toLocaleString(locale)),
          cell(next.visible, now.visible), cell(next.cards, now.cards));
      });
      table.replaceChildren(el("tr", {}, el("th", {}, ""), el("th", {}, t("Dal provider")), el("th", {}, preview ? t("Visibili (anteprima)") : t("Visibili")),
        el("th", {}, preview ? t("Schede (anteprima)") : t("Schede"))), ...rows);
    };
    showCounts(null);
    let previewTimer;
    const preview = () => {
      clearTimeout(previewTimer);
      previewTimer = setTimeout(async () => {
        try { showCounts(await api("POST", "/api/v1/subscriptions/" + sub.id + "/rules/preview", readRules())); }
        catch (e) { flash(e.message); }
      }, 300);
    };
    const form = el("form", { className: "form" },
      el("label", {}, t("Paesi esteri da tenere")), el("div", { className: "countries" }, ...(countryBoxes.length ? countryBoxes : [el("span", { className: "note" }, t("Nessun canale estero riconosciuto"))])),
      el("label", {}, t("Contenuti per adulti")), el("label", { className: "check" }, adult, t("Mostra")),
      el("label", {}, t("Doppioni a bassa qualità")), el("label", { className: "check" }, lowres, t("Mostra (es. “SD Connessioni lente”)")),
      ...field(t("Film dall'anno"), movieYear), ...field(t("Serie dall'anno"), seriesYear),
      el("label", {}, t("Titoli senza anno")), el("label", { className: "check" }, unknownYear, t("Nascondi quando c'è un anno minimo")));
    form.addEventListener("input", preview);
    form.addEventListener("change", preview);
    const save = button(t("Salva le regole"), async () => {
      save.disabled = true;
      try { await api("PUT", "/api/v1/subscriptions/" + sub.id + "/rules", readRules()); flash(t("Regole salvate e applicate")); render(); }
      catch (e) { flash(e.message); save.disabled = false; }
    }, "primary");
    main.append(form, table, el("div", { className: "buttons" }, save));
  }

  // ---- Categories ----
  async function renderCategories(main) {
    const sub = await pickSubscription(main, t("Categorie"),
      t("Includi o escludi le categorie una per una e scegli in quale sezione della TV vanno i canali. “Automatico” segue le regole della scheda Selezione."));
    if (!sub) return;
    const kinds = el("div", { className: "toolbar" }, ...[["live", t("Canali")], ["movie", t("Film")], ["series", t("Serie")]].map(([k, label]) => {
      const b = button(label, () => { state.kind = k; render(); }, state.kind === k ? "primary" : "");
      return b;
    }));
    const filter = input({ type: "search", placeholder: t("Filtra per nome"), className: "pick focusable" });
    kinds.append(filter);
    const cats = await api("GET", "/api/v1/subscriptions/" + sub.id + "/categories?kind=" + state.kind);
    const table = el("table", { className: "cats" });
    const head = el("tr", {}, el("th", {}, t("Categoria")), el("th", {}, t("Titoli")), el("th", {}, t("Inclusa")),
      state.kind === "live" ? el("th", {}, t("Sezione")) : null, el("th", {}, t("Stato")));
    const rows = cats.map((c) => {
      const include = el("select", { className: "focusable" }, el("option", { value: "" }, t("Automatico")),
        el("option", { value: "true" }, t("Sempre")), el("option", { value: "false" }, t("Mai")));
      include.value = c.enabled_override == null ? "" : String(c.enabled_override);
      let section = null;
      if (state.kind === "live") {
        section = el("select", { className: "focusable" }, el("option", { value: "" }, t("Automatico ({section})", { section: { tv: "TV", sport: "Sport", radio: "Radio" }[c.effective.section || c.section] || "TV" })),
          el("option", { value: "tv" }, "TV"), el("option", { value: "sport" }, "Sport"), el("option", { value: "radio" }, "Radio"));
        section.value = c.section_override || "";
      }
      const status = el("td", {}, c.effective.enabled ? el("span", { className: "ok" }, t("Inclusa")) : el("span", { className: "why" }, c.effective.reason || t("Esclusa")));
      const row = el("tr", { className: c.effective.enabled ? "" : "off" }, el("td", { className: "name" }, c.name),
        el("td", { className: "n" }, c.items.toLocaleString(locale)), el("td", {}, include), section ? el("td", {}, section) : null, status);
      row.dataset.name = c.name.toLowerCase();
      const save = async () => {
        const body = { enabled: include.value === "" ? null : include.value === "true", section: section && section.value ? section.value : null,
          country: c.country_override == null ? null : c.country_override, adult: c.adult_override == null ? null : c.adult_override };
        try {
          await api("PUT", "/api/v1/subscriptions/" + sub.id + "/categories/" + state.kind + "/" + encodeURIComponent(c.id), body);
          const fresh = (await api("GET", "/api/v1/subscriptions/" + sub.id + "/categories?kind=" + state.kind)).find((x) => x.id === c.id);
          if (fresh) {
            row.className = fresh.effective.enabled ? "" : "off";
            status.replaceChildren(fresh.effective.enabled ? el("span", { className: "ok" }, t("Inclusa")) : el("span", { className: "why" }, fresh.effective.reason || t("Esclusa")));
          }
          flash(t("Salvato: {name}", { name: c.name }));
        } catch (e) { flash(e.message); }
      };
      include.onchange = save;
      if (section) section.onchange = save;
      return row;
    });
    table.append(head, ...rows);
    filter.oninput = () => {
      const q = filter.value.trim().toLowerCase();
      for (const r of rows) r.hidden = q && !r.dataset.name.includes(q);
    };
    const included = cats.filter((c) => c.effective.enabled).length;
    main.append(kinds, el("p", { className: "note" }, t("{n} categorie incluse su {total}", { n: included, total: cats.length })), table);
  }

  // ---- System: problems and schedule ----
  async function renderSystem(main) {
    const status = await api("GET", "/api/v1/status");
    const parts = [el("h2", {}, t("Stato"))];
    if (!status.problems.length) parts.push(el("p", { className: "ok" }, "✓ " + t("Nessun problema: VPN collegate, abbonamenti attivi, guide aggiornate.")));
    for (const p of status.problems) parts.push(el("p", { className: p.level === "error" ? "bad" : "" }, "⚠ " + p.text));
    const sc = status.schedule;
    const hour = el("select", { className: "focusable" }, el("option", { value: -1 }, t("Mai (solo a mano)")),
      ...Array.from({ length: 24 }, (_, h) => el("option", { value: h }, String(h).padStart(2, "0") + ":00")));
    hour.value = sc.catalog_hour;
    const guide = el("select", { className: "focusable" }, el("option", { value: 0 }, t("Mai (solo con il catalogo)")),
      ...[1, 2, 3, 4, 6, 8, 12, 24].map((h) => el("option", { value: h }, h === 1 ? t("Ogni ora") : t("Ogni {h} ore", { h }))));
    guide.value = sc.guide_hours;
    const save = el("button", { className: "small primary focusable full", type: "submit" }, t("Salva"));
    const form = el("form", { className: "form" }, ...field(t("Catalogo, ogni notte alle"), hour), ...field(t("Guida TV"), guide), save);
    form.onsubmit = async (e) => {
      e.preventDefault();
      try {
        await api("PUT", "/api/v1/settings/schedule", { catalog_hour: Number(hour.value), guide_hours: Number(guide.value) });
        flash(t("Pianificazione salvata"));
        render();
      } catch (err) { flash(err.message); }
    };
    parts.push(el("h2", {}, t("Aggiornamenti automatici")),
      el("p", { className: "note" }, t("Il catalogo di ogni abbonamento (con la guida) si aggiorna ogni notte; se non riesce riprova ogni ora. La guida si aggiorna anche tra una notte e l'altra.") +
        (status.next_catalog ? " " + t("Prossimo aggiornamento del catalogo: {when}.", { when: when(status.next_catalog) }) : "")), form);
    const update = await api("GET", "/api/v1/update").catch(() => null);
    if (update) parts.push(...updateSection(update));
    if (window.VodMax.native) {
      const appButtons = el("div", { className: "buttons" }, button(t("Cambia server"), () => window.VodMax.native.changeServer()));
      const appStatus = el("p", { className: "note" });
      if (window.VodMax.appUpdate(update)) {
        const go = button(t("Aggiorna l'app a {version}", { version: update.latest.replace(/^v/, "") }), () => {
          go.disabled = true;
          window.VodMax.updateApp(update, (text, failed) => { appStatus.textContent = text; go.disabled = !failed; });
        }, "primary");
        appButtons.prepend(go);
      }
      parts.push(el("h2", {}, "App"), el("p", { className: "note" }, t("VodMax per Android {version}, collegata a {server}.", { version: window.VodMax.native.version(), server: location.origin })),
        appButtons, appStatus);
    }
    parts.push(...await downloadSection(), ...await liveSection(), ...await transcodeSection(), ...await outputSection(), ...await recordingLimits(), ...(await metadataSection()).filter(Boolean), ...await backupSection());
    main.replaceChildren(...parts);
  }

  // ---- Version and updates ----
  function updateSection(u) {
    const parts = [el("h2", {}, t("Versione e aggiornamenti"))];
    const facts = el("div", { className: "facts" }, el("span", {}, t("Server"), " ", el("b", {}, u.current || "?")),
      u.latest ? el("span", {}, t("Ultima versione"), " ", el("b", {}, u.latest)) : null,
      u.checked_at ? el("span", {}, t("Controllato"), " ", el("b", {}, when(u.checked_at))) : null);
    parts.push(facts);
    if (u.error) parts.push(el("p", { className: "note bad" }, u.error));
    if (u.available) {
      const box = el("section", { className: "box" }, el("h3", {}, t("È disponibile VodMax {version}", { version: u.latest.replace(/^v/, "") })));
      if (u.notes) box.append(el("pre", { className: "notes" }, u.notes));
      // A beta is installed by name: plain vodmax-update keeps to stable versions.
      box.append(el("p", { className: "note" }, t("Per aggiornare il server, sulla macchina dove gira:")),
        cmd(u.prerelease ? "sudo vodmax-update " + u.latest : "sudo vodmax-update"),
        el("p", { className: "note" }, t("Con Docker: immagine {image}.", { image: "ghcr.io/silirim/vodmax:" + (u.prerelease ? u.latest.replace(/^v/, "") : "latest") })),
        el("p", { className: "note" }, t("Lo script scarica la versione per questa macchina, verifica il checksum, tiene la precedente e riavvia il servizio. Le app Android si aggiornano da sole, con una conferma.")));
      if (u.page) box.append(el("p", {}, el("a", { href: u.page, target: "_blank", rel: "noopener", className: "focusable" }, t("Note complete su GitHub"))));
      parts.push(box);
    } else if (u.latest) {
      parts.push(el("p", { className: "ok" }, t("✓ Il server è aggiornato.")));
    }
    const auto = input({ type: "checkbox", checked: u.enabled });
    auto.onchange = async () => {
      try { await api("PUT", "/api/v1/settings/updates", { enabled: auto.checked, beta: u.beta }); flash(auto.checked ? t("Controllo giornaliero attivo") : t("Controllo giornaliero disattivato")); }
      catch (e) { flash(e.message); auto.checked = !auto.checked; }
    };
    const check = button(t("Controlla ora"), async () => {
      check.disabled = true;
      try { await api("POST", "/api/v1/update/check"); } catch (e) { flash(e.message); }
      render();
    });
    const beta = input({ type: "checkbox", checked: u.beta });
    beta.onchange = async () => {
      beta.disabled = true;
      try { await api("PUT", "/api/v1/settings/updates", { enabled: auto.checked, beta: beta.checked }); flash(beta.checked ? t("Ti verranno proposte anche le beta") : t("Ti verranno proposte solo le versioni stabili")); render(); }
      catch (e) { flash(e.message); beta.checked = !beta.checked; beta.disabled = false; }
    };
    parts.push(el("label", { className: "check" }, auto, t("Controlla ogni giorno se c'è una nuova versione (contatta solo GitHub, senza inviare dati)")),
      el("label", { className: "check" }, beta, t("Proponi anche le versioni beta (novità in prova, possono avere difetti); vale anche per le app Android")),
      el("div", { className: "buttons" }, check));
    return parts;
  }

  // ---- Downloads ----
  async function downloadSection() {
    // Not on a device running alone: there the list answers with an error.
    if (!(await api("GET", "/api/v1/downloads").then(() => true, () => false))) return [];
    const set = await api("GET", "/api/v1/settings/downloads");
    const night = input({ type: "checkbox", checked: set.night_only });
    const hours = (v) => {
      const sel = el("select", { className: "focusable" }, ...Array.from({ length: 24 }, (_, h) => el("option", { value: h }, String(h).padStart(2, "0") + ":00")));
      sel.value = v;
      return sel;
    };
    const from = hours(set.from_hour), to = hours(set.to_hour);
    const save = el("button", { className: "small primary focusable full", type: "submit" }, t("Salva"));
    const form = el("form", { className: "form" }, el("label", {}, t("Quando")), el("label", { className: "check" }, night, t("Solo in una fascia oraria")),
      ...field(t("Dalle"), from), ...field(t("Alle"), to), save);
    const sync = () => { from.disabled = to.disabled = !night.checked; };
    night.onchange = sync;
    sync();
    form.onsubmit = async (e) => {
      e.preventDefault();
      try {
        await api("PUT", "/api/v1/settings/downloads", { night_only: night.checked, from_hour: Number(from.value), to_hour: Number(to.value) });
        flash(t("Impostazioni dei download salvate"));
      } catch (err) { flash(err.message); }
    };
    return [el("h2", {}, t("Download")),
      el("p", { className: "note" }, t("Film ed episodi scaricati vanno nella cartella dei download del server e compaiono nelle raccolte «Scaricati». " +
        "Si scarica uno alla volta e solo con una connessione libera: chi guarda ha sempre la precedenza. L'elenco è nella pagina Download del menu.")), form];
  }

  // ---- Live TV ----
  async function liveSection() {
    const ls = await api("GET", "/api/v1/settings/live");
    const mode = el("select", { className: "focusable" }, el("option", { value: "timeshift" }, t("Pausa e riavvolgimento, come Sky (consigliato)")),
      el("option", { value: "direct" }, t("Ritardo minimo, senza pausa")));
    mode.value = ls.mode;
    const win = el("select", { className: "focusable" }, ...[30, 60, 120, 180, 240, 360].map((m) => el("option", { value: m }, m < 60 ? t("{n} minuti", { n: m }) : m === 60 ? t("1 ora") : t("{n} ore", { n: m / 60 }))));
    win.value = ls.window_minutes;
    const save = el("button", { className: "small primary focusable full", type: "submit" }, t("Salva"));
    const form = el("form", { className: "form" }, ...field(t("Diretta"), mode), ...field(t("Si torna indietro fino a"), win), save);
    form.onsubmit = async (e) => {
      e.preventDefault();
      try { await api("PUT", "/api/v1/settings/live", { mode: mode.value, window_minutes: Number(win.value) }); flash(t("Salvato: vale dal prossimo cambio di canale")); }
      catch (err) { flash(err.message); }
    };
    return [el("h2", {}, t("Diretta TV")),
      el("p", { className: "note" }, t("Con pausa e riavvolgimento il server tiene il canale che stai guardando su disco (circa 3 GB all'ora in HD; " +
        "si cancella cambiando canale): la diretta è 6–8 secondi indietro e il cambio canale richiede 2–4 secondi. Con ritardo minimo si torna al flusso diretto.")), form];
  }

  // ---- Video conversion (graphics card) ----
  async function transcodeSection() {
    const tc = await api("GET", "/api/v1/settings/transcode").catch(() => null);
    if (!tc) return [];
    const NAMES = { "": t("Processore"), vaapi: "Intel / AMD (VAAPI)", qsv: "Intel Quick Sync (QSV)", nvenc: "NVIDIA (NVENC)" };
    const WHY = { no_ffmpeg: t("ffmpeg non trovato"), no_encoder: t("questo ffmpeg non lo supporta"), no_device: t("scheda non visibile qui") };
    const accel = el("select", { className: "focusable" }, ...tc.options.map((o) =>
      el("option", { value: o.accel }, NAMES[o.accel] + (o.found ? "" : " — " + WHY[o.reason]))));
    accel.value = tc.accel;
    const device = el("select", { className: "focusable" }, ...(tc.devices.length ? tc.devices : ["/dev/dri/renderD128"]).map((d) => el("option", { value: d }, d)));
    if (tc.device) device.value = tc.device;
    const deviceLabel = el("label", {}, t("Dispositivo"));
    const result = el("p", { className: "note" });
    const sync = () => { deviceLabel.hidden = device.hidden = !["vaapi", "qsv"].includes(accel.value); result.textContent = ""; result.className = "note"; };
    accel.onchange = sync;
    sync();
    const body = () => ({ accel: accel.value, device: device.hidden ? "" : device.value });
    const test = button(t("Prova"), async () => {
      test.disabled = true;
      result.className = "note";
      result.textContent = t("Prova in corso…");
      try {
        const r = await api("POST", "/api/v1/settings/transcode/test", body());
        result.className = "ok";
        result.textContent = "✓ " + t("Funziona: 2 secondi di video Full HD convertiti in {s} s.", { s: r.seconds.toLocaleString(locale, { maximumFractionDigits: 1 }) });
      } catch (e) { result.className = "note bad"; result.textContent = e.message; }
      test.disabled = false;
    });
    const save = el("button", { className: "small primary focusable", type: "submit" }, t("Salva"));
    const form = el("form", { className: "form" }, ...field(t("Converti con"), accel), deviceLabel, device,
      el("div", { className: "buttons full" }, test, save), el("div", { className: "full" }, result));
    form.onsubmit = async (e) => {
      e.preventDefault();
      try { await api("PUT", "/api/v1/settings/transcode", body()); flash(t("Salvato: vale dal prossimo video")); }
      catch (err) { flash(err.message); }
    };
    return [el("h2", {}, t("Conversione video")),
      el("p", { className: "note" }, t("Quasi sempre il video passa così com'è. Si converte solo quando il browser non sa leggerlo (per esempio HEVC o MPEG-2): " +
        "con la scheda grafica è molto più veloce e lascia libero il processore. Se la scheda non parte, VodMax usa il processore da solo. " +
        "Con Docker la scheda va passata al container (--device /dev/dri, o il runtime NVIDIA).")), form];
  }

  // ---- The assistant: built in, an online service or the admin's server ----
  const gbytes = (n) => (n / 1073741824).toLocaleString(locale, { maximumFractionDigits: 1 }) + " GB";
  let assistantPoll;

  async function renderAssistant(main) {
    clearTimeout(assistantPoll);
    const a = await api("GET", "/api/v1/settings/assistant");
    const local = a.local;
    const mode = state.assistantMode || (a.mode === "online" || a.mode === "local" ? a.mode : a.url ? "server" : "local");
    const result = el("p", { className: "note" });
    const tryIt = async () => {
      result.className = "note";
      result.textContent = t("Prova in corso…");
      try {
        const r = await api("POST", "/api/v1/settings/assistant/test");
        result.className = "ok";
        result.textContent = "✓ " + t("Risponde in {s} s: «{answer}»", { s: r.seconds.toLocaleString(locale, { maximumFractionDigits: 1 }), answer: r.answer.slice(0, 160) });
      } catch (e) { result.className = "note bad"; result.textContent = e.message; }
    };
    const save = async (body, test = true) => {
      try {
        await api("PUT", "/api/v1/settings/assistant", body);
        flash(body.enabled ? t("Assistente acceso") : t("Assistente spento"));
        state.assistantMode = null;
        await render();
        assistantPromo();
        const tryIt = test && body.enabled && document.querySelector("#content .assistant-try");
        if (tryIt) tryIt.click(); // no "?.": old Android WebViews cannot read it
      } catch (e) { flash(e.message); }
    };

    const parts = [el("h2", {}, t("Assistente")),
      el("p", { className: "note" }, t("Un modello linguistico crea le regole di registrazione da una frase e propone cosa guardare stasera. " +
        "È facoltativo: spento, VodMax non mostra nessuna delle sue funzioni. Propone soltanto: le regole le controlli e le salvi tu."))];
    if (a.enabled) {
      const what = a.mode === "local" ? t("integrato") + " · " + ((local.models.find((m) => m.id === a.model) || {}).name || a.model)
        : a.mode === "online" ? ((a.providers.find((p) => p.id === a.provider) || {}).name || a.provider) + " · " + a.model
        : a.url + " · " + a.model;
      const tryButton = button(t("Prova"), tryIt, "assistant-try");
      parts.push(el("div", { className: "facts" }, el("span", {}, t("Stato") + " ", el("b", { className: "ok" }, t("acceso"))), el("span", {}, el("b", {}, what))),
        el("div", { className: "buttons" }, tryButton, button(t("Spegni"), () => save({ enabled: false, mode: a.mode, provider: a.provider, url: a.url, model: a.model }))), result);
      if (a.people && a.people.total) {
        parts.push(el("p", { className: "note" }, t("Cast e regia noti per {known} titoli su {total}: servono per richieste come «un film con…». VodMax completa l'elenco di notte, a poco a poco, finché l'assistente è acceso.",
          { known: a.people.known.toLocaleString(locale), total: a.people.total.toLocaleString(locale) })));
      }
    }

    // The three ways, as a choice.
    const ways = [["local", t("Integrato"), t("VodMax scarica e fa girare il modello qui. Niente esce di casa.")],
      ["online", t("Servizio online"), t("Claude, OpenAI, Mistral, Gemini… con una chiave API.")],
      ["server", t("Server tuo"), t("Ollama, LM Studio o un altro server compatibile OpenAI.")]];
    parts.push(el("h2", {}, a.enabled ? t("Cambia modo") : t("Scegli come")),
      el("div", { className: "ways" }, ...ways.map(([id, title, sub]) => {
        const b = el("button", { className: "way focusable" + (id === mode ? " chosen" : ""), type: "button", onclick: () => { state.assistantMode = id; render(); } },
          el("b", {}, title), el("span", {}, sub));
        b.setAttribute("aria-pressed", id === mode);
        return b;
      })));
    if (mode === "local") parts.push(...localPanel(a, local, save));
    if (mode === "online") parts.push(...onlinePanel(a, save, result));
    if (mode === "server") parts.push(...serverPanel(a, save, result));
    if (!a.enabled && mode !== "local") parts.push(result);
    main.replaceChildren(...parts.filter(Boolean));
    assistantPromo();
    if (local.job && local.job.phase !== "error") assistantPoll = setTimeout(() => { if (state.view === "assistant") render(); }, 1000);
  }

  function localPanel(a, local, save) {
    const hw = local.hardware;
    const parts = [el("h2", {}, t("Assistente integrato"))];
    if (hw.unavailable) return [...parts, el("p", { className: "note bad" }, hw.unavailable)];
    parts.push(el("p", { className: "note" }, hw.backend === "ollama"
      ? t("Il modello gira nel servizio \"assistant\" accanto a VodMax.")
      : t("Questo server: {ram} di RAM ({free} liberi), {disk} liberi su disco, {gpu}.", {
        ram: gbytes(hw.ram), free: gbytes(hw.ram_free), disk: gbytes(hw.disk_free), gpu: hw.gpu || t("nessuna scheda video") })),
    hw.gpu_note ? el("p", { className: "note" }, hw.gpu_note) : null);
    const job = local.job;
    if (job) {
      const phase = { engine: t("Scarico il motore…"), model: t("Scarico il modello…"), start: t("Avvio…"), test: t("Provo l'assistente…") }[job.phase];
      if (job.phase === "error") {
        parts.push(el("p", { className: "note bad" }, t("Installazione non riuscita: {error}", { error: job.error })),
          el("div", { className: "buttons" }, button(t("Riprova"), () => installLocal(job.model), "primary"), button(t("Chiudi"), cancelLocal)));
      } else {
        const pct = job.total ? Math.min(100, Math.round(job.done * 100 / job.total)) : 0;
        const bar = el("div", { className: "progress" }, el("div", {}));
        bar.firstChild.style.width = (job.total ? pct : 100) + "%";
        if (!job.total) bar.classList.add("busy");
        parts.push(el("p", {}, el("b", {}, phase), job.total ? " " + gbytes(job.done) + " / " + gbytes(job.total) + " (" + pct + "%)" : ""), bar,
          el("p", { className: "note" }, t("Puoi lasciare la pagina: il download continua. Se lo annulli, la prossima volta riparte da dove era arrivato.")),
          el("div", { className: "buttons" }, button(t("Annulla"), cancelLocal)));
        return parts;
      }
    }
    for (const m of local.models) {
      const using = a.enabled && a.mode === "local" && a.model === m.id;
      const tags = [m.recommended ? el("span", { className: "tag good" }, t("consigliato")) : null,
        m.installed ? el("span", { className: "tag" }, t("scaricato")) : null,
        !m.fits ? el("span", { className: "tag bad" }, t("RAM insufficiente")) : null];
      const act = using ? el("span", { className: "ok" }, "✓ " + t("in uso"))
        : m.installed ? button(t("Usa questo"), () => save({ enabled: true, mode: "local", model: m.id }), "primary")
        : button(t("Attiva"), () => installLocal(m.id), m.recommended ? "primary" : "");
      if (!m.fits && !m.installed) act.disabled = true;
      parts.push(el("section", { className: "box model" + (m.fits ? "" : " dim") },
        el("h3", {}, m.name, ...tags),
        el("p", { className: "note" }, m.note),
        el("div", { className: "facts" },
          el("span", {}, t("Da scaricare") + " ", el("b", {}, gbytes(m.size))),
          el("span", {}, t("RAM mentre risponde") + " ", el("b", {}, gbytes(m.ram)))),
        el("div", { className: "buttons" }, act)));
    }
    if (local.engine_installed || local.models.some((m) => m.installed)) {
      parts.push(el("p", { className: "note" }, t("Il modello resta in memoria solo mentre lo usi e si spegne dopo 10 minuti senza richieste.")),
        el("div", { className: "buttons" }, button(t("Rimuovi l'assistente integrato"), async () => {
          if (!confirm(t("Cancellare motore e modelli scaricati? Lo spazio torna libero."))) return;
          try { await api("DELETE", "/api/v1/settings/assistant/local"); flash(t("Assistente integrato rimosso")); render(); assistantPromo(); }
          catch (e) { flash(e.message); }
        }, "danger")));
    }
    return parts;
  }

  async function installLocal(id) {
    try { await api("POST", "/api/v1/settings/assistant/local", { model: id }); } catch (e) { flash(e.message); }
    render();
  }

  async function cancelLocal() {
    try { await api("POST", "/api/v1/settings/assistant/local/cancel"); } catch (e) { flash(e.message); }
    render();
  }

  // A model field with the list the service offers.
  function modelPicker(value, load) {
    const list = el("datalist", { id: "assistant-models" });
    const model = input({ value, spellcheck: false, autocomplete: "off" });
    model.setAttribute("list", "assistant-models");
    const loadButton = button(t("Elenco modelli"), async () => {
      loadButton.disabled = true;
      try {
        const ids = (await load()).models || [];
        list.replaceChildren(...ids.map((id) => el("option", { value: id })));
        if (!model.value) model.value = ids.find((id) => /haiku|mini|flash|small/.test(id)) || ids[0] || "";
        flash(t("{n} modelli disponibili", { n: ids.length }));
      } catch (e) { flash(e.message); }
      loadButton.disabled = false;
    });
    return { model, row: el("div", { className: "cmd-row" }, model, loadButton, list) };
  }

  function onlinePanel(a, save, result) {
    const provider = el("select", { className: "focusable" }, ...a.providers.map((p) => el("option", { value: p.id, selected: p.id === (a.provider || "anthropic") }, p.name)));
    const keyLink = el("a", { target: "_blank", rel: "noopener" }, t("Crea una chiave API"));
    const keepKey = a.mode === "online" && a.has_key;
    const key = input({ type: "password", autocomplete: "new-password", spellcheck: false,
      placeholder: keepKey ? t("configurata: lascia vuoto per non cambiarla") : t("la chiave API del servizio") });
    const picked = () => a.providers.find((p) => p.id === provider.value);
    const { model, row } = modelPicker(a.mode === "online" ? a.model : "", () =>
      api("POST", "/api/v1/settings/assistant/models", { mode: "online", provider: provider.value, key: key.value }));
    const sync = () => { const p = picked(); keyLink.href = p.key_url; if (p.model && (!model.value || a.provider !== p.id)) model.value = p.model; };
    provider.onchange = () => { model.value = ""; sync(); };
    sync();
    const form = el("form", { className: "form" }, ...field(t("Servizio"), provider), ...field(t("Chiave API"), key),
      el("label", {}, ""), el("p", { className: "note" }, keyLink),
      ...field(t("Modello"), row),
      el("div", { className: "buttons full" }, el("button", { className: "small primary focusable", type: "submit" }, t("Salva e prova"))));
    form.onsubmit = (e) => {
      e.preventDefault();
      const body = { enabled: true, mode: "online", provider: provider.value, model: model.value };
      if (key.value) body.key = key.value;
      save(body);
    };
    return [el("h2", {}, t("Servizio online")),
      el("p", { className: "note" }, t("Serve una chiave API, che si paga a consumo: l'abbonamento (Claude Pro o Max, ChatGPT Plus) non si può usare da un'app esterna. " +
        "Per questo uso la spesa è di pochi centesimi al mese. La guida TV della serata, i nomi dei canali e i titoli della tua lista vanno al servizio; mai credenziali né indirizzi del provider.")),
      form];
  }

  function serverPanel(a, save, result) {
    const url = input({ value: a.mode === "server" || !a.mode ? a.url : "", placeholder: "http://ollama:11434/v1", spellcheck: false });
    const key = input({ type: "password", autocomplete: "new-password", spellcheck: false,
      placeholder: a.has_key && a.mode !== "online" ? t("configurata: lascia vuoto per non cambiarla") : t("solo se il servizio la chiede") });
    const { model, row } = modelPicker(a.mode === "server" || !a.mode ? a.model : "", () =>
      api("POST", "/api/v1/settings/assistant/models", { mode: "server", url: url.value, key: key.value }));
    model.placeholder = t("es. llama3.1:8b");
    const form = el("form", { className: "form" }, ...field(t("Indirizzo (API compatibile OpenAI)"), url), ...field(t("Chiave"), key),
      ...field(t("Modello"), row),
      el("div", { className: "buttons full" }, el("button", { className: "small primary focusable", type: "submit" }, t("Salva e prova"))));
    form.onsubmit = (e) => {
      e.preventDefault();
      const body = { enabled: true, mode: "server", url: url.value, model: model.value };
      if (key.value) body.key = key.value;
      save(body);
    };
    return [el("h2", {}, t("Server tuo")),
      el("p", { className: "note" }, t("Va bene qualsiasi server con l'API compatibile OpenAI: Ollama, LM Studio, llama.cpp. Con un server in casa non esce nulla dalla tua rete.")),
      form];
  }

  // ---- Output to other apps (HDHomeRun, M3U, XMLTV) ----
  async function outputSection() {
    const o = await api("GET", "/api/v1/settings/output");
    const parts = [el("h2", {}, t("Uscita verso altre app")),
      el("p", { className: "note" }, t("VodMax può dare i suoi canali ad altre app di casa: Plex, Jellyfin ed Emby lo vedono come un sintonizzatore " +
        "HDHomeRun (e registrano con il loro DVR), TiviMate, Kodi e VLC usano la playlist M3U con la guida XMLTV. I canali passano comunque da VodMax: " +
        "stesse connessioni, stessa VPN. Gli indirizzi contengono una chiave segreta: chi li ha può guardare i canali, quindi non condividerli fuori casa."))];
    const toggle = button(o.enabled ? t("Spegni") : t("Accendi"), async () => {
      try { await api("PUT", "/api/v1/settings/output", { enabled: !o.enabled }); flash(o.enabled ? t("Uscita spenta") : t("Uscita accesa")); render(); }
      catch (e) { flash(e.message); }
    }, o.enabled ? "" : "primary");
    if (o.enabled && o.links) {
      const row = (label, url, hint) => el("div", { className: "box output-link" }, el("h3", {}, label), cmd(url), el("p", { className: "note" }, hint));
      parts.push(el("p", { className: "ok" }, "✓ " + t("Accesa: {n} canali alla volta (le connessioni degli abbonamenti).", { n: o.tuners })),
        row(t("Sintonizzatore HDHomeRun"), o.links.tuner, t("Plex: Impostazioni → TV in diretta e DVR → Configura → inserisci l'indirizzo a mano. Jellyfin/Emby: TV in diretta → Aggiungi sintonizzatore → HDHomeRun.")),
        row(t("Playlist M3U"), o.links.playlist, t("TiviMate, Kodi (PVR IPTV Simple), VLC, o Jellyfin come sintonizzatore M3U.")),
        row(t("Guida XMLTV"), o.links.guide, t("La guida dei canali della playlist, per le app che la chiedono a parte.")));
      const renew = button(t("Cambia la chiave"), async () => {
        if (!confirm(t("Gli indirizzi attuali smettono di funzionare: le app andranno configurate di nuovo. Continuare?"))) return;
        try { await api("PUT", "/api/v1/settings/output", { enabled: true, new_key: true }); flash(t("Chiave cambiata")); render(); }
        catch (e) { flash(e.message); }
      });
      parts.push(el("div", { className: "buttons" }, toggle, renew));
    } else {
      parts.push(el("div", { className: "buttons" }, toggle));
    }
    return parts;
  }

  // ---- Recording space ----
  async function recordingLimits() {
    const sp = await api("GET", "/api/v1/recordings/space");
    const gb = (b) => (b / 1e9).toLocaleString(locale, { maximumFractionDigits: 1 }) + " GB";
    const max = input({ type: "number", min: 0, value: sp.max_gb || "", placeholder: t("nessun limite") });
    const days = input({ type: "number", min: 0, value: sp.keep_days || "", placeholder: t("per sempre") });
    const save = el("button", { className: "small primary focusable full", type: "submit" }, t("Salva"));
    const form = el("form", { className: "form" }, ...field(t("Spazio massimo (GB)"), max), ...field(t("Tieni per (giorni)"), days), save);
    form.onsubmit = async (e) => {
      e.preventDefault();
      try { await api("PUT", "/api/v1/settings/recordings", { max_gb: Number(max.value) || 0, keep_days: Number(days.value) || 0 }); flash(t("Limiti salvati e applicati")); render(); }
      catch (err) { flash(err.message); }
    };
    const cs = await api("GET", "/api/v1/settings/commercials").catch(() => null);
    let ads = null;
    if (cs) {
      const box = input({ type: "checkbox", checked: cs.enabled, disabled: !cs.comskip && !cs.enabled });
      box.onchange = async () => {
        try { await api("PUT", "/api/v1/settings/commercials", { enabled: box.checked }); flash(box.checked ? t("Pubblicità: analisi attiva") : t("Pubblicità: analisi spenta")); }
        catch (e) { flash(e.message); box.checked = !box.checked; }
      };
      ads = [el("label", { className: "check" }, box, t("Trova la pubblicità nelle registrazioni finite (con Comskip) e offri «Salta pubblicità»")),
        el("p", { className: "note" }, cs.comskip ? t("Comskip trovato. Le impostazioni si cambiano con un file comskip.ini nella cartella delle registrazioni.")
          : t("Comskip non è installato su questa macchina: installalo (comskip nel PATH) per usare questa funzione."))];
    }
    return [el("h2", {}, t("Spazio delle registrazioni")),
      el("p", { className: "note" }, (sp.free_bytes >= 0
        ? t("{count} registrazioni, {used}; liberi sul disco {free} su {total}.", { count: sp.count, used: gb(sp.used_bytes), free: gb(sp.free_bytes), total: gb(sp.total_bytes) })
        : t("{count} registrazioni, {used}.", { count: sp.count, used: gb(sp.used_bytes) })) +
        " " + t("Oltre i limiti si cancellano le più vecchie; quelle protette (🔒) mai. Sotto 1 GB liberi le registrazioni non partono.")), form, ...(ads || [])];
  }

  // ---- Covers and details (TMDB) ----
  async function metadataSection() {
    const m = await api("GET", "/api/v1/settings/metadata");
    const key = input({ type: "password", autocomplete: "off", spellcheck: false,
      placeholder: m.tmdb ? t("configurata: incolla una nuova chiave per sostituirla") : t("Chiave API (v3) o token di accesso in lettura (v4)") });
    const save = el("button", { className: "small primary focusable full", type: "submit" }, t("Salva la chiave"));
    const form = el("form", { className: "form" }, ...field(t("Chiave TMDB"), key), save);
    form.onsubmit = async (e) => {
      e.preventDefault();
      save.disabled = true;
      try { await api("PUT", "/api/v1/settings/metadata", { tmdb_key: key.value }); flash(t("Chiave TMDB verificata e salvata")); render(); }
      catch (err) { flash(err.message); save.disabled = false; }
    };
    const remove = m.tmdb ? button(t("Togli la chiave"), async () => {
      try { await api("PUT", "/api/v1/settings/metadata", { tmdb_key: "" }); flash(t("Chiave tolta")); render(); } catch (e) { flash(e.message); }
    }, "danger") : null;
    return [el("h2", {}, t("Copertine e schede (TMDB)")),
      el("p", { className: "note" }, t("Le schede di film e serie vengono dal provider. Con una chiave gratuita di The Movie Database si aggiungono " +
        "la locandina dove il provider non ce l'ha o l'ha rotta, la trama in italiano quando manca, lo sfondo e il cast. " +
        "La chiave si crea su themoviedb.org → Impostazioni → API. Resta cifrata sul server e non viene mai mostrata.")),
      el("p", {}, m.tmdb ? el("span", { className: "ok" }, "✓ " + t("TMDB attivo")) : el("span", { className: "note" }, t("TMDB non configurato"))),
      form, remove ? el("div", { className: "buttons" }, remove) : null,
      // Attribution required by TMDB's terms of use.
      el("p", { className: "note" }, t("Questo prodotto usa le API di TMDB ma non è approvato né certificato da TMDB."))];
  }

  // ---- Backup ----
  async function download(method, path, body, headers = {}) {
    const res = await fetch(path, { method, body, headers: { Authorization: "Bearer " + window.VodMax.token(), ...headers } });
    if (!res.ok) {
      const data = await res.json().catch(() => null);
      throw new Error(data && data.error ? data.error : t("errore {status}", { status: res.status }));
    }
    const name = (/filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") || "") || [])[1] || "vodmax.vodmaxbackup";
    const blob = await res.blob();
    // The Android app's WebView does not save blob: links: the app asks
    // where to save the file and writes it.
    if (window.VodMaxNative && window.VodMaxNative.saveFile) return saveNative(name, blob);
    const url = URL.createObjectURL(blob);
    const a = el("a", { href: url, download: name });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10000);
  }

  function saveNative(name, blob) {
    return new Promise((resolve, reject) => {
      const r = new FileReader();
      r.onerror = () => reject(new Error(t("file non leggibile")));
      r.onload = () => {
        window.VodMaxSaveDone = (error) => (error ? reject(new Error(error)) : resolve());
        window.VodMaxNative.saveFile(name, blob.type || "application/octet-stream", String(r.result).split(",")[1] || "");
      };
      r.readAsDataURL(blob);
    });
  }

  async function restoreFrom(send) {
    if (!confirm(t("Il ripristino sostituisce abbonamenti, VPN, regole, preferiti, avanzamenti e registrazioni programmate con quelli del backup. " +
      "Prima viene fatta una copia automatica dello stato attuale. Continuare?"))) return;
    try {
      await send();
      flash(t("Ripristino fatto: i cataloghi si stanno scaricando di nuovo (circa un minuto per abbonamento)"));
      render();
    } catch (e) { flash(e.message); }
  }

  async function backupSection() {
    const pass = input({ type: "password", autocomplete: "new-password", placeholder: t("almeno 8 caratteri") });
    const pass2 = input({ type: "password", autocomplete: "new-password" });
    const make = el("button", { className: "small primary focusable full", type: "submit" }, t("Scarica il backup"));
    const exportForm = el("form", { className: "form" }, ...field("Password", pass), ...field(t("Ripeti la password"), pass2), make);
    exportForm.onsubmit = async (e) => {
      e.preventDefault();
      if (pass.value !== pass2.value) { flash(t("Le due password non coincidono")); return; }
      make.disabled = true;
      try { await download("POST", "/api/v1/backup", JSON.stringify({ password: pass.value }), { "Content-Type": "application/json" }); flash(t("Backup scaricato")); }
      catch (err) { flash(err.message); }
      make.disabled = false;
    };
    const file = input({ type: "file", accept: ".vodmaxbackup" });
    const rpass = input({ type: "password", autocomplete: "off" });
    const restoreButton = el("button", { className: "small focusable full", type: "submit" }, t("Ripristina da questo file"));
    const importForm = el("form", { className: "form" }, ...field("File", file), ...field(t("Password del backup"), rpass), restoreButton);
    importForm.onsubmit = (e) => {
      e.preventDefault();
      if (!file.files[0]) { flash(t("Scegli il file del backup")); return; }
      restoreFrom(async () => {
        const res = await fetch("/api/v1/restore", { method: "POST", body: file.files[0],
          headers: { Authorization: "Bearer " + window.VodMax.token(), "X-Backup-Password": rpass.value } });
        if (!res.ok) { const d = await res.json().catch(() => null); throw new Error(d && d.error ? d.error : t("errore {status}", { status: res.status })); }
      });
    };
    const list = await api("GET", "/api/v1/backups");
    const rows = list.map((b) => el("tr", {}, el("td", {}, when(b.created)), el("td", { className: "n size" }, Math.max(1, Math.round(b.size / 1024)) + " kB"),
      el("td", {}, button(t("Scarica"), () => download("GET", "/api/v1/backups/" + b.name).catch((e) => flash(e.message))),
        " ", button(t("Ripristina"), () => restoreFrom(() => api("POST", "/api/v1/backups/" + b.name + "/restore"))))));
    const now = button(t("Crea una copia adesso"), async () => {
      try { await api("POST", "/api/v1/backups"); flash(t("Copia creata")); render(); } catch (e) { flash(e.message); }
    });
    return [
      el("h2", {}, "Backup"),
      el("p", { className: "note" }, t("Contiene abbonamenti e VPN (con le credenziali), regole di selezione, categorie, titoli nascosti, preferiti, " +
        "avanzamenti, registrazioni programmate e impostazioni. Non contiene il catalogo né i file registrati: il catalogo si riscarica da solo dopo il ripristino.")),
      el("h3", {}, t("Backup da conservare altrove")),
      el("p", { className: "note" }, t("Cifrato con una password tua: si può ripristinare anche su un'altra installazione. Senza la password non si apre: non c'è modo di recuperarla.")),
      exportForm,
      el("h3", {}, t("Ripristina da un file")),
      importForm,
      el("h3", {}, t("Copie automatiche")),
      el("p", { className: "note" }, t("Una al giorno, le ultime 14, sul server. Si aprono solo su questa installazione: per un guasto del disco serve il backup con password.")),
      list.length ? el("table", { className: "cats" }, el("tr", {}, el("th", {}, t("Creata")), el("th", {}, t("Dimensione")), el("th", {}, "")), ...rows)
        : el("p", { className: "note" }, t("Nessuna copia ancora.")),
      el("div", { className: "buttons" }, now),
    ];
  }

  // ---- External guides ----
  async function renderGuideSources(main) {
    const list = await api("GET", "/api/v1/guide/sources");
    const parts = [el("h2", {}, t("Guide TV esterne")),
      el("p", { className: "note" }, t("La guida arriva dal provider. Una guida XMLTV esterna (un sito di EPG, il Tvheadend di casa, anche .xml.gz) " +
        "completa i canali che ne sono senza e i giorni che mancano; non sostituisce mai i programmi che il provider ha già. " +
        "Viene scaricata direttamente, senza VPN (non è il provider), e si aggiorna con la guida."))];
    for (const g of list) {
      const box = el("section", { className: "box" }, el("h3", {}, g.name), el("div", { className: "facts" },
        el("span", {}, t("Stato") + " ", el("b", { className: g.status === "error" ? "bad" : g.status === "ok" ? "ok" : "" },
          !g.enabled ? t("disattivata") : g.status === "ok" ? t("aggiornata") : g.status === "error" ? t("errore") : t("mai scaricata"))),
        g.synced_at ? el("span", {}, t("Ultimo aggiornamento") + " ", el("b", {}, when(g.synced_at))) : null,
        el("span", {}, t("Canali completati") + " ", el("b", {}, g.channels)), el("span", {}, t("Programmi") + " ", el("b", {}, g.programmes.toLocaleString(locale)))),
        el("p", { className: "note" }, g.url), g.error ? el("p", { className: "note bad" }, g.error) : null);
      const sync = button(t("Aggiorna ora"), async () => {
        sync.disabled = true;
        sync.textContent = t("Scaricamento…");
        try { const r = await api("POST", "/api/v1/guide/sources/" + g.id + "/sync"); flash(t("{channels} canali completati, {programmes} programmi", { channels: r.channels, programmes: r.programmes })); }
        catch (e) { flash(e.message); }
        render();
      }, "primary");
      sync.disabled = !g.enabled;
      const toggle = button(g.enabled ? t("Disattiva") : t("Attiva"), async () => {
        try { await api("PATCH", "/api/v1/guide/sources/" + g.id, { enabled: !g.enabled }); } catch (e) { flash(e.message); }
        render();
      });
      const del = button(t("Elimina"), async () => {
        if (!confirm(t("Eliminare la guida “{name}” e i suoi programmi?", { name: g.name }))) return;
        try { await api("DELETE", "/api/v1/guide/sources/" + g.id); } catch (e) { flash(e.message); }
        render();
      }, "danger");
      box.append(el("div", { className: "buttons" }, sync, toggle, del));
      parts.push(box);
    }
    const name = input({ placeholder: t("es. EPG Italia") });
    const url = input({ placeholder: t("https://…/guida.xml.gz"), spellcheck: false });
    const add = el("button", { className: "small primary focusable full", type: "submit" }, t("Aggiungi e scarica"));
    const form = el("form", { className: "form" }, ...field(t("Nome"), name), ...field(t("Indirizzo XMLTV"), url), add);
    form.onsubmit = async (e) => {
      e.preventDefault();
      add.disabled = true;
      add.textContent = t("Scaricamento…");
      try {
        const r = await api("POST", "/api/v1/guide/sources", { name: name.value, url: url.value });
        const res = await api("POST", "/api/v1/guide/sources/" + r.id + "/sync").catch((err) => { flash(err.message); return null; });
        if (res) flash(t("Guida aggiunta: {channels} canali completati", { channels: res.channels }));
        render();
      } catch (err) { flash(err.message); add.disabled = false; add.textContent = t("Aggiungi e scarica"); }
    };
    parts.push(el("h2", {}, t("Aggiungi una guida")), form);
    main.replaceChildren(...parts);
  }

  // ---- Profiles ----
  const KINDS = [["admin", t("Amministratore")], ["standard", t("Normale")], ["kids", t("Bambini")]];
  async function renderProfiles(main) {
    const list = await api("GET", "/api/v1/profiles");
    const parts = [el("h2", {}, t("Profili")),
      el("p", { className: "note" }, t("Ogni profilo ha i suoi preferiti e il suo “Continua a guardare”. I profili Bambini non vedono mai contenuti per adulti " +
        "e non programmano registrazioni. Solo gli amministratori aprono le impostazioni: dai loro un PIN se in casa ci sono bambini."))];
    for (const p of list) {
      const box = el("section", { className: "box" }, el("h3", {}, p.name), el("div", { className: "facts" },
        el("span", {}, t("Tipo") + " ", el("b", {}, KINDS.find((k) => k[0] === p.kind)[1])),
        el("span", {}, "PIN ", el("b", {}, p.has_pin ? t("sì") : t("no")))));
      const edit = button(t("Modifica"), () => { edit.disabled = true; box.append(profileForm(p)); });
      const del = button(t("Elimina"), async () => {
        if (!confirm(t("Eliminare il profilo “{name}” con i suoi preferiti e i suoi avanzamenti?", { name: p.name }))) return;
        try { await api("DELETE", "/api/v1/profiles/" + p.id); flash(t("Profilo eliminato")); } catch (e) { flash(e.message); }
        render();
      }, "danger");
      box.append(el("div", { className: "buttons" }, edit, del));
      parts.push(box);
    }
    parts.push(el("h2", {}, t("Nuovo profilo")), profileForm());
    main.replaceChildren(...parts);
  }

  function profileForm(existing) {
    const name = input({ value: existing ? existing.name : "", placeholder: t("es. Giulia") });
    const kind = el("select", { className: "focusable" }, ...KINDS.map(([v, l]) => el("option", { value: v }, l)));
    kind.value = existing ? existing.kind : "standard";
    const pin = input({ type: "password", inputMode: "numeric", autocomplete: "new-password", maxLength: 8,
      placeholder: existing && existing.has_pin ? t("lascia vuoto per non cambiarlo") : t("facoltativo, 4-8 cifre") });
    const removePin = input({ type: "checkbox" });
    const save = el("button", { className: "small primary focusable full", type: "submit" }, existing ? t("Salva") : t("Crea il profilo"));
    const form = el("form", { className: "form" }, ...field(t("Nome"), name), ...field(t("Tipo"), kind), ...field("PIN", pin),
      existing && existing.has_pin ? el("label", {}, "") : null, existing && existing.has_pin ? el("label", { className: "check" }, removePin, t("Togli il PIN")) : null, save);
    form.onsubmit = async (e) => {
      e.preventDefault();
      const body = { name: name.value, kind: kind.value };
      if (pin.value) body.pin = pin.value;
      else if (removePin.checked) body.pin = "";
      try {
        if (existing) await api("PATCH", "/api/v1/profiles/" + existing.id, body);
        else await api("POST", "/api/v1/profiles", body);
        flash(existing ? t("Profilo aggiornato") : t("Profilo creato"));
        render();
      } catch (err) { flash(err.message); }
    };
    return form;
  }

  // ---- Devices ----
  async function renderDevices(main) {
    const [devices, profiles] = await Promise.all([api("GET", "/api/v1/devices"), api("GET", "/api/v1/profiles")]);
    const profileName = fromEntries(profiles.map((p) => [p.id, p.name]));
    const lockSelect = (value) => {
      const s = el("select", { className: "focusable" }, el("option", { value: "" }, t("Tutti i profili (si sceglie all'avvio)")),
        ...profiles.map((p) => el("option", { value: p.id }, t("Solo {name}", { name: p.name }))));
      s.value = value == null ? "" : value;
      return s;
    };
    const code = input({ placeholder: t("es. K7Q-29M"), autocomplete: "off", spellcheck: false, maxLength: 7 });
    const name = input({ placeholder: t("es. TV salotto") });
    const lock = lockSelect(null);
    const pairButton = el("button", { className: "small primary focusable full", type: "submit" }, t("Abbina"));
    const pairForm = el("form", { className: "form" }, ...field(t("Codice mostrato"), code), ...field(t("Nome del dispositivo"), name), ...field(t("Profili"), lock), pairButton);
    pairForm.onsubmit = async (e) => {
      e.preventDefault();
      try {
        await api("POST", "/api/v1/pair/approve", { code: code.value, name: name.value, profile_lock: lock.value ? Number(lock.value) : null });
        flash(t("Dispositivo abbinato: entro pochi secondi si apre da solo"));
        render();
      } catch (err) { flash(err.message); }
    };
    const rows = devices.map((d) => {
      const sel = lockSelect(d.profile_lock);
      sel.onchange = async () => {
        try { await api("PATCH", "/api/v1/devices/" + d.id, { name: d.name, profile_lock: sel.value ? Number(sel.value) : null }); flash(t("Salvato")); }
        catch (e) { flash(e.message); }
      };
      return el("tr", {}, el("td", { className: "name" }, d.name), el("td", {}, d.last_seen ? when(d.last_seen) : t("mai")), el("td", {}, sel),
        el("td", {}, button(t("Revoca"), async () => {
          if (!confirm(t("Revocare “{name}”? Dovrà essere abbinato di nuovo.", { name: d.name }))) return;
          try { await api("DELETE", "/api/v1/devices/" + d.id); flash(t("Dispositivo revocato")); } catch (e) { flash(e.message); }
          render();
        }, "danger")));
    });
    main.replaceChildren(
      el("h2", {}, t("Abbina un dispositivo")),
      el("p", { className: "note" }, t("Apri VodMax sul nuovo dispositivo (TV, telefono, PC): mostra un codice. Inseriscilo qui. " +
        "Con “Solo …” il dispositivo usa sempre quel profilo, per esempio il tablet dei bambini.")),
      pairForm,
      el("h2", {}, t("Dispositivi abbinati")),
      devices.length ? el("table", { className: "cats" }, el("tr", {}, el("th", {}, t("Nome")), el("th", {}, t("Ultimo uso")), el("th", {}, t("Profili")), el("th", {}, "")), ...rows)
        : el("p", { className: "note" }, t("Nessun dispositivo abbinato: per ora si entra con il token di amministrazione.")),
      el("p", { className: "note" }, t("Profili: {names}", { names: Object.values(profileName).join(", ") })),
      ...await ssoSection(profiles));
  }

  // ---- Sign-in with an account of the house (OpenID Connect) ----
  async function ssoSection(profiles) {
    const o = await api("GET", "/api/v1/settings/sso");
    const issuer = input({ value: o.issuer, placeholder: "https://auth.example.org", spellcheck: false });
    const client = input({ value: o.client_id, placeholder: "vodmax", spellcheck: false, autocomplete: "off" });
    const secret = input({ type: "password", autocomplete: "new-password", spellcheck: false,
      placeholder: o.has_secret ? t("configurato: lascia vuoto per non cambiarlo") : t("dal provider di accesso") });
    const label = input({ value: o.label, placeholder: t("es. Authelia"), maxLength: 30 });
    const enabled = input({ type: "checkbox", checked: o.enabled });
    const rows = el("div", { className: "sso-accounts" });
    const addRow = (a = { account: "", profile_id: profiles[0] && profiles[0].id }) => {
      const account = input({ value: a.account, placeholder: t("e-mail o nome utente"), spellcheck: false, autocomplete: "off" });
      const profile = el("select", { className: "focusable" }, ...profiles.map((p) => el("option", { value: p.id }, p.name)));
      profile.value = a.profile_id;
      const row = el("div", { className: "sso-account" }, account, profile, button(t("Rimuovi"), () => row.remove(), "danger"));
      row.read = () => ({ account: account.value.trim(), profile_id: Number(profile.value) });
      rows.append(row);
    };
    for (const a of o.accounts) addRow(a);
    if (!o.accounts.length) addRow();
    const save = el("button", { className: "small primary focusable full", type: "submit" }, t("Salva"));
    const form = el("form", { className: "form" }, ...field(t("Indirizzo del provider (issuer)"), issuer), ...field("Client ID", client),
      ...field("Client secret", secret), ...field(t("Nome del pulsante"), label),
      el("label", {}, t("Account")), el("div", {}, rows, el("div", { className: "buttons" }, button(t("Aggiungi un account"), () => addRow()))),
      el("label", {}, ""), el("label", { className: "check" }, enabled, t("Mostra «Entra con…» sui dispositivi non abbinati")), save);
    form.onsubmit = async (e) => {
      e.preventDefault();
      const body = { enabled: enabled.checked, issuer: issuer.value, client_id: client.value, label: label.value,
        accounts: [...rows.children].map((r) => r.read()).filter((a) => a.account) };
      if (secret.value) body.client_secret = secret.value;
      try { await api("PUT", "/api/v1/settings/sso", body); flash(t("Accesso con account salvato")); render(); }
      catch (err) { flash(err.message); }
    };
    return [el("h2", {}, t("Accesso con account di casa")),
      el("p", { className: "note" }, t("Se in casa usi Authelia, Authentik, Keycloak o un altro provider OpenID Connect, chi apre VodMax da un browser " +
        "può entrare con il suo account invece del codice. Ogni account entra sempre e solo nel profilo che gli colleghi qui, senza PIN; " +
        "il browser compare tra i dispositivi abbinati e si revoca come gli altri. Nel provider crea un client con questo indirizzo di ritorno:")),
      cmd(o.redirect_uri),
      el("p", { className: "note" }, t("Scope: openid, email, profile. Il secret resta cifrato sul server e non viene mai mostrato.")), form];
  }

  // ---- Language (this device only; also open to non-admin profiles) ----
  function languageSection() {
    return [el("h2", {}, t("Lingua")),
      el("p", { className: "note" }, t("La lingua vale per questo dispositivo; i messaggi del server seguono la stessa lingua.")),
      window.VodMaxI18n.picker()];
  }
  async function renderLanguage(main) {
    main.replaceChildren(...languageSection());
  }

  buildSections();
  phone.addEventListener("change", () => { if (!phone.matches && !state.view) open(store.get("settingsView", "subscriptions")); });
  // ---- Remote control: arrows move among everything that takes focus,
  // OK presses or ticks, Back goes back. ----
  const CONTROLS = "a[href], button, input, select, textarea";
  function moveFocus(dir) {
    const all = [...document.querySelectorAll(CONTROLS)].filter((e) => !e.disabled && e.offsetParent !== null && !e.closest("[hidden]"));
    const current = document.activeElement;
    if (!current || !all.includes(current)) {
      const start = document.querySelector(".item.active") || all[0];
      if (start) start.focus();
      return;
    }
    // From the list of sections into the open one: its first control.
    if (dir === "ArrowRight" && $("side").contains(current)) {
      const first = all.find((e) => $("content").contains(e));
      if (first) { first.focus(); first.scrollIntoView({ block: "nearest" }); }
      return;
    }
    const a = current.getBoundingClientRect();
    const ax = a.left + a.width / 2, ay = a.top + a.height / 2;
    let best = null, bestScore = Infinity;
    for (const e of all) {
      if (e === current) continue;
      const b = e.getBoundingClientRect();
      const dx = b.left + b.width / 2 - ax, dy = b.top + b.height / 2 - ay;
      const ok = { ArrowLeft: dx < -4, ArrowRight: dx > 4, ArrowUp: dy < -4, ArrowDown: dy > 4 }[dir];
      if (!ok) continue;
      const along = dir === "ArrowLeft" || dir === "ArrowRight" ? Math.abs(dx) : Math.abs(dy);
      const across = dir === "ArrowLeft" || dir === "ArrowRight" ? Math.abs(dy) : Math.abs(dx);
      const score = along + across * 3;
      if (score < bestScore) { bestScore = score; best = e; }
    }
    if (best) {
      best.focus();
      best.scrollIntoView({ block: "nearest" });
    }
  }
  const typing = (e) => e && (e.tagName === "TEXTAREA" || (e.tagName === "INPUT" && !/checkbox|radio|button|submit/.test(e.type)));
  document.addEventListener("keydown", (e) => {
    const key = window.VodMax.keyOf(e), active = document.activeElement;
    if (window.VodMax.sheetOpen()) return;
    if (key.startsWith("Arrow")) {
      // In a text field Left/Right move the cursor; Up/Down always move on
      // (and do not change a list or a number by mistake).
      if (typing(active) && (key === "ArrowLeft" || key === "ArrowRight")) return;
      e.preventDefault();
      moveFocus(key);
      return;
    }
    if (key === "OK" && active && active.tagName === "INPUT" && active.type === "checkbox") {
      e.preventDefault();
      active.click();
      return;
    }
    if (key === "Back" && e.key !== "Backspace" && typing(active)) { // out of the field first
      e.preventDefault();
      moveFocus("ArrowDown");
      return;
    }
    if ((key === "Back" || e.key === "Backspace") && !typing(active)) {
      e.preventDefault();
      back();
    }
  });
  window.VodMax.bottomNav("more");
  window.VodMax.withToken(async () => {
    const who = await window.VodMax.me().catch(() => null);
    if (!who) return;
    if (!who.admin) {
      $("content").replaceChildren(el("h2", {}, t("Impostazioni")),
        el("p", { className: "note" }, t("Le impostazioni sono riservate ai profili amministratore.")),
        button(t("Cambia profilo"), () => { store.set("profile", ""); window.VodMax.showProfiles(); }, "primary"),
        ...languageSection());
      $("sections").hidden = true;
      document.body.classList.remove("at-list");
      return;
    }
    render();
  });

  window.VodMaxNativeBack = () => {
    if (window.VodMax.sheetOpen()) window.VodMax.closeSheet(); else back();
    return true;
  };
})();
