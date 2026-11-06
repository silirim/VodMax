# 0046 — Disco quasi pieno: prima le registrazioni, poi la pausa della diretta

Stato: **Proposta** (6 novembre 2026, da una revisione dello streaming)

## Il problema

La pausa della diretta tiene su disco fino a 2 ore (al massimo 6) del
canale che si guarda, una per dispositivo. Lo spazio libero si controllava
solo all'avvio (servono 2 GB). Il buffer è pensato a segmenti di 2 s, ma
copiando il video ffmpeg taglia solo sui fotogrammi chiave: con un canale
che ne manda uno ogni 5 s i segmenti sono più lunghi e il buffer occupa più
del previsto. Una serata di sport in FHD su TV e telefono può riempire il
disco, e la registrazione delle 21:00 non parte ("spazio su disco
esaurito", sotto 1 GB libero).

## Decisione

- Ogni 15 s un buffer in corso controlla lo spazio libero. Sotto 1,5 GB si
  ferma: il lettore vede la fine del buffer e riapre il canale, che sotto
  i 2 GB parte senza pausa (diretta semplice). Nel log resta un avviso.
- Le registrazioni vengono prima: si fermano i buffer, mai una
  registrazione.

## Non ancora

- Dimensionare il buffer dalla durata vera dei segmenti o dai byte.
- Dire sullo schermo che la pausa non è disponibile per lo spazio.
