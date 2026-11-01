# Installing VodMax

[Italiano](installazione.md)

VodMax has two parts: the **server**, which keeps the catalog, guide,
recordings and everyone's progress, and the **apps** (browser, Android,
Android TV, Fire TV, LG webOS) that show it. An Android device can also run
VodMax **alone**, without a server (see [Fire TV and Android without a
server](#fire-tv-and-android-without-a-server)).

Packages are in the [releases](https://github.com/silirim/VodMax/releases):

| File | For |
|---|---|
| `vodmax-server_<v>_linux_amd64.tar.gz` | Server on a PC, mini PC, virtual machine or container (x86-64) |
| `vodmax-server_<v>_linux_arm64.tar.gz` | Server on Raspberry Pi 4/5 or an ARM NAS (64-bit) |
| `vodmax-server_<v>_linux_armv7.tar.gz` | Server on 32-bit ARM boards |
| `VodMax-<v>.apk` | Android phones and tablets, Android TV, Google TV, Fire TV |
| `app.vodmax.client_<v>_all.ipk` | LG TVs with webOS |
| `SHA256SUMS` | Checksums: `sha256sum -c --ignore-missing SHA256SUMS` (only the files you downloaded) |

## 1. Server

Any Linux machine that stays on: a mini PC, a NAS, a Raspberry Pi, a
virtual machine or an LXC container. It needs little: a few hundred MB of memory and
about 200 MB of disk for a catalog of 100,000+ titles, plus the space for
recordings.

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

- **ffmpeg**: install it from your distribution (`sudo apt install ffmpeg`;
  it also brings `libgomp1`, needed by the built-in assistant).
  Without it VodMax works, but there is no pause of live TV, no recordings
  and MKV/AVI files do not play in the browser (the Android app plays them
  anyway).
- **Port**: the interface is on `http://<server>:8095`. Change it in
  `vodmax.service` (`-listen`). Keep it inside your home network; from
  outside use your own VPN (WireGuard, Tailscale...) rather than opening the
  port.
- **Data**: everything is in `/var/lib/vodmax` (database, encryption key,
  admin token, recordings, downloads, backups). Downloads can go to another
  disk with `-downloads /path` in `vodmax.service` (the folder must be
  writable by `vodmax`, e.g. listed in `ReadWritePaths`).
- **Folders for collections**: the service runs as the `vodmax` user and,
  with the provided unit, cannot see `/home` or `/tmp`. Put or mount your
  movie folders under `/mnt`, `/srv` or `/media` and make them readable by
  `vodmax`.

### With Docker

The image `ghcr.io/silirim/vodmax` (x86-64, ARM64, ARMv7) contains the
server and ffmpeg. With [`docker-compose.yml`](../packaging/docker-compose.yml):

```sh
curl -fLO https://raw.githubusercontent.com/silirim/VodMax/main/packaging/docker-compose.yml
mkdir vodmax-data && sudo chown 1000:1000 vodmax-data
docker compose up -d
docker compose exec vodmax cat /var/lib/vodmax/admin.token
```

Mount your movie folders read-only (e.g. `/srv/media:/srv/media:ro`) and use
those paths for the collections. Update with
`docker compose pull && docker compose up -d`.

**Unraid, NAS**: started as root with `PUID`/`PGID` (`--user 0 -e PUID=99
-e PGID=100`), the container gives the data folder to that user and runs
VodMax as that user. For Unraid there is a template:
[`packaging/unraid/vodmax.xml`](../packaging/unraid/vodmax.xml) (Docker →
Add container → Template, or copy it to `/boot/config/plugins/dockerMan/templates-user/`).

**Graphics card**: to convert videos the browser can't read (HEVC, MPEG-2)
with the graphics card instead of the processor, pass it to the container:
Intel/AMD with `--device /dev/dri` and `--group-add <render group id>`
(`getent group render`; the image has the Intel VAAPI driver on x86-64, AMD works with a native install), NVIDIA
with the NVIDIA Container Toolkit (`--gpus all`, with an ffmpeg that has
NVENC). Then Settings → System → *Video conversion* → *Test*.

### Updating

The server checks the GitHub releases once a day (it contacts GitHub only
and sends nothing about your installation; it can be turned off in
Settings → System → Version and updates) and shows there when a new version
is out.

- **Server**: `sudo vodmax-update` downloads the build for this machine,
  checks it against the release's checksums, keeps the running one as
  `vodmax-server.old`, installs the new one and restarts the service.
  `sudo vodmax-update v0.4.0` installs a given version (also to go back).
  The database is upgraded automatically at start; a backup first is wise
  (Settings → System → Backup). Before changing it the server keeps a copy
  in `<data>/pre-upgrade/` (the last three): an older version refuses a
  database upgraded by a newer one, so to go back stop the service, put
  that copy in place of `vodmax.db`, then install the older version.
  From 0.5.x, name the version (`sudo vodmax-update v0.6.0`): the 0.5.x
  updater would also take a beta.
- **Android apps** (phone, Android TV, Fire TV) offer the update by
  themselves: *Update*, then confirm in the Android window (the first time,
  allow VodMax to install apps). It works from version 0.4.0, the first
  signed with the release key: devices with an older version must uninstall
  it and install 0.4.0 once.
- **LG webOS**: the interface comes from the server, so it is always
  current; the app itself rarely changes (reinstall it as above when it
  does).
- **Betas**: anyone can install one (`sudo vodmax-update v0.6.0-beta.2`, the
  APK from the release page, or the Docker tag), but they are offered only
  with *Offer beta versions too* on in Settings → System. Plain
  `sudo vodmax-update` installs stable versions only and never goes back.

## 2. First setup

1. Open `http://<server>:8095` in a browser. It asks for the **admin
   token**: copy it from the server with
   `sudo cat /var/lib/vodmax/admin.token`.
2. **Settings → Language**: Italian or English (per device; the profile
   screen has the same choice).
3. **Settings → VPN**: VodMax contacts playlists **only through a VPN**.
   Import the WireGuard `.conf` file of your VPN service (Proton VPN,
   Mullvad, NordVPN... — one file per device is best), or declare an
   *external VPN* if the server already goes out through a VPN on the
   router (then it is up to you that the traffic really goes through it).
4. **Settings → Subscriptions**: add an M3U playlist (its URL) or an Xtream
   Codes account (server, user, password), choose the VPN, then *Sync now*.
   The catalog and the guide are refreshed every night and the guide every
   few hours (Settings → System).
5. **Settings → Selection** and **Categories**: keep only the countries,
   years and categories you want; the preview shows the effect before
   saving.
6. **Settings → Collections** (optional): add folders of movies or of
   series (one subfolder per series). Names like `Movie (2010).mkv` and
   `Series/Season 1/S01E03.mkv` are recognised; `.nfo` files and pictures
   next to the videos are used when present.
7. **Settings → Profiles** (optional): one profile per person, kids
   profiles, PINs.
8. **Settings → System → Artwork and details (TMDB)** (optional): a free
   TMDB key adds posters, plots and cast where the playlist has none.
9. **Settings → Assistant** (optional): writes recording rules from a
   sentence and suggests what to watch tonight. Off, VodMax shows none of
   it. Three ways:
   - **Built in**: VodMax offers the models that fit the server's memory
     and, when you press *Turn on*, downloads the llama.cpp engine
     (~35 MB) and the model (0.9–8.4 GB) into `/var/lib/vodmax/assistant`,
     checking their fingerprints. It runs on the server, listening only on
     the server itself, and stops after 10 minutes without requests.
     *Remove* deletes everything. Linux x86-64 or ARM64; a graphics card
     is used through Vulkan when `libvulkan1` and its drivers are
     installed. With **Docker** the model runs in the optional `assistant`
     service of the compose file: `docker compose --profile assistant up -d`.
   - **Online service**: Claude, OpenAI, Mistral, Gemini or OpenRouter with
     an **API key** (a Claude Pro/Max or ChatGPT Plus subscription can't be
     used by another app). For this use it costs a few cents a month; the
     evening guide, the channel names and your list's titles go to the
     service.
   - **Your server**: any OpenAI-compatible API (Ollama, LM Studio...).

   For requests like "a film with…", VodMax keeps the cast and director of
   your films and series: from the detail pages you open and, while the
   assistant is on, from one page every 3 seconds between 1:00 and 7:00
   (through the subscription's VPN).

## 3. Devices

Every device is **paired** once: it shows a code, and you approve it in
**Settings → Devices** on a device that is already signed in. The code is
valid for 10 minutes.

### Browser (PC, Mac, tablet, any TV)

Open `http://<server>:8095`. It works on any recent browser, including the
browser of Samsung, Philips and other smart TVs (without the colour buttons
of the remote).

If your home already has a sign-in provider (Authelia, Authentik, Keycloak or
any OpenID Connect provider), browsers can enter with an account instead of a
pairing code: Settings → Devices → *Sign-in with a home account*. Create a
client in the provider with the redirect address shown there (scopes
`openid email profile`), then link each account (e-mail or username) to a
profile. Every account always enters only its own profile. The provider must
be reachable over `https://`.

### Android phone, tablet, Android TV, Google TV

1. Download `VodMax-<v>.apk` on the device (or copy it with a USB stick).
2. Allow installing apps from that source when Android asks
   (*Install unknown apps*).
3. Open VodMax, enter the server address (`http://192.168.1.10:8095`), then
   approve the pairing code.

Films and series play in the native player (every format, audio tracks and
subtitles); the rest is the same interface as the browser. On Android TV the
app is in the launcher with its banner.

### Fire TV

Works on Fire TV devices with **Fire OS** (Fire TV Stick Lite, HD, 4K,
4K Max, Fire TV Cube, Fire TV models). It does **not** work on the
Fire TV Stick 4K Select (2025), which runs Vega OS and cannot install apps
from outside the store.

1. *Settings → My Fire TV → About*: click the device name 7 times to show
   *Developer options*; there enable *Apps from unknown sources* (or allow
   the Downloader app).
2. Install **Downloader** from the Amazon Appstore and open in it the link
   of `VodMax-<v>.apk` from the release page.
3. Open VodMax: enter the server address, or choose *Use this device on its own*.

The Fire TV remote has no channel and colour buttons: use Up/Down and OK
(channel list) on live TV.

### Fire TV and Android without a server

On the first screen choose **Use this device on its own**: the app starts its own
VodMax server inside the device and opens Settings → Subscriptions. Add the
VPN `.conf` and the playlist as above. Limits: no recordings and no pause of
live TV (no ffmpeg on the device); films, series and live TV work. The
device contacts the playlist itself, through its VPN.

### LG TV (webOS)

LG TVs install apps from outside the store only in **Developer Mode**.
If that is too much, use the TV browser instead (above).

1. Create a free account on [webostv.developer.lge.com](https://webostv.developer.lge.com)
   (it is your LG account).
2. On the TV, install **Developer Mode** from the LG Content Store, sign in,
   turn on *Dev Mode Status* (the TV restarts), then turn on *Key Server*.
   Note the TV's IP address and the passphrase it shows.
3. On a computer with Node.js in the same network:

   ```sh
   npm install -g @webos-tools/cli
   ares-setup-device -a tv -i "{'host':'<tv-ip>','port':'9922','username':'prisoner'}"
   ares-novacom --device tv --getkey          # type the passphrase
   ares-setup-device -m tv -i "{'privatekey':'tv_webos','passphrase':'<passphrase>'}"
   ares-install --device tv app.vodmax.client_<v>_all.ipk
   ```

   (`tv_webos` is the key file `--getkey` saves in `~/.ssh`.)
4. Open VodMax on the TV, enter the server address and approve the pairing
   code.

Developer Mode lasts **50 hours**: open the Developer Mode app and press
*Extend* before it expires, otherwise apps installed this way stop opening
until you extend it. The full interface needs webOS 22 (2022 TVs) or later;
older TVs work with a less polished layout.

### Samsung and other TVs

There is no Samsung (Tizen) app yet: use the TV browser at
`http://<server>:8095`.

### Other apps: Plex, Jellyfin, Emby, TiviMate, Kodi

**Settings → System → Output to other apps → Turn on** gives three
addresses with a secret key:

- **HDHomeRun tuner** — Plex (*Settings → Live TV & DVR → Set up*, enter the
  address manually), Jellyfin and Emby (*Live TV → Add tuner → HDHomeRun*)
  see VodMax as a tuner and record with their own DVR.
- **M3U playlist** and **XMLTV guide** — TiviMate, Kodi (PVR IPTV Simple),
  VLC, or Jellyfin as an M3U tuner.

Channels still go through VodMax (same connections, same VPN); as many play
at once as your subscriptions allow. Whoever has the addresses can watch the
channels: keep them at home; *Change the key* cuts off the old ones.

## 4. Troubleshooting

| Problem | What to do |
|---|---|
| "The server does not answer" on a device | Same network? Try the address in a phone browser. Check `systemctl status vodmax`. |
| The pairing code expired | It lasts 10 minutes; a new one appears by itself. Once approved, the device collects it within 30 minutes, also if the app was in the background. |
| A collection folder "does not exist" | The `vodmax` user must be able to read it, and with the provided unit it must not be under `/home` or `/tmp`. |
| Live TV does not start, "all connections in use" | Your playlist allows few connections at once: close another device, or take over. |
| Playlist sync fails | Settings → VPN: is the VPN connected? Settings → Subscriptions shows the error. |
| MKV or AVI films do not play in the browser | Install `ffmpeg` on the server, or use the Android app. |
| Logs | `journalctl -u vodmax -f` |
