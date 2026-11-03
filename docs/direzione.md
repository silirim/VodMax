# Direzione di VodMax

Che cos'è VodMax, i principi che non cambiano, gli scenari per cui è
pensato e dove sta andando. Le scelte puntuali, con alternative e motivi,
sono nelle [decisioni](decisioni/README.md); le istruzioni per chi ci
lavora in [AGENTS.md](../AGENTS.md).

*Aggiornato al 3 novembre 2026 (0.6.0 stabile).*

## In una frase

VodMax è un server per la TV di casa: prende le playlist che hai già
(Xtream Codes, M3U, portali Stalker) e le tue cartelle di film e serie, e ne
fa un'esperienza da decoder per tutta la famiglia, su TV, telefoni e PC,
con guida, registrazioni, profili e un assistente facoltativo.

## Principi

Questi principi valgono per ogni funzione nuova. Se una proposta ne viola
uno, la proposta cambia.

1. **Nessun contenuto.** VodMax organizza quello che l'utente ha. Non
   fornisce, non cerca e non suggerisce playlist, canali o provider.
2. **Il provider tramite VPN, se lo scegli** ([0012](decisioni/0012-connessioni-vpn.md),
   [0039](decisioni/0039-vpn-facoltativa.md)). Ogni richiesta al provider
   passa da un solo client (`internal/provider`) e dalla connessione
   dell'abbonamento. Con "solo tramite VPN" acceso (il predefinito) senza
   VPN il provider non viene contattato; spento, un abbonamento può usare
   la rete normale. Mai ripiego dalla VPN alla rete normale.
3. **Tutto in casa.** Dati, credenziali (cifrate) e storico restano sul
   server. Nessun servizio cloud obbligatorio, nessuna telemetria. Quello
   che va fuori casa (TMDB, un assistente online) è scelto e configurato
   dall'utente, con la sua chiave, e la pagina dice cosa esce.
4. **Telecomando prima di tutto.** Ogni schermata si usa con frecce, OK e
   Indietro; poi telefono e mouse. Un'azione che si fa solo con il mouse
   è un difetto.
