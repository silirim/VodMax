# 0057 — Playlist da un file o da un link di Google Drive e Dropbox

Stato: **Approvata** (8 novembre 2026: "Approvata, implementa", dopo le
risposte alle domande riportate in fondo; nata da un commento su Reddit)

## Il problema

Oggi una playlist M3U si aggiunge solo con un indirizzo http(s) che
risponde con la playlist. Chi ha la playlist come file sul telefono, o in
Google Drive o Dropbox, non ha modo di usarla. Il link di condivisione di
un cloud apre una pagina web, e la sincronizzazione finisce con "la
risposta non è una playlist M3U (sembra una pagina web o un altro
documento)".

Il commento veniva da un'interfaccia in polacco. Il link incollato era un
`get.php` di Xtream Codes, e il pannello lo rifiutava con HTTP 884. Il
messaggio è comparso in italiano. L'utente racconta anche che "sincronizza
per minuti e poi si blocca". Il link `get.php` che diventa un abbonamento
Xtream Codes e la traduzione dell'errore sono una correzione a parte.
Questa decisione riguarda la richiesta di un file dal dispositivo e dei
link dei cloud.

## Decisione

### Un file dal dispositivo

- In Impostazioni → Abbonamenti, tipo **Playlist M3U**, sotto "Indirizzo
  della playlist" compare **"oppure un file (.m3u, .m3u8)"**. Nel browser
  è un `<input type=file>`. Nell'app Android si apre il selettore di
  Android: `onShowFileChooser` c'è già, usato per il `.conf` della VPN e
  per il backup, e chiede `*/*` perché il tipo dei `.m3u` cambia da
  un'app all'altra. Sul telefono il selettore mostra anche le app che si
  presentano come archivio, come Google Drive. Il campo si raggiunge con
  le frecce e si apre con OK.
- Il file va al server così com'è, come corpo della richiesta (come il
  ripristino di un backup). La pagina non lo legge in memoria. La
  richiesta è riservata all'amministratore.
- Il limite è **64 MB**. La pagina lo controlla prima dell'invio e il
  server lo controlla di nuovo. Una lista più grande si usa con un
  indirizzo o con un link.
- Il server legge il file con lo stesso parser e gli stessi limiti di una
  playlist scaricata. Se non è una playlist lo rifiuta senza salvarlo.
- Il file resta **cifrato con `secret.Box`**, come l'indirizzo della
  playlist: dentro ci sono gli indirizzi dei canali, spesso con utente e
  password.
- Il file sta in una tabella sua. Il segreto dell'abbonamento si legge a
  ogni visione e non deve portarsi dietro decine di MB. La migrazione si
  aggiunge in coda.
- L'API non restituisce mai il file. La pagina mostra nome, dimensione e
  data del caricamento. Il file si cancella con l'abbonamento.
- **Nessun aggiornamento automatico.** La sincronizzazione notturna salta
  questi abbonamenti. "Sincronizza ora" rilegge il file salvato senza
  contattare nessuno.
- **Per aggiornarlo**: Modifica → **"Sostituisci il file"**. Il file nuovo
  si carica e la sincronizzazione parte subito.
- I titoli rimasti nel file nuovo tengono preferiti e avanzamenti.
  L'identità non cambia rispetto a oggi: `tvg-id` o nome.
- Sostituire il file è una scelta esplicita. Per questo un file con meno
  della metà dei titoli si accetta: il controllo sul "calo anomalo" vale
  per le sincronizzazioni automatiche.
- In Modifica si passa da un file a un indirizzo, e al contrario. Salvare
  un indirizzo toglie il file; caricare un file toglie l'indirizzo.
- Chi vuole l'aggiornamento automatico usa un link (sezione sotto) e
  aggiorna il file nel cloud.
- **La VPN dell'abbonamento serve ancora.** Canali e film del file si
  guardano dal provider, attraverso la connessione dell'abbonamento, come
  oggi. Importare il file non contatta nessuno.
- **Dispositivo da solo** ([0021](0021-dispositivo-da-solo.md)): la pagina
  è la stessa e il file va al server dentro l'app.
- Sulla TV (Android TV, Fire TV) spesso il selettore di file non c'è.
  L'app dice alla pagina se ce n'è uno (`resolveActivity` su
  `ACTION_GET_CONTENT`, attraverso `VodMaxNative`). Se manca, il campo del
  file non compare e resta il link. Il campo non compare nemmeno nell'app
  LG.

### Un link di Google Drive o Dropbox

- Il link di condivisione si incolla in "Indirizzo della playlist", così
  come lo dà "Copia link" nell'app del cloud. Il file deve essere
  condiviso con **"chiunque abbia il link"**.
- Non serve accedere a Google o a Microsoft, e non serve una chiave: lo
  stesso limite della [0055](0055-voti-esterni.md), "Io non voglio fare
  account o accessi a niente". Una nota sotto il campo lo dice.
- VodMax salva il link così com'è, cifrato come ogni indirizzo di
  playlist. A ogni sincronizzazione lo trasforma in un link di download.
  Se il servizio cambia formato, basta un aggiornamento di VodMax e non
  serve reinserire il link.
- **Google Drive**:
  - Questi link diventano
    `https://drive.google.com/uc?export=download&id=ID`:
    `drive.google.com/file/d/ID/view?usp=sharing` (anche `/edit`,
    `/preview`, con `/u/N/` nel percorso), `drive.google.com/open?id=ID` e
    `drive.google.com/uc?id=ID`.
  - Google risponde con un rimando a
    `drive.usercontent.google.com/download?id=ID&export=download`.
  - Per i file grandi Google non dà il file. Dà una pagina "impossibile
    controllare la presenza di virus" con un modulo `download-form` e i
    campi nascosti `id`, `export`, `confirm` e `uuid`.
  - VodMax riconosce quella pagina e rifà la richiesta **una volta sola**
    con quei campi. Se non basta, si ferma con un messaggio chiaro.
  - Le cartelle (`/drive/folders/`) e i documenti Google (`docs.google.com`)
    si rifiutano al salvataggio, con il motivo.
