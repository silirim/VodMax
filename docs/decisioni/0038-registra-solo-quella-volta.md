# 0038 — Assistente: registrare solo quella volta

Stato: **Approvata** (3 novembre 2026: "io farei in modo che proponesse se
registrare solo quella sera, nel caso ci fosse un programma nell'epg di
quell'ora chiedesse se si tratta di quello o se è una registrazione
ricorsiva di quel titolo")

## Il problema

Dalla 0034 l'assistente trasforma una frase in una regola, che registra
ogni programma con quel titolo. Chi scrive "registra il film di stasera alle
21 su Rai 1" vuole una registrazione sola, e spesso non sa il titolo.

## La decisione

- Il modello riporta anche il giorno e l'ora, se la frase li nomina. VodMax
  li accetta solo entro i giorni della guida.
- VodMax cerca nella guida i programmi di quel momento: quelli che la regola
  trova intorno a quell'ora e, se la frase nomina un canale, il programma che
  su quel canale comincia più vicino all'ora detta, qualunque sia il titolo.
  Al massimo tre.
- Il modulo chiede "È questo?" con due pulsanti per programma: **Solo questa
  volta** (la stessa registrazione dalla guida, con i margini) e **Ogni volta
  che va in onda** (la regola per quel titolo, da controllare e salvare).
- Senza giorno né ora non cambia niente: si propone la regola come prima.

## Non ancora

- Registrazione singola chiesta da "Stasera" o dalla guida a parole.
