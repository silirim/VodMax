# App VodMax

Le app usano l'interfaccia del server VodMax (la stessa del browser) e si
abbinano con il codice di Impostazioni → Dispositivi. Seguono la lingua del
sistema (italiano, altrimenti inglese). Installazione passo passo per chi le
usa: [docs/installazione.md](../docs/installazione.md).

## Android (telefono, Android TV, Fire TV) — `android/`

Un solo APK. L'interfaccia è in una WebView; film, episodi, registrazioni
e catch-up sono riprodotti dal player nativo Media3 (MKV, AC-3/E-AC-3,
HEVC senza conversione sul server, tracce audio e sottotitoli). Sul
telefono il lettore si apre in orizzontale; il pulsante schermo intero torna
alla rotazione del telefono (decisione 0047). Le registrazioni in MPEG-TS si
spostano riaprendo il file dal secondo scelto. Il tasto Indietro passa dalla
pagina (prima esce dallo schermo intero della TV in diretta); il banner
compare nel launcher di Android TV.

```sh
cd clients/android
echo "sdk.dir=/percorso/android-sdk" > local.properties
JAVA_HOME=/percorso/jdk-17 ./gradlew assembleDebug
# → app/build/outputs/apk/debug/app-debug.apk
```

Primo avvio: indirizzo del server (es. `http://192.168.1.10:8095`), poi il
codice di abbinamento. Server diverso: Impostazioni → Sistema → Cambia server
(o "Cambia server" nel menu Altro sul telefono).

**Senza server** ("Usa questo dispositivo da solo", decisione 0021): l'APK
contiene il server stesso, che va compilato prima dell'APK (Go e Android
NDK):

```sh
./build-server.sh   # → app/src/main/jniLibs/<abi>/libvodmax.so
```

Senza registrazioni né pausa della diretta. Funziona anche su Fire TV con
Fire OS (non sulla Fire TV Stick 4K Select, che usa Vega OS).

## LG webOS — `webos/`

Un avvio leggero: chiede l'indirizzo del server, controlla che risponda e
apre l'interfaccia (player del browser della TV). All'avvio con l'indirizzo
salvato aspetta un secondo e mezzo: OK o Indietro tornano al modulo per
cambiare server; Indietro sul modulo chiude l'app. Pacchetto con gli
strumenti LG:

```sh
npm install @webos-tools/cli
npx ares-package clients/webos -o dist      # → app.vodmax.client_<versione>_all.ipk
npx ares-setup-device                         # la TV in modalità sviluppatore
npx ares-install -d <tv> dist/app.vodmax.client_*.ipk
```

Compatibilità: interfaccia completa sulle TV LG dal 2022 (webOS 22); sulle
precedenti funziona ma l'impaginazione può essere imperfetta.
