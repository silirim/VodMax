# 0055 — Voti esterni (Rotten Tomatoes, IMDb, Metacritic)

Stato: **Respinta** (7 novembre 2026: "Io non voglio fare account o
accessi a niente")

## Il problema

Accanto al voto di TMDB si vorrebbero quelli di Rotten Tomatoes e IMDb,
come fanno Plex e i plugin di Jellyfin.

## Perché no

Rotten Tomatoes non ha un'API pubblica: quella ufficiale è per partner con
licenza, e leggere il sito viola le sue condizioni. Le vie praticabili
(OMDb, MDBList) richiedono un account e una chiave, anche gratuiti: il
proprietario non ne vuole. Il pomodoro e il popcorn sono marchi di
Fandango e non si possono usare nell'interfaccia.

## Cosa resta

Il voto di TMDB, mostrato meglio: badge sulle schede e pillola nel
dettaglio ([0052](0052-immagini-sfondi-loghi-badge.md)). Se un giorno si
vorrà, OMDb si aggiunge con una chiave accanto a quella di TMDB e
`external_ids` già richiesto.
