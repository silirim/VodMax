# 0047 — Il lettore dell'app Android a schermo intero, anche per le registrazioni

Stato: **Proposta** (6 novembre 2026: "le registrazioni su android hanno il
player senza schermo intero e non posso andare avanti con il tempo")

## Il problema

- Sul telefono il lettore nativo seguiva la rotazione dello schermo: con la
  rotazione automatica spenta il video restava piccolo in verticale, e non
  c'era un pulsante per lo schermo intero.
- Nelle registrazioni rimaste in MPEG-TS e in quelle in corso la barra del
  tempo era vuota (durata 0:00) e non si poteva trascinare; anche il salto
  nel file di Media3 sbagliava, perché l'orologio di un TS riparte a ogni
  riconnessione con il provider.
- Nell'app il pulsante "Schermo intero" della TV in diretta non faceva
  nulla: la WebView lo ignora se l'app non lo gestisce.

## La decisione

- Sul telefono e sul tablet il lettore nativo (film, episodi, registrazioni,
  catch-up) si apre in orizzontale, senza barre di sistema. Il pulsante
  schermo intero dei comandi torna alla rotazione del telefono e viceversa.
  Sui televisori non cambia nulla.
- Registrazioni TS, finite o in corso: per spostarsi il lettore riapre il
  file dal secondo scelto (`?t=`), come fa già il browser; la barra mostra la
  durata vera data dal server. Le registrazioni MP4 restano file normali.
- Nell'app lo schermo intero delle pagine (TV in diretta) copre tutto lo
  schermo, in orizzontale sul telefono; Indietro ne esce.

## Non ancora

- La barra di una registrazione in corso si allunga solo quando il lettore
  riapre il flusso (a ogni spostamento), non mentre si guarda.
