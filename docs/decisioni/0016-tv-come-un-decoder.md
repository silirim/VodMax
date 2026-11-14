# 0016 — La sezione TV come un decoder (stile Sky)

Stato: **Approvata** (29 ottobre 2026), prima versione realizzata

## Richiesta

La sezione TV deve funzionare come le "dirette" del VodMax precedente, ma
con l'uso di un decoder Sky:
- cambio canale con il telecomando;
- preferiti, e possibilità di usare solo la lista dei preferiti;
- vedere cosa c'è sui canali successivi senza cambiare canale, anche con una
  lista a schermo da scorrere.

## Verifiche sul provider reale (29 ottobre 2026)

| | Risultato |
|---|---|
| Formato dei canali | MPEG-TS, H.264 + AAC, su tutti i canali provati (anche "4K") |
| Avvio di un canale | circa 270 ms al primo dato dal provider, circa 340 ms tramite il server VodMax |
| HLS | Disponibile, ma su alcuni canali lento (5,4 s) |
| Guida XMLTV del provider | 5 MB, 2,5 giorni, programmi per 128 canali |

## Decisioni

1. **Numerazione da telecomando.** I canali nazionali vengono riconosciuti e
   ricevono il numero LCN ufficiale del digitale terrestre (lista del 10
   settembre 2026, in `internal/lineup/it.json`). Gli altri canali prendono i
   numeri dal 100 in su: prima TV, poi sport, poi radio. Il numero non cambia
   tra lista completa, sezioni e preferiti.
2. **Preferiti per canale, non per versione.** Restano validi anche se il
   provider cambia i flussi; hanno il loro ordine. Il tasto rosso fa scorrere
   P+/P− solo tra i preferiti.
3. **Guida TV.** Si importa la guida XMLTV del provider (tramite VPN) a ogni
   sincronizzazione, tenendo solo i canali visibili e una finestra da -6 ore
   a +72 ore. L'abbinamento usa l'id EPG del canale oppure, in mancanza, il
   nome normalizzato.
4. **Il flusso passa sempre dal server.** I client non possono contattare il
   provider (VPN interna, 0012):
   - il client chiede un permesso di visione monouso valido 30 secondi;
   - il server apre il canale tramite il tunnel e lo ritrasmette;
   - **cambio canale**: lo stesso player che chiede un altro canale chiude
     prima il flusso precedente, così un account con una sola connessione non
     viene mai superato;
   - **connessioni esaurite**: un altro dispositivo riceve "connessioni in
     uso" e può scegliere di prendere il controllo.
5. **Interfaccia da telecomando** (servita dal server, `/`):

| Tasto | Azione |
|---|---|
| P+ / P− (PagSu/PagGiù, + / −) | Cambio canale |
| Su / Giù | Mostra nel banner cosa c'è sugli altri canali **senza cambiare**; OK sintonizza, Indietro annulla |
| OK | Lista dei canali con ora e a seguire; ◀ ▶ tra Tutti, Preferiti, Sport, Radio |
| Giallo (F) | Aggiunge o toglie dai preferiti |
| Rosso (P) | Cambio canale solo tra i preferiti; nell'elenco dei canali, per l'amministratore: nasconde il canale con tutte le sue versioni (si rimette da Impostazioni › Categorie) |
| Numeri | Vai al canale |
| Info (I) | Banner |

   Tasti mappati per browser, LG webOS (P+ 33, P− 34, colori 403–406, Indietro
   461) e Android TV.

   Con la vista Cartelle ([0058](0058-vista-a-cartelle.md)) le liste di
   ◀ ▶ sono Preferiti, Tutti e le cartelle del provider, nel suo ordine.
6. **Nessuna risorsa esterna nell'interfaccia** (CSP `default-src 'self'`).
   I loghi dei canali, ospitati dal provider o da terzi, sono scaricati dal
   server **tramite la VPN dell'abbonamento**:
   - validati (PNG, JPEG, GIF o WebP; niente SVG; massimo 1 MB);
   - salvati in cache nel database, anche i fallimenti (ritentati dopo 24 ore);
   - un solo download per immagine anche con molte richieste insieme;
   - serviti con un indirizzo **firmato** (HMAC con una chiave derivata da
     quella dell'installazione), quindi senza token e utilizzabili solo per
     l'immagine di quel canale.

   L'elenco dei canali non contiene più gli indirizzi del provider.

## Prova dell'interfaccia (29 ottobre 2026)

Chromium 140 automatico, catalogo reale di un account di prova, server di prova in rete locale:
banner con numero, logo, qualità, preferito, ora e a seguire; scorrimento
senza cambiare; lista con loghi; preferiti; numeri; tasto giallo; P+/P− e
"solo preferiti" (3 → 22 → 1). Il browser ha contattato **solo** il server
VodMax. Questo Chromium non decodifica H.264/AAC, quindi la riproduzione
vera non è stata provata, e il player non ha aperto flussi inutili.

**Riproduzione verificata** lo stesso giorno dall'utente con Chrome su un PC di
casa (server di prova in rete locale, account di prova tramite VPN):
"va da dio".

## Ancora da fare

- **Riproduzione su TV** (LG webOS, Android TV) e sul telefono.
- Profili: oggi i preferiti appartengono a un unico profilo.
- Guida per le playlist M3U (`url-tvg`).
- Riordino manuale dei preferiti e dei numeri.
- Accesso senza token di amministrazione (abbinamento del dispositivo, 0008).
