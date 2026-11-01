<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/vodmax-wordmark-dark.svg">
  <img src="docs/brand/vodmax-wordmark-light.svg" alt="VodMax" width="360">
</picture>

**Your live TV, your movies and your series in one self-hosted media center.**

Live channels with a full TV guide, pause and recordings like a set-top box,
movies and series from your playlists and your own folders — on the TV, the
phone and the browser.

[![CI](https://github.com/silirim/VodMax/actions/workflows/ci.yml/badge.svg)](https://github.com/silirim/VodMax/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/silirim/VodMax?include_prereleases&sort=semver)](https://github.com/silirim/VodMax/releases)
[![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/silirim/VodMax)](go.mod)

[Features](#features) · [Screenshots](#screenshots) · [Install](#install) · [Quick start](#quick-start) · [Italiano](README.it.md)

<img src="docs/screenshots/home.jpg" alt="VodMax home: continue watching, movies and series" width="100%">

</div>

## Why VodMax

Jellyfin and Plex are great for files on disk, but slow and awkward with
live TV and with playlists of hundreds of thousands of titles. Playlist
players are fast but stay on one device and remember nothing elsewhere.
VodMax is both: a **server for the home** that keeps the catalog, the guide,
the recordings and everyone's progress, and **apps** that feel like a TV
decoder with the remote.

- **Built for huge catalogs** — hundreds of thousands of titles in one
  SQLite database, full-text search, one card per title instead of dozens of
  copies.
- **Live TV first** — channel numbers, P+/P−, favourites, now/next banner,
  pause and rewind, catch-up and recordings.
- **One place** — playlists (M3U, Xtream Codes: the TV playlists you are
  entitled to use) and local folders of movies and series in the same home,
  search and "continue watching".
- **Private by design** — playlist traffic goes only through a VPN (the
  built-in WireGuard tunnel or one you run on the router); credentials are
  encrypted; no telemetry.

## Features

| | |
|---|---|
| 📺 **Live TV** | Remote-friendly zapping (numbers, P+/P−, favourites only), channel list, now/next, official channel numbering, sport and radio sections, **multiview** (2 or 4 channels at once) |
| 🗓️ **TV guide** | Programme grid from XMLTV (playlist guide and external sources), record or replay from the guide |
| ⏸️ **Timeshift & catch-up** | Pause and rewind live TV, start the programme from the beginning, replay from the playlist's archive |
| ⏺️ **Recordings (DVR)** | Record what is on, from the guide or by rule ("every match", "the news", live only), with disk and connection checks; "skip ad" with Comskip |
| 🎬 **Movies & series** | Posters, plots and cast, continue watching, my list, next episode, skip intro, audio and subtitle languages, MKV/AVI/HEVC converted on the fly (also with the graphics card: VAAPI, Quick Sync, NVENC) |
| ⬇️ **Downloads** | Copy movies, episodes, seasons or whole series to the server's disk, to watch them without the playlist; one at a time, only on a free connection (whoever watches comes first), optionally at night |
| 📁 **Local collections** | Folders of movies or series named like Jellyfin/Kodi/Plex libraries, `.nfo` files and artwork next to the videos |
| 📡 **Sources** | Xtream Codes, M3U playlists and Stalker portals (MAC) |
| 🧹 **Catalog selection** | Keep only the countries, years and categories you want, with a live preview |
| 👪 **Profiles** | Per-person progress and favourites, kids profiles, devices paired with a short code or signed in with your home account (OpenID Connect: Authelia, Authentik, Keycloak…) |
| ✨ **Assistant** | Optional: built in (VodMax downloads and runs the model at home), Claude and other online services with an API key, or your own server. Write a recording rule as a sentence, ask what to watch tonight |
| 🔌 **Other apps** | Plex, Jellyfin and Emby see VodMax as an HDHomeRun tuner; M3U playlist and XMLTV guide for TiviMate, Kodi, VLC |
| 🔒 **Built-in VPN** | WireGuard in user space inside the server and the app: without the VPN, playlists are never contacted |
| 💾 **Backups** | Encrypted backups and automatic copies, health warnings for VPN, playlists, guide and disk |
| 🖼️ **Metadata** | From the playlist, from `.nfo` files, or from TMDB with your own key |

### Apps

| Platform | |
|---|---|
| **Browser** | The full interface, made for the remote and the mouse |
| **Android, Android TV, Fire TV** | Native player (Media3) for every format; can also run **standalone, without a server** |
| **LG webOS** | TV app (webOS 22+ recommended) |
| **Linux server** | x86-64, ARM64 (Raspberry Pi 4/5), ARMv7; one binary, SQLite |

## Screenshots

| | |
|---|---|
| <img src="docs/screenshots/tv.jpg" alt="Live TV with now and next"> | <img src="docs/screenshots/guide.jpg" alt="TV guide grid"> |
| Live TV: browse other channels without zapping | TV guide |
| <img src="docs/screenshots/tv-list.jpg" alt="Channel list"> | <img src="docs/screenshots/movies.jpg" alt="Movies"> |
| Channel list with favourites | Movies |
| <img src="docs/screenshots/detail.jpg" alt="Movie details"> | <img src="docs/screenshots/series.jpg" alt="Series with seasons and episodes"> |
| Movie details | Series, seasons and progress |
| <img src="docs/screenshots/settings.jpg" alt="Local collections settings"> | <img src="docs/screenshots/phone-home.jpg" alt="Phone home" width="45%"> <img src="docs/screenshots/phone-tv.jpg" alt="Phone channel list" width="45%"> |
| Local collections with the folder picker | On the phone |

<sub>Screenshots use demo data: invented channels and guide, and the titles
of the Blender Studio open movies with generated artwork. The interface is in
Italian and English (chosen per device; new languages are one dictionary file
each).</sub>

## Install

Download the packages from the
[latest release](https://github.com/silirim/VodMax/releases).
**Step-by-step guide** for the server, Android, Android TV, Fire TV and LG
TVs: [docs/install.md](docs/install.md).

**Server (Linux)**

```sh
tar xzf vodmax-server_*_linux_amd64.tar.gz && cd vodmax-server_*/
sudo useradd --system --home /var/lib/vodmax --shell /usr/sbin/nologin vodmax
sudo install -m 755 vodmax-server vodmax-update /usr/local/bin/
sudo install -d -o vodmax -g vodmax -m 700 /var/lib/vodmax
sudo cp vodmax.service /etc/systemd/system/ && sudo systemctl enable --now vodmax
```

Install `ffmpeg` from your distribution for timeshift, recordings and MKV/AVI
in the browser. To update later: `sudo vodmax-update` (the Android apps offer
their updates by themselves).

**Docker** — `ghcr.io/silirim/vodmax` (x86-64, ARM64, ARMv7, ffmpeg included):

```sh
docker run -d --name vodmax --restart unless-stopped -p 8095:8095 -v vodmax-data:/var/lib/vodmax ghcr.io/silirim/vodmax
```

(the data stays in the `vodmax-data` volume; a
[compose file](packaging/docker-compose.yml) is ready).

**Android / Android TV / Fire TV** — install `VodMax-<version>.apk`, then enter
the server address or choose *Use this device on its own* (standalone).

**LG webOS** — install `app.vodmax.client_<version>_all.ipk` with the
[webOS CLI](https://webostv.developer.lge.com/develop/tools/cli-introduction)
on a TV in Developer Mode (see the [guide](docs/install.md#lg-tv-webos)).
Other TVs (Samsung...): use the TV browser.

Build from source: [docs/sviluppo.md](docs/sviluppo.md) and
`scripts/release.sh`.

## Quick start

1. Open `http://<server>:8095` and paste the admin token from
   `/var/lib/vodmax/admin.token`. The language (Italian or English) follows
   the device and can be changed in **Settings → Language**.
2. **Settings → VPN**: import your WireGuard `.conf`, or declare a VPN you
   already run on the router.
3. **Settings → Subscriptions**: add an M3U or Xtream Codes playlist, then
   *Sync now*.
4. **Settings → Collections**: add a folder of movies or series.
5. **Settings → Devices**: pair TVs and phones with the code they show.

## Documentation

- [Design decisions](docs/decisioni/README.md) (Italian): one page per choice,
  with options and reasons.
- [Direction: principles, scenarios and roadmap](docs/direzione.md) (Italian).
- [Development, code structure and HTTP API](docs/sviluppo.md) (Italian), and
  [AGENTS.md](AGENTS.md): rules, checks and releases for contributors.

## Content

VodMax **does not provide any content**. It is a player and a media center:
playlists, guides and folders are added by whoever uses it, who is
responsible for them. Use only sources you have the rights to.
Issues and discussions asking for playlists, channels or providers are
closed without an answer.

This product uses the TMDB API but is not endorsed or certified by
[TMDB](https://www.themoviedb.org).

## Contributing

Issues and pull requests are welcome: read [CONTRIBUTING.md](CONTRIBUTING.md)
(contributions are accepted under a contributor license agreement) and
[SECURITY.md](SECURITY.md) for vulnerabilities. VodMax speaks Italian and
English; new languages are plain JSON files, ready for Weblate (see
[Translations](CONTRIBUTING.md#translations)).

## License

VodMax is free software under the
**[GNU Affero General Public License v3.0](LICENSE)**: you can use, study,
change and share it; whoever distributes a modified version, or offers it as
a network service, must make its source available under the same license.

**Commercial license.** To include VodMax in a product or service without
publishing its source, a commercial license is available: contact the author
([silirim on GitHub](https://github.com/silirim)).

**Name and logo.** The license covers the code, not the brand. The name
"VodMax" and the logos in [`docs/brand`](docs/brand) identify this project:
forks and modified versions are welcome, but must use a different name and
logo, and must not suggest they are the official VodMax or endorsed by its
author. Saying that a project is "based on VodMax" or "a fork of VodMax" is
fine. Unmodified builds of the official releases may keep the name.

Bundled libraries keep their own licenses (Apache-2.0, MIT, ISC, BSD),
listed next to their files.
