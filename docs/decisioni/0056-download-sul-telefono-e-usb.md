# 0056 — Download sul telefono e su USB

Stato: **Approvata** (8 novembre 2026: "Ok per tutte e 3", dopo le
risposte alle domande riportate in fondo)

## Il problema

I download (decisione 0025) e le registrazioni restano sul server di casa.
Chi parte vuole portare con sé un film, qualche episodio o una
registrazione. Li vuole guardare senza rete:

- sul telefono, anche con altre app come VLC o la Galleria;
- sulla TV di una casa al mare o di un albergo, con un Fire TV o un box
  Android TV e una chiavetta USB o un disco esterno.

Oggi l'app sa salvare solo file piccoli: il salvataggio passa tutto il
file in memoria. Va bene per un backup, non per un film di 4 GB. Il
dispositivo non deve mai contattare il provider: in molte case solo il
server può farlo, e la connessione al provider spesso è una sola.

## La decisione

### Cosa si copia

- **Si copiano solo file già sul disco del server**:
  - titoli delle raccolte, compresi gli «Scaricati»;
  - download del server finiti;
  - registrazioni finite.
- Un titolo del provider si scarica prima sul server con **Scarica**
  (0025). Quando è pronto compare il pulsante della copia. Il provider non
  riceve richieste nuove e chi guarda non perde la connessione.
- Restano due passi: prima sul server, poi sul dispositivo.

### Pulsanti e chi li vede

- **Sul telefono** nell'app Android del telefono o del tablet.
- **Su USB** nell'app Android su Android TV, Google TV e Fire TV. Compare
  solo quando è collegata una chiavetta o un disco.
- Dove si trova il pulsante:
  - scheda di un film: se il film è già sul server;
  - scheda di una serie: apre una scelta con «Tutta la stagione N» e i
    singoli episodi già sul server; le card degli episodi non cambiano;
  - Registrazioni: accanto a Elimina, per quelle finite;
  - Download: sulle righe finite.
- Nessun profilo bambini, come per i download.
- Il pulsante non c'è:
  - nel browser;
  - sulla TV LG: l'app è una pagina web e non può scrivere su una
    chiavetta. Il lettore della TV LG legge però una chiavetta preparata
    altrove, per esempio da un Fire TV o da un PC;
  - nel dispositivo da solo (0021);
  - nelle app Android vecchie, senza i metodi nuovi del ponte.

  Dove manca, non lascia traccia.

### Dove vanno i file

- **Sul telefono**, per impostazione: nella cartella pubblica
  `Movies/VodMax`. Non serve scegliere niente.
- **Scegli cartella**: si sceglie un'altra cartella con la scelta di
  sistema di Android: una scheda SD, una chiavetta o un disco. La scelta
  resta finché non la si cambia. **Usa Movies/VodMax** torna alla cartella
  del telefono.
- **Sulla TV** non c'è una cartella predefinita: la memoria interna è
  piccola. La prima copia apre la scelta della cartella sulla chiavetta o
  sul disco. Poi la scelta resta.
- Da Android 11 la radice della memoria interna e di una scheda SD non si
  può scegliere. Si sceglie o si crea una cartella, per esempio `VodMax`.
- Nomi leggibili, uguali in ogni cartella:
  - `Titolo (Anno).mkv`
  - `Serie (Anno)/Serie S01E02 - Titolo.mkv`
  - `Registrazioni/Titolo - Canale 2026-11-08 2100.mp4`

  I nomi vengono da `fileName` (0025), che toglie già i caratteri vietati
  da FAT32 ed exFAT.
- Finché la copia non è finita, il file non si vede come un film: VLC, la
  Galleria e la TV non mostrano mezzo file.
- Sul telefono li aprono VLC, la Galleria e l'app File; su Fire TV VLC
  (da provare su Fire OS 8) o il lettore del Cube 3; sulla TV LG il suo
  lettore. Non servono VodMax né la rete. Restano anche se si disinstalla
  l'app, tranne nel ripiego per i box senza scelta della cartella (sotto).

### Su Fire TV e sui box Android TV

Quello che sappiamo viene da AFTVnews, in parte del 2023, non dalla
documentazione di Amazon. Va confermato con le prove a mano.

