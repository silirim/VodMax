# 0054 — Aspetto: accento, nero OLED, effetti ridotti, salvaschermo

Stato: **Approvata** (7 novembre 2026: "Procedi ai punti elencati come
meglio credi")

## Il problema

Chi guarda su un OLED chiede il nero vero; chi ha una TV lenta chiede
meno effetti; molti vogliono il proprio colore. Oggi il tema è uno solo e
una TV lasciata sulla Home resta ferma con le stesse immagini.

## Decisione

- In Impostazioni, "Aspetto": **colore d'accento** (sei scelte più
  l'Aurora di oggi), **nero OLED** (sfondo `#000`, vetro più scuro) ed
  **effetti ridotti** (niente sfocatura, niente panoramiche, niente
  transizioni: lo stesso che fa `prefers-reduced-motion`). Le scelte
  valgono per il dispositivo (nel browser), non per il profilo: una TV
  resta nera anche cambiando chi guarda.
- **Salvaschermo** nelle pagine di navigazione (non durante il video):
  dopo cinque minuti senza tasti una sequenza lenta degli sfondi del
  catalogo con titolo e ora; un tasto qualsiasi lo chiude e torna dove si
  era, senza perdere il fuoco.
- Gli effetti "vetro" si disattivano da soli dove il motore non li regge
  (`@supports`) e quando "effetti ridotti" è attivo.

## Non ancora

- Tema chiaro.
- Scelta dell'aspetto per profilo o salvata sul server.
- Il salvaschermo come applicazione della TV (fuori da VodMax).
