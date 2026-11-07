# 0052 — Immagini: sfondi, loghi dei titoli, segnaposto

Stato: **Approvata** (7 novembre 2026: "Procedi ai punti elencati come
meglio credi")

## Il problema

Il motivo più citato per cui la gente ama Plex, Streamyfin, Moonfin e il
tema Ultrachromic di Jellyfin è l'immagine: uno sfondo grande con
dissolvenza e il logo del titolo al posto del testo. In VodMax:

- lo sfondo in alto c'è solo in Home, e finché TMDB non risponde è la
  locandina sfocata; Film, Serie e l'elenco TV cominciano con un titolo
  di pagina e basta;
- il dettaglio di un film o di una serie ha locandina, testo e pulsanti,
  senza cast con le foto né titoli simili;
- un canale senza logo mostra l'icona dell'immagine rotta nella guida e
  nell'elenco TV;
- TMDB viene già interrogato per locandina e sfondo, ma non per il logo
  del titolo (`images`, che la stessa chiamata può restituire).

## Decisione

- La chiamata a TMDB chiede anche `images` (loghi in italiano, poi
  inglese, poi senza lingua) e `external_ids`; il titolo porta `Logo`,
  salvato nella stessa cache dei metadati. Nessun servizio in più,
  nessuna chiave in più.
- **Sfondo con dissolvenza** in Home, Film, Serie e nell'elenco TV
  ("in onda ora": l'immagine del programma o il logo del canale su
  sfondo colorato), con lenta panoramica ("Ken Burns") quando gli effetti
  sono attivi. Il logo del titolo sostituisce il testo dell'intestazione
  quando c'è; il testo resta come alternativa e per chi legge con uno
  screen reader.
- **Dettaglio**: sfondo a tutta pagina con sfumatura, logo o titolo,
  pillole di anno, durata, genere e voto, cast con le foto di TMDB
  (`profile_path`), riga "Titoli simili" presa dal catalogo di casa per
  generi in comune (niente chiamate in più).
- **Segnaposto** per un canale senza logo: una tessera colorata con le
  iniziali del nome, colore derivato dal nome, uguale in guida, elenco
  TV, zapping e multiview. Per un titolo senza locandina resta la tessera
  con il titolo, con lo stesso stile.
- Il modulo "Stasera" lascia il posto fra lo sfondo e "Continua a
  guardare": diventa un riquadro compatto in fondo alla Home, con la
  stessa funzione.

## Non ancora

- Voti di Rotten Tomatoes, IMDb o Metacritic: servono account
  ([0055](0055-voti-esterni.md), respinta).
- Immagini dei programmi TV nella guida: la guida XMLTV le porta di rado;
  si userà `icon` quando c'è, senza cercarle altrove.
