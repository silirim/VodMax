# 0058 — Vista a cartelle per TV, film e serie

Stato: **Approvata** (14 novembre 2026: "Vorrei che se uno vuole la
suddivisione per cartelle possa flaggare nelle impostazioni o nella
schermata delle serie per mettere "cartelle" oppure "libero""; nata dalle
richieste di alcuni utenti)

## Il problema

VodMax mostra i canali in un unico elenco numerato (Tutti, Sport, Radio,
Preferiti) e i film e le serie in una griglia con i filtri. È la scelta
della [0016](0016-tv-come-un-decoder.md) e della
[0017](0017-film-serie-registrazioni.md). Alcuni utenti vogliono invece
vedere le cose come le raggruppa il loro provider, come fanno altre app:

- i canali per cartella, per esempio "DAZN LaLiga" con i suoi canali,
  poi "Movistar LaLiga";
- i film e le serie per categoria, per esempio "Películas HBO" o
  "Películas 2026";
- tutto nell'ordine del provider, non in ordine alfabetico.

Il proprietario non vuole cambiare la vista di oggi per tutti: ognuno
sceglie.

## Decisione

### La scelta: Libera o Cartelle

- Per ciascuna delle tre sezioni, **TV**, **Film** e **Serie**, c'è la
  scelta **Libera | Cartelle**. Libera è la vista di oggi ed è quella
  predefinita.
- Si sceglie in due posti, che vanno sempre d'accordo:
  - Impostazioni → **Aspetto**: "Vista dei canali TV", "Vista dei film",
    "Vista delle serie";
  - nella sezione stessa: in fondo alla barra dei filtri, "Vista: Libera |
    Cartelle".
- Come il resto dell'Aspetto ([0054](0054-aspetto-accento-oled-effetti.md))
  la scelta vale **per il dispositivo**, per tutti i profili, e sta nel
  browser (`vodmax.look`: `foldersLive`, `foldersMovie`, `foldersSeries`).

### Le cartelle

- Una cartella è una **categoria del provider** con almeno un titolo o un
  canale visibile al profilo, con il numero di titoli o di canali.
- L'ordine è **quello del provider**, abbonamento per abbonamento. Le
  cartelle di due abbonamenti restano separate anche con lo stesso nome.
- Un profilo bambini non vede le cartelle per adulti.
- Un titolo è in ogni categoria in cui c'è una sua versione disponibile e
  visibile, come già per il filtro Categoria dei film. Vale anche per i
  canali: "Rai 1 HD" e "Rai 1 FHD" sono un solo canale, presente in tutte
  e due le cartelle se le versioni stanno in due categorie.
- Dentro una cartella i canali tengono il loro numero e il loro ordine. I
  numeri sono gli stessi in tutte le viste.
- Le categorie escluse in Impostazioni → Categorie non diventano cartelle.
- Ogni elenco di cartelle ha anche **Tutti**: tutti i titoli o tutti i
  canali, senza filtro, compresi quelli che non sono in nessuna categoria
  (le voci M3U senza `group-title`, una categoria che il provider non
  elenca). Viene subito dopo "★ Preferiti" dove c'è (TV nella Home,
  diretta), per primo in Film e Serie. Si apre, si chiude e si ricorda
  come le altre cartelle.

### Film e Serie

- In Cartelle la sezione si apre sull'elenco delle cartelle: una griglia
  di pulsanti a colonne fisse, che si percorre con le frecce e si apre con
  OK. La prima è Tutti: così si trovano anche i film senza categoria, per
  esempio quando la sola categoria è quella della raccolta "Scaricati".
- Una cartella aperta mostra i suoi titoli con il filtro Categoria che c'è
  già. Ordina e Voto continuano a funzionare.
- Si torna all'elenco con Indietro o con il pulsante "Cartella: nome"
  della barra. Il fuoco torna sulla cartella appena lasciata.
- La cartella aperta si ricorda sul dispositivo, una per sezione. Se non
  c'è più, si torna all'elenco.
- Senza nessuna categoria (una playlist senza gruppi) la sezione mostra
  la griglia di Libera.
- In Cartelle "Vedi tutti" di "Nuovi film" e "Nuove serie" nella Home non
  compare: la sezione non si apre sui più recenti.

### TV nella Home

- L'elenco delle cartelle comincia con "★ Preferiti", se ci sono
  preferiti, poi Tutti. Dentro una cartella i canali hanno le righe di
  oggi.
- La ricerca cerca sempre in tutti i canali.
- Se nessun canale ha una cartella, la sezione è quella di Libera, con le
  sue liste (Tutti, TV, Sport, Radio, Preferiti) e la scelta della vista.
- "Vedi tutti" di "In onda sui tuoi canali" apre la cartella Preferiti.

### La diretta

- In Cartelle le liste di ◀ ▶ della lista OK diventano: Preferiti, Tutti,
  poi una lista per ogni cartella con canali, nell'ordine del provider.
