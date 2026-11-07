# 0044 — Il token di amministrazione non resta nel browser

Stato: **Approvata** (7 novembre 2026: "per le altre procedi")

## Il problema

Al primo avvio si incolla `admin.token` nel browser (README). Il browser
lo teneva per sempre: entrava da solo nel profilo amministratore anche se
quel profilo ha un PIN, non compariva in Impostazioni → Dispositivi e
quindi non si poteva revocare, e con il token di amministrazione anche un
profilo bambini scelto lì aveva i diritti di amministratore. Su un PC o
una TV di famiglia, chiunque premesse "Cambia profilo" e ricaricasse la
pagina tornava amministratore senza PIN. Lo stesso vale per l'app Android
che fa da server da sola (0021), che apre l'interfaccia con quel token.

## Decisione

- Quando l'interfaccia si accorge di usare il token di amministrazione,
  lo **scambia con un dispositivo suo**, "Accesso con il token di
  amministrazione" (`POST /api/v1/devices/adopt`, solo con quel token), e
  resta nel profilo in cui era (l'amministratore). Il browser compare in
  Dispositivi, si può rinominare, bloccare su un profilo o revocare.
- Da lì in poi vale come ogni dispositivo: cambiando profilo, il profilo
  amministratore chiede il suo PIN.
- `admin.token` resta la **chiave di emergenza** (0019): incollarlo di
  nuovo, o aprire `/#token=…`, rientra nel profilo amministratore senza
  PIN, per esempio per cambiare un PIN dimenticato.
- Con il token di amministrazione e un profilo non amministratore scelto,
  le richieste hanno i diritti di quel profilo.

Per chi ha già VodMax: il browser in cui era stato incollato il token, al
primo caricamento dopo l'aggiornamento, compare tra i dispositivi.

## Non ancora

- Scegliere il nome del dispositivo al momento dello scambio (si cambia
  dopo in Dispositivi).
- Togliere i dispositivi "Accesso con il token di amministrazione" che
  nessuno usa più (si tolgono a mano, come gli altri).
