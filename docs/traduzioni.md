# Traduzioni (Weblate)

VodMax è scritto in italiano e tradotto in inglese, spagnolo, francese, tedesco e polacco. Le altre lingue possono
arrivare dalla comunità con [Weblate](https://weblate.org), che lavora
direttamente sul repository: chi traduce usa il sito, Weblate apre le pull
request.

## I file

| Componente | Maschera dei file | File base | Formato Weblate |
|------------|-------------------|-----------|-----------------|
| Interfaccia | `internal/web/static/i18n/*.json` | `internal/web/static/i18n/it.json` | JSON file (monolingue) |
| Messaggi del server | `internal/service/i18n/*.json` | `internal/service/i18n/it.json` | JSON file (monolingue) |
| App Android | `clients/android/app/src/main/res/values-*/strings.xml` | `clients/android/app/src/main/res/values/strings.xml` (inglese) | Android String Resource |

- Le chiavi sono i testi italiani; nei file base il valore è uguale alla
  chiave. Un valore vuoto vale "non tradotto": si vede l'italiano.
- L'app Android fa eccezione: il file base `values/strings.xml` è in
  inglese, perché Android lo mostra in ogni lingua che non ha un suo file;
  l'italiano sta in `values-it`. Un testo nuovo va in `values/` (inglese)
  e in `values-it/` (italiano).
- Segnaposti: `{nome}` nell'interfaccia, `%s %d %v %w` nei messaggi del
  server, `%1$d` in Android. In Weblate conviene attivare i controlli
  "C format" sul componente dei messaggi e "Placeholders" (`{\w+}`)
  sull'interfaccia.
- Una lingua nuova (per esempio `de.json`) compare da sola nel selettore
  della lingua alla build successiva. Il nome mostrato è nella lingua
  stessa (tabella in `internal/web/static/i18n.js`).
- I test (`go test ./...`) controllano che ogni testo del codice sia nei
  file base e tradotto in inglese: per aggiungere un testo basta scriverlo
  in italiano nel codice e aggiungerlo a `it.json` ed `en.json`.

## Attivare Weblate (una volta, da fare con l'account del proprietario)

1. Accedi a [hosted.weblate.org](https://hosted.weblate.org) con l'account
   GitHub e chiedi l'hosting gratuito per progetti liberi (VodMax è
   AGPL-3.0).
2. Crea il progetto "VodMax" e i tre componenti della tabella, con il
   repository `https://github.com/silirim/VodMax`, ramo `main`, lingua
   sorgente italiano (inglese per l'app Android).
3. In GitHub installa l'app Weblate (o aggiungi il webhook che Weblate
   indica), così le modifiche arrivano come pull request.
4. Aggiungi il badge di Weblate al README.
