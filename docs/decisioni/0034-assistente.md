# 0034 — Assistente: regole a parole e "cosa guardo stasera"

Stato: **Approvata** (31 ottobre 2026: "Parti dalle prime due")

## Decisione

- Un modello linguistico **scelto dall'amministratore**, spento di default:
  qualsiasi server con l'API compatibile OpenAI (`/chat/completions`):
  Ollama, LM Studio, llama.cpp in casa, oppure un servizio su internet con
  la sua chiave (cifrata, mai mostrata). Impostazioni → Sistema →
  Assistente, con una prova.
- **Regola da una frase** (pagina Registrazioni, nuova regola): "registra le
  gare di Formula 1 in diretta, non le qualifiche" → il modello riceve i
  campi della regola e i nomi dei canali e risponde in JSON; VodMax compila
  il modulo con gli stessi controlli di una regola scritta a mano e mostra
  l'anteprima. **Non salva niente**: salva la persona.
- **Cosa guardo stasera** (Home, se l'assistente è acceso): il modello
  riceve i programmi della serata sui canali del profilo (prima delle 18
  dalle 20:00 alle 00:30, non quello che va in onda la mattina; dopo, da
  adesso a fine serata) (al
  massimo 60, righe brevi: circa 2000 token con la libreria, letti in ~40 s da un modello piccolo su CPU; prima quelli in onda e di prima serata) e i titoli della
  libreria (iniziati, in lista, di tendenza), ognuno con un codice. Sceglie
  fino a 5 codici con un motivo: **un codice che VodMax non ha dato viene
  scartato**, quindi non può proporre titoli inventati. Un programma in onda
  si apre, uno più tardi si registra (con conferma), un film o una serie
  apre la scheda. Risposta tenuta 20 minuti per profilo e richiesta.
- Il profilo bambini lo dice al modello e vede comunque solo i suoi canali.
- Al modello non vanno mai credenziali, indirizzi del provider o dati dei
  dispositivi: solo guida, nomi dei canali e titoli. Con un modello in casa
  nulla esce dalla rete.
- Server che non accettano `response_format` ricevono la stessa domanda
  senza; la risposta JSON viene cercata anche dentro testo o blocchi di
  codice.

## Non ancora

- Le altre idee: ricerca a parole nel catalogo, "nella puntata precedente",
  pulizia del catalogo, diagnosi dei problemi, avvisi intelligenti.
- Un container Ollama nella rete di casa (sulla P4000): solo con un via
  libera esplicito.

## Aggiornamento del 4 novembre 2026: generi nel catalogo

Su richiesta dell'utente ("la AI cerca solo nei canali live o anche nei
vod? se cerco ad esempio 'voglio vedere un film thriller' cosa mi da?",
poi "si" alla proposta): per un genere o un'atmosfera ("un thriller",
"qualcosa che faccia paura") **Cosa guardo stasera** cerca anche in tutto il
catalogo, non solo fra i titoli iniziati, la propria lista e le tendenze.
Il modello indica le parole del genere e alcuni titoli noti; VodMax cerca
quei titoli nel catalogo e prende i titoli meglio votati delle categorie
del provider che portano quel nome (una parola intera: "Thriller",
"Horror"), alcuni da ognuna, diversi ogni sera. Se il provider non ha
categorie per genere restano i titoli che il modello nomina e che il
catalogo ha. I generi delle schede TMDB non sono ancora usati.
