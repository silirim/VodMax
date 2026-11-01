<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/vodmax-wordmark-dark.svg">
  <img src="docs/brand/vodmax-wordmark-light.svg" alt="VodMax" width="360">
</picture>

**La tua TV in diretta, i tuoi film e le tue serie in un unico media center da ospitare in casa.**

Canali con la guida completa, pausa e registrazioni come un decoder, film e
serie dalle tue playlist e dalle tue cartelle: sulla TV, sul telefono e nel
browser.

[![CI](https://github.com/silirim/VodMax/actions/workflows/ci.yml/badge.svg)](https://github.com/silirim/VodMax/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/silirim/VodMax?include_prereleases&sort=semver)](https://github.com/silirim/VodMax/releases)
[![Licenza: AGPL-3.0](https://img.shields.io/badge/licenza-AGPL--3.0-blue)](LICENSE)

[Funzioni](#funzioni) · [Schermate](#schermate) · [Installazione](#installazione) · [Primi passi](#primi-passi) · [English](README.md)

<img src="docs/screenshots/home.jpg" alt="Home di VodMax: continua a guardare, film e serie" width="100%">

</div>

## Perché VodMax

Jellyfin e Plex sono ottimi per i file su disco, ma lenti e scomodi con la
TV in diretta e con playlist da centinaia di migliaia di titoli. I player di
playlist sono veloci, ma restano su un solo dispositivo. VodMax fa entrambe
le cose: un **server per la casa** che tiene catalogo, guida, registrazioni e
avanzamenti di tutti, e **app** che si usano come un decoder, con il
telecomando.

- **Fatto per cataloghi enormi**: centinaia di migliaia di titoli in un
  database SQLite, ricerca immediata, una scheda per titolo invece di decine
  di copie.
- **La diretta prima di tutto**: numeri dei canali, P+/P−, preferiti, banner
  con "ora" e "a seguire", pausa e riavvolgimento, programmi già andati in
  onda e registrazioni.
- **Tutto in un posto**: playlist (M3U, Xtream Codes: le playlist TV che
  hai diritto di usare) e cartelle di film e serie nella stessa Home, ricerca
  e "Continua a guardare".
- **Riservato**: il traffico verso le playlist passa solo da una VPN (quella
  WireGuard interna o una sul router); credenziali cifrate; nessuna
  telemetria.

## Funzioni

| | |
|---|---|
| 📺 **Diretta** | Zapping da telecomando (numeri, P+/P−, solo preferiti), elenco dei canali, ora e a seguire, numerazione ufficiale, sezioni sport e radio |
| 🗓️ **Guida TV** | Griglia dei programmi da XMLTV (della playlist e fonti esterne); registra o riguarda dalla guida |
| ⏸️ **Pausa e archivio** | Pausa e riavvolgimento della diretta, programma dall'inizio, archivio della playlist |
| ⏺️ **Registrazioni** | Il programma in onda, dalla guida o per regola ("ogni partita", "il telegiornale"), con controllo di disco e connessioni |
| 🎬 **Film e serie** | Locandine, trama e cast, continua a guardare, episodio successivo, lingua dell'audio e sottotitoli, MKV/AVI/HEVC convertiti al volo |
| ⬇️ **Download** | Film, episodi, stagioni o serie intere copiati sul disco del server, da guardare senza la playlist; uno alla volta e solo con una connessione libera (chi guarda ha la precedenza), anche solo di notte |
| 📁 **Raccolte locali** | Cartelle di film o serie con i nomi di Jellyfin/Kodi/Plex, file `.nfo` e immagini accanto ai video |
| 🧹 **Selezione del catalogo** | Solo i paesi, gli anni e le categorie che vuoi, con anteprima |
| 👪 **Profili** | Avanzamenti e preferiti per persona, profili bambini, dispositivi abbinati con un codice |
| 🔒 **VPN interna** | WireGuard dentro server e app: senza VPN le playlist non vengono mai contattate |
| 💾 **Backup** | Backup cifrati e copie automatiche; avvisi su VPN, playlist, guida e disco |
| 🖼️ **Schede** | Dalla playlist, dai file `.nfo` o da TMDB con una tua chiave |

### App

| Piattaforma | |
|---|---|
| **Browser** | L'interfaccia completa, per telecomando e mouse |
| **Android, Android TV, Fire TV** | Player nativo (Media3) per ogni formato; funziona anche **da sola, senza server** |
| **LG webOS** | App per TV (consigliato webOS 22 o successivo) |
| **Server Linux** | x86-64, ARM64 (Raspberry Pi 4/5), ARMv7; un solo programma, SQLite |

## Schermate

| | |
|---|---|
| <img src="docs/screenshots/tv.jpg" alt="Diretta con ora e a seguire"> | <img src="docs/screenshots/guide.jpg" alt="Guida TV"> |
| Diretta: gli altri canali senza cambiare | Guida TV |
| <img src="docs/screenshots/tv-list.jpg" alt="Elenco dei canali"> | <img src="docs/screenshots/movies.jpg" alt="Film"> |
| Elenco dei canali con i preferiti | Film |
| <img src="docs/screenshots/detail.jpg" alt="Scheda di un film"> | <img src="docs/screenshots/series.jpg" alt="Serie con stagioni ed episodi"> |
| Scheda di un film | Serie, stagioni e avanzamento |
| <img src="docs/screenshots/settings.jpg" alt="Raccolte locali"> | <img src="docs/screenshots/phone-home.jpg" alt="Home sul telefono" width="45%"> <img src="docs/screenshots/phone-tv.jpg" alt="Canali sul telefono" width="45%"> |
| Raccolte, con la scelta della cartella | Sul telefono |

<sub>Dati di esempio: canali e guida inventati, titoli dei film open source
del Blender Studio con immagini generate.</sub>

## Installazione

Scarica i pacchetti dall'[ultima release](https://github.com/silirim/VodMax/releases).
**Guida passo passo** per server, Android, Android TV, Fire TV e TV LG:
[docs/installazione.md](docs/installazione.md).

**Server (Linux)**

```sh
tar xzf vodmax-server_*_linux_amd64.tar.gz && cd vodmax-server_*/
sudo useradd --system --home /var/lib/vodmax --shell /usr/sbin/nologin vodmax
sudo install -m 755 vodmax-server vodmax-update /usr/local/bin/
sudo install -d -o vodmax -g vodmax -m 700 /var/lib/vodmax
sudo cp vodmax.service /etc/systemd/system/ && sudo systemctl enable --now vodmax
```

Installa `ffmpeg` della tua distribuzione per pausa della diretta,
registrazioni e MKV/AVI nel browser. Per aggiornare poi: `sudo vodmax-update`
(le app Android propongono da sole il loro aggiornamento).

**Docker**: `ghcr.io/silirim/vodmax` (x86-64, ARM64, ARMv7, con ffmpeg):

```sh
docker run -d --name vodmax --restart unless-stopped -p 8095:8095 -v vodmax-data:/var/lib/vodmax ghcr.io/silirim/vodmax
```

(i dati restano nel volume `vodmax-data`; c'è un
[file Compose](packaging/docker-compose.yml) pronto).

**Android / Android TV / Fire TV**: installa `VodMax-<versione>.apk`, poi
indica l'indirizzo del server oppure scegli *Usa questo dispositivo da solo*.

**LG webOS**: installa `app.vodmax.client_<versione>_all.ipk` con la
[CLI webOS](https://webostv.developer.lge.com/develop/tools/cli-introduction)
su una TV in Modalità sviluppatore (vedi la [guida](docs/installazione.md#tv-lg-webos)).
Altre TV (Samsung…): usa il browser della TV.

Dai sorgenti: [docs/sviluppo.md](docs/sviluppo.md) e `scripts/release.sh`.

## Primi passi

1. Apri `http://<server>:8095` e incolla il token di amministrazione che
   trovi in `/var/lib/vodmax/admin.token`. La lingua (italiano o inglese)
   segue il dispositivo e si cambia in **Impostazioni → Lingua**.
2. **Impostazioni → VPN**: importa il file `.conf` WireGuard, oppure indica
   una VPN che usi già sul router.
3. **Impostazioni → Abbonamenti**: aggiungi una playlist M3U o Xtream Codes,
   poi *Sincronizza ora*.
4. **Impostazioni → Raccolte**: aggiungi una cartella di film o di serie.
5. **Impostazioni → Dispositivi**: abbina TV e telefoni con il codice che
   mostrano.

## Documentazione

- [Decisioni di progetto](docs/decisioni/README.md): una scheda per ogni
  scelta, con opzioni e motivi.
- [Direzione: principi, scenari e roadmap](docs/direzione.md).
- [Sviluppo, struttura del codice e API HTTP](docs/sviluppo.md), e
  [AGENTS.md](AGENTS.md): regole, verifiche e rilasci per chi contribuisce.

## Contenuti

VodMax **non fornisce contenuti**: è un player e un media center. Playlist,
guide e cartelle le aggiunge chi lo usa, che ne è responsabile: usa solo
sorgenti per cui hai i diritti. Le issue e le discussioni che chiedono
playlist, canali o provider vengono chiuse senza risposta.

Questo prodotto usa le API di TMDB ma non è approvato né certificato da
[TMDB](https://www.themoviedb.org).

## Contribuire

Segnalazioni e pull request sono benvenute: leggi
[CONTRIBUTING.md](CONTRIBUTING.md) (i contributi si accettano con un accordo
di contribuzione) e [SECURITY.md](SECURITY.md) per le vulnerabilità.

## Licenza

VodMax è software libero con licenza
**[GNU Affero General Public License v3.0](LICENSE)**: puoi usarlo, studiarlo,
modificarlo e condividerlo; chi distribuisce una versione modificata, o la
offre come servizio in rete, deve renderne disponibile il codice con la
stessa licenza.

**Licenza commerciale.** Per includere VodMax in un prodotto o servizio
senza pubblicarne il codice è disponibile una licenza commerciale: contatta
l'autore ([silirim su GitHub](https://github.com/silirim)).

**Nome e logo.** La licenza riguarda il codice, non il marchio. Il nome
"VodMax" e i loghi in [`docs/brand`](docs/brand) identificano questo
progetto: fork e versioni modificate sono benvenuti, ma devono usare un nome
e un logo diversi e non presentarsi come il VodMax ufficiale o come
approvati dall'autore. Dire che un progetto è "basato su VodMax" o "un fork
di VodMax" va bene. Le build non modificate delle release ufficiali possono
mantenere il nome.

Le librerie incluse mantengono le loro licenze (Apache-2.0, MIT, ISC, BSD),
indicate accanto ai file.
