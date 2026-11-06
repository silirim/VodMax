# 0045 — Accesso con account solo dagli indirizzi di VodMax elencati

Stato: **Proposta** (6 novembre 2026, da una revisione di sicurezza)

## Il problema

L'indirizzo di ritorno che VodMax manda al provider di accesso (0029) si
costruiva dall'indirizzo della richiesta (`Host` o `X-Forwarded-Host`), che
qualsiasi client può scegliere. Con un provider che accetta indirizzi di
ritorno larghi (succede con Keycloak e Authentik configurati con `*`),
qualcuno in casa poteva far partire un accesso con il proprio indirizzo,
mandare il link all'amministratore e, quando questi entrava, ricevere il
suo codice e un dispositivo nel profilo amministratore.

## Decisione

- In Impostazioni → Dispositivi → *Accesso con account di casa* c'è
  l'elenco degli **indirizzi di VodMax** da cui si entra con l'account, uno
  per riga (per esempio quello di casa e quello del tunnel, 0040). Il campo
  parte con l'indirizzo da cui si sta guardando la pagina; per accendere
  l'accesso serve almeno un indirizzo.
- Da un indirizzo fuori dall'elenco il pulsante "Entra con…" non compare e
  `/auth/sso/start` risponde con un errore, senza andare dal provider.
- La pagina mostra un indirizzo di ritorno per ogni indirizzo dell'elenco,
  da registrare nel provider.

Per chi ha già l'accesso con account acceso: finché non salva di nuovo la
configurazione, si entra da qualsiasi indirizzo come prima (nessuno resta
fuori dopo l'aggiornamento). Al primo salvataggio vale l'elenco: chi entra
anche da fuori casa deve aggiungere l'indirizzo del tunnel.

## Non ancora

- Riconoscere da soli l'indirizzo del tunnel.
