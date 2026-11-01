# Installare VodMax

[English](install.md)

VodMax ha due parti: il **server**, che tiene catalogo, guida,
registrazioni e avanzamenti di tutti, e le **app** (browser, Android,
Android TV, Fire TV, LG webOS) che lo mostrano. Un dispositivo Android può
anche usare VodMax **da solo**, senza server (vedi [Fire TV e Android senza
server](#fire-tv-e-android-senza-server)).

I pacchetti sono nelle [release](https://github.com/silirim/VodMax/releases):

| File | Per |
|---|---|
| `vodmax-server_<v>_linux_amd64.tar.gz` | Server su PC, mini PC, macchina virtuale o container (x86-64) |
| `vodmax-server_<v>_linux_arm64.tar.gz` | Server su Raspberry Pi 4/5 o NAS ARM (64 bit) |
| `vodmax-server_<v>_linux_armv7.tar.gz` | Server su schede ARM a 32 bit |
| `VodMax-<v>.apk` | Telefoni e tablet Android, Android TV, Google TV, Fire TV |
| `app.vodmax.client_<v>_all.ipk` | TV LG con webOS |
| `SHA256SUMS` | Checksum: `sha256sum -c SHA256SUMS` |

## 1. Server

Una qualsiasi macchina Linux sempre accesa: mini PC, NAS, Raspberry Pi,
macchina virtuale o container LXC. Serve poco: qualche centinaio di MB di
memoria e circa 200 MB di disco per un catalogo di oltre 100.000 titoli, più
lo spazio per le registrazioni.

```sh
tar xzf vodmax-server_*_linux_amd64.tar.gz
cd vodmax-server_*/
sudo useradd --system --home /var/lib/vodmax --shell /usr/sbin/nologin vodmax
sudo install -m 755 vodmax-server vodmax-update /usr/local/bin/
sudo install -d -o vodmax -g vodmax -m 700 /var/lib/vodmax
sudo cp vodmax.service /etc/systemd/system/
sudo systemctl enable --now vodmax
vodmax-server -version
```

- **ffmpeg**: installalo dalla distribuzione (`sudo apt install ffmpeg`;
  porta anche `libgomp1`, che serve all'assistente integrato).
  Senza, VodMax funziona, ma niente pausa della diretta, niente
  registrazioni, e i file MKV/AVI non partono nel browser (l'app Android li
  riproduce comunque).
- **Porta**: l'interfaccia è su `http://<server>:8095`; si cambia in
  `vodmax.service` (`-listen`). Tienila dentro la rete di casa; da fuori usa
  una tua VPN (WireGuard, Tailscale…) invece di aprire la porta.
- **Dati**: tutto in `/var/lib/vodmax` (database, chiave di cifratura,
  token di amministrazione, registrazioni, download, backup). I download
  possono andare su un altro disco con `-downloads /percorso` in
  `vodmax.service` (la cartella deve essere scrivibile dall'utente `vodmax`,
  per esempio aggiunta a `ReadWritePaths`).
- **Cartelle delle raccolte**: il servizio gira come utente `vodmax` e, con
  il file fornito, non vede `/home` né `/tmp`. Metti o monta le cartelle dei
  film sotto `/mnt`, `/srv` o `/media`, leggibili dall'utente `vodmax`.

### Con Docker

L'immagine `ghcr.io/silirim/vodmax` (x86-64, ARM64, ARMv7) contiene il server
e ffmpeg. Con [`docker-compose.yml`](../packaging/docker-compose.yml):

```sh
mkdir vodmax-data && sudo chown 1000:1000 vodmax-data
docker compose up -d
docker compose exec vodmax cat /var/lib/vodmax/admin.token
```

Monta le cartelle dei film in sola lettura (es. `/srv/media:/srv/media:ro`) e
usa quei percorsi per le raccolte. Si aggiorna con
`docker compose pull && docker compose up -d`.

**Unraid, NAS**: avviato come root con `PUID`/`PGID` (`--user 0 -e PUID=99
-e PGID=100`), il container dà la cartella dei dati a quell'utente e fa
girare VodMax con quell'utente. Per Unraid c'è un template:
[`packaging/unraid/vodmax.xml`](../packaging/unraid/vodmax.xml) (Docker →
Aggiungi container → Template, oppure copialo in
`/boot/config/plugins/dockerMan/templates-user/`).

**Scheda grafica**: per convertire con la scheda grafica, invece che con il
processore, i video che il browser non sa leggere (HEVC, MPEG-2), passala al
container: Intel/AMD con `--device /dev/dri` e `--group-add <id del gruppo
render>` (`getent group render`; l'immagine ha il driver VAAPI Intel su x86-64, AMD funziona con l'installazione nativa),
NVIDIA con NVIDIA Container Toolkit (`--gpus all`, con un ffmpeg che abbia
NVENC). Poi Impostazioni → Sistema → *Conversione video* → *Prova*.

### Aggiornare

Il server controlla le release su GitHub una volta al giorno (contatta solo
GitHub e non invia nulla sulla tua installazione; si disattiva in
Impostazioni → Sistema → Versione e aggiornamenti) e lì mostra quando esce
una versione nuova.

- **Server**: `sudo vodmax-update` scarica la versione per questa macchina,
  la verifica con i checksum della release, tiene quella in uso come
  `vodmax-server.old`, installa la nuova e riavvia il servizio.
  `sudo vodmax-update v0.4.0` installa una versione precisa (anche per
  tornare indietro). Il database si aggiorna da solo all'avvio; meglio fare
  prima un backup (Impostazioni → Sistema → Backup). Prima di cambiarlo il
  server ne tiene una copia in `<dati>/pre-upgrade/` (le ultime tre): una
  versione precedente rifiuta un database aggiornato da una più nuova, quindi
  per tornare indietro ferma il servizio, metti quella copia al posto di
  `vodmax.db` e poi installa la versione precedente. Dalla 0.5.x indica la
  versione (`sudo vodmax-update v0.6.0`): lo script della 0.5.x prenderebbe
  anche una beta.
- **App Android** (telefono, Android TV, Fire TV): propongono da sole
  l'aggiornamento: *Aggiorna*, poi conferma nella finestra di Android (la
  prima volta permetti a VodMax di installare app). Funziona dalla 0.4.0,
  la prima firmata con la chiave di rilascio: chi ha una versione
  precedente deve disinstallarla e installare la 0.4.0 una volta.
- **LG webOS**: l'interfaccia arriva dal server, quindi è sempre aggiornata;
  l'app in sé cambia di rado (si reinstalla come sopra).
- **Beta**: chiunque può installarne una (`sudo vodmax-update v0.6.0-beta.2`,
  l'APK dalla pagina della release o il tag Docker), ma vengono proposte solo
  con *Proponi anche le versioni beta* acceso in Impostazioni → Sistema. Il
  semplice `sudo vodmax-update` installa solo versioni stabili e non torna
  mai indietro.

## 2. Prima configurazione

1. Apri `http://<server>:8095` nel browser. Chiede il **token di
   amministrazione**: lo trovi sul server con
   `sudo cat /var/lib/vodmax/admin.token`.
2. **Impostazioni → Lingua**: italiano o inglese (per dispositivo; la stessa
   scelta è nella schermata dei profili).
3. **Impostazioni → VPN**: VodMax contatta le playlist **solo tramite una
   VPN**. Importa il file WireGuard `.conf` del tuo servizio VPN (Proton
   VPN, Mullvad, NordVPN…; meglio un file per ogni dispositivo), oppure
   indica una *VPN esterna* se il server esce già da una VPN sul router (in
   quel caso sta a te che il traffico passi davvero da lì).
4. **Impostazioni → Abbonamenti**: aggiungi una playlist M3U (il suo
   indirizzo) o un account Xtream Codes (server, utente, password), scegli
   la VPN, poi *Sincronizza ora*. Catalogo e guida si aggiornano ogni notte,
   la guida anche ogni poche ore (Impostazioni → Sistema).
5. **Impostazioni → Selezione** e **Categorie**: tieni solo i paesi, gli
   anni e le categorie che vuoi; l'anteprima mostra l'effetto prima di
   salvare.
6. **Impostazioni → Raccolte** (facoltativo): aggiungi cartelle di film o di
   serie (una sottocartella per serie). Nomi come `Film (2010).mkv` e
   `Serie/Stagione 1/S01E03.mkv` vengono riconosciuti; i file `.nfo` e le
   immagini accanto ai video si usano quando ci sono.
7. **Impostazioni → Profili** (facoltativo): un profilo per persona, profili
   bambini, PIN.
8. **Impostazioni → Sistema → Copertine e schede (TMDB)** (facoltativo): con
   una chiave TMDB gratuita arrivano locandine, trame e cast dove la
   playlist non li ha.
9. **Impostazioni → Assistente** (facoltativo): crea le regole di
   registrazione da una frase e propone cosa guardare stasera. Spento,
   VodMax non ne mostra nulla. Tre modi:
   - **Integrato**: VodMax propone i modelli che stanno nella memoria del
     server e, quando premi *Attiva*, scarica il motore llama.cpp (~35 MB)
     e il modello (0,9–8,4 GB) in `/var/lib/vodmax/assistant`, controllando
     le impronte. Gira sul server, in ascolto solo sul server stesso, e si
     spegne dopo 10 minuti senza richieste. *Rimuovi* cancella tutto.
     Linux x86-64 o ARM64; la scheda video si usa tramite Vulkan se ci sono
     `libvulkan1` e i suoi driver. Con **Docker** il modello gira nel
     servizio facoltativo `assistant` del compose:
     `docker compose --profile assistant up -d`.
   - **Servizio online**: Claude, OpenAI, Mistral, Gemini o OpenRouter con
     una **chiave API** (l'abbonamento Claude Pro/Max o ChatGPT Plus non si
     può usare da un'altra app). Per questo uso costa pochi centesimi al
     mese; la guida della serata, i nomi dei canali e i titoli della tua
     lista vanno al servizio.
   - **Server tuo**: qualsiasi API compatibile OpenAI (Ollama, LM Studio…).

   Per richieste come "un film con…" VodMax tiene cast e regia dei tuoi film
   e serie: dalle schede che apri e, finché l'assistente è acceso, da una
   scheda ogni 3 secondi tra l'1 e le 7 (tramite la VPN dell'abbonamento).

## 3. Dispositivi

Ogni dispositivo si **abbina** una volta: mostra un codice, che approvi in
**Impostazioni → Dispositivi** da un dispositivo già collegato. Il codice
vale 10 minuti.

### Browser (PC, Mac, tablet, qualsiasi TV)

Apri `http://<server>:8095`. Funziona in ogni browser recente, anche in
quello delle TV Samsung, Philips e delle altre smart TV (senza i tasti
colorati del telecomando).

Se in casa c'è già un provider di accesso (Authelia, Authentik, Keycloak o un
qualsiasi provider OpenID Connect), dai browser si può entrare con un account
invece del codice di abbinamento: Impostazioni → Dispositivi → *Accesso con
account di casa*. Nel provider crea un client con l'indirizzo di ritorno
mostrato lì (scope `openid email profile`), poi collega ogni account (e-mail
o nome utente) a un profilo. Ogni account entra sempre e solo nel suo profilo.
Il provider deve essere raggiungibile in `https://`.

### Telefono e tablet Android, Android TV, Google TV

1. Scarica `VodMax-<v>.apk` sul dispositivo (o copialo con una chiavetta
   USB).
2. Quando Android lo chiede, permetti l'installazione da quella fonte
   (*Installa app sconosciute*).
3. Apri VodMax, scrivi l'indirizzo del server (`http://192.168.1.10:8095`),
   poi approva il codice di abbinamento.

Film e serie partono nel player nativo (ogni formato, tracce audio e
sottotitoli); il resto è la stessa interfaccia del browser. Su Android TV
l'app compare nel launcher con il suo banner.

### Fire TV

Funziona sui Fire TV con **Fire OS** (Fire TV Stick Lite, HD, 4K, 4K Max,
Fire TV Cube, TV Fire TV). **Non** funziona sulla Fire TV Stick 4K Select
(2025), che usa Vega OS e non installa app esterne allo store.

1. *Impostazioni → La mia Fire TV → Informazioni*: clicca 7 volte sul nome
   del dispositivo per far comparire *Opzioni sviluppatore*; lì attiva *App
   di origine sconosciuta* (o permettilo all'app Downloader).
2. Installa **Downloader** dall'Amazon Appstore e aprici il link di
   `VodMax-<v>.apk` dalla pagina della release.
3. Apri VodMax: scrivi l'indirizzo del server, oppure scegli *Usa questo
   dispositivo da solo*.

Il telecomando Fire TV non ha tasti dei canali né colorati: in diretta usa
Su/Giù e OK (elenco dei canali).

### Fire TV e Android senza server

Nella prima schermata scegli **Usa questo dispositivo da solo**: l'app
avvia un server VodMax tutto suo dentro il dispositivo e apre Impostazioni
→ Abbonamenti. Aggiungi il `.conf` della VPN e la playlist come sopra.
Limiti: niente registrazioni né pausa della diretta (sul dispositivo non c'è
ffmpeg); film, serie e diretta funzionano. Il dispositivo contatta la
playlist da sé, tramite la sua VPN.

### TV LG (webOS)

Le TV LG installano app esterne allo store solo in **Modalità
sviluppatore**. Se è troppo complicato, usa il browser della TV (sopra).

1. Crea un account gratuito su [webostv.developer.lge.com](https://webostv.developer.lge.com)
   (è il tuo account LG).
2. Sulla TV installa **Developer Mode** dall'LG Content Store, entra, attiva
   *Dev Mode Status* (la TV si riavvia), poi attiva *Key Server*. Annota
   l'indirizzo IP della TV e la passphrase che mostra.
3. Da un computer con Node.js nella stessa rete:

   ```sh
   npm install -g @webos-tools/cli
   ares-setup-device -a tv -i "{'host':'<ip-tv>','port':'9922','username':'prisoner'}"
   ares-novacom --device tv --getkey          # scrivi la passphrase
   ares-setup-device -m tv -i "{'privatekey':'tv_webos','passphrase':'<passphrase>'}"
   ares-install --device tv app.vodmax.client_<v>_all.ipk
   ```

   (`tv_webos` è il file della chiave che `--getkey` salva in `~/.ssh`.)
4. Apri VodMax sulla TV, scrivi l'indirizzo del server e approva il codice di
   abbinamento.

La Modalità sviluppatore dura **50 ore**: prima che scada apri l'app
Developer Mode e premi *Extend*, altrimenti le app installate così non si
aprono finché non la rinnovi. L'interfaccia completa richiede webOS 22 (TV
dal 2022) o successivo; sulle TV più vecchie funziona con un'impaginazione
meno curata.

### Samsung e altre TV

Non c'è ancora un'app Samsung (Tizen): usa il browser della TV su
`http://<server>:8095`.

### Altre app: Plex, Jellyfin, Emby, TiviMate, Kodi

**Impostazioni → Sistema → Uscita verso altre app → Accendi** dà tre
indirizzi con una chiave segreta:

- **Sintonizzatore HDHomeRun**: Plex (*Impostazioni → TV in diretta e DVR →
  Configura*, inserisci l'indirizzo a mano), Jellyfin ed Emby (*TV in diretta
  → Aggiungi sintonizzatore → HDHomeRun*) vedono VodMax come un sintonizzatore
  e registrano con il loro DVR.
- **Playlist M3U** e **guida XMLTV**: TiviMate, Kodi (PVR IPTV Simple), VLC, o
  Jellyfin come sintonizzatore M3U.

I canali passano comunque da VodMax (stesse connessioni, stessa VPN); ne
vanno insieme quanti ne permettono gli abbonamenti. Chi ha gli indirizzi può
guardare i canali: tienili in casa; *Cambia la chiave* blocca i vecchi.

## 4. Problemi comuni

| Problema | Cosa fare |
|---|---|
| "Il server non risponde" su un dispositivo | Stessa rete? Prova l'indirizzo dal browser del telefono. Controlla `systemctl status vodmax`. |
| Il codice di abbinamento è scaduto | Dura 10 minuti, poi ne compare uno nuovo. Una volta approvato, il dispositivo lo ritira entro 30 minuti, anche se l'app era in background. |
| La cartella di una raccolta "non esiste" | L'utente `vodmax` deve poterla leggere e, con il file di servizio fornito, non deve stare sotto `/home` o `/tmp`. |
| La diretta non parte, "connessioni in uso" | La playlist permette poche connessioni insieme: chiudi un altro dispositivo, o prendi il controllo. |
| La sincronizzazione fallisce | Impostazioni → VPN: la VPN è collegata? Impostazioni → Abbonamenti mostra l'errore. |
| Film MKV o AVI non partono nel browser | Installa `ffmpeg` sul server, o usa l'app Android. |
| Log | `journalctl -u vodmax -f` |
