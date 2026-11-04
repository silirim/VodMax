# 0040 — Fuori casa dietro un login (tunnel o reverse proxy)

Stato: **Approvata** (4 novembre 2026: "ho pubblicato VodMax di casa su
internet con il tunnel Cloudflare esistente, dietro Cloudflare Access […]
Se serve un decision record, decidilo tu")

## Il problema

Fuori casa la strada indicata era la VPN di casa. Chi ha già un tunnel
(Cloudflare Tunnel) o un reverse proxy con login (Cloudflare Access,
Authelia, Authentik) pubblica VodMax così. Nel browser funziona; nell'app
Android no: il login del proxy sta su un altro sito, l'app apriva gli altri
siti nel browser esterno, quindi login e abbinamento finivano in Chrome e
l'app restava fuori. In più il player nativo e le altre app chiamano il
server senza il cookie del login.

## Decisione

- **VodMax continua a non aprire porte su internet.** La VPN di casa resta
  la strada consigliata; un tunnel o un proxy con login è una scelta di chi
  lo gestisce.
- **App Android**: quando il server reindirizza a un altro sito (il login
  del proxy), la pagina resta nell'app, senza il ponte con l'app, finché il
  login non riporta al server. Il cookie del login viene salvato quando
  l'app va in pausa. Indietro sulla pagina del login riporta alla prima
  schermata, dove si può cambiare indirizzo. I link toccati (trailer, siti
  esterni) si aprono ancora fuori dall'app.
- **Guide** (installazione): davanti a VodMax il login copre tutto tranne
  `/stream`, `/out`, `/api/v1/progress` e `/api/v1/health`. Li chiamano il
  player dell'app e le altre app; VodMax li protegge già con biglietti
  monouso, la chiave dell'uscita o il token del dispositivo.

## Non ancora

- App LG webOS dietro un proxy con login: non provata.
- Quando il login del proxy scade con l'app aperta, le chiamate della
  pagina falliscono finché non si cambia pagina (che riporta al login).
- Nessun controllo automatico che i quattro percorsi siano davvero
  lasciati liberi: se non lo sono, il player dell'app non parte.
