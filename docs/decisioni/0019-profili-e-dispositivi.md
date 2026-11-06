# 0019 — Profili, dispositivi e abbinamento

Stato: **Approvata** (29 ottobre 2026, richiesta dell'utente: profili per
la famiglia, profilo bambini, abbinamento con un codice breve)

## Decisione

- **Dispositivo** (TV, telefono, PC): si abbina una volta e riceve un
  token suo, salvato sul server solo come hash e revocabile. Il token di
  amministrazione (`admin.token`) resta come chiave di emergenza.
- **Abbinamento**: il dispositivo nuovo mostra un codice (`K7Q-29M`,
  6 caratteri senza 0/O e 1/I, valido 10 minuti); un amministratore lo
  inserisce in Impostazioni → Dispositivi; il dispositivo riceve il token
  una sola volta. Massimo 5 codici in attesa per indirizzo, 50 in tutto.
- **Profilo** (chi guarda): preferiti e "Continua a guardare" separati.
  Tipi: amministratore (impostazioni), normale, bambini. PIN facoltativo
  (4–8 cifre, PBKDF2), 5 errori bloccano quel profilo su quel
  dispositivo per 5 minuti. La scelta produce un token firmato valido
  solo per quel dispositivo; cambiare il PIN la annulla ovunque.
- **PIN dell'amministratore** (30 ottobre 2026, segnalazione dell'utente:
  chiunque, anche da un profilo normale, poteva entrare nel profilo
  amministratore senza PIN): quando esistono altri profili, un profilo
  amministratore **deve avere un PIN**. Il server rifiuta di creare altri
  profili, di togliere il PIN all'amministratore o di creare un
  amministratore senza PIN. Sulle installazioni precedenti, chi entra in un
  amministratore senza PIN non ha i diritti di amministratore finché non
  imposta il PIN (una finestra obbligatoria lo chiede); il token di
  amministrazione resta la chiave di emergenza.
- **Il mio profilo**: ognuno sceglie il proprio avatar (una trentina di
  icone Lucide, o l'iniziale) e il colore, e cambia il proprio PIN dando
  quello attuale. I profili bambini scelgono l'avatar, non il PIN.
- Un dispositivo può essere **bloccato su un profilo** (il tablet dei
  bambini): non sceglie, non vede gli altri.
- **Bambini**: niente contenuti delle categorie per adulti, né in elenco
  né in riproduzione (il server risponde "non trovato" anche a richieste
  dirette), niente registrazioni da programmare, niente impostazioni.
  Nemmeno i titoli di una categoria che il provider non elenca (non si
  può sapere se è per adulti) né i titoli appena importati, finché la
  selezione non li ha controllati (6 novembre 2026).
  Se esiste un profilo bambini, ogni profilo adulto senza PIN è
  segnalato come avviso.

## Accessi per livello

| Livello | Cosa |
|---|---|
| dispositivo | `me`, elenco e scelta del profilo, elenco degli avatar |
| profilo | il proprio avatar, colore e PIN (`PUT /api/v1/profile`) |
| profilo | catalogo, TV, film, serie, avanzamenti, registrazioni (vedere) |
| non bambini | programmare e cancellare registrazioni |
| amministratore | abbonamenti, VPN, selezione, backup, profili, dispositivi |
