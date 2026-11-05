# 0041 — La VPN si sceglie alla prima playlist

Stato: **Approvata** (5 novembre 2026: "devi assolutamente farlo. ora,
subito", dopo la proposta di chiedere come raggiungere il provider alla
prima playlist invece dell'interruttore nella scheda VPN)

## Il problema

Con la 0039 "solo tramite VPN" è acceso in modo predefinito. Chi installa
VodMax per la prima volta e non ha una VPN apre Impostazioni → Abbonamenti
e non trova il modulo della playlist, solo "Prima aggiungi una connessione
VPN (scheda VPN)". Per andare avanti senza VPN deve sapere che nella
scheda VPN c'è un interruttore da spegnere. Chi è esperto ci arriva; chi
prova VodMax per la prima volta, per esempio sull'app Android da sola,
pensa che non funzioni.

## Decisione

Quando "solo tramite VPN" è acceso e non c'è ancora nessuna VPN, la
pagina Abbonamenti chiede, al posto del modulo, **come VodMax raggiunge il
provider**, con una riga di spiegazione per ogni risposta:

- **Con una VPN WireGuard** (consigliato se si ha un servizio VPN): compare
  il modulo della VPN, già su WireGuard; dopo "Aggiungi" compare il
  modulo della playlist.
- **Con la VPN che ho già** (router o dispositivo): VodMax aggiunge una
  VPN esterna, "VPN del router o del dispositivo", con l'avvertenza che
  è quella rete a dover passare dalla VPN.
- **Senza VPN**: VodMax spegne "solo tramite VPN"; la spiegazione dice che
  il provider vede l'indirizzo internet di casa.

Ogni risposta fa solo quello che si può già fare nella scheda VPN, e lì si
cambia in qualsiasi momento. Restano valide la 0012 e la 0039:
"solo tramite VPN" resta acceso in modo predefinito, nessun ripiego dalla
VPN alla rete normale, nessuna richiesta al provider prima della scelta.
Chi ha già una VPN o un abbonamento non vede niente di nuovo. Vale anche
per l'app Android da sola, che usa la stessa pagina.

## Non ancora

- Un avviso nella home quando "solo tramite VPN" è spento (già nella 0039).
- La stessa domanda per un secondo abbonamento: la scelta è una sola, poi
  vale il modulo di sempre.
