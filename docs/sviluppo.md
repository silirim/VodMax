# Sviluppo

Struttura del codice, avvio del server dai sorgenti e API HTTP. Regole,
verifiche e procedura di rilascio sono in [AGENTS.md](../AGENTS.md);
principi, scenari e direzione in [direzione.md](direzione.md).

## Struttura del codice

| Pacchetto | Cosa fa |
|---|---|
| `internal/vpn` | Lettura dei file WireGuard `.conf`; tunnel WireGuard in user space con stack di rete proprio. DNS compreso, tutto resta nel tunnel. |
| `internal/provider` | L'unico client HTTP ammesso verso i provider: si collega solo tramite il tunnel, ignora i proxy di sistema, toglie le credenziali dagli errori. |
| `internal/playlist/m3u` | Lettura di playlist M3U estese (`tvg-*`, `group-title`, catch-up, `#EXTVLCOPT`). |
| `internal/playlist/xtream` | Client Xtream Codes: account, diretta, film, serie, episodi, URL di riproduzione e catch-up. |
| `internal/playlist/stalker` | Client dei portali Stalker/Ministra (MAC): handshake, profilo, generi, canali, link di riproduzione. |
| `internal/tmdb` | Schede da TMDB con la chiave dell'utente: titoli italiani, trame, cast, immagini. |
| `internal/secret` | Cifratura AES-256-GCM delle credenziali con chiave locale (`secret.key`, 0600). |
| `internal/store` | SQLite: lettori e scrittore separati (WAL), id stabili, import "tutto o niente" con protezione dai cali anomali, ricerca FTS5 senza accenti, paginazione a cursore. |
| `internal/catalog` | Conversione delle playlist in catalogo; titoli puliti (anno separato, tag di qualità tolti), separatori scartati. |
| `internal/service`, `internal/api` | Logica e API HTTP `/api/v1`, protetta da token: VPN, abbonamenti, catalogo, TV e guida, film e serie, registrazioni e regole, profili e dispositivi, accesso con account di casa, download, backup, aggiornamenti, uscita verso altre app, conversione con la scheda grafica, Comskip e l'assistente (`assistant*.go`: modi integrato, online e server; `people.go`: indice delle persone). |
| `internal/library` | Raccolte locali: titoli e anni dai nomi dei file, episodi (`S01E03`, `1x03`, `Stagione N`), `.nfo` e immagini accanto ai video. |
| `internal/lineup` | Riconoscimento dei canali nazionali e numerazione LCN ufficiale. |
| `internal/epg` | Lettura in streaming delle guide XMLTV. |
| `internal/web` | Interfaccia da telecomando: menu (`/`: Home, Film, Serie, Registrazioni), TV (`/tv.html`), guida (`/guide.html`), multiview (`/multiview.html`), Impostazioni (`/settings.html`); HTML/JS senza framework, mpegts.js 1.8.2 incluso, testi tradotti da `static/i18n/`. Un link `/#token=…` salva il token di accesso; se il browser ne ha già uno che funziona, chiede prima di sostituirlo, dicendo i nomi dei due dispositivi; il token di amministrazione si scambia subito con un dispositivo ([0044](decisioni/0044-token-di-amministrazione-fuori-dal-browser.md)). |
| `clients/android`, `clients/webos` | App Android (WebView sull'interfaccia del server, lettore Media3, server interno per l'uso da solo) e avvio per TV LG webOS. |
| `cmd/vodmax-server` | Il server. |
| `cmd/vodmax-probe` | Strumento di prova: `.conf`, tunnel, login Xtream. |

## Avvio del server

```sh
go build ./cmd/vodmax-server
./vodmax-server -data ./vodmax-data -listen 127.0.0.1:8095
```

Al primo avvio crea in `-data` il database, la chiave di cifratura
(`secret.key`) e il token di amministrazione (`admin.token`). Le chiamate
`/api/v1` richiedono `Authorization: Bearer <token>`, con il token di un
dispositivo abbinato o `admin.token`, e quasi tutte il profilo scelto in
`X-Profile`; sono aperte `/api/v1/health`, l'abbinamento (`/api/v1/pair`) e
l'accesso con account (`/api/v1/sso`, `/auth/sso`). `/stream`, `/out` e
`/img` hanno il permesso nell'indirizzo (ticket, sessione o chiave).
La tabella elenca le chiamate principali; l'elenco completo, con il livello
di accesso di ognuna, è in `internal/api/api.go`. Gli errori sono
`{"error": "…"}` nella lingua della richiesta; il testo di un errore
inatteso (500) arriva solo a un profilo amministratore, gli altri leggono
un messaggio generico e il dettaglio resta nel log del server. Una
richiesta di visione superata da una più recente dello stesso player
(stesso `viewer`: zapping veloce) risponde 410 e non segna il canale come
guasto; il client la ignora.