- P+ e P− cambiano canale dentro la cartella, come oggi dentro Sport; in
  Tutti scorrono tutti i canali.
- Al posto delle schede c'è "‹ cartella ›". Con Su dal primo canale si
  seleziona la cartella e OK apre un pannello con tutte le cartelle e un
  campo di ricerca, come la scelta delle categorie dei film. Con decine
  di cartelle non serve premere ▶ decine di volte.
- La ricerca della lista cerca in tutti i canali, non solo nella cartella.
  Un canale scelto da un'altra cartella fa passare alla prima cartella
  del provider che lo contiene, o a Tutti se non è in nessuna.
- I numeri sintonizzano qualsiasi canale, come oggi.
- La cartella si ricorda sul dispositivo ed è la stessa della TV nella
  Home: una cartella scelta in un posto si ritrova nell'altro, Preferiti
  compresi.
- All'avvio:
  - si usa la cartella ricordata, se contiene il canale da sintonizzare
    (Tutti li contiene tutti);
  - altrimenti la prima cartella del provider che lo contiene, nel suo
    ordine;
  - altrimenti Tutti;
  - se la cartella ricordata non c'è più, o non ce n'è una, Tutti.
- Rosso (solo preferiti) spento, anche dopo essere entrati dai Preferiti
  della Home: si resta sul canale e si passa alla cartella ricordata se lo
  contiene, altrimenti alla prima del provider che lo contiene,
  altrimenti a Tutti.
- La correzione della lista vuota (canali tutti in Sport, commit
  `9803f61`) vale in tutte e due le viste.
- Se il server non dà cartelle, la pagina resta in Libera.
- In Libera la diretta è quella di oggi.

### Cosa non cambia

La guida, il multiview e le righe della Home non cambiano.

## Come funziona

### Server

- Migrazione 22: `category.position`. Ogni sincronizzazione scrive la
  posizione della categoria tra quelle dello stesso tipo, nell'ordine in
  cui la dà la sorgente:
  - per Xtream Codes, l'ordine dell'elenco delle categorie;
  - per una playlist M3U, la prima comparsa di ogni `group-title`;
  - per Stalker, l'ordine dei generi;
  - una raccolta locale ha una sola categoria.
- Le categorie salvate prima della migrazione stanno tutte a 0, quindi in
  ordine di nome, fino alla sincronizzazione successiva.
- `GET /api/v1/browse/{kind}/categories?order=provider`: abbonamento, poi
  posizione, poi nome. Senza il parametro l'ordine resta per nome, per il
  filtro Categoria di Libera.
- `GET /api/v1/tv/channels`: ogni canale ha `folders`, le chiavi
  `abbonamento:categoria` delle categorie in cui ha una versione
  disponibile e visibile, nell'ordine del provider.
  - Basta una query per tutto l'elenco, non una per canale.
  - Le chiavi si confrontano intere: l'id di una categoria M3U è il nome
    del gruppo e può contenere `:`.
  - La guida non le riceve.

### Interfaccia

- La diretta scarica l'elenco completo una volta e lo filtra per ogni
  cartella. ◀ ▶ non fanno richieste. L'elenco si riscarica dopo 30
  secondi, per ora e a seguire, o quando cambia un preferito.
