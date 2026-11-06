# 0048 — Registrazioni: insistere quando serve, non quando è inutile

Stato: **Proposta** (6 novembre 2026, da una revisione del registratore)

## Il problema

Una registrazione che si interrompe si ricollega dopo 3 s, e ogni volta
prende la connessione a chi sta guardando. Il 05/11 questa insistenza ha
salvato la partita mentre un altro client dell'account continuava a
prendersi la connessione. Ma quando il canale non funziona più (il
provider risponde 404) o il disco è pieno, la registrazione riprova ogni
3 s per tutto il programma, fino a 6 ore: a ogni tentativo la TV di chi
guarda si interrompe, il provider riceve circa 1200 richieste l'ora e alla
fine non resta niente. Il 05/11, invece, 55 espulsioni in 23 minuti sono
finite solo come righe INFO nel log: nessuno se ne è accorto.

## Decisione

- Dopo una connessione che ha portato dati la registrazione si ricollega
  subito (3 s) e prende la connessione a chi guarda, come prima.
- Dopo una connessione che non ha portato niente l'attesa raddoppia fino
  a 1 minuto, e la connessione si toglie a chi guarda al massimo una volta
  ogni 5 minuti. Un canale che torna viene comunque registrato.
- Se la connessione è occupata (da chi guarda o dalla registrazione del
  programma prima) la registrazione riprova ogni 3 s senza chiedere niente
  al provider.
- Un errore di scrittura sul disco (disco pieno) ferma la registrazione e
  tiene quanto registrato.
- Una registrazione di una regola fermata e poi cancellata resta
  cancellata: la regola non la riprogramma mentre il programma è ancora in
  onda (il segno di cancellazione si tiene una settimana).
- Dopo 3 connessioni di fila chiuse dal provider entro un minuto, lo stato
  avvisa per 12 ore "forse un altro dispositivo usa l'abbonamento" e la
  registrazione finita lo dice nella sua nota.

### Due programmi di seguito sullo stesso canale

I margini (2 minuti prima, 5 dopo) del primo programma coprono l'inizio
del secondo: una regola lo considerava già in programma e non lo
registrava mai, e dalla guida si rifiutava. Ora conta solo la parte di un
programma che i margini dei vicini non raggiungono: si registrano tutti e
due. Con una sola connessione la seconda registrazione parte quando
finisce la prima; i suoi primi minuti sono alla fine del file della
prima. Con più connessioni, finché la prima è in corso la seconda prende
solo una connessione libera: non toglie la TV a chi guarda un altro
canale per minuti che la prima sta già registrando.

### Conversione in MP4

Una registrazione finita si converte in MP4 (il browser e la TV LG ci si
spostano dentro). Prima bastava un solo frame audio difettoso, normale nel
flusso di un provider, per lasciarla in MPEG-TS, e il log indicava la
causa sbagliata.

- La registrazione si salva come finita prima della conversione: Ferma
  risponde subito, e un riavvio durante la conversione non la fa
  ripartire.
- Se la copia non riesce, si riprova una volta convertendo solo l'audio
  in AAC (160 kbit/s); il video resta copiato. Nel log va la prima riga
  di ffmpeg che nomina un errore.
- Il TS si cancella solo se l'MP4 è plausibile: almeno metà del TS e,
  quando ffprobe c'è e la durata è nota, almeno l'80% della durata.
  Altrimenti resta il TS.
- A ogni avvio le registrazioni finite rimaste in TS (versioni precedenti,
  conversioni fallite, riavvii) si riconvertono, una alla volta.
- La conversione parte solo se sul disco c'è posto per una seconda copia
  (oltre al giga che resta sempre libero): l'MP4 si scrive accanto al TS,
  e riempire il disco fermerebbe le registrazioni in corso. Altrimenti
  resta il TS, e si riprova al prossimo avvio.

## Non ancora

- Vedere gli altri client dell'account: il provider non lo dice.
- Avvisare in un altro modo (notifica, e-mail).
- Ricordare le conversioni non riuscite: una TS che non passa il
  controllo si riprova a ogni avvio.