- Su Fire OS 8 (Android 11) la cartella si sceglie con l'app di sistema
  «Files», come fa TiviMate per le registrazioni. Secondo AFTVnews (2023)
  «Files» non è fatto per il telecomando e può servire una tastiera. Va
  provato.
- Su Fire OS il codice della chiavetta dentro il permesso può cambiare. In
  quel caso l'app dice «Cartella non raggiungibile: ricollega la chiavetta
  o scegli di nuovo la cartella».
- Fire TV vede solo la prima chiavetta o il primo disco collegato.
- Su Fire TV la chiavetta va formattata in FAT32; il Fire TV Cube di terza
  generazione legge anche NTFS. exFAT non risulta letto da Fire TV (da
  provare su Fire OS 8).
- In FAT32 un file non può superare 4 GB. Se un film è più grande, la
  copia si ferma con «Oltre 4 GB: la chiavetta è in FAT32. Su Fire TV
  serve NTFS su un Cube 3; altrove exFAT».
- Sul Fire TV Stick l'unica porta USB è quella dell'alimentazione. Serve un
  cavo OTG con alimentazione, e un disco esterno ha bisogno di un
  alimentatore suo.
- **Ripiego** (da provare): dove la scelta della cartella manca (Fire OS 7,
  cioè Android 9, e forse alcuni box Android TV) l'app usa la sua cartella
  sulla chiavetta, `Android/data/app.vodmax.client/files`. Non chiede
  permessi, ma Android la cancella quando si disinstalla VodMax. Prima
  della prima copia l'app lo dice: «Su questo box i file copiati si
  cancellano se disinstalli VodMax».

### Avanzamento: «Su questo dispositivo»

- La copia la fa VodMax, con un servizio che continua anche con l'app
  chiusa. Sul telefono una notifica mostra l'avanzamento.
- Sulle TV la notifica non si vede. Per questo la pagina Download ha una
  sezione **Su questo dispositivo**, anche sul telefono:
  - la cartella in uso e **Scegli cartella**;
  - **Copia anche su rete mobile**, spento all'inizio (vedi Regole);
  - le copie in coda, in corso e finite, con la percentuale e lo stato;
  - **Annulla** su una copia non finita: ferma la copia e cancella il
    pezzo copiato;
  - **Togli** su una copia finita o non riuscita: toglie solo la riga. Il
    file copiato resta; sul telefono si elimina con l'app File, su Fire TV
    con un PC o un gestore file di terzi.
- Tutto si usa con il telecomando.
- Se Android chiude l'app a metà, la copia riprende quando si riapre
  VodMax.

### Fuori casa

- La copia passa dall'indirizzo che l'app usa già, anche da un tunnel con
  login (0040). Il link della copia sta sotto `/stream`, che il login
  lascia già libero, quindi le regole del login non cambiano.
- L'app non segue i reindirizzamenti. Se il proxy manda al login o lascia
  libero `/stream` solo da alcuni paesi, la copia si ferma con «Server non
  raggiungibile da qui». Non salva mai una pagina di login al posto del
  film. Fuori dai paesi ammessi la copia va fatta prima di partire.
- Guardare i file copiati non chiede rete.

### Ripresa

- Se la rete cade, la copia aspetta e riprende da dove era arrivata.
- Anche un riavvio o un aggiornamento del server non la ferma, in casa e
  attraverso il tunnel: VodMax riprova da solo.
- Dopo 30 minuti senza dati la copia va «In pausa» e riprende alla
  prossima apertura dell'app.
- Il link vale 48 ore e funziona per chiunque lo abbia, anche da internet.
  Smette di valere se si toglie il dispositivo che lo ha chiesto, se
  cambia il PIN del profilo o se il dispositivo viene legato a un altro
  profilo.
- Se il link è scaduto, si tocca di nuovo il pulsante. Se il file sul
  server è lo stesso, la copia riprende dal pezzo già copiato.
- Se nel frattempo il file sul server è cambiato, la copia si ferma con
  «Il file sul server è cambiato» e cancella il pezzo copiato. Succede per
  esempio con una registrazione appena convertita in MP4. Si tocca di
  nuovo il pulsante per copiare il file nuovo.

