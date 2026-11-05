# 0043 — Il paese al primo avvio, con le bandiere

Stato: **Approvata** (5 novembre 2026: "appena si avvia secondo me
bisognerebbe fargli scegliere il paese tramite un popup con le bandierine
dei paese e affianco il nome")

## Il problema

Con la 0042 il proprio paese si sceglie in Impostazioni → Selezione, per
abbonamento. Chi installa VodMax non sa che esiste: con una lista
internazionale vede tutti i paesi finché non va a cercare quella pagina.

## Decisione

- **Al primo avvio**, sul primo dispositivo dell'amministratore, VodMax
  chiede **"Da dove guardi?"** con una griglia di bandiere e il nome del
  paese nella lingua dell'interfaccia. Il paese della lingua del
  dispositivo è proposto per primo e già selezionato: basta OK.
- La scelta vale per **tutti gli abbonamenti**, anche quelli aggiunti dopo,
  finché in Selezione non se ne sceglie uno diverso per un abbonamento.
- **"Più tardi"** (o Indietro) chiude la finestra; su quel dispositivo
  ritorna dopo una settimana, sugli altri dispositivi dell'amministratore
  alla prima apertura. Gli altri profili non la vedono mai.
- Le bandiere sono quelle di flag-icons (MIT), solo per i paesi che VodMax
  riconosce, in PNG piccoli dentro il programma: niente emoji (su Windows
  e su molte TV diventano lettere) e niente file da internet.

Per chi ha già VodMax: la finestra compare una volta. Scegliendo il
proprio paese la selezione non cambia se le categorie del paese sono già
quelle di casa (sul catalogo di casa: nessuna differenza scegliendo
Italia, vedi 0042).

## Non ancora

- Più di un paese al primo avvio (gli altri si aggiungono in Selezione).
- Le bandiere dei gruppi di lingue e regioni (in inglese, arabo…).
