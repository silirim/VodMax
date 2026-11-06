# 0036 — Indice delle persone: "un film con…" senza fidarsi della memoria del modello

Stato: **Approvata** (31 ottobre 2026: "Procedi come meglio credi", dopo la
valutazione su indice e addestramento)

## Il problema

Chiesto "un film con catrin z jones", l'assistente ha proposto *Troy*, dove
Catherine Zeta-Jones non c'è. Dalla 0.6.0-beta.5 VodMax controlla il cast
nella scheda di ogni titolo che il modello nomina, quindi non propone più
titoli sbagliati. Ma ne trova pochi: il modello ricorda uno o due titoli
per persona (*Chicago*), mentre nel catalogo ce ne sono di più (*Ocean's
Twelve*, *La maschera di Zorro*, *Entrapment*).

## Decisione

- **Indice delle persone**: una tabella `item_people` (migrazione 19) con
  cast e regia di ogni film e serie, scritti come li riporta la scheda
  (provider, TMDB o `.nfo` delle raccolte), in minuscolo e senza accenti.
  Non va nel backup: si ricostruisce da solo.
- **Si riempie**:
  - ogni volta che qualcuno apre una scheda (nessuna richiesta in più);
  - di notte, dall'1 alle 7, solo se l'assistente è acceso: una scheda
    ogni 3 secondi per i titoli Xtream (circa 7.000 a notte, per non
    pesare sul provider), subito per le raccolte locali. Le richieste
    passano dalla VPN dell'abbonamento, come quando si apre una scheda.
    Una scheda senza cast viene richiesta di nuovo dopo 30 giorni, e
    così una scheda che VodMax non riesce a leggere (6 novembre 2026: prima
    fermava l'indice ogni notte sullo stesso titolo). Se il provider non
    risponde, l'indice si ferma fino alla notte dopo; l'errore vero è nel
    registro.
- **Nell'assistente**: il modello fa solo quello in cui è bravo, capire la
  persona chiesta anche scritta male. VodMax cerca nell'indice tutti i suoi
  titoli che hai, li mette per primi, poi aggiunge quelli che il modello
  nomina e che passano il controllo della scheda (0.6.0-beta.5).
- In Impostazioni → Assistente si vede a che punto è l'indice ("cast noto
  per N titoli su M").

## Addestrare un modello? Non ora

- Per i fatti (chi recita in cosa) addestrare è lo strumento sbagliato:
  costa, invecchia a ogni film nuovo e il modello sbaglia con più
  sicurezza. I dati giusti sono già nelle schede.
- Potrebbe servire più avanti per rendere affidabili i modelli piccoli sul
  processore (regole e formato delle risposte), con un affinamento LoRA
  sulla A2000. Prima va raccolta una serie di 50–100 richieste reali per
  misurare dove sbagliano.

## Non ancora

- Cercare per genere, paese o regista nella guida TV (i programmi della
  serata non hanno scheda).
- Una pagina "Persone" nell'interfaccia.