### Regole

- **Wi-Fi**: per impostazione la copia va solo su reti non a consumo (Wi-Fi
  o cavo), mai in roaming. Altrimenti aspetta: «In attesa del Wi-Fi».
  Con **Copia anche su rete mobile** acceso (in «Su questo dispositivo»,
  vale per quel dispositivo) va anche su rete mobile, mai in roaming.
- **Galleria**: i file in `Movies/VodMax` sono visibili come gli altri
  video. Li vedono la Galleria, VLC e gli altri lettori; finiscono in un
  backup su cloud solo se lo si attiva per quella cartella.
- **Spazio**: prima di partire l'app guarda lo spazio libero, meno quello
  che serve alle copie già in coda nella stessa cartella. Se non basta lo
  dice e non parte.
- **Doppioni**: se lo stesso file è già in coda o in copia, l'app risponde
  «Già in copia». Se è già nella cartella, risponde «Già copiato». Da
  Android 10, in `Movies/VodMax`, dopo una reinstallazione i file copiati
  prima non si vedono più e il doppione non viene riconosciuto.
- **Registrazioni**: solo quelle finite. Mentre una registrazione viene
  convertita in MP4, la risposta è «In conversione, riprova tra poco».
- **Serie**: «Tutta la stagione N» copia gli episodi della stagione già
  sul server. Quelli mancanti si scaricano prima sul server con **Scarica
  la stagione N**.
- **Una copia alla volta** per dispositivo, nell'ordine in cui sono state
  chieste.

## Come funziona

### Server

Due rotte nuove, nessuna impostazione.

- `POST /api/v1/save/{movie|episode|recording}/{id}`, profili non bambini.
  - Cerca il file in quest'ordine: raccolta locale, poi la riga di
    download `done` con gli stessi `media` e `media_id`, oppure la
    registrazione `completed` e non in conversione. Se non lo trova
    risponde 409.
  - Accetta solo le estensioni di `library.VideoExt` (oggi `videoExt`, da
    esportare), le stesse delle raccolte. Le altre: 409.
  - Risponde `{url, name, mime, size, etag}`. Il nome viene da `fileName`
    e `withYear`, come in 0025.
  - Nel dispositivo da solo risponde 400. Con il token di amministrazione
    (dispositivo 0) risponde 403.
- `GET /stream/save/{token}`; in `ServeMux` `GET` risponde anche a `HEAD`.
  - Il token è firmato con HMAC (`s.box.Derive("save-file")`) e non sta in
    memoria. La firma copre tipo, id, dimensione e data del file,
    scadenza, `DeviceID` con la data di abbinamento (`created_at`),
    profilo con il suo `PINEpoch`.
  - A ogni richiesta:
    - il dispositivo deve esistere con la stessa data di abbinamento
      (SQLite riusa gli id); il profilo deve avere lo stesso `PINEpoch`,
      essere ammesso dal `profile_lock` del dispositivo e non essere un
      profilo bambini; altrimenti 404;
    - il file viene ritrovato; se dimensione o data non tornano, risponde
      410.
  - Serve il file con `http.ServeContent`, che gestisce `Range`,
    `If-Range` e `HEAD`.
  - Header: `ETag` forte da dimensione e data, `Content-Type` da una
    tabella esplicita estensione → `video/*` in `save.go` (non
    `mime.TypeByExtension`: senza `/etc/mime.types` non conosce nessun
    video, e con quello di Debian `ts` diventa
    `text/vnd.trolltech.linguist`), `Content-Disposition`,
    `Cache-Control: private, no-store`, `nosniff`.
  - Il percorso di un download resta dentro la cartella dei download.
  - **Il token non va nei log**: l'handler non passa mai da `a.fail`, che
    su un 500 scrive `r.URL.Path`. Risponde da solo 404 o 410; il 416 lo
    scrive `http.ServeContent`; un errore di lettura dà 500 e nel log
    vanno solo tipo e id.

### Android: la copia

Il `DownloadManager` di sistema non basta:

- non scrive in una cartella scelta con la scelta di sistema, né su una
  chiavetta o una scheda SD;