| Metodo e percorso | Scopo |
|---|---|
| `POST /api/v1/vpn` `{name, config}` | Registra una VPN WireGuard (testo `.conf`) |
| `POST /api/v1/vpn/{id}/connect` · `/disconnect` | Collega o scollega il tunnel |
| `GET`/`PUT /api/v1/settings/country` `{home}` | Il paese scelto al primo avvio (codice ISO, "" finché non scelto); `GET` dà anche i paesi tra cui scegliere. Vale per gli abbonamenti senza un paese proprio nelle regole (0043) |
| `GET`/`PUT /api/v1/settings/network` `{vpn_only}` | "Solo tramite VPN" (predefinito `true`); con `false` un abbonamento può avere `vpn_id: 0` e usare la rete normale (0039) |
| `POST /api/v1/subscriptions` `{name, kind, vpn_id, server, username, password}` o `{…, url}` | Aggiunge un abbonamento Xtream o M3U. `url` può essere il link di condivisione di un file di Google Drive o Dropbox: salvato com'è, diventa un link di download a ogni sincronizzazione e passa dalla VPN dell'abbonamento come ogni playlist; per un file grande di Drive si segue una volta la pagina di conferma. Cartelle, documenti Google e OneDrive si rifiutano al salvataggio (0057). Un link Xtream Codes (`…/get.php` o `…/player_api.php` con `username` e `password`), dato come `url` di una M3U o come `server`, si salva come `kind: "xtream"`: `server` resta l'indirizzo senza il file e la query, utente e password si prendono dal link (vincono sui campi) e la password va nel segreto sigillato; con `***` al posto dei dati (il link mascherato di `GET`) si rifiuta. La risposta è solo `{id}`: il tipo salvato si legge da `GET` |
| `POST /api/v1/subscriptions` `{name, kind: "local", path, content: movie\|series}` | Aggiunge una raccolta locale (cartella del server, senza VPN) |
| `PATCH /api/v1/subscriptions/{id}` `{name, vpn_id, server, username, password, url, mac}` o `{name, path, content}` | Modifica un abbonamento o una raccolta. I campi assenti o `null` restano come sono; `password`, `url` e `mac` vuoti tengono quelli salvati. Un link completo in `server` si separa come all'aggiunta. Quello salvato da una versione precedente torna da `GET` mascherato (`username=***&password=***`): rimandato così se ne tiene solo l'indirizzo, e utente e password restano quelli dei campi o salvati. Il tipo non si sceglie: una M3U con un link Xtream Codes (`…/get.php` o `…/player_api.php` con `username` e `password`) diventa `xtream` quando il link arriva in `url`, oppure, con il link già salvato, se non ha mai sincronizzato o se l'ultima sincronizzazione è finita con il rifiuto della playlist da parte del pannello (una risposta HTTP fuori dallo standard, come 884: è l'unico errore che dice di salvare). Con un altro errore (VPN, 401, 5xx) resta M3U, perché con il tipo cambiano gli id delle categorie e se ne perderebbero le correzioni |
| `POST /api/v1/subscriptions/file?name=&vpn_id=&file=` (corpo: il file) | Aggiunge una playlist M3U da un file (0057): al massimo 64 MB, controllati mentre arriva; letto con lo stesso parser di una playlist scaricata, rifiutato senza salvarlo se non lo è. Il file resta gzip e cifrato con `secret.Box` nella tabella `playlist_file`, non si restituisce mai (in `settings`: `file`, `size`, `uploaded`), entra nei backup e si cancella con l'abbonamento. La sincronizzazione lo rilegge senza contattare nessuno, nemmeno la VPN; quella notturna lo salta, salvo una prima lettura mai fatta o interrotta. Un backup con i file può superare i 64 MB: il ripristino ne accetta fino a 256 |
| `PUT /api/v1/subscriptions/{id}/file?file=` (corpo: il file) | "Sostituisci il file": il file prende il posto dell'indirizzo o del file di prima e la sincronizzazione parte subito (202, come `/sync`), senza il controllo sul calo anomalo. Salvare un indirizzo con `PATCH` toglie il file |
| `GET /api/v1/folders?path=` | Cartelle del server, per scegliere una raccolta |
| `GET /api/v1/subscriptions` | Abbonamenti e raccolte con conteggi e stato della sincronizzazione: `syncing`, `sync_phase` (tradotta), `sync_count` e, quando si conosce, `sync_total` ("Salvo il catalogo (45.000 / 220.000)"), `sync_started`, `sync_seconds`; `sync_interrupted` per una lasciata a metà da un processo chiuso, anche durante la selezione che mostra i titoli nuovi (`synced_at` cambia solo dopo di essa): con la sincronizzazione automatica attiva la manutenzione la riprende dopo 30 minuti per tentativo, anche di giorno, al massimo tre volte; `last_sync`, il risultato (o `error`) dell'ultima finita da questo processo |
| `POST /api/v1/subscriptions/{id}/sync` | Avvia la sincronizzazione in background e risponde subito (202) con l'abbonamento come in `GET`, anche se ne era già in corso una; `?wait=true` aspetta la fine e risponde con il risultato (409 se già in corso); `?allow_drop=true` accetta un calo anomalo. Mentre una gira, eliminare l'abbonamento o ripristinare un backup risponde 409 |
| `GET /api/v1/catalog/{live\|movie\|series}` | Catalogo selezionato, una scheda per titolo: `q`, `section` (tv, sport, radio), `category` (con `category_subscription`, l'abbonamento della categoria: gli id si ripetono tra abbonamenti), `subscription`, `sort=recent`, `cursor`, `limit`, `all=true` per vedere anche l'escluso |
| `GET` · `PUT /api/v1/subscriptions/{id}/rules` | Regole di selezione (il proprio paese `home`, altri paesi, adulti, SD, anno minimo; 0042) |
| `POST /api/v1/subscriptions/{id}/rules/preview` | Effetto di nuove regole senza salvarle |
| `GET /api/v1/subscriptions/{id}/selection` | Conteggi: offerti, visibili, schede, per sezione |
| `GET /api/v1/subscriptions/{id}/categories?kind=` | Categorie con classificazione, correzioni e motivo dell'esclusione |
| `PUT /api/v1/subscriptions/{id}/categories/{kind}/{categoria}` | Correzione manuale: `enabled`, `section`, `country`, `adult` (`null` = automatico) |
| `PUT /api/v1/items/{id}/hidden` | Nasconde o ripristina un singolo titolo o canale |
| `GET /api/v1/items/{id}/variants` | Le versioni dello stesso titolo, migliore per prima |
| `GET /api/v1/items/{id}/details` | Scheda di un film o una serie: trama, cast con foto (`people`), logo del titolo, sfondo, trailer, fonti |
| `GET /api/v1/items/{id}/similar` | "Titoli simili": fino a 12 titoli dello stesso tipo del catalogo con più categorie in comune (poi voto e novità), stesse schede del catalogo; mai il titolo stesso o le sue versioni |
| `GET /api/v1/tv/channels` | Canali in ordine da telecomando con numero, ora e a seguire: `section`, `list=favorites` |
| `GET /api/v1/tv/channels/{id}/guide` | Guida di un canale (`hours`, massimo 72) |
| `PUT /api/v1/tv/favorites/{id}` `{favorite}` | Aggiunge o toglie un canale dai preferiti |
| `POST /api/v1/subscriptions/{id}/guide/sync` | Aggiorna la guida (fatto anche a ogni sincronizzazione); una guida vuota non cancella i programmi in arrivo e si riprova dopo un'ora |
| `POST /api/v1/live/{id}/ticket` `{viewer}` | Permesso di visione monouso (30 s) → `{url}` |
| `GET /stream/live/{permesso}` | Il flusso MPEG-TS ritrasmesso dal server (`?force=1` per prendere il controllo della connessione) |
| `GET /api/v1/series/{id}/episodes` | Episodi di una serie, con avanzamento |
| `GET` · `PUT /api/v1/progress/{movie\|episode}/{id}` | Avanzamento (`position`, `duration` in secondi) |
| `GET /api/v1/continue` · `DELETE …/{id}` | Continua a guardare; togliere un film o una serie (id del titolo) finché non lo si riguarda |
| `POST /api/v1/vod/{movie\|episode}/{id}/session` `{viewer, mode?, force?}` | Sessione di riproduzione: `direct` (MP4, intervalli) o `remux` (ffmpeg, `?t=` secondi). Con la connessione occupata da un altro dispositivo risponde 409 (423 se la tiene una registrazione); `force: true` la prende per l'analisi del file, e il lettore web carica poi il video con `force=<valore>`: un valore nuovo a ogni conferma, che il server rispetta una volta sola (le richieste successive dello stesso indirizzo chiedono come tutti) |
| `GET` · `POST /api/v1/recordings` | Registrazioni; `{channel_id}` registra il programma in onda, oppure `start`/`stop` esatti |
| `DELETE /api/v1/recordings/{id}` · `POST …/{id}/ticket` | Annulla o elimina; permesso di visione: `url`, `duration` (secondi reali, non la fascia programmata), `ts` (file MPEG-TS: ci si sposta riaprendo `url?t=secondi`, anche a registrazione finita; convertita in MP4 mentre la si guarda, `url?t=` risponde con il file MP4 intero e il lettore prosegue da lì come con un file normale), `recorded` (secondi già registrati, solo se in corso; `?t=live` dalla fine) |
| `POST /api/v1/recordings/{id}/stop` | Ferma una registrazione in corso tenendo quanto registrato |
| `GET` · `POST /api/v1/downloads` `{media: movie\|episode\|season\|series, id, season?}` | Download sul disco del server ([0025](decisioni/0025-download.md)), profili non bambini: l'elenco con stato e avanzamento; `POST` mette in coda e risponde `{added, already}` |
| `POST /api/v1/downloads/{id}/pause` · `…/resume` · `DELETE …/{id}` | Mette in pausa, riprende o elimina un download (con i suoi file) |
| `GET` · `PUT /api/v1/settings/downloads` `{night_only, from_hour, to_hour}` | Fascia oraria dei download (`PUT` solo amministratori) |
| `POST /api/v1/save/{movie\|episode\|recording}/{id}` | Copia sul dispositivo ([0056](decisioni/0056-download-sul-telefono-e-usb.md)), profili non bambini di un dispositivo abbinato: un titolo della raccolta, un download finito o una registrazione finita → `{url, name, mime, size, etag}`. `name` ha al più una cartella (`Serie (Anno)/…`, `Registrazioni/…`). 409 se non è un file del server, se è in conversione o non è un video delle raccolte; 403 con il token di amministrazione; 400 nel dispositivo da solo |
| `GET /stream/save/{token}` | Il file da copiare, valido 48 ore: `Range`, `If-Range` e `If-Match` sull'`ETag` forte, `HEAD`. 404 se il link è falso o scaduto, se il dispositivo è stato tolto o il profilo ha cambiato PIN; 410 se il file sul server è cambiato o non c'è più. Il token non va nei log |
| `GET /img/logo/{id}?s=firma` | Logo del canale scaricato tramite VPN e in cache (indirizzo firmato, fornito dall'elenco dei canali) |
| `GET` · `PUT /api/v1/settings/assistant` | Assistente: modo (`local`, `online`, `server`), modello, chiave; stato dell'integrato e dell'indice delle persone |
| `POST /api/v1/settings/assistant/local` `{model}` · `…/local/cancel` · `DELETE …/local` | Installa, annulla o rimuove l'assistente integrato |
| `POST /api/v1/settings/assistant/models` · `…/test` | Modelli offerti da un servizio; prova dell'assistente |
| `POST /api/v1/assistant/rule` `{text}` · `POST /api/v1/assistant/tonight` `{wish}` | Una regola da una frase (non salvata); proposte per stasera. 502 quando il modello o il suo servizio non danno una risposta usabile, 504 oltre il tempo massimo ([0050](decisioni/0050-assistente-serata-e-tempi.md)) |
| `POST /api/v1/debug/player` `{event, detail}` | Un errore del player del browser, scritto nel log del server; tutti gli eventi solo da un browser con `localStorage` `vodmax.playerDebug` = `true` |

## Test

Serve Go (versione indicata in `go.mod`).

```sh
go vet ./...
go test -race ./...
```

I test del tunnel creano due VPN interne nello stesso processo e verificano
che il traffico passi dal tunnel, che a VPN chiusa non parta nessuna
connessione e che un server VPN irraggiungibile non causi fughe di traffico.
`packaging/update_test.go` esegue `vodmax-update` con curl e server finti
(la scelta della versione, senza rete né installazione).
Per prove manuali e schermate senza chiave TMDB, `VODMAX_TMDB_BASE=http://…`
fa parlare il server con un TMDB finto (solo QA, non è una funzione).

L'app Android si compila con i suoi test JVM (anche in CI, job `android`):

```sh
cd clients/android
./gradlew assembleDebug testDebugUnitTest
```
