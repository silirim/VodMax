# 0023 — Lingue dell'interfaccia

Stato: **Approvata** (30 ottobre 2026: "aggiungiamo la selezione della lingua")

## Decisione

- **Italiano e inglese**. I testi restano scritti in italiano nel codice e
  passano da `t("…")`; ogni lingua è un dizionario (`internal/web/static/i18n/
  <lingua>-*.js`) con il testo italiano come chiave. Un testo senza
  traduzione resta in italiano.
- La lingua è **per dispositivo**: quella del dispositivo al primo avvio
  (italiano se è italiano, altrimenti inglese), poi la scelta fatta in
  Impostazioni → Lingua o nella schermata "Chi guarda?" (anche per i profili
  non amministratori).
- Il server riceve la lingua (`X-VodMax-Lang`, altrimenti
  `Accept-Language`) e risponde nella stessa lingua: errori e avvisi sono
  formati in italiano e tradotti dal catalogo `messages_<lingua>.go`; un
  test verifica che ogni messaggio abbia la traduzione con gli stessi
  segnaposto.
- App Android e avvio webOS seguono la lingua del sistema; in una lingua
  che non hanno, l'inglese.

## Limiti

- Restano nella lingua in cui sono stati salvati gli errori memorizzati
  (ultima sincronizzazione, registrazioni fallite) e i testi dei provider
  (titoli, guida, categorie). Trame da TMDB in italiano.
- Nuova lingua: un dizionario per area, `messages_<lingua>.go`, e la voce in
  `LANGS` (`i18n.js`) e `Languages` (`lang.go`).

## Aggiornamento (8 novembre 2026: "Fai 0023")

Su un telefono in una lingua senza traduzione (portoghese, olandese…) la
prima schermata dell'app Android usciva in italiano, mentre l'interfaccia
e l'avvio webOS passano all'inglese. Ora anche l'app usa l'inglese: il
file base `values/strings.xml` è in inglese e l'italiano sta in
`values-it/`. Per Weblate il componente dell'app Android ha l'inglese come
lingua sorgente.
