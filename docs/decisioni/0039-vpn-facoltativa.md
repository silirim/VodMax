# 0039 — VPN facoltativa, "solo tramite VPN" come scelta

Stato: **Approvata** (3 novembre 2026: "Facciamo in modo che non sia
obbligatoria la vpn ma selezionabile la possibilità di far sì che possa
essere contattato solo tramite vpn")

## Il problema

Dalla 0012 ogni abbonamento deve avere una VPN, e senza VPN il provider
non viene mai contattato. Chi non usa una VPN doveva comunque creare una
"VPN esterna" finta per poter aggiungere un abbonamento.

## Decisione

In **Impostazioni → VPN** c'è un interruttore: **Contatta i provider solo
tramite VPN**.

- **Acceso** (il valore predefinito, anche per chi aggiorna): come prima.
  Ogni abbonamento ha una VPN; uno senza VPN resta in attesa e il provider
  non viene contattato. Se la VPN cade, il flusso si ferma: mai ripiego
  sulla rete normale.
- **Spento**: un abbonamento può avere "Nessuna VPN (rete normale)". VodMax
  usa la rete normale del server per quel provider, come per una VPN
  esterna. Gli abbonamenti che hanno una VPN continuano a usarla, sempre
  senza ripiego.
- Riaccendendo l'interruttore, gli abbonamenti senza VPN tornano in attesa
  finché non se ne sceglie una. Nessuna richiesta parte nel frattempo.

Il resto della 0012 resta valido. Ogni richiesta al provider passa ancora
da `internal/provider` e dalla connessione dell'abbonamento: un tunnel
WireGuard, la rete del sistema per una VPN esterna, o la rete normale
quando l'abbonamento non ha una VPN e l'interruttore è spento.

## Non ancora

- Una scelta per abbonamento invece che per tutto il server.
- Un avviso nella home quando l'interruttore è spento.
