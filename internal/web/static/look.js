// The device's look (decision 0054): accent colour, OLED black, reduced
// effects, text size and the screensaver (screensaver.js reads it), kept
// in localStorage as vodmax.look. Loaded
// first in every page's <head> (the CSP allows no inline script), so the
// classes are on <html> before ui.css paints; common.js exposes it as
// VodMax.look and the settings page changes it. Never sent to the server.
"use strict";

window.VodMaxLook = (() => {
  const KEY = "vodmax.look";
  const ACCENTS = ["aurora", "oceano", "tramonto", "lavanda", "menta", "rubino", "grafite"];
  // The screensaver starts off on touch screens: a phone locks itself.
  const touch = Boolean(window.matchMedia && window.matchMedia("(pointer: coarse)").matches);
  // TVs (LG webOS, other smart TVs, the Android app without a touch screen)
  // start with reduced effects: blur over moving video costs frames on
  // their graphics chips. A choice made in Aspetto wins.
  const ua = navigator.userAgent || "";
  const tv = /Web0S|webOS|SmartTV|SMART-TV|Tizen|NetCast/i.test(ua) || (/VodMaxAndroid/.test(ua) && !touch);
  const DEFAULTS = { accent: "aurora", oled: false, nofx: tv, fontScale: 1, screensaver: !touch, screensaverMinutes: 5 };

  function get() {
    try { return Object.assign({}, DEFAULTS, JSON.parse(localStorage.getItem(KEY)) || {}); }
    catch { return Object.assign({}, DEFAULTS); }
  }
  function apply(look) {
    const h = document.documentElement;
    h.classList.toggle("oled", Boolean(look.oled));
    h.classList.toggle("nofx", Boolean(look.nofx));
    // Aurora is the default of ui.css: no attribute.
    if (ACCENTS.includes(look.accent) && look.accent !== "aurora") h.dataset.accent = look.accent;
    else delete h.dataset.accent;
    const scale = Number(look.fontScale);
    if (scale >= 0.8 && scale <= 1.4 && scale !== 1) h.style.setProperty("--font-scale", String(scale));
    else h.style.removeProperty("--font-scale");
  }
  // set({oled: true}) changes one or more fields, saves and applies them.
  function set(partial) {
    const look = Object.assign(get(), partial);
    try { localStorage.setItem(KEY, JSON.stringify(look)); } catch { /* private mode: this page only */ }
    apply(look);
    return look;
  }

  apply(get());
  return { get, set, apply, ACCENTS };
})();