- Le chiavi del dispositivo:
  - `folder.live`, `folder.movie`, `folder.series`: la cartella aperta,
    oppure nessuna per l'elenco;
  - Preferiti è `0:favorites`, Tutti è `0:all` (nessun abbonamento ha
    l'id 0);
  - `list` e `favoritesOnly` di Libera restano come sono.

### File toccati

- Server:
  - `internal/store/store.go`: la migrazione;
  - `internal/store/catalog.go`: la posizione e l'ordine delle categorie;
  - `internal/store/tv.go`: `AttachFolders`;
  - `internal/service/tv.go`, `internal/service/guide.go`,
    `internal/service/service.go`, `internal/service/assistant.go`;
  - `internal/api/api.go`.
- Interfaccia:
  - `internal/web/static/look.js`, `settings.js`, `vod.js`, `tv.js`,
    `vod.css`, `tv.css`;
  - le sei lingue in `internal/web/static/i18n/`.
- Documenti: `docs/sviluppo.md`, `README.md`, `docs/decisioni/README.md`, `0016-tv-come-un-decoder.md`, `0017-film-serie-registrazioni.md`, `0054-aspetto-accento-oled-effetti.md`, questa decisione.

## Prove (14 novembre 2026)

- Test Go:
  - `TestCategoriesInProviderOrder`: ordine del provider, ordine per nome
    senza il parametro, posizioni contate per tipo, nuovo ordine dopo una
    sincronizzazione con le categorie riordinate, categorie senza
    posizione in ordine di nome.
  - `TestChannelFolders`: un canale con due versioni in due abbonamenti
    con lo stesso id di categoria ha due cartelle distinte; un canale
    senza chiave di gruppo ha solo la cartella della sua riga; un id con
    `:` resta intero; due abbonamenti in ordine, ognuno con il suo.
  - `TestTVChannelFolders` (API): un canale con tre versioni in tre
    categorie; una categoria per adulti vista dall'amministratore e non
    dal profilo bambini, nei due endpoint; una categoria esclusa che non è
    più una cartella.
- Chromium automatico, con un provider finto su 127.0.0.1 (nove categorie
  di canali in ordine non alfabetico, una nascosta, una per adulti; un
  canale con due versioni in due categorie). Viste a 1280×720, 1920×1080
  e sul telefono a 390×844, con i tasti del telecomando e con il tocco:
  - Libera identica a oggi in TV, Film, Serie e diretta;
  - la scelta da Impostazioni e dalla barra della sezione, che vanno
    d'accordo;
  - cartelle nell'ordine del provider, con il numero, apertura con OK,
    ritorno con Indietro, con il pulsante e dall'app Android;
  - una cartella ricordata e una sparita;
  - profilo bambini senza cartelle per adulti;
  - diretta: ◀ ▶, P+/P− dentro la cartella, il pannello delle cartelle,
    i numeri, il ritorno dopo il ricaricamento, una cartella sparita;
  - il caso della `9803f61`, con solo Sport visibile;
  - una nuova sincronizzazione con le categorie del provider in ordine
    inverso;
  - testi in inglese e spagnolo, niente scorrimento orizzontale sul
    telefono, nessun errore nella console.
- Il primo giro ha trovato due difetti: il suggerimento in fondo alla
  lista era tagliato e la ricerca della lista guardava solo la cartella.
  Dopo le correzioni il giro completo è passato di nuovo. Altre prove
  confermano:
  - il suggerimento va su due righe in italiano, inglese e spagnolo;
  - la ricerca trova i canali di tutte le cartelle;
  - Preferiti passa dalla Home alla diretta;
  - la cartella ricordata ha la precedenza;
  - cinque ▶ veloci non fanno nessuna richiesta;
  - in Libera Giù su una lista vuota non apre più il pannello delle
    cartelle.
- Il secondo giro ha trovato quattro difetti:
  - i film e i canali senza categoria non si raggiungevano: con i film del
    provider senza categoria e la sola cartella "Scaricati · Film", Film
    mostrava solo quella;
  - Rosso spento tornava alla prima cartella, anche se non conteneva il
    canale;
  - la TV nella Home senza cartelle mostrava tutti i canali senza le liste
    di Libera;
  - il suggerimento della lista di Libera era tagliato in italiano,
    tedesco, francese e polacco.
- Dopo le correzioni (Tutti, Rosso, Libera senza cartelle, suggerimento su
  due righe) un terzo giro conferma:
  - Film senza categorie del provider: Tutti, poi "Scaricati · Film"; Tutti
    mostra i 15 film come Libera, si ricorda, il filtro Voto non lo chiude,
    Indietro torna con il fuoco su Tutti;
  - TV nella Home con "Orphan TV" senza categoria: "★ Preferiti 2", "Tutti
    16", poi le cartelle; Tutti contiene Orphan TV;
  - diretta: Tutti tra Preferiti e le cartelle con ◀ ▶ e nel pannello, P+
    in Tutti scorre tutti i numeri;
  - avvio: Orphan TV con la cartella Sport ricordata va in Tutti; un canale
    fuori dalla cartella ricordata va nella prima del provider che lo
    contiene, non in Tutti; Tutti ricordata resta; una cartella sparita
    porta a Tutti; Orphan TV scelto con la ricerca da Notizie porta a Tutti;
  - Rosso: da Notizie, P+ fino a DAZN LaLiga 1 tra i preferiti, Rosso
    spento porta a Sport; entrando dai Preferiti della Home o dopo un
    ricaricamento porta a Tutti;
  - TV nella Home senza categorie: le liste di Libera e "Vista: Libera |
    Cartelle", Preferiti compreso;
  - suggerimento di Libera e di Cartelle su due righe, intero, in italiano,
    tedesco, francese e polacco a 1280×720; il pannello non cambia misura
    con ◀ ▶.

## Non ancora

- **Numero dei titoli di Tutti in Film e Serie**: non compare, l'API non
  dà il totale del catalogo. Nella TV c'è.
- **Nomi uguali in due abbonamenti**: le cartelle restano separate, ma non
  dicono di quale abbonamento sono.
- **Cartella ricordata per profilo**: è per dispositivo, come i filtri dei
  film. Un profilo bambini sulla TV di un adulto vede per un attimo il
  nome della cartella aperta dall'adulto, poi torna all'elenco se non può
  vederla.
- **Ordine dei Preferiti**: è quello dei preferiti del server, non quello
  dei numeri.
- **Prova sulle TV vere** (LG webOS, Android TV) e con il telecomando
  vero: la navigazione è stata provata solo con i tasti del browser.
- La traduzione inglese "Free" di "Libera" va rivista.