5. **Leggero.** Un solo programma Go con SQLite, a suo agio in un
   container da 4 GB su un mini PC. Le parti pesanti (motore e modelli
   dell'assistente, driver della scheda grafica) arrivano solo quando
   l'utente le attiva, e si possono togliere.
6. **Facoltativo davvero.** Una funzione spenta non lascia tracce:
   niente pulsanti grigi, niente "attiva per usare". Le funzioni che
   meritano di essere conosciute si propongono una volta, in un punto
   preciso, con "Non ora".
7. **L'IA propone, VodMax verifica.** Un modello linguistico può sbagliare:
   sceglie solo tra codici che VodMax gli dà, le regole si salvano solo
   dopo il controllo della persona, "un film con…" passa dalla scheda del
   film. Al modello non vanno mai credenziali o indirizzi del provider.
8. **Mai perdere dati.** Import del catalogo tutto o niente (un calo
   anomalo tiene il precedente), backup cifrati e automatici, migrazioni
   del database che si applicano da sole dopo una copia.
9. **Una voce misurata in pubblico.** Nome, logo, sito e testi pubblici non
   usano la parola "IPTV": VodMax parla di playlist, TV, film e serie.
10. **Aperto.** AGPL-3.0 con licenza commerciale su richiesta
    ([0009](decisioni/0009-licenza-e-pubblicazione.md)), traduzioni in JSON
    pronte per Weblate, decisioni pubbliche.

## Scenari

### Dove gira il server

| Scenario | Come | Note |
|---|---|---|
| **Container Proxmox (LXC)** — il caso di riferimento | Debian 12/13, 2–4 core, 4 GB di RAM, `vodmax-server` + `vodmax.service`, ffmpeg | Aggiornamenti con `vodmax-update`. L'assistente integrato gira nello stesso container (serve `libgomp1`, che arriva con ffmpeg) oppure in un container accanto. |
| **Docker / Compose** | `ghcr.io/silirim/vodmax`, dati in un volume | Immagine Alpine leggera; l'assistente integrato usa il servizio facoltativo `assistant` (Ollama) del compose. Scheda grafica con `--device /dev/dri` o runtime NVIDIA. |
| **Unraid** | Template in `packaging/unraid` | Come Docker; l'assistente punta a un container Ollama con `VODMAX_ASSISTANT_OLLAMA`. |
| **PC o VM Linux** | Pacchetto `.tar.gz` per amd64, arm64, armv7 | Stesso servizio systemd del container. |
| **Solo un dispositivo** | APK Android con il server dentro ([0021](decisioni/0021-dispositivo-da-solo.md)) | Per una Fire TV senza server in casa: niente registrazioni né pausa della diretta. |

### Da dove si guarda

- **Browser** (PC, telefono): l'interfaccia completa, servita dal server.
- **App Android** (telefono e Android TV): l'interfaccia del server con il
  lettore nativo Media3 per film e serie, aggiornamenti automatici.
- **TV LG webOS**: un avvio che apre l'interfaccia del server.
- **Altre app** ([0028](decisioni/0028-uscita-verso-altre-app.md)): Plex,
  Jellyfin ed Emby vedono VodMax come un sintonizzatore HDHomeRun; TiviMate,
  Kodi e VLC usano la playlist M3U e la guida XMLTV del server.
- **Fuori casa**: tramite la VPN di casa (WireGuard, Tailscale…). VodMax non
  apre porte su internet.

### In famiglia

- **Profili** per persona, con avanzamenti e preferiti propri; **profili
  bambini** con soli contenuti adatti; PIN per gli amministratori.
- **Dispositivi abbinati** con un codice breve, oppure accesso con l'account
  di casa (OpenID Connect: Authelia, Authentik, Keycloak).
- **Una sola connessione del provider**, caso frequente: chi guarda ha la
  precedenza, i download partono solo su una connessione libera, una
  registrazione in conflitto viene rifiutata con il motivo.

### TV e registrazioni

- Zapping da decoder: numeri ufficiali dei canali, P+/P−, preferiti, guida,
  pausa della diretta, catch-up dove il provider lo offre, multiview.
- Registrazioni dal programma in onda, dalla guida o da **regole** ("tutte
  le partite", "il telegiornale, tienine 3"), con controllo dello spazio e
  pubblicità segnata con Comskip.

### L'assistente (facoltativo)

| Scenario | Modo | Cosa aspettarsi |
|---|---|---|
| Mini PC senza scheda video | Integrato, modello Bilanciato (Qwen 3 4B) | Regola in ~12 s, "stasera" in 30–60 s; nulla esce di casa. |
| Server con scheda video | Integrato (Vulkan) o server Ollama in casa, modello Qualità (Qwen 2.5 14B) | Regola in 3–8 s, "stasera" in ~10 s. |
| Nessun hardware adatto | Servizio online con chiave API (Claude Haiku 5.5 predefinito) | Veloce, pochi centesimi al mese; guida della serata e titoli vanno al servizio. |
| Assistente spento | — | Nessuna sua funzione compare. |

Cosa fa oggi: crea regole di registrazione da una frase; "Cosa guardo
stasera?" tra i programmi della serata e i tuoi titoli; richieste precise
("un film con Catherine Zeta-Jones", "un film di Nolan") cercate nel catalogo
e verificate sulle schede, con l'indice delle persone
([0036](decisioni/0036-indice-delle-persone.md)).

## Stato

La **0.6.0** è in beta: assistente (integrato, online, server proprio),
accesso con account di casa, conversione con la scheda grafica, multiview,
portali Stalker, Comskip, traduzioni pronte per Weblate, indice delle
persone. Le beta si propongono solo a chi attiva "Proponi anche le versioni
beta".

## Dove va

Le voci "deciso" hanno una decisione approvata; le altre sono candidate e
diventano lavoro solo con una decisione.

### Rimasto dalla 0.6.0 (deciso)

La 0.6.0 è uscita stabile il 3 novembre 2026 per scelta del proprietario;
queste prove restano da chiudere nelle 0.6.x:

- Prove su dispositivi veri: lettore Android nelle serie, picture in
  picture, tasto Invio dalle tastiere Android, app LG.
- Lasciare che l'indice delle persone si riempia e controllare che il ritmo
  notturno non pesi sul provider.
- Progetto Weblate per le traduzioni; template Unraid nelle Community Apps.

### Candidati per le versioni successive

- **Assistente**
  - Una raccolta di 50–100 richieste reali per misurare dove i modelli
    sbagliano, prima di cambiare modelli o istruzioni.
  - Ricerca a parole nel catalogo ("quel film con il cane e il bambino").
  - "Nella puntata precedente", pulizia del catalogo, diagnosi dei problemi,
    avvisi intelligenti ([0034](decisioni/0034-assistente.md)).
- **Persone**: ricerca per attore o regista anche nell'interfaccia, senza
  assistente.
- **Programma per Windows** con WebView2 e libmpv
  ([0018](decisioni/0018-player-e-formati.md), proposta).
- **Rivedere le decisioni iniziali** ancora "Proposta" (0001–0008, 0010,
  0014), scritte prima del codice: confermare, aggiornare o chiudere.

### Più avanti, solo con misure in mano

- Affinare (LoRA) un modello piccolo per l'uso sul processore: utile per
  regole e formato delle risposte, non per i fatti (chi recita in cosa sta
  nelle schede, non nella memoria del modello).
- L'idea originale "Agent che diventa Server"
  ([0001](decisioni/0001-modalita-agent-server.md)).

### Cosa VodMax non farà

- Fornire o cercare contenuti, playlist o provider.
- Un servizio cloud VodMax o account VodMax.
- Telemetria, pubblicità, tracciamento.
- Aggirare protezioni o i limiti di connessione del provider.
