# 0051 — Aspetto: tema "vetro" e caratteri propri

Stato: **Approvata** (7 novembre 2026: "La parte grafica mi interessa
particolarmente. Una sorta di tema glass stile Apple")

## Il problema

VodMax funziona, ma non è bello quanto le app con cui lo si confronta
(Plex, Streamyfin, Moonfin, Stremio, i temi di Jellyfin). Guardando il
codice e le schermate a 1920×1080, 1600×900 e 390×844:

- `ui.css` dichiara il carattere "Inter" ma non lo carica mai: su ogni
  dispositivo esce il carattere di sistema (DejaVu sulla TV).
- Pannelli, pulsanti e schede hanno nove raggi diversi (6–20 px più le
  pillole) e tre famiglie di pulsanti copiate in quattro file
  (`.button`, `button.small`, `.controls button`); `.panel`,
  `.setup-panel`, `.status` sono duplicati in `tv.css` e `vod.css`.
- Le intestazioni di sezione in turchese con la riga sotto, le schede
  senza badge e il fuoco "bianco pieno" ovunque danno un'aria datata.
- `#0b0c10` è scritto fisso una trentina di volte, due verdi e due rossi
  sono duplicati fuori dalle variabili: cambiare tema è impossibile.

## Decisione

- Un **tema "vetro"**: superfici traslucide sfocate sopra le immagini
  (`backdrop-filter`), bordo chiaro di 1 px, riflesso in alto, ombre
  morbide, raggi continui. Barra di navigazione, pannelli, finestre,
  barra del lettore, barra di zapping, elenco canali e schede delle
  impostazioni usano le stesse tre superfici (`--glass`, `--glass-strong`,
  `--glass-line`). Dove il motore non sa sfocare (`@supports not
  (backdrop-filter)`, WebView vecchie) la superficie è semiopaca, mai
  trasparente: il testo resta leggibile.
- **Inter** (variabile, licenza OFL) nel repository, in
  `internal/web/static/fonts/`, con la licenza accanto. Niente CDN: la TV
  deve essere bella anche senza internet. Titoli con spaziatura stretta
  (`letter-spacing: -0.02em`), testi con `font-feature-settings` per le
  cifre tabellari negli orari.
- **Un solo sistema di componenti** in `ui.css`: due raggi (`--r-s` per
  chip e badge, `--r` per schede e pannelli, più la pillola), due
  pulsanti (`.button` vetro, `.button.primary` pieno), un pannello, una
  finestra. Le regole copiate nei CSS di pagina spariscono.
- **Fuoco** da divano: la scheda a fuoco cresce (`scale(1.06)`) con anello
  bianco e alone; i pulsanti a fuoco si riempiono di bianco come oggi, il
  passaggio del mouse sul PC è più discreto (vetro più chiaro, non
  bianco).
- Intestazioni di sezione bianche in grassetto, con "Vedi tutti" a
  destra; badge sulle schede (anno, voto, HD, "Nuovo") e barra del
  progresso sottile.
- Android (`values/colors.xml`, `PlayerActivity`) e webOS
  (`launcher.css`) riprendono gli stessi colori.

## Non ancora

- Un tema chiaro: con il vetro sopra le immagini serve lavoro a parte
  ([0054](0054-aspetto-accento-oled-effetti.md) lascia il posto).
- Un carattere di titolo diverso dal testo: Inter a peso 700–800 basta.