- con un 502 di cloudflared o un 530 di Cloudflare abbandona la copia e
  cancella il pezzo copiato;
- segue i reindirizzamenti, e salverebbe la pagina di login come film.

Per questo la copia la fa VodMax, in una classe nuova `CopyService`. Nessuna
libreria nuova.

- **Servizio in primo piano** di tipo `dataSync`.
  - Manifest: `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_DATA_SYNC`,
    `ACCESS_NETWORK_STATE` e il `<service>` con
    `foregroundServiceType="dataSync"`.
  - Da Android 13 si chiede `POST_NOTIFICATIONS` alla prima copia. Se la
    persona rifiuta, la copia va lo stesso; l'avanzamento resta nella
    sezione «Su questo dispositivo».
  - Il servizio parte solo con l'app aperta, come chiede Android 12.
- **Coda** in un file della memoria privata dell'app. Contiene i link, che
  quindi non escono dal telefono: c'è `allowBackup="false"` e
  `dataExtractionRules` esclude il trasferimento tra dispositivi.
  All'apertura dell'app le copie non finite ripartono.
- **Richieste**: `HttpURLConnection` con `setInstanceFollowRedirects(false)`.
  - Alla prima richiesta vuole 200 e `Content-Length` uguale a `size`.
  - Alla ripresa manda `Range: bytes=N-` e `If-Range` con l'`etag` avuto
    dal server nel `POST`. Così non dipende da come Cloudflare mostra
    l'ETag. Vuole 206 con `Content-Range` giusto.
  - Se il file parziale è già lungo `size`, non si chiede nulla e si passa
    al controllo finale.
  - Un 200 a una ripresa, o un 416, fa ripartire la copia da zero,
    riaprendo il file con `"wt"` o ricreandolo.
  - 3xx, 401 e 403: «Server non raggiungibile da qui».
  - 404: «Link scaduto o non più valido».
  - 410: «Il file sul server è cambiato».
  - Errori di rete e 5xx, compresi 502 e 530: si riprova dopo 5 s, poi
    raddoppiando fino a 5 minuti. Dopo 30 minuti senza dati, «In pausa».
- **Ripresa** dalla lunghezza vera del file parziale, non da un numero
  salvato. Se il file parziale non c'è più (cancellato a mano, o scaduto
  dopo una settimana in sospeso da Android 11), si riparte da zero.
- **Controllo finale**: la lunghezza del file deve essere `size`. Solo
  allora il file diventa visibile.
- **Wi-Fi**: prima di ogni tentativo `ConnectivityManager` deve dire
  `NOT_METERED` e `NOT_ROAMING` (su API 24-27 il roaming si legge da
  `NetworkInfo.isRoaming()`).
- **Android 15**: il sistema concede ai servizi `dataSync` 6 ore ogni 24.
  Allo scadere `onTimeout` ferma la copia in modo pulito e chiama
  `stopSelf`. La copia riprende alla prossima apertura dell'app, che
  azzera il conto.
- **FAT32**: un errore di scrittura a 4 GB diventa il messaggio «Oltre 4
  GB».

### Android: dove scrive

- **`Movies/VodMax`, Android 10 e successivi** (API 29+): `MediaStore` con
  `RELATIVE_PATH` e `IS_PENDING` finché la copia non è finita. Nessun
  permesso. Il sistema indicizza il file da solo.
- **`Movies/VodMax`, Android 7-9** (API 24-28): il percorso classico, con
  `mkdirs()` per le sottocartelle. Serve `WRITE_EXTERNAL_STORAGE` con
  `maxSdkVersion="28"`, chiesto alla prima copia. Il file si chiama
  `….part` finché non è finito, poi si rinomina e si avvia la scansione dei
  media.
- **Cartella scelta** (scheda SD, chiavetta, disco):
  - `ACTION_OPEN_DOCUMENT_TREE` e `takePersistableUriPermission` in
    lettura e scrittura;
  - `DocumentsContract` della piattaforma (niente libreria
    `documentfile`): il file nasce come `….part` di tipo
    `application/octet-stream` e alla fine si rinomina;
  - `ContentResolver.openOutputStream(uri, "wa")` per riprendere;
  - spazio libero con `Os.fstatvfs` sul file appena creato, quando la
    copia parte;
  - se il permesso non vale più (chiavetta tolta, codice cambiato), la
    copia aspetta: «Cartella non raggiungibile».

  La modalità `"wa"`, la rinomina e `fstatvfs` sono **da provare** per
  ciascun fornitore: la memoria esterna di Android e «Files» di Fire OS.
