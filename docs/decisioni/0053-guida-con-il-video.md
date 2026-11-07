# 0053 — Guida: il video continua, il programma a vista

Stato: **Approvata** (7 novembre 2026: "Procedi ai punti elencati come
meglio credi")

## Il problema

Aprire la guida dalla diretta cambia pagina e ferma il video. È la
richiesta più frequente agli altri programmi (Channels, Plex, Emby, Pluto):
la gente vuole sfogliare la guida senza perdere quello che sta guardando.
Il dettaglio del programma si apre in una finestra sopra la griglia, che
copre le righe vicine e costringe a chiudere per continuare a sfogliare.

## Decisione

- Dalla diretta (`tv.html`) la guida si apre **sopra il video**, che
  continua in un riquadro in alto a destra con l'audio; OK sintonizza e
  chiude la guida, Indietro chiude e torna al canale di prima. La pagina
  `guide.html` resta per chi arriva dal menu e dal telefono, con la stessa
  griglia: il codice della griglia è uno solo, condiviso.
- Il **programma a fuoco** si legge in un pannello fisso sopra la griglia
  (titolo, canale, orario, trama, immagine se c'è) senza aprire nulla; OK
  apre le azioni (Guarda, Dall'inizio, Registra) nello stesso pannello.
- Le celle portano il logo del canale (o il segnaposto di
  [0052](0052-immagini-sfondi-loghi-badge.md)), il segno di catch-up sui
  programmi passati che si possono rivedere e il punto rosso della
  registrazione; una tinta leggera per genere (film, sport, notizie,
  bambini) quando la guida lo dice.
- La griglia riceve il tema vetro di
  [0051](0051-aspetto-vetro-e-caratteri.md): celle traslucide, riga
  dell'ora in evidenza, "adesso" sempre al bordo sinistro.

## Non ancora

- Anteprima video dentro la cella della guida (Plex): costa una seconda
  connessione al provider, che in casa spesso non c'è.
- Guida sopra il video anche nell'app Android con il lettore nativo: il
  lettore nativo serve solo per film e serie, la diretta usa già
  l'interfaccia web.
