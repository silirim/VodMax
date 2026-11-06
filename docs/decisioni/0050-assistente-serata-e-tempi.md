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

Una domanda all'assistente non aveva un tempo massimo: ogni chiamata al
modello poteva durare 5 minuti (con un nuovo tentativo per Claude), e
"stasera" ne fa due di seguito, più l'avvio del motore integrato (fino a
3 minuti). Un servizio online bloccato teneva la persona davanti a "sta
scegliendo…" per più di dieci minuti.

## Decisione

- Ogni canale porta al massimo due programmi: quello in onda adesso (solo
  dalle 18, quando la serata è adesso) e il suo programma principale,
  quello che comincia più vicino alle 21:15 (o ad adesso, se è più tardi):
  il film delle 21:30, non il quiz delle 20:30.
- Prima i canali preferiti del profilo, poi i numeri ufficiali, come
  nell'elenco dei canali. Restano al massimo 60 programmi, per i modelli
  piccoli.
- Una domanda (una regola, "stasera") ha un tempo massimo per intero:
  2 minuti con un servizio online, 6 con un modello in casa (integrato o
  server proprio), che sul processore legge lentamente. Una singola
  chiamata a un servizio online si ferma dopo 1 minuto. Allo scadere la
  persona legge "l'assistente non ha risposto in tempo: riprova o scegli
  un modello più veloce".
- Dietro un reverse proxy il tempo di lettura del proxy va alzato
  (nginx: `proxy_read_timeout`), come scritto nell'installazione.

## Non ancora

- Scegliere i canali dai gusti della persona (quello che guarda di più).
- Una risposta che arriva a pezzi mentre il modello scrive, per non
  dipendere dal tempo massimo di un proxy.