- **Ripiego sulla TV** (da provare): `getExternalFilesDirs(null)`, la
  cartella dell'app sulla chiavetta. File normali, nessun permesso. Si
  usa se `ACTION_OPEN_DOCUMENT_TREE` non si risolve o si risolve nello
  stub `com.google.android.tv.frameworkpackagestubs/.Stubs$DocumentsStub`,
  che mostra solo «nessuna app».

### Android: il ponte

Il ponte `VodMaxNative` riceve cinque metodi nuovi. Come gli altri, c'è
solo sulle pagine del server.

- `copyTarget()`: `"phone"` sul telefono; `"tv"` su una TV con una
  chiavetta o un disco collegato (`StorageManager`, volume rimovibile
  montato); `""` altrove, anche nel dispositivo da solo.
- `copy({url, name, mime, size, etag, title})`.
  - Accetta solo un percorso `/stream/save/…` e ci aggiunge davanti
    l'indirizzo del server, come fa `play`.
  - Ripulisce il nome: niente `..`, al massimo due cartelle.
  - Controlla spazio e doppioni. Doppione: una copia in coda o in corso
    verso lo stesso file, oppure il file già nella cartella.
  - Risponde con un codice che la pagina traduce: `""` (copia avviata),
    `folder`, `permission`, `space`, `already`, `copying`, `invalid`.
  - `folder`/`permission`: si è aperta la scelta o la richiesta; la copia
    parte dopo il sì e l'esito arriva con `window.VodMaxCopyDone(codice)`,
    come `VodMaxSaveDone`.
  - La pagina passa solo il link firmato: né cookie né token del
    dispositivo.
- `chooseFolder(reset)`: apre la scelta della cartella; con `true` torna
  a `Movies/VodMax` sul telefono.
- `copies()`: la cartella in uso e le copie, in JSON, per la sezione «Su
  questo dispositivo».
- `cancelCopy(id)`: Annulla o Togli.

### Interfaccia

- `common.js` legge `copyTarget()`. Se è vuoto non c'è nessun pulsante.
  La sezione «Su questo dispositivo» c'è se `copyTarget()` non è vuoto o
  se `copies()` ha delle copie, per esempio con la chiavetta tolta.
- Per «Tutta la stagione N» la pagina chiede un link per ogni episodio e
  lo passa a `copy`, un episodio alla volta, fermandosi a
  `folder`/`permission` fino all'esito.
- La sezione «Su questo dispositivo» si aggiorna ogni 2 secondi mentre è
  visibile, come la lista dei download.

### File toccati

- Nuovi:
  - `internal/service/save.go` e `internal/service/save_test.go`;
  - `clients/android/app/src/main/java/app/vodmax/client/CopyService.java`;
  - `clients/android/app/src/test/java/app/vodmax/client/CopyServiceTest.java`.
- Server: `internal/api/api.go`, `internal/library/library.go` e
  `nfo.go` (`VideoExt` esportato), `internal/store/profiles.go`
  (`Device` per id), `internal/store/downloads.go` (download per `media`
  e `media_id`), `internal/service/i18n/*.json`.
- Interfaccia: `internal/web/static/common.js`, `vod.js`, `vod.css`
  (`.rec-actions` più larga, la sezione nuova), `i18n/*.json` nelle sei
  lingue.
- Android:
  - `AndroidManifest.xml`: i permessi `FOREGROUND_SERVICE`,
    `FOREGROUND_SERVICE_DATA_SYNC`, `POST_NOTIFICATIONS`,
    `ACCESS_NETWORK_STATE`, `WRITE_EXTERNAL_STORAGE` con
    `maxSdkVersion="28"`, il `<service>` di `CopyService` e
    `dataExtractionRules`, con il nuovo `res/xml/data_extraction_rules.xml`;
  - `MainActivity.java`: i metodi del ponte, il risultato della scelta
    della cartella e `onRequestPermissionsResult`, la ripresa della coda
    all'apertura;
  - `res/values*/strings.xml`: testi della notifica nelle sei lingue.
