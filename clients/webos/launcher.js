// VodMax for LG webOS: remembers the server address, checks that the
// server answers, then opens its interface (the same as in a browser).
// Written for old webOS browsers too (no async/await, no fetch).
(function () {
  "use strict";
  var KEY = "vodmax.server";
  var $ = function (id) { return document.getElementById(id); };

  // Italian if the TV speaks Italian, else English. Keys: the Italian texts.
  var italian = /^it/i.test(navigator.language || navigator.userLanguage || "");
  var EN = {
    "Avvio…": "Starting…",
    "http://indirizzo:8095": "http://address:8095",
    "Collega": "Connect",
    "Riprova": "Try again",
    "L'indirizzo del server VodMax di casa, per esempio http://192.168.1.10:8095. Poi la TV si abbina con un codice.":
      "The address of your home VodMax server, for example http://192.168.1.10:8095. Then the TV pairs with a code.",
    "Collegamento a {base}…": "Connecting to {base}…",
    "Il server non risponde a {base}: controlla che sia acceso e nella stessa rete.":
      "The server isn't responding at {base}: make sure it's on and on the same network.",
    "Inserisci l'indirizzo del server.": "Enter the server address."
  };
  function t(text, base) {
    var s = (!italian && EN[text]) || text;
    return s.replace("{base}", base || "");
  }
  if (!italian) {
    document.documentElement.lang = "en";
    $("message").textContent = t($("message").textContent);
    $("server").placeholder = t($("server").placeholder);
    $("connect").textContent = t($("connect").textContent);
    $("retry").textContent = t($("retry").textContent);
    var hint = document.querySelector(".hint");
    hint.textContent = t(hint.textContent);
  }

  function normalize(s) {
    s = (s || "").replace(/^\s+|\s+$/g, "");
    if (!s) return null;
    if (!/^https?:\/\//i.test(s)) s = "http://" + s;
    return s.replace(/\/+$/, "");
  }

  function check(base, done) {
    var x = new XMLHttpRequest();
    var finished = false;
    var end = function (ok) { if (!finished) { finished = true; done(ok); } };
    x.open("GET", base + "/api/v1/health", true);
    x.timeout = 6000;
    x.onload = function () { end(x.status === 200); };
    x.onerror = x.ontimeout = function () { end(false); };
    x.send();
  }

  function open(base) {
    $("message").textContent = t("Collegamento a {base}…", base);
    $("message").className = "";
    check(base, function (ok) {
      if (ok) {
        window.location.replace(base + "/");
        return;
      }
      showForm(base, t("Il server non risponde a {base}: controlla che sia acceso e nella stessa rete.", base));
    });
  }

  function showForm(value, error) {
    $("form").hidden = false;
    $("retry").hidden = !value;
    $("server").value = value || "";
    $("message").textContent = error || t("Inserisci l'indirizzo del server.");
    $("message").className = error ? "bad" : "";
    $("server").focus();
  }

  $("form").addEventListener("submit", function (e) {
    e.preventDefault();
    var base = normalize($("server").value);
    if (!base) return;
    try { localStorage.setItem(KEY, base); } catch (err) { /* private storage off */ }
    open(base);
  });
  $("retry").addEventListener("click", function () {
    var base = normalize($("server").value);
    if (!base) { showForm("", null); return; }
    try { localStorage.setItem(KEY, base); } catch (err) { /* private storage off */ }
    open(base);
  });

  // Remote: Up/Down between the field and the buttons, Left/Right between
  // the buttons.
  document.addEventListener("keydown", function (e) {
    var order = [$("server"), $("connect"), $("retry")].filter(function (el) { return !el.hidden; });
    var i = order.indexOf(document.activeElement);
    var onButton = document.activeElement !== $("server");
    if ((e.keyCode === 40 || (e.keyCode === 39 && onButton)) && i < order.length - 1) { order[i + 1].focus(); e.preventDefault(); }
    if ((e.keyCode === 38 || (e.keyCode === 37 && onButton && i > 1)) && i > 0) { order[i - 1].focus(); e.preventDefault(); }
  });

  var saved = null;
  try { saved = localStorage.getItem(KEY); } catch (err) { /* nothing saved */ }
  if (saved) open(saved);
  else showForm("", null);
})();
