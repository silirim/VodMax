# 0012 — VPN interna obbligatoria per il provider

Stato: **Approvata** (29 ottobre 2026) per il principio; dettagli tecnici
in **Proposta**

## Decisione

**Né il Server né l'app possono contattare un provider se la VPN interna di
VodMax non è collegata.** Non esiste un'impostazione per disattivare questo
comportamento.

- Ogni abbonamento (0011) ha una **connessione VPN obbligatoria**. Senza VPN
  l'abbonamento si può salvare, ma resta "in attesa della VPN" e non scarica
  playlist, EPG, copertine del provider né flussi.
- Se la VPN cade durante una visione, il flusso si ferma. Mai ripiego sulla
  connessione normale.
- Una VPN può servire più abbonamenti.

## "Interna": cosa vuol dire

La VPN vive **dentro il processo di VodMax**, non nel sistema operativo:

- **non** usa la VPN di Android (`VpnService`) né interfacce di rete del
  server;
- solo il traffico verso i provider passa dal tunnel; il resto del
  dispositivo (e le altre VPN che l'utente usa) non viene toccato;
- funziona uguale su Server e app, e più VPN possono essere attive insieme
  anche sul telefono.

Tecnicamente: client **WireGuard in user space** con uno stack TCP/IP
proprio (lo stesso schema usato da Tailscale `tsnet` e da `wireproxy`:
`wireguard-go` + stack di rete gVisor). Il client HTTP e il player di VodMax
aprono le connessioni verso il provider **attraverso questo stack**; non
esiste un altro percorso nel codice.

## Garanzie da verificare con test automatici

1. Il modulo che parla col provider non ha accesso a socket normali: può
   aprire connessioni solo tramite il tunnel.
2. Con VPN spenta o in errore: zero pacchetti verso gli host del provider
   (test con rete simulata).
3. Anche la risoluzione DNS dei nomi del provider passa dal tunnel.
4. Player: i segmenti video arrivano dal tunnel o dal Server, mai dalla
   rete normale.

## Cosa non passa dalla VPN

Servizi che non sono il provider, se configurati: metadati e copertine TMDB,
aggiornamenti dell'app, comunicazione fra app e Server in casa.

## Configurazione

- **L'utente usa il proprio abbonamento VPN.** VodMax non include né
  fornisce una VPN (servirebbero server propri o accordi commerciali).
- Import di file WireGuard `.conf` o QR code: Mullvad, Proton (piani a
  pagamento; piano gratuito da verificare), Surfshark, IVPN, AirVPN,
  Windscribe Pro…
- **NordVPN** (facoltativo, in seguito): non fornisce file `.conf`; un
  assistente genera la configurazione dal "token di accesso" dell'account
  tramite l'API non ufficiale usata da strumenti come `wg-nord`. Marcato
  "non ufficiale, può smettere di funzionare".
- **OpenVPN**: in seguito, perché in user space è molto più complesso di
  WireGuard.
- La configurazione deve indicare un `DNS` raggiungibile nel tunnel: senza,
  i nomi del provider andrebbero risolti fuori dalla VPN, quindi VodMax la
  rifiuta.
- Chiave privata cifrata a riposo, mai nei log o nelle API.
- Stato visibile: collegata / non collegata, IP di uscita, ultimo handshake;
  per ogni abbonamento "via VPN X" oppure "bloccato: VPN non collegata".

## Domande aperte

- OpenVPN serve? In user space è molto più complesso di WireGuard.
- Il traffico TMDB deve passare anch'esso da una VPN?
- Nell'impianto di casa il Server va comunque nella rete dedicata al
  provider, come oggi, o la VPN interna basta a soddisfare la regola attuale?

## Aggiornamento del 29 ottobre 2026: VPN esterna come scelta

Su richiesta dell'utente: chi ha già una VPN fuori da VodMax (sul router
per tutta la rete, o l'app VPN sul dispositivo) può usarla invece della
WireGuard interna. Ogni connessione VPN ha un tipo:

- `wireguard` (consigliato, come prima): tunnel interno a VodMax;
- `system`: VodMax usa la rete normale del dispositivo. Non può
  verificare che passi davvero da una VPN: l'interfaccia lo dice
  chiaramente e la responsabilità è dell'utente.

Ogni abbonamento continua a indicare la sua connessione: il provider non
si contatta mai senza averne scelta una.

## Aggiornamento del 3 novembre 2026: VPN facoltativa (0039)

Su richiesta dell'utente la VPN non è più obbligatoria: "solo tramite VPN"
è un interruttore in Impostazioni → VPN, acceso in modo predefinito. Spento,
un abbonamento può non avere una VPN e usare la rete normale. Dettagli in
[0039](0039-vpn-facoltativa.md).
