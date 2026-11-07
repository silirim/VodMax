// Screensaver (decision 0054): after a few minutes without keys, pointer
// or touch, the browsing pages show the catalogue's backdrops one after
// the other, slowly, with the title and the clock. Any input closes it
// and does nothing else: the page and its focus stay as they were. The
// TV pages (tv.html, multiview.html) do not load it, and it never starts
// while a video plays. Settings → Aspetto turns it off or changes the
// minutes (VodMax.look: screensaver, screensaverMinutes).
// Styles go through the CSSOM: the CSP allows no inline <style>, and an
// important inline transition is the only one html.nofx does not cancel.
"use strict";

(() => {
  const V = window.VodMax;
  const SLIDE = 12000; // each backdrop stays this long
  const FADE = 2000; // cross-fade between two backdrops
  let last = Date.now(); // last input
  let layer = null; // the open screensaver
  let closing = null; // the layer fading out: it still takes the input
  let slides = null; // [{src, logo, title}], fetched once per page
  let timers = [];
  let px = -1, py = -1; // browsers send a mousemove without a move when the page under the pointer changes

  const css = (el, props) => { for (const [k, v] of Object.entries(props)) el.style.setProperty(k, v, "important"); return el; };
  const div = (props) => css(document.createElement("div"), Object.assign({ position: "absolute" }, props));
  const still = () => document.documentElement.classList.contains("nofx") || window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const busy = () => {
    const player = document.getElementById("player"); // index.html: the films' and series' player
    return document.hidden || (player && !player.hidden) || [...document.querySelectorAll("video")].some((v) => !v.paused && !v.ended);
  };

  // A dozen films and series with a backdrop. Plain fetch, not VodMax.api:
  // a screensaver must not open the pairing or the profile panel.
  async function backdrops() {
    if (slides) return slides;
    const get = (path) => fetch(path, { headers: V.authHeaders() }).then((r) => (r.ok ? r.json() : null), () => null);
    const lists = await Promise.all(["movie", "series"].map((kind) => get("/api/v1/catalog/" + kind + "?limit=10")));
    const items = [].concat(...lists.map((l) => (l && l.items) || []));
    const found = await Promise.all(items.map((i) => get("/api/v1/items/" + i.id + "/details")
      .then((d) => (d && d.backdrop ? { src: d.backdrop, logo: d.logo || "", title: i.display_title || i.title } : null))));
    slides = found.filter(Boolean);
    for (let i = slides.length - 1; i > 0; i--) { // shuffled: not the alphabet every time
      const j = Math.floor(Math.random() * (i + 1));
      [slides[i], slides[j]] = [slides[j], slides[i]];
    }
    return slides;
  }

  async function open() {
    const list = await backdrops();
    // Something happened while the backdrops were coming, or nothing to show.
    if (!list.length || layer || busy() || Date.now() - last < minutes() * 60000) return;
    layer = css(document.createElement("div"), { position: "fixed", top: "0", left: "0", width: "100%", height: "100%", "z-index": "2000",
      overflow: "hidden", background: "#000", cursor: "none", opacity: "0", transition: "opacity 1s ease" });
    layer.setAttribute("aria-hidden", "true");
    for (const type of ["touchstart", "touchmove", "wheel"]) layer.addEventListener(type, (e) => { e.preventDefault(); close(); }, { passive: false });
    const pictures = [0, 1].map(() => div({ top: "0", left: "0", width: "100%", height: "100%", "background-size": "cover",
      "background-position": "center", opacity: "0", "will-change": "opacity, transform" }));
    // Dark corner behind the title, dark top-right behind the clock.
    const scrim = div({ top: "0", left: "0", width: "100%", height: "100%",
      "background-image": "linear-gradient(to top right, rgba(0,0,0,.72), rgba(0,0,0,0) 55%), linear-gradient(to bottom left, rgba(0,0,0,.5), rgba(0,0,0,0) 30%)" });
    const caption = div({ left: "var(--safe, 5vw)", bottom: "8vh", "max-width": innerWidth < 800 ? "80vw" : "40vw", color: "#fff", transition: "opacity " + FADE / 2 + "ms ease" });
    const clock = div({ top: "6vh", right: "var(--safe, 5vw)", color: "#fff", "font-size": "var(--t-2xl, 2.2rem)", "font-weight": "600",
      "font-variant-numeric": "tabular-nums", "letter-spacing": "-0.02em", "text-shadow": "0 1px 3px rgba(0,0,0,.6)" });
    // The pictures stack inside their own layer, under the text.
    const stage = div({ top: "0", left: "0", width: "100%", height: "100%", "z-index": "0" });
    stage.append(...pictures);
    for (const e of [scrim, caption, clock]) css(e, { "z-index": "1" });
    layer.append(stage, scrim, caption, clock);
    document.body.append(layer);
    // Android's Back button calls this, not a key event.
    const nativeBack = window.VodMaxNativeBack;
    window.VodMaxNativeBack = () => { close(); return true; };
    layer.restoreBack = () => { window.VodMaxNativeBack = nativeBack; };

    const tick = () => { clock.textContent = V.hhmm(Date.now() / 1000); };
    tick();
    timers.push(setInterval(tick, 10000));
    const text = (t) => Object.assign(css(document.createElement("div"), { "font-size": "var(--t-3xl, 3.2rem)", "font-weight": "800",
      "line-height": "1.05", "letter-spacing": "-0.02em", "text-shadow": "0 2px 12px rgba(0,0,0,.6)" }), { textContent: t });
    let n = 0;
    const show = () => {
      const s = list[n % list.length], pic = pictures[n % 2], other = pictures[(n + 1) % 2];
      n++;
      const img = new Image();
      img.onload = () => {
        if (!layer) return;
        const move = !still();
        // Ken Burns: start a little zoomed and drift to the other side.
        const dx = n % 2 ? 2 : -2, dy = n % 3 ? 1.5 : -1.5;
        css(pic, { transition: "none", "background-image": "url(" + JSON.stringify(s.src) + ")", "z-index": String(n),
          transform: move ? "scale(1.06) translate(" + dx + "%, " + dy + "%)" : "none" });
        void pic.offsetWidth; // the start position is laid out before the transition
        css(pic, { transition: "opacity " + FADE + "ms ease" + (move ? ", transform " + (SLIDE + 2 * FADE) + "ms linear" : ""), opacity: "1",
          transform: move ? "scale(1.16) translate(" + -dx + "%, " + -dy + "%)" : "none" });
        timers.push(setTimeout(() => { if (layer) css(other, { opacity: "0" }); }, FADE));
        css(caption, { opacity: "0" });
        timers.push(setTimeout(() => {
          if (!layer) return;
          let title;
          if (s.logo) {
            title = css(document.createElement("img"), { display: "block", "max-width": innerWidth < 800 ? "60vw" : "32vw", "max-height": "20vh", filter: "drop-shadow(0 2px 8px rgba(0,0,0,.6))" });
            title.alt = s.title;
            title.onerror = () => title.replaceWith(text(s.title));
            title.src = s.logo;
          } else title = text(s.title);
          caption.replaceChildren(title);
          css(caption, { opacity: "1" });
        }, FADE / 2));
      };
      img.onerror = () => { if (layer) timers.push(setTimeout(show, 50)); };
      img.src = s.src;
    };
    show();
    timers.push(setInterval(show, SLIDE));
    requestAnimationFrame(() => { if (layer) css(layer, { opacity: "1" }); });
  }

  function close() {
    if (!layer) return;
    const l = layer;
    layer = null;
    closing = l;
    for (const t of timers) { clearTimeout(t); clearInterval(t); }
    timers = [];
    l.restoreBack();
    css(l, { opacity: "0", transition: "opacity .3s ease" });
    setTimeout(() => { l.remove(); if (closing === l) closing = null; }, 350);
  }

  const minutes = () => Number(V.look.get().screensaverMinutes) || 5;
  // Any input: counts as activity; while the screensaver is open (or
  // fading out) it only closes it and never reaches the page.
  function input(e) {
    last = Date.now();
    if (!layer && !closing) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    close();
  }
  for (const type of ["keydown", "keyup", "keypress", "pointerdown", "pointerup", "mousedown", "mouseup", "click", "contextmenu"]) {
    window.addEventListener(type, input, true);
  }
  // Scrolling inputs: passive on the window (a blocking one would slow
  // every scroll of the page); the open layer blocks them itself.
  for (const type of ["touchstart", "touchend", "wheel"]) window.addEventListener(type, () => { last = Date.now(); }, { capture: true, passive: true });
  window.addEventListener("mousemove", (e) => {
    const moved = Math.abs(e.screenX - px) + Math.abs(e.screenY - py) > 3;
    px = e.screenX;
    py = e.screenY;
    if (!moved) return;
    last = Date.now();
    close();
  }, true);

  setInterval(() => {
    if (layer || closing) return;
    if (busy()) { last = Date.now(); return; }
    if (V.look.get().screensaver === false || Date.now() - last < minutes() * 60000) return;
    open();
  }, 5000);
})();
