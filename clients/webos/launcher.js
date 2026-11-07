// VodMax for LG webOS: remembers the server address, checks that the
// server answers, then opens its interface (the same as in a browser).
// Written for old webOS browsers too (no async/await, no fetch).
(function () {
  "use strict";
  var KEY = "vodmax.server";
  var $ = function (id) { return document.getElementById(id); };

  // The TV's language if VodMax speaks it, else English; Italian is the
  // text itself. Keys: the Italian texts.
  var DICTS = {
    en: {
      "Avvio…": "Starting…",
      "http://indirizzo:8095": "http://address:8095",
      "Collega": "Connect",
      "Riprova": "Try again",
      "L'indirizzo del server VodMax di casa, per esempio http://192.168.1.10:8095. Poi la TV si abbina con un codice.": "The address of your home VodMax server, for example http://192.168.1.10:8095. Then the TV pairs with a code.",
      "Collegamento a {base}…": "Connecting to {base}…",
      "Il server non risponde a {base}: controlla che sia acceso e nella stessa rete.": "The server isn't responding at {base}: make sure it's on and on the same network.",
      "Inserisci l'indirizzo del server.": "Enter the server address.",
      "OK o Indietro: cambia server": "OK or Back: change server"
    },
    de: {
      "Avvio…": "Wird gestartet…",
      "http://indirizzo:8095": "http://adresse:8095",
      "Collega": "Verbinden",
      "Riprova": "Erneut versuchen",
      "L'indirizzo del server VodMax di casa, per esempio http://192.168.1.10:8095. Poi la TV si abbina con un codice.": "Die Adresse deines VodMax-Servers zu Hause, z. B. http://192.168.1.10:8095. Danach wird der Fernseher mit einem Code gekoppelt.",
      "Collegamento a {base}…": "Verbindung mit {base}…",
      "Il server non risponde a {base}: controlla che sia acceso e nella stessa rete.": "Der Server antwortet nicht unter {base}: Prüfe, ob er eingeschaltet und im selben Netzwerk ist.",
      "Inserisci l'indirizzo del server.": "Gib die Serveradresse ein.",
      "OK o Indietro: cambia server": "OK oder Zurück: Server wechseln"
    },
    es: {
      "Avvio…": "Iniciando…",
      "http://indirizzo:8095": "http://direccion:8095",
      "Collega": "Conectar",
      "Riprova": "Reintentar",
      "L'indirizzo del server VodMax di casa, per esempio http://192.168.1.10:8095. Poi la TV si abbina con un codice.": "La dirección del servidor VodMax de casa, por ejemplo http://192.168.1.10:8095. Luego la TV se vincula con un código.",
      "Collegamento a {base}…": "Conectando con {base}…",
      "Il server non risponde a {base}: controlla che sia acceso e nella stessa rete.": "El servidor no responde en {base}: comprueba que esté encendido y en la misma red.",
      "Inserisci l'indirizzo del server.": "Introduce la dirección del servidor.",
      "OK o Indietro: cambia server": "OK o Atrás: cambiar servidor"
    },
    fr: {
      "Avvio…": "Démarrage…",
      "http://indirizzo:8095": "http://adresse:8095",
      "Collega": "Connecter",
      "Riprova": "Réessayer",
      "L'indirizzo del server VodMax di casa, per esempio http://192.168.1.10:8095. Poi la TV si abbina con un codice.": "L'adresse du serveur VodMax de la maison, par exemple http://192.168.1.10:8095. Ensuite, la TV s'associe avec un code.",
      "Collegamento a {base}…": "Connexion à {base}…",
      "Il server non risponde a {base}: controlla che sia acceso e nella stessa rete.": "Le serveur ne répond pas à {base} : vérifiez qu'il est allumé et sur le même réseau.",
      "Inserisci l'indirizzo del server.": "Saisissez l'adresse du serveur.",
      "OK o Indietro: cambia server": "OK ou Retour : changer de serveur"
    },
    pl: {
      "Avvio…": "Uruchamianie…",
      "http://indirizzo:8095": "http://adres:8095",
      "Collega": "Połącz",
      "Riprova": "Spróbuj ponownie",
      "L'indirizzo del server VodMax di casa, per esempio http://192.168.1.10:8095. Poi la TV si abbina con un codice.": "Adres domowego serwera VodMax, na przykład http://192.168.1.10:8095. Potem telewizor sparuje się za pomocą kodu.",
      "Collegamento a {base}…": "Łączenie z {base}…",
      "Il server non risponde a {base}: controlla che sia acceso e nella stessa rete.": "Serwer nie odpowiada pod adresem {base}: sprawdź, czy jest włączony i w tej samej sieci.",
      "Inserisci l'indirizzo del server.": "Wpisz adres serwera.",
      "OK o Indietro: cambia server": "OK lub Wstecz: zmień serwer"
    }
  };
  var lang = String(navigator.language || navigator.userLanguage || "").slice(0, 2).toLowerCase();
  var dict = lang === "it" ? null : DICTS[lang] || DICTS.en;
  function t(text, base) {
    var s = (dict && dict[text]) || text;
    return s.replace("{base}", base || "");
  }
  if (dict) {
    document.documentElement.lang = DICTS[lang] ? lang : "en";
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

  // The address being checked; null once OK or Back stopped it.
  var checking = null;
  var hintText = document.querySelector(".hint").textContent;

  // pause: at start-up with the saved address the redirect waits a
  // moment, the only chance to choose another server (the launcher's
  // storage cannot be reached from the server's pages).
  function open(base, pause) {
    var since = Date.now();
    checking = base;
    $("message").textContent = t("Collegamento a {base}…", base);
    $("message").className = "";
    if (pause) document.querySelector(".hint").textContent = t("OK o Indietro: cambia server");
    check(base, function (ok) {
      if (checking !== base) return;
      if (!ok) {
        checking = null;
        showForm(base, t("Il server non risponde a {base}: controlla che sia acceso e nella stessa rete.", base));
        return;
      }
      setTimeout(function () {
        if (checking === base) window.location.replace(base + "/");
      }, pause ? Math.max(0, 1500 - (Date.now() - since)) : 0);
    });
  }

  function showForm(value, error) {
    document.querySelector(".hint").textContent = hintText;
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
  // the buttons. OK or Back while connecting: the form, to change server.
  // Back on the form leaves the app (webOS sends it to the page, 461).
  document.addEventListener("keydown", function (e) {
    if (checking && (e.keyCode === 13 || e.keyCode === 461)) {
      var base = checking;
      checking = null;
      e.preventDefault();
      showForm(base, null);
      return;
    }
    if (e.keyCode === 461 && !$("form").hidden) { window.close(); return; }
    var order = [$("server"), $("connect"), $("retry")].filter(function (el) { return !el.hidden; });
    var i = order.indexOf(document.activeElement);
    var onButton = document.activeElement !== $("server");
    if ((e.keyCode === 40 || (e.keyCode === 39 && onButton)) && i < order.length - 1) { order[i + 1].focus(); e.preventDefault(); }
    if ((e.keyCode === 38 || (e.keyCode === 37 && onButton && i > 1)) && i > 0) { order[i - 1].focus(); e.preventDefault(); }
  });

  var saved = null;
  try { saved = localStorage.getItem(KEY); } catch (err) { /* nothing saved */ }
  if (saved) open(saved, true);
  else showForm("", null);
})();