- **Dropbox**:
  - `www.dropbox.com/scl/fi/…?rlkey=…&dl=0` e il vecchio
    `www.dropbox.com/s/…?dl=0` diventano lo stesso link con `dl=1`. Gli
    altri parametri restano, `rlkey` compreso.
  - Dropbox rimanda a un suo host di download.
  - Le cartelle (`/scl/fo/`) si rifiutano: con `dl=1` darebbero uno zip.
- **OneDrive**: si rifiuta al salvataggio con il messaggio "OneDrive non dà
  un link di download senza accesso: scarica il file e caricalo da qui".
  Vedi "Formati verificati".
- Il link si legge come una playlist da indirizzo:
  - Passa da `internal/provider` e dalla connessione dell'abbonamento
    (`Service.tunnel`).
  - Lo stesso client segue i rimandi, quindi anche i rimandi passano
    dalla stessa connessione.
  - Valgono lo stesso parser e la stessa sincronizzazione notturna.
  - Con "solo tramite VPN" acceso e senza VPN, il cloud non si contatta.
- A volte la risposta è una pagina web: un link non pubblico, un file
  tolto, una quota di download superata, una verifica "traffico insolito"
  di Google verso un indirizzo VPN. La sincronizzazione finisce allora con
  un messaggio che nomina il servizio, per esempio: "Google Drive non ha
  dato il file: controlla che sia condiviso con chiunque abbia il link".
- Dropbox risponde 200 con una pagina anche per un link inesistente. Il
  controllo quindi guarda il contenuto, non lo stato HTTP.
- Test con link sintetici. Un `httptest` imita i rimandi e la pagina di
  conferma. Nessun link vero nel repository.

### Messaggi

- Ogni testo nuovo, del server e dell'interfaccia, va in tutte e sei le
  lingue (it, en, es, fr, de, pl), non solo in `it.json` e `en.json`.

## Formati verificati (8 novembre 2026)

| Servizio | Cosa | Verificato? |
|---|---|---|
| Google Drive | `uc?export=download&id=…` risponde 303 verso `drive.usercontent.google.com/download?id=…&export=download` | Sì, provato con un id inesistente (303, poi 404). |
| Google Drive | Forme del link di condivisione (`/file/d/ID/view`, `open?id=`) | Da fonti della comunità e dall'uso comune. Google non documenta il download diretto. |
| Google Drive | Pagina di conferma per i file grandi, con `download-form` e campi nascosti | Dal codice di gdown, che legge tutti i campi nascosti del modulo, e da segnalazioni. **Non provato con un file vero.** Soglia non documentata: le segnalazioni dicono tra 25 e 100 MB. **Non verificato** se servono i cookie. |
| Dropbox | `dl=1` forza il download, `rlkey` resta, i rimandi vanno seguiti | Sì, dalla guida ufficiale di Dropbox. |
| Dropbox | Link inesistente: 200 con una pagina HTML | Sì, provato. Download di un file vero **non provato**. |
| OneDrive | `1drv.ms/u/s!…` rimanda a `onedrive.live.com/redir?resid=…` | Sì, provato con un id inesistente. |
| OneDrive | Formato nuovo `1drv.ms/u/c/…` (dal 2024) | Solo da forum. |
| OneDrive | Download senza accesso | **Non trovato.** L'API `shares` di Microsoft Graph vuole un token (documentazione Microsoft). Secondo i forum `api.onedrive.com/v1.0/shares/u!…/root/content` risponde 401 o 400 dal 2024. `download=1` funzionava solo con gli account di lavoro (forum vecchi, non verificato). |
| Android | Google Drive tra le fonti di `ACTION_GET_CONTENT` sul telefono | Comportamento noto, **non provato qui**. Dropbox **non verificato**. Su quali Android TV o Fire TV ci sia un selettore: **non verificato**. |

## Non ancora

- OneDrive, e OneDrive o SharePoint di lavoro: solo dopo aver trovato un
  link pubblico che scarica senza accesso, provato con un file vero.
- Cartelle condivise, cioè scegliere il file dentro una cartella di Drive o
  di Dropbox.
- Altri servizi (MEGA, iCloud, Box, pCloud…). Un link che dà già il file
  funziona oggi come qualsiasi indirizzo.
- Aggiornare da solo un file caricato: resta quello finché non lo si
  sostituisce.
- File più grandi di 64 MB.
- Una guida XMLTV da un file del dispositivo: le fonti della guida
  accettano solo un indirizzo.
- Mandare il link dal telefono alla TV: scrivere un link di Drive con il
  telecomando è lungo.
- Accedere a Google, Dropbox o Microsoft per i file privati: no
  ([0055](0055-voti-esterni.md)).

## Scelte del proprietario

8 novembre 2026, alle domande della proposta:

1. Limite del file: «64 MB», come il ripristino del backup.
2. Backup: «Sì, compresso». Dopo un ripristino il file non va ricaricato.
3. OneDrive: «Fuori per ora». Al salvataggio un messaggio consiglia di
   caricare il file.
4. e 5. Modulo: «Un tipo: link oppure file». "Playlist M3U" con la scelta
   Indirizzo / File; sulle TV senza selettore di file il campo File si
   nasconde.

Nell'implementazione il ripristino di un backup accetta fino a 256 MB
(prima 64): i backup ora contengono i file delle playlist, e con più liste
grandi un backup scaricato non si sarebbe più potuto ripristinare. Il
limite di 64 MB per ogni file resta.
