# 0049 — Backup: profili compresi, registrazioni recenti tenute

Stato: **Proposta** (6 novembre 2026, da una revisione di registrazioni e backup)

## Il problema

Il backup con password è la strada per ripartire su un'installazione
nuova quando il disco si rompe. Ma non conteneva i profili: dopo il
ripristino restava solo "Principale", senza PIN, il profilo bambini
spariva, e il primo profilo creato dopo prendeva preferiti e "continua a
guardare" di chi aveva quell'id prima. Anche i titoli tolti da "continua a
guardare" si perdevano. Le registrazioni tornavano senza file (non si
potevano guardare). Ripristinando la copia di ieri sullo stesso server, le
registrazioni fatte oggi sparivano dalla lista, con i loro GB ancora sul
disco; i download in lista restavano agganciati a numeri di titolo che
dopo la nuova sincronizzazione appartengono ad altri film.

## Decisione

- Il backup contiene i profili (nome, tipo, PIN cifrato con hash, colore,
  icona) e i titoli tolti da "continua a guardare". Il ripristino
  sostituisce i profili con quelli del backup; il profilo 1 resta sempre.
  Ogni dispositivo sceglie di nuovo chi guarda (un profilo scelto prima
  del ripristino non vale più). Un dispositivo bloccato su un profilo (il
  tablet dei bambini) resta bloccato sul profilo con lo stesso nome e
  tipo; se la copia non lo ha, il dispositivo va abbinato di nuovo: non
  resta mai libero né finisce su un altro profilo che ha preso lo stesso
  numero.
- Ogni registrazione porta il nome del suo file. Al ripristino una
  registrazione punta solo a un file della cartella delle registrazioni
  di questo server; se il file non c'è, diventa "non riuscita" con la
  nota "file della registrazione non trovato dopo il ripristino".
- Le registrazioni finite che non sono nel backup e il cui file è ancora
  nella cartella restano nella lista.
- I download restano e ritrovano il loro titolo dopo la sincronizzazione
  (per abbonamento e codice del provider).
- I backup fatti prima restano ripristinabili: i profili restano quelli
  che ci sono, e una registrazione ritrova il suo file se sullo stesso
  server c'è ancora la stessa registrazione.

## Non ancora

- Dispositivi abbinati e accessi con account di casa: si abbinano di nuovo.
- Riagganciare il download di un episodio prima che la serie venga
  riaperta.
