# 0018 — Player e formati di consegna (browser, APK, EXE)

Stato: **In parte approvata** (29 ottobre 2026): diretta con pausa (punto 2) scelta dall'utente e fatta; APK ed EXE da decidere

## Contesto

La prima prova dell'utente in Chrome (registrazione e diretta) ha mostrato:
- mancavano i comandi a schermo (play/pausa, indietro, chiudi);
- il video MPEG-TS si fermava e ripartiva solo premendo un tasto.

Il browser è il player più limitato: non legge MPEG-TS (serve mpegts.js su
Media Source Extensions), né MKV, AC-3, DTS e spesso HEVC; blocca l'audio
automatico finché l'utente non interagisce; non controlla il buffer come un
player nativo. Le app APK ed EXE previste non devono ereditare questi
limiti.

## Interventi già fatti nel browser

- Player MPEG-TS comune (`common.js`): buffer di avvio più ampio, senza
  inseguimento aggressivo della diretta (salta in buchi del buffer) e con un
  controllo che scavalca i buchi e riprende una pausa non chiesta
  dall'utente.
- Comandi a schermo cliccabili e da telecomando: TV (Menu, Canali, −10 s,
  Pausa, +30 s, In diretta / Torna in diretta, Preferito, Registra) e player
  di film, serie e registrazioni (Chiudi, −10 s, Pausa, +30 s, Torna in
  diretta).

## Proposta

### 1. Il server offre più "sorgenti" per ogni riproduzione

La sessione di riproduzione restituisce l'elenco di ciò che il server sa
fornire, con i codec; ogni client sceglie quello che sa riprodurre (stesso
schema del `PlaybackInfo` di Jellyfin):

| Sorgente | Per cosa | Chi la usa |
|---|---|---|
| **diretta** (file o flusso così com'è) | film, episodi, diretta TS | APK, EXE, browser per gli MP4 |
| **HLS** (segmenti, senza ricodifica) | diretta con pausa e riavvolgimento, registrazioni in corso | browser (hls.js), webOS (nativo), APK, EXE |
| **conversione** (ffmpeg, solo audio o video incompatibili) | MKV/AVI/HEVC per il browser | solo browser |

### 2. Diretta con pausa come su Sky: HLS con buffer sul server

Per ogni canale guardato, il server riconfeziona il flusso in HLS senza
ricodifica e tiene un buffer su disco (proposta: 2 ore). Si può mettere in
pausa, tornare indietro e tornare in diretta; il cambio canale chiude il
buffer precedente; una registrazione dello stesso canale usa gli stessi
segmenti.
- Costo: un ffmpeg in copia per canale guardato (CPU minima), circa 4 GB per
  2 ore di un canale HD, latenza di 4–8 secondi rispetto al flusso diretto.
- In alternativa resta il flusso diretto (latenza minima, pausa solo nel
  buffer del player, come oggi).

### 3. Player per piattaforma

| Piattaforma | Player | Perché |
|---|---|---|
| Browser, LG webOS | `<video>` + hls.js (webOS: HLS nativo), mpegts.js di riserva | unica scelta possibile nel browser |
| **APK Android (telefono e TV)** | **Media3 / ExoPlayer** nativo | legge MPEG-TS, HLS, MP4, MKV; decodifica hardware; AC-3/DTS in passthrough o con estensione ffmpeg; D-pad del telecomando |
| **EXE Windows** | **interfaccia web in WebView2 + libmpv per il video** (lo schema di Jellyfin Media Player) | mpv legge tutto (MKV, HEVC, AC-3, DTS, sottotitoli) con decodifica hardware; l'interfaccia resta una sola |

Alternativa per l'EXE: solo WebView2, più semplice, ma con gli stessi limiti
di codec del browser (servirebbe la conversione sul server).

### 4. Comandi comuni a tutti i player

Play/pausa, −10/+30 s, chiudi, barra di avanzamento, scelta della traccia
audio e dei sottotitoli, episodio successivo; in diretta: pausa, riavvolgi,
torna in diretta. Lo stesso schema di tasti del telecomando su tutte le
piattaforme.

## Domande per l'utente

1. Diretta con **pausa e riavvolgimento** (HLS con buffer sul server) o
   flusso diretto con latenza minima?
2. EXE: **WebView2 + libmpv** (consigliato) o solo WebView2?

## Diretta con pausa e catch-up: com'è stata fatta

- Il server apre il canale una volta (stessa connessione e stesse regole
  della diretta) e ffmpeg lo scrive in segmenti HLS da 2 s (video
  copiato, audio AAC), con l'ora di ogni segmento. Finestra predefinita
  2 ore (10–360 minuti, Impostazioni → Sistema → Diretta TV). Un buffer
  per dispositivo: cambiando canale, aprendo un film o lasciando la
  pagina si cancella. Player: hls.js 1.7.3 (o l'HLS del dispositivo).
- Misure sul server di prova (Rai 1 HD): pronto in 0,9–1,4 s, ffmpeg ~42 MB di
  memoria e CPU trascurabile, circa 1,8 GB di disco all'ora.
- "Ritardo minimo" resta disponibile (flusso diretto, mpegts.js).
- Catch-up: archivio del provider (50 canali nell'account di prova, quasi tutti 7 giorni)
  con l'ora nel fuso del server Xtream (server_info.timezone); si usa
  la variante del canale che ha l'archivio. Riprodotto come un film
  (remux in MP4, salti chiedendo l'archivio dal minuto giusto): circa
  3 s per partire. La guida conserva 7 giorni di programmi passati.
- Catch-up nel browser da 0.6.16: il player della TV LG G4 non reggeva
  l'MP4 a frammenti (buffer fermo a ~10 s, poi blocco o MediaError 3, a
  volte audio senza video). Con FFmpeg 6.1 o più recente la sessione ha
  `ts: true` e il browser chiede `?ts=1`: il server manda MPEG-TS (video
  copiato, audio AAC) a velocità di riproduzione, con `-readrate 1` e una
  scorta iniziale (`-readrate_initial_burst`) pari ai secondi saltati più
  30, e il browser lo riproduce con mpegts.js come le registrazioni. Il
  ritmo serve perché mpegts.js smette per sempre di caricare a buffer
  pieno e il flusso non si può richiedere da un byte: per lo stesso motivo
  la ripresa da una pausa con più di 60 s già caricati davanti, o un salto
  a più di 60 s dalla fine di quanto caricato, riapre il flusso da lì. Con
  un FFmpeg più vecchio (Debian 12 ha la 5.1) e nell'app Android resta
  l'MP4.
- "Dall'inizio" in TV: dentro il buffer se c'è, altrimenti archivio.
