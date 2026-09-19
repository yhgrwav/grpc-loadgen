# Welche Lasttest-Probleme behebt es?

[← Zur Übersicht](README.md)

Womit Lastwerkzeuge lügen oder im Weg stehen — und wie es bei uns darum steht.

Die Angaben sind ehrlich: `fertig` funktioniert heute; `in Arbeit` entsteht gerade; `geplant`
heißt, die Entscheidung steht, der Code kommt noch.

| Problem | Status |
|---|---|
| Coordinated Omission: ein Ausfall verschwindet aus dem Bericht | in Arbeit |
| Eine Methode pro Lauf: eine Lastmischung ist nicht ausdrückbar | in Arbeit |
| Ein Closed Model drosselt genau dann, wenn der Dienst schwächelt | in Arbeit |
| Der Generator stirbt vor dem Ziel an hängenden Anfragen | geplant |
| Perzentile werden gemittelt und verlieren ihre Bedeutung | geplant |
| Die Metrik-Erfassung bremst den Generator selbst | geplant |
| `.proto`-Dateien und Codegen nötig | geplant |
| Ein Tippfehler in der Konfiguration ergibt einen leeren grünen Lauf | fertig |
| Rate Limiting des Ziels zählt als Serverfehler | geplant |
| Der Kaltstart verzerrt die Perzentile | in Arbeit |
| Aufrufe lassen sich nicht über Daten verketten | Stufe 1 |
| Während des Laufs ist unklar, was passiert | geplant |

## Coordinated Omission

Der teuerste Fehler im Lasttest. Ziel sind 1000 RPS, also eine Anfrage pro Millisekunde. Der
Dienst friert eine Sekunde ein. In dieser Sekunde wären 1000 Anfragen fällig gewesen.

Misst man ab dem tatsächlichen Absenden, gehen sie alle nach dem Auftauen raus, brauchen je 5 ms,
und der Bericht meldet `p99 = 5ms`. Der ganze Ausfall ist verschwunden: Das Werkzeug meldet
Bestzustand genau dann, wenn nichts in Ordnung war.

Wir stempeln jede Anfrage bei der Planung mit ihrer Sollzeit und messen ab dieser. Eine für `t=0`
geplante Anfrage, die um `t=1000ms` rausging und um `t=1005ms` beantwortet wurde, dauerte
`1005ms`. Das steckt ab der ersten Zeile im Kern — einer fertigen Engine lässt sich das nicht
nachrüsten.

## Eine Methode pro Lauf

Produktion besteht nicht aus einem Endpunkt. Ein Dienst, der einzeln 800 RPS Lesezugriffe und
50 RPS Schreibzugriffe verträgt, kann an deren Summe scheitern: gemeinsamer Verbindungspool,
Sperren, Cache-Konkurrenz. Getrennte Läufe können das nicht zeigen. Wir nehmen eine Liste von
Methoden mit eigenen Raten — ein Lauf, ein Bericht.

## Das Closed Model

„N virtuelle Nutzer, die je auf eine Antwort warten" wirkt natürlich und hat einen eingebauten
Fehler: Wird der Dienst langsamer, warten die Nutzer länger und die tatsächliche Last sinkt von
selbst. Das Werkzeug hört genau dann auf zu drücken, wenn das Drücken interessant wird.

Wir arbeiten mit einem Open Model: Die Zielrate ist ein Fahrplan, Anfragen gehen nach der Uhr
raus, unabhängig davon, ob frühere beantwortet sind.

## Den Generator umbringen

Die Kehrseite des Open Models: Hängt das Ziel, häufen sich offene Anfragen, und der Generator
stirbt vor dem getesteten Dienst. Das Gegenmittel ist eine explizite Obergrenze gleichzeitiger
Anfragen. Entscheidend: Das Erreichen dieser Grenze ist ein Lauffehler und keine stille
Lastreduktion — ein „1000 RPS"-Lauf, der klammheimlich zu „so viel wie ging" wird, ist schlimmer
als ein fehlgeschlagener.

## Perzentile und Metriken

Der Mittelwert zweier p99-Werte ist kein p99 — sie lassen sich nicht mitteln. Zusammenführen kann
man nur Verteilungen, also wandert ein Histogramm über die Grenze, und die Perzentile werden
daraus berechnet. Dieselbe Eigenschaft macht später Läufe über mehrere Maschinen möglich.

Dazu kommt der Preis der Erfassung. Ein Mutex um ein gemeinsames Slice von Latenzen macht den
Generator bei hohen Raten zu seinem eigenen Engpass: Er misst dann Sperrkonkurrenz statt den
Dienst. Daher HDR-Histogramme und Sharded Counters.

## Vorbereitung und Tippfehler

Kein `.proto`, kein Codegen: Die Methodenbeschreibungen kommen per Reflection vom Dienst.

Die Konfiguration wird streng gelesen. `rsp: 800` statt `rps: 800` ist ein Fehler mit
Ortsangabe, kein stiller Lauf mit Nulllast und grünem Bericht. Auch `rps: 0` ist ein Fehler: ein
leerer erfolgreicher Lauf ist die schlimmste Lüge, weil er wie Erfolg aussieht.

## Fehler und Aufwärmen

Ein `RESOURCE_EXHAUSTED` vom Rate Limiter des Ziels ist nicht dasselbe wie ein Timeout oder ein
Absturz. Alles zu „Fehler: 12 %" zusammenzuwerfen wirft die Bedeutung weg. Der Bericht hält die
Kategorien auseinander.

Die ersten Sekunden eines Laufs sind immer langsam: leere Caches, Verbindungsaufbau, JIT-Warmlauf.
Sie verzerren die Perzentile des gesamten Laufs, deshalb hält `warmup` sie aus dem Bericht heraus.

## Sichtbarkeit

Ein Lauf darf keine zehnminütige Blackbox sein. Während der Arbeit sind aktuelle Rate, Anzahl
offener Anfragen, Fehleranteil und aktueller p99 sichtbar — genug, um einen sinnlosen Lauf in der
zweiten Minute abzubrechen statt in der zehnten.
