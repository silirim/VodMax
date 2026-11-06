# 0042 — Il tuo paese, scelto da te

Stato: **Approvata** (5 novembre 2026: "si", alla proposta di riconoscere
il paese in tutte le grafie comuni e di far scegliere a ognuno il proprio
paese per abbonamento, senza cambiare nulla a chi ha già VodMax)

## Il problema

La selezione del catalogo era pensata per le liste del mercato italiano:
le categorie senza un paese nel nome erano "di casa" e restavano sempre;
erano estere solo quelle dei canali scritte come "Live Francia" (o
"International"). Le liste internazionali mettono il paese davanti a ogni
categoria, anche di film e serie ("UK | Sport", "|DE| Filme", "FR: Cinéma",
"EN - Action"): chi vive nel Regno Unito o in Germania non poteva tenere il
proprio paese ed escludere gli altri.

## Decisione

- **Il paese si riconosce in tutte le grafie comuni**, per canali, film e
  serie: "Live X", "X |", "|X|", "X:", "X -", "[X]", "(X)" in fondo, e un
  nome di paese all'inizio ("Italia 4K - FHD", "Germany Movies"). Codici e
  nomi nelle lingue più diffuse portano a un solo codice: ISO per i paesi
  (GB, non UK), più alcuni gruppi che i provider usano per lingue e regioni
  (in inglese, arabo, ex Jugoslavia, America Latina, internazionale…). La
  pagina mostra i nomi nella lingua dell'interfaccia.
- **Il tuo paese, per abbonamento**, in Impostazioni → Selezione. Restano le
  categorie del tuo paese e quelle che non nominano un paese; gli altri
  paesi si escludono, tranne quelli che spunti. VodMax propone il paese
  dalla lingua del dispositivo ("en-GB" → Regno Unito, "de" → Germania) e,
  per un paese di lingua inglese, spunta anche "in inglese"; l'anteprima
  mostra l'effetto prima di salvare.
- **Chi ha già VodMax non vede cambiamenti** finché non sceglie il paese:
  senza paese scelto valgono le regole di prima (estero solo "Live X" e
  "International"), e i paesi salvati con il vecchio nome ("UK") valgono
  come il codice nuovo ("GB"). Verificato sul catalogo di casa: 579
  categorie, nessuna differenza, anche scegliendo Italia.
- Una lista internazionale, prima della scelta, resta tutta visibile: niente
  è scritto come "Live X".

Il paese si ricava dal nome della categoria quando serve, non è un dato
nuovo nel database: le regole aggiungono solo il campo "home".

## Non ancora

- Il paese dei singoli titoli ("ITA", "SUB ITA" nel nome) e la lingua
  dell'audio: conta solo la categoria.
- Correggere il paese di una categoria dalla pagina Categorie (il server lo
  accetta già).

## Correzione (6 novembre 2026)

Dopo "Live" o "TV" non c'è sempre un paese: "Live Calcio", "Live Serie A",
"LIVE EVENTS", "LIVE | PPV" e "TV Locali" sono gruppi del tuo paese, e
venivano esclusi come paesi chiamati "Calcio" o "EVENTS", anche con le
regole di prima. Ora un nome che VodMax non riconosce come paese e che
parla di sport, radio, eventi, PPV o canali locali o regionali resta del
tuo paese. Gli altri nomi sconosciuti restano un paese, come prima, per non
mostrare per errore un gruppo estero scritto in un modo che la tabella non
conosce.
