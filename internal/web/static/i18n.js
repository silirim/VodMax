// Interface languages. Texts are written in Italian in the code and shown
// through t("Testo con {nome}", { nome }); each language has its
// dictionary, i18n/<lang>.json keyed by the Italian text (translated on
// Weblate), which the server hands out together as i18n/languages.js. A
// text without a translation stays in Italian.
//
// Static text in the pages: an element with data-i18n has its text nodes
// translated; data-i18n-attr="title,placeholder,aria-label" translates
// those attributes.
"use strict";

(() => {
  // Each language is named in itself.
  const NAMES = { it: "Italiano", en: "English", de: "Deutsch", fr: "Français", es: "Español", pt: "Português", nl: "Nederlands",
    pl: "Polski", ro: "Română", cs: "Čeština", sv: "Svenska", da: "Dansk", fi: "Suomi", el: "Ελληνικά", hu: "Magyar", tr: "Türkçe",
    uk: "Українська", ru: "Русский", ar: "العربية", sq: "Shqip", hr: "Hrvatski", sl: "Slovenščina", sk: "Slovenčina", bg: "Български" };
  const LOCALES = { it: "it-IT", en: "en-GB" };
  const KEY = "vodmax.lang";
  const dicts = window.VodMaxDicts || {};
  const LANGS = { it: NAMES.it };
  for (const code of Object.keys(dicts).sort()) LANGS[code] = NAMES[code] || code.toUpperCase();

  let saved = null;
  try { saved = JSON.parse(localStorage.getItem(KEY)); } catch { /* private mode */ }
  const device = String(navigator.language || "").slice(0, 2).toLowerCase();
  // Saved choice, else the device's language, else English.
  const lang = LANGS[saved] ? saved : LANGS[device] ? device : "en";
  document.documentElement.lang = lang;
  // Right-to-left layout for Arabic (the only RTL language so far).
  document.documentElement.dir = lang === "ar" ? "rtl" : "ltr";

  function t(text, vars) {
    let s = (dicts[lang] && dicts[lang][text]) || text;
    if (vars) s = s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m));
    return s;
  }

  function add(code, entries) { dicts[code] = Object.assign(dicts[code] || {}, entries); }

  function setLang(code) {
    if (!LANGS[code] || code === lang) return;
    try { localStorage.setItem(KEY, JSON.stringify(code)); } catch { /* private mode */ }
    location.reload();
  }

  // Translates the static text of root (the page, or a part added later).
  function apply(root = document) {
    for (const el of root.querySelectorAll("[data-i18n]")) {
      const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        const text = n.nodeValue.trim();
        if (!text) continue;
        if (n.vodmaxKey === undefined) n.vodmaxKey = text;
        n.nodeValue = n.nodeValue.replace(text, t(n.vodmaxKey));
      }
    }
    for (const el of root.querySelectorAll("[data-i18n-attr]")) {
      for (const a of el.dataset.i18nAttr.split(",")) {
        const name = a.trim(), key = "i18n" + name.replace(/(^|-)(\w)/g, (m, d, c) => c.toUpperCase());
        if (!el.hasAttribute(name)) continue;
        if (el.dataset[key] === undefined) el.dataset[key] = el.getAttribute(name);
        el.setAttribute(name, t(el.dataset[key]));
      }
    }
    if (root === document && document.title) document.title = t(document.title);
  }

  // A row of buttons to choose the language (settings, profile choice).
  function picker(className = "") {
    const row = document.createElement("div");
    row.className = "lang-picker " + className;
    for (const [code, name] of Object.entries(LANGS)) {
      const b = document.createElement("button");
      b.type = "button";
      b.className = "focusable" + (code === lang ? " active" : "");
      b.lang = code;
      b.textContent = (code === lang ? "✓ " : "") + name;
      b.onclick = () => setLang(code);
      row.append(b);
    }
    return row;
  }

  // Translate the static text once the page is parsed.
  document.addEventListener("DOMContentLoaded", () => apply());

  window.VodMaxI18n = { lang, locale: LOCALES[lang] || lang, langs: LANGS, t, add, setLang, apply, picker };
})();
