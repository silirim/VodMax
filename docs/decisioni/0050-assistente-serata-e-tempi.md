# 0050 — Assistente: tutta la serata, e risposte che si possono usare

Stato: **Proposta** (6 novembre 2026, da una revisione dell'assistente)

## Il problema

"Cosa guardo stasera" manda al modello al massimo 60 programmi
([0034](0034-assistente.md)). Prima delle 18 prendeva quelli che
cominciano prima dopo le 20: con più di 60 canali con guida, il caso
normale in Italia, erano tutti telegiornali e quiz delle 20:30, e i film
delle 21:30 non arrivavano mai al modello. Dopo le 18 vincevano i
programmi cominciati da più tempo (blocchi lunghi, repliche a ciclo). I
preferiti e i numeri ufficiali non contavano.

## Decisione

- Ogni canale porta al massimo due programmi: quello in onda adesso (solo
  dalle 18, quando la serata è adesso) e il suo programma principale,
  quello che comincia più vicino alle 21:15 (o ad adesso, se è più tardi):
  il film delle 21:30, non il quiz delle 20:30.
- Prima i canali preferiti del profilo, poi i numeri ufficiali, come
  nell'elenco dei canali. Restano al massimo 60 programmi, per i modelli
  piccoli.

## Non ancora

- Scegliere i canali dai gusti della persona (quello che guarda di più).
