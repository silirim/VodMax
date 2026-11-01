// Shared by the VodMax pages: settings in localStorage, device pairing and
// profile choice, the player id and API calls.
"use strict";

window.VodMax = (() => {
  const t = window.VodMaxI18n.t;
  const store = {
    get(key, fallback) {
      try { const v = localStorage.getItem("vodmax." + key); return v === null ? fallback : JSON.parse(v); }
      catch { return fallback; }
    },
    set(key, value) { try { localStorage.setItem("vodmax." + key, JSON.stringify(value)); } catch { /* private mode */ } },
  };

  const viewer = store.get("viewer", "") || (() => {
    const id = "v" + Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, "0")).join("");
    store.set("viewer", id);
    return id;
  })();

  // A link may carry the token once: "/#token=…". It is saved and removed
  // from the address bar.
  function tokenFromLink() {
    const t = new URLSearchParams(location.hash.slice(1)).get("token");
    if (t) {
      store.set("token", t);
      store.set("profile", "");
      history.replaceState(null, "", location.pathname + location.search);
    }
    return Boolean(t);
  }
  tokenFromLink();
  // Pasting a new link in the same tab only changes the hash: no reload.
  addEventListener("hashchange", () => { if (tokenFromLink()) location.reload(); });

  // Back from the sign-in with an account of the house: "/#sso=…" is a
  // one-time code for the tokens, "/#sso_error=…" the reason it failed.
  let ssoError = "";
  const ssoReady = (async () => {
    const h = new URLSearchParams(location.hash.slice(1));
    const code = h.get("sso");
    ssoError = h.get("sso_error") || "";
    if (!code && !ssoError) return;
    history.replaceState(null, "", location.pathname + location.search);
    if (!code) return;
    try {
      const res = await fetch("/api/v1/sso/claim", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ code }) });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || t("errore {status}", { status: res.status }));
      store.set("token", data.token);
      store.set("profile", data.profile);
    } catch (e) { ssoError = e.message; }
  })();

  function token() { return store.get("token", ""); }
  function profileToken() { return store.get("profile", ""); }
  const handled = (msg) => Object.assign(new Error(msg), { handled: true });

  // What is open, as classes on body for the style sheets (":has()" is too
  // new for webOS 22 and older WebViews).
  const syncOpen = () => {
    const shown = (id) => { const e = document.getElementById(id); return Boolean(e && !e.hidden); };
    const b = document.body;
    b.classList.toggle("with-player", shown("player"));
    b.classList.toggle("with-detail", shown("detail"));
    b.classList.toggle("with-panel", shown("programme") || Boolean(document.querySelector(".setup-panel, #my-profile, #update-banner, #picker")));
  };
  new MutationObserver(syncOpen).observe(document.documentElement, { subtree: true, childList: true, attributes: true, attributeFilter: ["hidden"] });

  // Android keyboards close without sending a form unless the field says
  // what Enter does: every text field gets one, set before the keyboard
  // opens.
  document.addEventListener("focusin", (e) => {
    const i = e.target;
    if (i.tagName !== "INPUT" || i.hasAttribute("enterkeyhint")) return;
    if (/^(text|search|password|number|email|url|tel)$/.test(i.type)) i.setAttribute("enterkeyhint", i.type === "search" ? "search" : "done");
  }, true);

  // A full-screen panel (pairing, profile choice, token): one at a time.
  function overlay(id, ...children) {
    for (const old of document.querySelectorAll(".setup-panel")) old.remove();
    const box = Object.assign(document.createElement("section"), { id, className: "panel setup-panel" });
    box.append(...children.filter((c) => c != null && c !== false));
    // The remote moves inside the panel (not to the menu behind it); Back
    // does what the panel says (box.back), e.g. from a PIN to the profiles.
    panelKeys(box, () => { if (box.back) box.back(); });
    document.body.append(box);
    return box;
  }
  const el = (tag, props = {}, ...children) => {
    const e = Object.assign(document.createElement(tag), props);
    e.append(...children.filter((c) => c !== null && c !== undefined));
    return e;
  };

  // Asks for the admin token (the server's master key, file admin.token).
  function askToken(message) {
    const input = el("input", { type: "password", autocomplete: "off", spellcheck: false });
    input.setAttribute("aria-label", t("Token di amministrazione"));
    const form = el("form", {}, input, el("button", { type: "submit" }, t("Continua")));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      store.set("token", input.value.trim());
      store.set("profile", "");
      location.reload();
    });
    overlay("token-setup", el("h1", {}, "VodMax"),
      el("p", {}, message || t("Token di amministrazione del server (file admin.token).")), form,
      el("button", { type: "button", className: "link", onclick: () => showPairing() }, t("← Abbina con un codice")));
    input.focus();
  }

  // Pairing: the device shows a code, an admin approves it elsewhere.
  let pairingTimer;
  async function showPairing(fresh) {
    clearTimeout(pairingTimer);
    // The code of this device is kept while it is valid: moving between
    // pages does not ask for new ones (the server allows a few per address).
    let req = fresh ? null : store.get("pairing", null);
    if (req && !(req.expires > Date.now() + 30000)) req = null;
    try {
      if (!req) {
        const res = await fetch("/api/v1/pair", { method: "POST" });
        req = await res.json();
        if (!res.ok) throw new Error(req.error || t("errore {status}", { status: res.status }));
        req.expires = Date.now() + req.expires_in * 1000;
        store.set("pairing", req);
      }
    } catch (e) {
      const admin = el("button", { type: "button", className: "link", onclick: () => askToken() }, t("Ho il token di amministrazione"));
      const again = el("button", { type: "button", onclick: () => showPairing(true) }, t("Riprova"));
      overlay("pairing", el("h1", {}, "VodMax"), el("p", {}, t("Impossibile avviare l'abbinamento: {error}", { error: e.message })), again, admin);
      again.focus();
      return;
    }
    const code = el("p", { className: "pair-code" }, req.code);
    const admin = el("button", { type: "button", className: "link", onclick: () => { clearTimeout(pairingTimer); askToken(); } }, t("Ho il token di amministrazione"));
    const sso = await fetch("/api/v1/sso").then((r) => r.json()).catch(() => ({}));
    const ssoButton = sso.enabled ? el("button", { type: "button", className: "sso", onclick: () => { location.href = "/auth/sso/start"; } },
      sso.label ? t("Entra con {name}", { name: sso.label }) : t("Entra con il tuo account")) : null;
    overlay("pairing", el("h1", {}, t("Abbina questo dispositivo")),
      ssoError ? el("p", { className: "hint bad" }, ssoError) : null,
      el("p", {}, t("Su un dispositivo già abbinato, con un profilo amministratore, apri Impostazioni → Dispositivi → Abbina, e inserisci il codice:")),
      code, el("p", { className: "hint" }, t("Il codice vale 10 minuti, poi ne compare uno nuovo.")),
      ssoButton ? el("p", { className: "hint" }, t("Oppure entra con l'account di casa:")) : null, ssoButton, admin);
    (ssoButton || admin).focus();
    const poll = async () => {
      try {
        const res = await fetch("/api/v1/pair/" + req.poll);
        if (res.status === 200) {
          store.set("token", (await res.json()).token);
          store.set("profile", "");
          store.set("pairing", null);
          location.reload();
          return;
        }
        if (res.status === 404) { store.set("pairing", null); showPairing(true); return; }
      } catch { /* network hiccup: keep polling */ }
      pairingTimer = setTimeout(poll, 3000);
    };
    pairingTimer = setTimeout(poll, 3000);
    // Back in the foreground (the code was sent from another app): check now.
    if (!pairingVisible) {
      pairingVisible = true;
      document.addEventListener("visibilitychange", () => {
        if (document.visibilityState === "visible" && document.getElementById("pairing") && pairingPoll) {
          clearTimeout(pairingTimer);
          pairingPoll();
        }
      });
    }
    pairingPoll = poll;
  }
  let pairingVisible = false, pairingPoll = null;

  const KIND = { admin: "amministratore", standard: "", kids: "bambini" };

  // "Who is watching?"
  async function showProfiles() {
    let list;
    try { list = await api("GET", "/api/v1/profiles"); } catch { return; }
    const buttons = list.map((p) => el("button", { type: "button", className: "profile-choice focusable", onclick: () => choose(p) },
      avatar(p, "big"), el("span", { className: "profile-name" }, p.name), el("span", { className: "profile-kind" }, KIND[p.kind] ? t(KIND[p.kind]) : "",
        p.has_pin && window.VodMaxIcons ? VodMaxIcons.icon("lock", t("con PIN")) : "")));
    const box = overlay("profiles", el("h1", {}, t("Chi guarda?")), el("div", { className: "profile-grid" }, ...buttons),
      window.VodMaxI18n.picker("on-overlay"));
    if (buttons[0]) buttons[0].focus();
    async function choose(p) {
      if (!p.has_pin) return select(p, "");
      const pin = el("input", { type: "password", inputMode: "numeric", autocomplete: "off", maxLength: 8 });
      pin.setAttribute("aria-label", t("PIN di {name}", { name: p.name }));
      const msg = el("p", { className: "hint" }, "");
      const form = el("form", {}, pin, el("button", { type: "submit" }, t("Entra")));
      form.addEventListener("submit", (e) => { e.preventDefault(); select(p, pin.value, msg); });
      box.replaceChildren(el("h1", {}, p.name), el("p", {}, t("PIN del profilo")), form, msg,
        el("button", { type: "button", className: "link", onclick: () => showProfiles() }, t("← Altri profili")));
      box.back = () => showProfiles();
      pin.setAttribute("enterkeyhint", "go");
      pin.focus();
    }
    async function select(p, pin, msg) {
      try {
        const r = await api("POST", "/api/v1/profiles/" + p.id + "/select", { pin });
        store.set("profile", r.profile_token);
        location.reload();
      } catch (e) {
        if (msg) msg.textContent = e.message; else alert(e.message);
      }
    }
  }

  // Shows the profile in the page header and hides what it cannot use.
  let meCache;
  async function me() {
    if (!meCache) meCache = api("GET", "/api/v1/me");
    return meCache;
  }
  async function decorate() {
    let who;
    try { who = await me(); } catch { return; }
    for (const a of document.querySelectorAll('a[href="/settings.html"]')) a.hidden = !who.admin;
    const nav = document.getElementById("tabs");
    if (nav && who.profile && !document.getElementById("profile-chip")) {
      const chip = el("button", { id: "profile-chip", className: "tab focusable", type: "button",
        title: t("Il mio profilo ({name})", { name: who.profile.name }), onclick: () => myProfile() });
      chip.append(avatar(who.profile, "small"), el("span", { className: "tab-text" }, who.profile.name));
      // Next to the search box, outside the menu bar (which scrolls sideways).
      const search = document.getElementById("search");
      const upd = document.getElementById("update-tab");
      if (upd) upd.after(chip);
      else if (search) { chip.style.marginLeft = "auto"; search.before(chip); } else nav.append(chip);
    }
    if (who.pin_required) setAdminPIN(who.profile);
  }

  // ---- Avatars and "my profile" ----
  const AVATAR_COLORS = ["#00d6ca", "#3d8bff", "#8b5cf6", "#ec4899", "#ff4d4f", "#ff8a1f", "#f5c518", "#22c55e"];
  // A round badge: the chosen icon, or the initial, on the chosen colour.
  function avatar(p, size) {
    const a = el("span", { className: "avatar" + (size ? " " + size : "") });
    a.style.background = p.color || AVATAR_COLORS[(p.id || 0) % AVATAR_COLORS.length];
    if (p.avatar && window.VodMaxIcons) a.append(VodMaxIcons.icon(p.avatar));
    else a.append(el("span", { className: "avatar-initial" }, (p.name || "?").trim().charAt(0).toUpperCase()));
    return a;
  }

  // Arrows and Back inside a panel, the same on every page.
  function panelKeys(box, onBack) {
    const onKey = (e) => {
      if (!document.body.contains(box)) { document.removeEventListener("keydown", onKey, true); return; }
      const key = keyOf(e);
      if (key === "Back" && onBack) {
        if (e.target.tagName === "INPUT" && e.key === "Backspace") return;
        e.preventDefault(); e.stopPropagation(); onBack(); return;
      }
      if (!key.startsWith("Arrow")) return;
      const typing = e.target.tagName === "INPUT";
      if (typing && (key === "ArrowLeft" || key === "ArrowRight")) return;
      e.preventDefault(); e.stopPropagation();
      const all = [...box.querySelectorAll("button, input, a, summary")].filter((x) => x.offsetParent !== null && !x.disabled);
      const cur = document.activeElement;
      if (!all.includes(cur)) { if (all[0]) all[0].focus(); return; }
      const a = cur.getBoundingClientRect(), ax = a.left + a.width / 2, ay = a.top + a.height / 2;
      let best = null, score = Infinity;
      for (const x of all) {
        if (x === cur) continue;
        const b = x.getBoundingClientRect(), dx = b.left + b.width / 2 - ax, dy = b.top + b.height / 2 - ay;
        const ok = { ArrowLeft: dx < -4, ArrowRight: dx > 4, ArrowUp: dy < -4, ArrowDown: dy > 4 }[key];
        if (!ok) continue;
        const horizontal = key === "ArrowLeft" || key === "ArrowRight";
        const along = horizontal ? Math.abs(dx) : Math.abs(dy);
        const across = horizontal ? Math.max(0, Math.max(a.top, b.top) - Math.min(a.bottom, b.bottom))
          : Math.max(0, Math.max(a.left, b.left) - Math.min(a.right, b.right));
        const sc = along + across * 3;
        if (sc < score) { score = sc; best = x; }
      }
      if (best) { best.focus(); best.scrollIntoView({ block: "nearest" }); }
    };
    document.addEventListener("keydown", onKey, true);
  }

  // After a PIN change the earlier choice of the profile no longer works:
  // choose it again with the new PIN and start over.
  async function reselect(p, pin) {
    const r = await api("POST", "/api/v1/profiles/" + p.id + "/select", { pin });
    store.set("profile", r.profile_token);
    location.reload();
  }

  // A PIN form: the current one when there is one, the new one twice.
  function pinForm(hasPIN, done) {
    const field = (label) => { const i = el("input", { type: "password", inputMode: "numeric", autocomplete: "off", maxLength: 8, className: "focusable" }); i.setAttribute("aria-label", label); return [el("label", {}, label, i), i]; };
    const [l0, current] = field(t("PIN attuale"));
    const [l1, pin] = field(t("Nuovo PIN (4–8 cifre)"));
    const [l2, again] = field(t("Ripeti il nuovo PIN"));
    const msg = el("p", { className: "hint" });
    const form = el("form", { className: "pin-form" }, hasPIN ? l0 : null, l1, l2, el("button", { type: "submit", className: "focusable" }, t("Salva il PIN")), msg);
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      if (!/^[0-9]{4,8}$/.test(pin.value)) { msg.textContent = t("Il PIN deve avere da 4 a 8 cifre."); return; }
      if (pin.value !== again.value) { msg.textContent = t("I due PIN non coincidono."); return; }
      try {
        await api("PUT", "/api/v1/profile", { pin: pin.value, current_pin: current.value });
        await done(pin.value);
      } catch (err) { msg.textContent = err.message; }
    });
    return form;
  }

  // An admin profile without PIN while other profiles exist: anyone could
  // enter it, so nothing else until a PIN is set (or another profile).
  function setAdminPIN(p) {
    if (document.getElementById("admin-pin")) return;
    const box = overlay("admin-pin", el("h1", {}, t("Proteggi il profilo amministratore")),
      el("p", {}, t("Ci sono altri profili: senza un PIN chiunque potrebbe entrare in “{name}” e cambiare le impostazioni. Scegli un PIN: lo chiederà una volta su ogni dispositivo.", { name: p.name })),
      pinForm(false, (pin) => reselect(p, pin)),
      el("button", { type: "button", className: "link focusable", onclick: () => { store.set("profile", ""); showProfiles(); } }, t("← Altri profili")));
    box.classList.add("modal"); // keys: overlay() already moves inside it
    const first = box.querySelector("input");
    if (first) first.focus();
  }

  async function myProfile() {
    let who, choice;
    try { [who, choice] = await Promise.all([me(), api("GET", "/api/v1/avatars")]); } catch { return; }
    const p = who.profile;
    if (!p) { showProfiles(); return; }
    const scrim = el("div", { className: "scrim me-scrim" });
    const close = () => { box.remove(); scrim.remove(); const chip = document.getElementById("profile-chip"); if (chip) chip.focus(); };
    scrim.onclick = close;
    const msg = el("p", { className: "hint" });
    const preview = el("div", { className: "me-preview" });
    const colorOf = () => p.color || AVATAR_COLORS[p.id % AVATAR_COLORS.length];
    const draw = () => {
      preview.replaceChildren(avatar(p, "big"), el("div", {}, el("strong", {}, p.name), el("span", { className: "hint" }, KIND[p.kind] ? t(KIND[p.kind]) : "")));
      for (const b of icons.children) { b.classList.toggle("active", b.dataset.avatar === (p.avatar || "")); b.firstChild.style.background = colorOf(); }
      for (const b of colors.children) b.classList.toggle("active", b.dataset.color === colorOf());
      const chip = document.getElementById("profile-chip");
      if (chip) chip.firstChild.replaceWith(avatar(p, "small"));
    };
    const save = async (change) => {
      try { await api("PUT", "/api/v1/profile", change); Object.assign(p, change); msg.textContent = ""; draw(); }
      catch (e) { msg.textContent = e.message; }
    };
    const icons = el("div", { className: "avatar-grid" }, ...["", ...choice.avatars].map((name) => {
      const b = el("button", { type: "button", className: "avatar-pick focusable", title: name ? name : t("Iniziale"), onclick: () => save({ avatar: name }) },
        avatar({ ...p, avatar: name }, ""));
      b.dataset.avatar = name;
      return b;
    }));
    const colors = el("div", { className: "color-row" }, ...choice.colors.map((c) => {
      const b = el("button", { type: "button", className: "color-pick focusable", title: c, onclick: () => save({ color: c }) });
      b.style.background = c;
      b.dataset.color = c;
      return b;
    }));
    const box = el("section", { id: "my-profile", className: "panel", role: "dialog" },
      el("div", { className: "me-head" }, preview,
        el("button", { type: "button", className: "me-close focusable", "aria-label": t("Chiudi"), onclick: close }, "✕")),
      el("h2", {}, t("Avatar")), icons, el("h2", {}, t("Colore")), colors, msg,
      p.kind === "kids" ? null : el("details", { className: "me-pin" }, el("summary", { className: "focusable" }, p.has_pin ? t("Cambia il PIN") : t("Imposta un PIN")),
        pinForm(p.has_pin, (pin) => reselect(p, pin))),
      el("div", { className: "me-actions" },
        el("button", { type: "button", className: "focusable", onclick: () => { close(); store.set("profile", ""); showProfiles(); } }, t("Cambia profilo"))));
    document.body.append(scrim, box);
    draw();
    panelKeys(box, close);
    (icons.querySelector(".active") || icons.firstChild).focus();
  }

  // ---- Phone: navigation bar at the bottom, like the streaming apps;
  // "Altro" opens a sheet with the rest. On the menu page the entries
  // press its tabs; elsewhere they are links. ----
  const phoneNav = window.matchMedia("(max-width: 800px)");
  let navBar = null;
  function bottomNav(active) {
    if (!navBar) {
      const ic = (n) => (window.VodMaxIcons ? VodMaxIcons.icon(n) : "");
      const onMenu = location.pathname === "/" || location.pathname === "/index.html";
      const go = (view) => () => {
        const tab = onMenu && document.querySelector('.tab[data-view="' + view + '"]');
        if (tab) { closeSheet(); tab.click(); window.scrollTo(0, 0); } else location.href = "/?view=" + view;
      };
      const item = (key, icon, text, action) => {
        const b = el("button", { type: "button", className: "bnav-item focusable", onclick: action }, ic(icon), el("span", {}, text));
        b.dataset.key = key;
        return b;
      };
      navBar = el("nav", { className: "bnav", "aria-label": t("Sezioni") },
        item("home", "house", t("Home"), go("home")), item("movie", "film", t("Film"), go("movie")),
        item("series", "clapperboard", t("Serie"), go("series")), item("tv", "tv", t("TV"), go("tv")),
        item("more", "list", t("Altro"), () => openSheet()));
      document.body.append(navBar);
      document.body.classList.add("has-bnav");

      const entry = (icon, text, action, extra) => {
        const b = el("button", { type: "button", className: "sheet-item focusable", onclick: action }, el("span", { className: "item-icon" }, ic(icon)), el("span", {}, text));
        if (extra) b.classList.add(extra);
        return b;
      };
      const sheet = el("div", { id: "more-sheet", className: "sheet", hidden: true },
        el("div", { className: "sheet-grip" }),
        entry("bookmark", t("La mia lista"), go("mylist")),
        entry("video", t("Registrazioni"), go("recordings")),
        entry("download", t("Download"), go("downloads"), "downloads-only"),
        entry("calendar-days", t("Guida TV"), () => { location.href = "/guide.html"; }),
        entry("settings", t("Impostazioni"), () => { location.href = "/settings.html"; }, "admin-only"),
        entry("user", t("Il mio profilo"), () => { closeSheet(); myProfile(); }),
        native ? entry("rotate-cw", t("Aggiorna l'app"), () => { closeSheet(); if (updateInfo) updateBanner(updateInfo); }, "update-only") : "",
        native ? entry("tv", t("Cambia server"), () => native.changeServer()) : "");
      for (const a of sheet.querySelectorAll(".update-only")) a.hidden = !updateInfo;
      const scrim = el("div", { id: "more-scrim", className: "scrim", hidden: true, onclick: () => closeSheet() });
      document.body.append(scrim, sheet);
      me().then((who) => {
        for (const a of sheet.querySelectorAll(".admin-only")) a.hidden = !who.admin;
        // Downloads: not for kids profiles, not on a device running alone.
        const kids = who.profile && who.profile.kind === "kids";
        for (const a of sheet.querySelectorAll(".downloads-only")) a.hidden = true;
        if (!kids) api("GET", "/api/v1/downloads").then(() => { for (const a of sheet.querySelectorAll(".downloads-only")) a.hidden = false; }, () => {});
      }).catch(() => {});
    }
    if (active) for (const b of navBar.children) b.classList.toggle("active", b.dataset.key === active);
  }
  function openSheet() {
    document.getElementById("more-sheet").hidden = document.getElementById("more-scrim").hidden = false;
    const first = document.querySelector("#more-sheet .sheet-item:not([hidden])");
    if (first) first.focus();
  }
  function closeSheet() {
    const s = document.getElementById("more-sheet");
    if (s) s.hidden = document.getElementById("more-scrim").hidden = true;
  }
  document.addEventListener("keydown", (e) => {
    const s = document.getElementById("more-sheet");
    if (e.key === "Escape" && s && !s.hidden) { closeSheet(); e.stopImmediatePropagation(); e.preventDefault(); }
  }, true);
  const sheetOpen = () => { const s = document.getElementById("more-sheet"); return Boolean(s && !s.hidden); };

  // Runs start() once the device is paired, otherwise pairs it.
  async function withToken(start) {
    await ssoReady;
    if (!token()) { showPairing(); return; }
    start();
    decorate();
    watchStatus();
    offerAppUpdate();
  }

  // ---- Updates of the Android app ----
  // The server knows the newest release (it checks GitHub once a day); the
  // app downloads its APK, checks the checksum and opens the installer.
  // A beta ("0.6.0-beta.2") comes before its release, in the order of its
  // number.
  function newerVersion(a, b) {
    const parse = (v) => {
      const m = String(v || "").match(/^v?(\d+)\.(\d+)\.(\d+)(?:-(.+))?$/);
      if (!m) return [];
      const pre = m[4] && !/^\d+-g[0-9a-f]+/.test(m[4]) ? -1000 + Math.min(Number((m[4].match(/\d+/) || [0])[0]), 999) : 0;
      return [Number(m[1]), Number(m[2]), Number(m[3]), pre];
    };
    const x = parse(a), y = parse(b);
    if (x.length !== 4 || y.length !== 4) return false;
    for (let i = 0; i < 4; i++) if (x[i] !== y[i]) return x[i] > y[i];
    return false;
  }
  const canUpdateApp = () => Boolean(native && typeof native.update === "function");
  function appUpdate(info) {
    return canUpdateApp() && info && info.apk && newerVersion(info.latest, native.version()) ? info.apk : null;
  }
  // Starts the update; status receives the progress texts.
  function updateApp(info, status) {
    const apk = appUpdate(info);
    if (!apk) return;
    status(t("Scaricamento dell'aggiornamento…"));
    window.VodMaxUpdateProgress = (p) => status(t("Scaricamento dell'aggiornamento… {pct}%", { pct: p }));
    window.VodMaxUpdateNote = (text) => status(text, false);
    window.VodMaxUpdateDone = (err) => status(err ? t("Aggiornamento non riuscito: {error}", { error: err })
      : t("Conferma l'installazione nella finestra di Android."), Boolean(err));
    native.update(JSON.stringify({ url: apk.url, sha256: apk.sha256, version: info.latest }));
  }
  // Any profile but a kids one may update the app on its own device (the
  // server stays the admin's). The menu keeps an "Update the app" entry
  // while a newer version exists; the banner comes by itself unless put
  // off ("Later": three days) or skipped (that version, for good).
  const SNOOZE = 3 * 24 * 3600 * 1000;
  let updateInfo = null;
  async function offerAppUpdate() {
    if (!canUpdateApp()) return;
    let info, who;
    try { [info, who] = await Promise.all([api("GET", "/api/v1/update"), me()]); } catch { return; }
    if (!appUpdate(info) || (who.profile && who.profile.kind === "kids")) return;
    updateInfo = info;
    showUpdateEntries();
    try { if (sessionStorage.getItem("vodmax.updateOffered")) return; } catch { /* private mode */ }
    if (store.get("updateSkipped", "") === info.latest) return;
    const snooze = store.get("updateSnoozed", null);
    if (snooze && snooze.version === info.latest && Date.now() < snooze.until) return;
    try { sessionStorage.setItem("vodmax.updateOffered", "1"); } catch { /* private mode */ }
    updateBanner(info);
  }
  function showUpdateEntries() {
    if (!updateInfo) return;
    for (const a of document.querySelectorAll(".update-only")) a.hidden = false;
    // Next to the search box, outside the menu bar (which scrolls sideways).
    const search = document.getElementById("search");
    if (search && !document.getElementById("update-tab")) {
      const tab = el("button", { id: "update-tab", className: "tab focusable", type: "button", title: t("Aggiorna l'app"), "aria-label": t("Aggiorna l'app"), onclick: () => updateBanner(updateInfo) });
      tab.append(window.VodMaxIcons ? VodMaxIcons.icon("rotate-cw") : "", el("span", { className: "dot" }));
      const chip = document.getElementById("profile-chip");
      if (chip && chip.nextElementSibling === search) { chip.style.marginLeft = ""; chip.before(tab); } else search.before(tab);
    }
  }
  // What a release brings, from its notes on GitHub: the English part, or
  // the Italian summary after the "---" line. A little Markdown, as text.
  function releaseNotes(text) {
    const [en, it] = String(text || "").split(/\n-{3,}\n/);
    let part = window.VodMaxI18n.lang === "it" && it ? it : en;
    part = part.split(/\n## (Updating|Aggiornare)/)[0];
    const plain = (x) => {
      const s = x.replace(/\[([^\]]+)\]\([^)]+\)/g, "$1").replace(/\*\*|`/g, "").replace(/^In breve:\s*/, "").trim();
      return s.charAt(0).toUpperCase() + s.slice(1);
    };
    const out = [];
    let list = null;
    for (const line of part.split("\n")) {
      const l = line.trim();
      if (!l || /^## (What's new|Novità)/.test(l)) continue;
      if (l.startsWith("- ")) { if (!list) out.push(list = el("ul")); list.append(el("li", {}, plain(l.slice(2)))); continue; }
      list = null;
      out.push(l.startsWith("## ") ? el("h3", {}, plain(l.slice(3))) : el("p", {}, plain(l)));
    }
    return out.length ? out : [el("p", {}, t("Nessuna nota per questa versione."))];
  }

  function updateBanner(info) {
    const open = document.getElementById("update-banner");
    if (open) { const b = open.querySelector("button"); if (b) b.focus(); return; }
    const msg = el("p", {}, t("È disponibile VodMax {version} per questa app.", { version: info.latest.replace(/^v/, "") }));
    const close = () => { box.remove(); document.removeEventListener("keydown", onKey, true); };
    const go = el("button", { type: "button", className: "focusable primary" }, t("Aggiorna"));
    const later = el("button", { type: "button", className: "focusable" }, t("Più tardi"));
    const skip = el("button", { type: "button", className: "focusable" }, t("Salta questa versione"));
    const news = el("button", { type: "button", className: "focusable" }, t("Novità"));
    const notes = el("div", { className: "update-notes", hidden: true }, ...releaseNotes(info.notes),
      info.page ? el("a", { href: info.page, target: "_blank", rel: "noopener", className: "focusable" }, t("Tutte le novità su GitHub")) : null);
    news.onclick = () => { notes.hidden = !notes.hidden; news.classList.toggle("active", !notes.hidden); };
    const buttons = el("div", { className: "buttons" }, go, news, later, skip);
    const box = el("div", { id: "update-banner", role: "dialog" }, msg, notes, buttons);
    go.onclick = () => {
      buttons.hidden = true;
      updateApp(info, (text, failed) => {
        msg.textContent = text;
        if (failed) { buttons.hidden = false; go.focus(); }
      });
    };
    later.onclick = () => { store.set("updateSnoozed", { version: info.latest, until: Date.now() + SNOOZE }); close(); };
    skip.onclick = () => { store.set("updateSkipped", info.latest); close(); };
    const onKey = (e) => { if (keyOf(e) === "Back") { e.preventDefault(); e.stopPropagation(); close(); } };
    document.addEventListener("keydown", onKey, true);
    document.body.append(box);
    go.focus();
  }

  async function api(method, path, body) {
    const headers = { "Content-Type": "application/json" };
    if (token()) headers.Authorization = "Bearer " + token();
    if (profileToken()) headers["X-Profile"] = profileToken();
    headers["X-VodMax-Lang"] = window.VodMaxI18n.lang; // server messages in the same language
    const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    const data = res.status === 204 ? null : await res.json().catch(() => null);
    if (res.status === 401) {
      showPairing();
      throw handled(t("dispositivo non abbinato"));
    }
    if (res.status === 403 && data && data.code === "profile_required") {
      store.set("profile", "");
      showProfiles();
      throw handled(t("scegli un profilo"));
    }
    if (!res.ok) throw Object.assign(new Error(data && data.error ? data.error : t("errore {status}", { status: res.status })), { status: res.status });
    return data;
  }

  // Remote control keys of browsers, LG webOS and Android TV.
  function keyOf(e) {
    const byCode = { 33: "ChannelUp", 34: "ChannelDown", 403: "Red", 404: "Green", 405: "Yellow", 406: "Blue",
      457: "Info", 458: "Guide", 461: "Back", 427: "ChannelUp", 428: "ChannelDown", 415: "Play", 19: "Pause", 413: "Stop" };
    if (byCode[e.keyCode]) return byCode[e.keyCode];
    const byKey = { PageUp: "ChannelUp", PageDown: "ChannelDown", ColorF0Red: "Red", ColorF1Green: "Green",
      ColorF2Yellow: "Yellow", ColorF3Blue: "Blue", Escape: "Back", BrowserBack: "Back", GoBack: "Back", Enter: "OK",
      MediaPlayPause: "PlayPause", MediaPlay: "Play", MediaPause: "Pause", MediaStop: "Stop" };
    return byKey[e.key] || e.key;
  }

  const pad = (n) => String(n).padStart(2, "0");
  function clock(seconds) {
    seconds = Math.max(0, Math.floor(seconds || 0));
    const h = Math.floor(seconds / 3600), m = Math.floor((seconds % 3600) / 60), s = seconds % 60;
    return (h ? h + ":" + pad(m) : m) + ":" + pad(s);
  }
  function hhmm(unix) { const d = new Date(unix * 1000); return pad(d.getHours()) + ":" + pad(d.getMinutes()); }

  // ---- MPEG-TS playback (live channels, recordings in progress) ----
  //
  // mpegts.js feeds the browser's video element through Media Source
  // Extensions. Live streams can leave small gaps in the buffer (audio and
  // video starting at different times, a reconnect) where the element
  // stalls without any error; aggressive latency chasing makes it worse by
  // seeking into such gaps. So: a larger start-up buffer, no chasing, and
  // a watchdog that steps over gaps and resumes a stall the user did not
  // ask for.
  // onEnd: a live stream ended (taken by another device, or stopped by the
  // provider); onSilent: no picture after a while (nothing decodable).
  function tsPlayer(video, url, { live, onError, onStart, onEnd, onSilent } = {}) {
    const player = mpegts.createPlayer({ type: "mpegts", isLive: live, url }, {
      enableWorker: false,
      enableStashBuffer: true,
      stashInitialSize: 384 * 1024,
      liveBufferLatencyChasing: false,
      autoCleanupSourceBuffer: true,
      lazyLoad: false,
    });
    let userPaused = false, lastTime = -1, stuck = 0, destroyed = false;
    player.attachMediaElement(video);
    player.on(mpegts.Events.ERROR, (type, detail, info) => { if (!destroyed && onError) onError(type, detail, info); });
    player.on(mpegts.Events.MEDIA_INFO, () => { if (!destroyed && onStart) onStart(); });
    player.on(mpegts.Events.LOADING_COMPLETE, () => { if (!destroyed && live && onEnd) onEnd(); });
    const silent = setTimeout(() => { if (!destroyed && !userPaused && video.readyState < 2 && onSilent) onSilent(); }, 15000);
    player.load();
    const tryPlay = () => { const p = video.play(); if (p && p.catch) p.catch(() => {}); };
    tryPlay();

    const watchdog = setInterval(() => {
      if (destroyed || userPaused) return;
      const b = video.buffered;
      if (!b.length) return;
      const t = video.currentTime;
      // Before the first buffered range (start-up): jump in.
      if (t < b.start(0) - 0.05) { video.currentTime = b.start(0) + 0.05; return; }
      if (video.paused) { tryPlay(); return; } // paused by the browser, not by the user
      if (Math.abs(t - lastTime) < 0.01) stuck++; else stuck = 0;
      lastTime = t;
      if (stuck < 2) return;
      // Stalled: move to the next buffered data, or nudge forward.
      for (let i = 0; i < b.length; i++) {
        if (b.start(i) > t + 0.01) { video.currentTime = b.start(i) + 0.05; stuck = 0; return; }
      }
      if (b.end(b.length - 1) - t > 0.5) { video.currentTime = t + 0.3; stuck = 0; }
    }, 1000);

    return {
      pause() { userPaused = true; video.pause(); },
      play() { userPaused = false; stuck = 0; tryPlay(); },
      toggle() { if (userPaused || video.paused) this.play(); else this.pause(); },
      // Browsers block autoplay with sound until the user interacts; a key
      // or click resumes such a pause, never one the user asked for.
      resumeIfBlocked() { if (!userPaused && video.paused) tryPlay(); },
      get paused() { return userPaused || video.paused; },
      get userPaused() { return userPaused; }, // paused by the viewer, not loading
      // Seconds behind the newest buffered data (live time-shift).
      behind() { const b = video.buffered; return b.length ? Math.max(0, b.end(b.length - 1) - video.currentTime) : 0; },
      goLive() { const b = video.buffered; if (b.length) video.currentTime = Math.max(b.start(b.length - 1), b.end(b.length - 1) - 1.5); this.play(); },
      seek(delta) {
        const b = video.buffered;
        if (!b.length) return;
        video.currentTime = Math.min(Math.max(video.currentTime + delta, b.start(0) + 0.1), b.end(b.length - 1) - 0.5);
      },
      destroy() {
        destroyed = true;
        clearInterval(watchdog);
        clearTimeout(silent);
        try { player.pause(); player.unload(); player.detachMediaElement(); player.destroy(); } catch { /* already gone */ }
      },
    };
  }

  // ---- HLS playback (live TV with pause and rewind) ----
  //
  // The server keeps the channel as HLS segments; hls.js (or the
  // device's own HLS, e.g. webOS) plays it with the same controls as
  // tsPlayer, plus moving to a wall-clock time inside the buffer.
  function hlsPlayer(video, url, { onError, onStart } = {}) {
    let userPaused = false, destroyed = false, recoveries = 0;
    let hls = null;
    const tryPlay = () => { const p = video.play(); if (p && p.catch) p.catch(() => {}); };
    if (window.Hls && Hls.isSupported()) {
      hls = new Hls({
        enableWorker: true,
        liveSyncDurationCount: 3,          // about 6 s behind the newest segment
        liveMaxLatencyDurationCount: Infinity, // paused or rewound: never pulled back to live
        backBufferLength: 120,             // the browser keeps 2 minutes; older parts are reloaded from the server
        maxBufferLength: 30,
      });
      hls.on(Hls.Events.MANIFEST_PARSED, () => { if (!destroyed) { tryPlay(); if (onStart) onStart(); } });
      hls.on(Hls.Events.ERROR, (_, data) => {
        if (destroyed || !data.fatal) return;
        const status = data.response && data.response.code;
        if (data.type === Hls.ErrorTypes.NETWORK_ERROR && (status === 403 || status === 404) && data.details === Hls.ErrorDetails.MANIFEST_LOAD_ERROR) {
          if (onError) onError("ended", status);
        } else if (data.type === Hls.ErrorTypes.NETWORK_ERROR && recoveries < 5) {
          recoveries++;
          hls.startLoad();
        } else if (data.type === Hls.ErrorTypes.MEDIA_ERROR && recoveries < 5) {
          recoveries++;
          hls.recoverMediaError();
        } else if (onError) onError(data.type, data.details);
      });
      hls.loadSource(url);
      hls.attachMedia(video);
    } else {
      video.src = url; // native HLS
      video.addEventListener("loadedmetadata", () => { tryPlay(); if (onStart) onStart(); }, { once: true });
    }
    const range = () => {
      const s = video.seekable;
      return s.length ? [s.start(0), s.end(s.length - 1)] : null;
    };
    return {
      pause() { userPaused = true; video.pause(); },
      play() { userPaused = false; tryPlay(); },
      toggle() { if (userPaused || video.paused) this.play(); else this.pause(); },
      get paused() { return userPaused || video.paused; },
      get userPaused() { return userPaused; }, // paused by the viewer, not loading
      resumeIfBlocked() { if (!userPaused && video.paused) tryPlay(); },
      behind() {
        const live = hls && hls.liveSyncPosition != null ? hls.liveSyncPosition : (range() || [0, 0])[1];
        return Math.max(0, live - video.currentTime);
      },
      // Seconds that can be rewound from here.
      back() { const r = range(); return r ? Math.max(0, video.currentTime - r[0]) : 0; },
      goLive() {
        const live = hls && hls.liveSyncPosition != null ? hls.liveSyncPosition : (range() || [0, 0])[1] - 6;
        video.currentTime = live;
        this.play();
      },
      seek(delta) {
        const r = range();
        if (!r) return;
        video.currentTime = Math.min(Math.max(video.currentTime + delta, r[0] + 0.5), r[1] - 1);
      },
      // The wall-clock time of the frame on screen (Date), if known.
      clockTime() { return hls && hls.playingDate ? hls.playingDate : null; },
      // Moves to a wall-clock moment (unix seconds) if it is in the buffer.
      seekToTime(unix) {
        const now = this.clockTime(), r = range();
        if (!now || !r) return false;
        const target = video.currentTime + (unix * 1000 - now.getTime()) / 1000;
        if (target < r[0] || target > r[1]) return false;
        video.currentTime = target;
        this.play();
        return true;
      },
      destroy() {
        destroyed = true;
        if (hls) hls.destroy();
        video.removeAttribute("src");
        video.load();
      },
    };
  }

  // ---- Server problems (VPN down, failed sync, expiring account) ----
  // A small notice in a corner, on every page, linking to the settings.
  let statusWatched = false;
  function watchStatus() {
    if (statusWatched) return;
    statusWatched = true;
    const check = async () => {
      let problems = [];
      try { problems = (await api("GET", "/api/v1/status")).problems; } catch { return; }
      let box = document.getElementById("server-problems");
      if (!problems.length) { if (box) box.remove(); return; }
      if (!box) {
        box = Object.assign(document.createElement("a"), { id: "server-problems" });
        box.classList.add("focusable"); // the remote reaches it
        const open = () => { box.classList.remove("compact"); clearTimeout(box.timer); box.timer = setTimeout(() => box.classList.add("compact"), 6000); };
        box.addEventListener("focus", open);
        box.addEventListener("mouseenter", open);
        // A page can give it a place of its own instead of floating.
        (document.getElementById("problems-slot") || document.body).append(box);
      }
      const error = problems.some((p) => p.level === "error");
      box.classList.toggle("error", error);
      box.classList.toggle("warning", !error);
      box.href = "/settings.html?view=" + (problems[0].area === "profile" ? "profiles" : "system");
      const text = "⚠ " + (problems.length > 1 ? t("{text} (e altri {count})", { text: problems[0].text, count: problems.length - 1 }) : problems[0].text);
      // Read once, then a small ⚠ that does not cover the page; focus or
      // the pointer show the text again. A new problem shows it again too.
      if (box.textContent !== text && box.dataset.text !== text) {
        box.dataset.text = text;
        box.textContent = text;
        box.title = text;
        box.classList.remove("compact");
        clearTimeout(box.timer);
        box.timer = setTimeout(() => box.classList.add("compact"), 10000);
      }
    };
    check();
    setInterval(check, 30000);
  }

  // A button's icon and text together (icons.js).
  function label(el, iconName, text) {
    const parts = [];
    if (iconName && window.VodMaxIcons) parts.push(VodMaxIcons.icon(iconName));
    const span = document.createElement("span");
    span.className = "lbl";
    span.textContent = text;
    parts.push(span);
    el.replaceChildren(...parts);
    if (el.tagName === "BUTTON") el.setAttribute("aria-label", text); // the text can be hidden on phones
  }

  // The Android app's native player (window.VodMaxNative), if present.
  const native = window.VodMaxNative || null;

  return { native, newerVersion, appUpdate, updateApp, bottomNav, closeSheet, sheetOpen, label, store, viewer, token, withToken, askToken, showProfiles, me, api, keyOf, clock, hhmm, tsPlayer, hlsPlayer, watchStatus };
})();
