# 0017 — Film, serie, "Continua a guardare" e registrazioni

Stato: **Approvata** (29 ottobre 2026), prima versione realizzata

## Verifiche sul provider reale

| Contenitore | Film | Contenuto |
|---|---|---|
| MP4 | 60.997 (66%) | H.264 (+ AAC), indice all'inizio |
| MKV | 30.744 (33%) | H.264 + AAC, AC-3 o DTS |
| AVI | 681 | Xvid o MP4 con indice in fondo |

Il provider accetta le richieste a intervalli (206) e risponde in 0,5–10 s.

## Decisioni

1. **Due modi di riproduzione nel browser** (il server fa sempre da tramite,
   perché solo lui raggiunge il provider tramite VPN):
   - **diretto** per gli MP4: il browser chiede intervalli, il server li
     inoltra; avanzamento e salti nativi;
   - **conversione** per il resto: ffmpeg riconfeziona in MP4 frammentato da
     una posizione, copiando il video H.264 e convertendo l'audio non AAC.
     Saltare = ripartire da un'altra posizione. ffmpeg legge da un relay
     interno in ascolto solo su 127.0.0.1, che passa dal tunnel: non ha una
     strada propria verso il provider.
   - Se il browser non riesce a leggere un MP4 diretto, l'interfaccia passa
     da sola alla conversione dalla stessa posizione.
   - Le app Android future useranno sempre il diretto (ExoPlayer legge MKV,
     AC-3, DTS).
2. **Connessioni al provider gestite in un solo punto** per diretta, film e
   registrazioni: un player ha un solo flusso (cambio canale, salto, episodio
   successivo); a connessioni esaurite "occupato" o presa di controllo; **una
   registrazione non viene mai interrotta da chi guarda**.
3. **Episodi** caricati dal provider quando si apre la serie, conservati 12
   ore; titoli puliti ("Serie - S01E01 - Titolo" → "Titolo").
4. **Avanzamento** per film ed episodi, salvato ogni 15 secondi e all'uscita;
   un titolo è "visto" dal 95% o negli ultimi 3 minuti. **Continua a
   guardare**: film e ultimo episodio di ogni serie, dal più recente.
5. **Registrazioni**:
   - dalla TV (tasto blu) si registra il programma in onda secondo la guida,
     con 2 minuti prima e 5 dopo; dall'API anche con orari esatti;
   - il server registra tramite la VPN, si riconnette se il flusso cade,
     rifiuta sovrapposizioni sullo stesso canale; massimo 6 ore;
   - il canale in registrazione si guarda dal file che cresce, senza
     seconda connessione;
   - a fine registrazione il file MPEG-TS viene riconfezionato in MP4 senza
     ricodifica, così nel browser si può saltare; se non riesce resta TS;
   - un riavvio del server a metà segna la registrazione come interrotta.
6. **Interfaccia Film e Serie** (`/`, il menu principale; tasto verde dalla TV):
   Home con "Continua a guardare", nuovi film, nuove serie; schede Film, Serie,
   Registrazioni; ricerca; scheda con Riprendi/Dall'inizio/Altre versioni;
   serie con stagioni, episodi e avanzamento; player con OK pausa, ◀ −10 s,
   ▶ +30 s, Indietro esce salvando. Navigazione con le frecce verso
   l'elemento più vicino. Con la vista Cartelle
   ([0058](0058-vista-a-cartelle.md)) Film e Serie si aprono sulle
   categorie del provider, nel suo ordine.

## Prove (29 ottobre 2026, account di prova, server di prova in rete locale)

- Interstellar MP4 14,5 GB: intervalli a 0, 500 MB e 2 GB in 0,5 s.
- MKV: analisi ffprobe 0,4 s; conversione dal minuto 10 con primo dato in
  1,4 s.
- Breaking Bad: 62 episodi in 0,4 s, riapertura 1 ms.
- Registrazione reale di Rai 1: completata, MP4 H.264 + AAC, riproducibile
  con intervalli; canale guardato durante la registrazione dal file.
- Interfaccia guidata da Chromium: Home, ricerca, scheda film, scheda serie,
  registrazioni; il browser contatta solo il server VodMax. La riproduzione
  video nel browser automatico non è verificabile (niente H.264).

## Ancora da fare

- Prova della riproduzione di film, episodi e registrazioni in Chrome dall'utente.
- Registrazioni ricorrenti e per regole (squadre, F1), dalla guida per
  programmi futuri, spazio su disco e pulizia.
- Sottotitoli e scelta della traccia audio.
- Episodio successivo automatico.