- Documenti:
  - `docs/decisioni/README.md`;
  - `docs/sviluppo.md`, che deve elencare anche le rotte
    `/api/v1/downloads`, oggi mancanti;
  - `docs/direzione.md`: in «Da dove si guarda» lo scenario nuovo,
    guardare senza rete fuori casa, sul telefono o da una chiavetta.

### Prove

- Go:
  - un provider finto che deve ricevere zero richieste;
  - 200, 206 e HEAD; `If-Range` giusto dà 206, sbagliato dà 200;
  - 410 dopo il passaggio da TS a MP4;
  - token falso, scaduto, di un dispositivo tolto (anche con l'id
    riusato da un dispositivo nuovo) o con il PIN cambiato: 404;
  - bambini 403; token di amministrazione 403; dispositivo da solo 400;
    estensione fuori da `VideoExt` 409;
  - `Content-Type` di ogni estensione di `VideoExt`, con e senza
    `/etc/mime.types`;
  - il log non contiene il token.
- Android, test JVM: controllo del link e del nome; lettura delle
  risposte (200, 206, 3xx, 404, 410, 416, 502, 530); attese tra i
  tentativi; lettura e scrittura della coda.
- A mano:
  - Android 9: permesso, sottocartelle di `Movies/VodMax`, scansione.
  - Android 10: sottocartelle con `MediaStore`, file in sospeso non
    visibile.
  - Android 15: notifiche rifiutate, la copia va lo stesso. Limite di 6
    ore accorciato con
    `adb shell device_config put activity_manager data_sync_fgs_timeout_duration 60000`:
    la copia si ferma pulita e riprende all'apertura.
  - Telefono: **Scegli cartella** su una scheda SD.
  - Fire OS 8 con una chiavetta USB, in FAT32 e in exFAT: scelta della
    cartella con il telecomando, copia, chiavetta tolta e rimessa, film
    oltre 4 GB in FAT32, la chiavetta letta da una TV LG.
  - Fire OS 7: c'è la scelta della cartella? Se no, il ripiego.
  - Un box Google TV o Android TV con una chiavetta.
  - App fermata a metà copia (`adb shell am force-stop`): riprende alla
    riapertura.
  - Modalità aereo a metà copia.
  - Ripresa attraverso il tunnel: `Range` e `If-Range` passano da
    Cloudflare.
  - Riavvio del server a metà copia, in casa e attraverso il tunnel (502 e
    530).
  - Login del proxy o paese escluso: errore, nessun file salvato.
  - VLC senza rete.
  - Browser e TV LG: nessun pulsante.

## Non ancora

- Copia diretta dal provider senza passare dal disco del server:
  prenderebbe l'unica connessione come chi guarda.
- Un tocco solo per «sul server e poi sul dispositivo»: per ora sono due
  passi.
- Pausa e ripresa a mano. Per ora c'è solo Annulla, e la ripresa avviene
  da sola.
- Eliminare dall'app i file già copiati: si usa l'app File o un PC.
- Una copia che riparte da sola senza riaprire l'app, per esempio con i
  trasferimenti avviati dall'utente di Android 14, che non hanno il limite
  di 6 ore.
- Controllo sha256 del file copiato.
- Un'interfaccia senza rete dentro VodMax: i file si aprono con altre app.
- TV LG webOS, browser, dispositivo da solo.

## Scelte del proprietario

8 novembre 2026, alle domande della proposta:

1. Rete mobile: «Wi-Fi, scelta in Impostazioni». Di base solo Wi-Fi o
   cavo; si può permettere la rete mobile, per dispositivo.
2. Cloudflare: «Sì, anche fuori casa». La copia passa dal tunnel come lo
   streaming.
3. Galleria: «Sì, visibili». Nessun `.nomedia`.
4. Box senza scelta della cartella: «Sì, con avviso». Si usa la cartella
   dell'app sulla chiavetta e l'app avvisa che si cancella con VodMax.
