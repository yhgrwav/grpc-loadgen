# Welche Lasttest-Probleme behebt es?

[← Zur Übersicht](README.md)

> Diese Übersetzung kann hinter dem [russischen Original](../ru/pitfalls.md) zurückliegen.

Eine Liste der Stellen, an denen Lastwerkzeuge lügen oder im Weg stehen, und wo wir jeweils stehen.

Die Status sind ehrlich: `fertig` — funktioniert; `geplant` — die Entscheidung steht, der Code
kommt noch.

| Problem | Status |
|---|---|
| Coordinated Omission: Stillstände des Dienstes verschwinden aus dem Bericht | fertig |
| Eine Methode pro Lauf: gemischte Last lässt sich nicht ausdrücken | fertig |
| Closed Model senkt die Last genau dann, wenn der Dienst degradiert | fertig |
| Ein Timeout wird als Zahl erfasst und verdeckt das Ende der Verteilung | fertig |
| Eine Verbindungsablehnung in Bruchteilen einer Millisekunde „verbessert" die Latenz | fertig |
| Perzentile werden gemittelt und verlieren ihren Sinn | fertig |
| Metriken bremsen den Generator selbst | fertig |
| `.proto`-Dateien und Codegen nötig | fertig |
| Ein Tippfehler in der Konfiguration ergibt einen leeren grünen Lauf | fertig |
| Kaltstart verzerrt Perzentile | fertig |
| Der Generator stirbt vor dem Ziel an hängenden Anfragen | fertig |
| Der Generator stößt an seine Grenze, und der Bericht beschuldigt den Dienst | geplant |
| Rate-Limits des Ziels zählen als Dienstfehler | geplant |
| Aufrufe lassen sich nicht über Daten verketten | geplant |
| Unklar, was während des Laufs passiert | fertig |

## Coordinated Omission

Der teuerste Fehler im Lasttest. Ziel sind 1000 RPS, eine Anfrage pro Millisekunde. Der Dienst
hängt eine Sekunde. In dieser Sekunde hätten 1000 Anfragen rausgehen müssen.

Zählt man die Latenz ab dem tatsächlichen Senden, gehen alle nach der Erholung raus, brauchen je
5 ms, und der Bericht zeigt `p99 = 5ms`. Der einsekündige Stillstand ist verschwunden — das
Werkzeug meldete, alles sei bestens, genau als es das nicht war.

Wir geben jeder Anfrage beim Planen ihren geplanten Zeitpunkt und zählen die Latenz ab diesem. Eine
für `t=0` geplante Anfrage ging bei `t=1000ms` raus, die Antwort kam bei `t=1005ms` — Latenz
`1005ms`. Das ist von der ersten Zeile an im Kern angelegt: Auf eine fertige Engine lässt es sich
nicht aufschrauben.

## Eine Methode pro Lauf

Produktion ist nicht eine Methode. Ein Dienst, der getrennt 800 RPS Lesen und 50 RPS Schreiben
hält, kann an ihrer Summe kippen — gemeinsamer Verbindungspool, Sperren, Cache-Konkurrenz.
Getrennte Läufe zeigen das nie. Bei uns eine Liste von Methoden mit eigener RPS — ein Lauf, ein
Bericht.

## Closed Model

„N virtuelle Nutzer, jeder wartet auf eine Antwort vor der nächsten Anfrage" wirkt natürlich, hat
aber einen eingebauten Makel: Wird der Dienst langsam, warten die Nutzer länger, und die
tatsächliche Last sinkt von selbst. Das Werkzeug hört genau dann auf zu drücken, wenn es spannend
wird, was unter Druck passiert.

Wir arbeiten mit einem Open Model: Die Ziel-RPS ist ein Zeitplan, Anfragen gehen nach der Uhr raus,
ob die vorigen zurück sind oder nicht.

## Timeouts und Verbindungsfehler

Eine nach zwei Sekunden abgebrochene Anfrage sagt eines: Sie dauerte **mehr** als zwei Sekunden.
Genau zwei zu erfassen unterschätzt das Ende; sie wegzuwerfen tut so, als hätte es sie nie
gegeben. Wir setzen keine Zahl ein: Könnten abgebrochene Anfragen den Platz eines Perzentils
einnehmen, druckt der Bericht eine untere Schranke, `p99 >2.0s`. Um das Ende zu sehen, den Timeout
erhöhen.

Eine abgelehnte Verbindung kommt in Bruchteilen einer Millisekunde zurück. Als Messung erfasst,
würde sie die Latenz nach unten ziehen — der Dienst liegt, und der Bericht zeigt eine
Beschleunigung. Solche Anfragen zählen als Fehler, bleiben aber aus den Perzentilen.

## Perzentile und Metriken

Der Mittelwert zweier p99 ist kein p99 — sie dürfen nicht gemittelt werden. Addieren lassen sich
nur Verteilungen, daher wird die Zusammenfassung über Methoden aus zusammengeführten Histogrammen
berechnet, nicht aus fertigen Perzentilen. Dasselbe wird nötig, wenn mehrere Maschinen die Last
erzeugen.

Dazu die Kosten der Metrikerfassung. Ein Mutex um ein gemeinsames Latenz-Slice macht den Generator
bei hoher RPS zu seinem eigenen Engpass. Wir schreiben in HDR-Histogramme: ein Schreibvorgang
kostet einige zehn Nanosekunden, bei 5000 RPS ein Hundertstelprozent der Zeit — gemessen, nicht
angenommen.

## Vorbereitung und Tippfehler

Kein `.proto`, kein Codegen: Methodenbeschreibungen kommen per Reflection vom Dienst.

Die Konfiguration wird im strikten Modus gelesen. `rsp: 800` statt `rps: 800` ist ein Fehler mit
Ortsangabe, kein stiller Lauf mit null Last und grünem Bericht. Auch `rps` gleich null ist ein
Fehler: Ein leerer erfolgreicher Lauf ist die schlimmste Lüge, weil er wie Erfolg aussieht.

## Aufwärmen

Die ersten Sekunden eines Laufs sind immer langsam: leere Caches, Verbindungsaufbau, JIT-Aufwärmen.
Sie verzerren die Perzentile des ganzen Laufs, daher hält `warmup` sie aus den Perzentilen heraus.

## Tod des Generators

Die Kehrseite des Open Model: Steht das Ziel, stauen sich hängende Anfragen, und der Generator
stirbt vor dem getesteten Dienst. Abhilfe ist eine explizite Obergrenze gleichzeitiger Anfragen.
Die Last an der Grenze still zu senken ist nicht erlaubt: Ein „1000 RPS"-Lauf, der still zu
„so viel es eben ging" wurde, ist schlimmer als ein abgebrochener. Heute beendet das Erreichen der
Grenze den Lauf mit einem Fehler. Geplant ist, den Lauf stattdessen mit Bericht und Urteil
anzuhalten — „der Dienst hält die angeforderte RPS nicht" —, genau die Antwort, für die der Test
lief.

## Der Generator stößt an seine Grenze

Schafft die Maschine die angeforderte RPS nicht, gehen Anfragen verspätet raus, die Latenz steigt
— und der Bericht beschuldigt den Dienst, obwohl der Generator schuld ist. Die Verspätung des
Generators messen wir bereits getrennt von der Antwortzeit des Dienstes. Geplant ist, daraus ein
Urteil zu machen: Ein Lauf, in dem der Generator an seine Grenze stieß, wird für ungültig erklärt,
mit Angabe, was zu ändern ist.

## Fehler nach Kategorien

Ein `RESOURCE_EXHAUSTED` vom Rate-Limiter des Ziels ist nicht dasselbe wie ein Timeout oder ein
Absturz des Dienstes. Alles in „Fehler: 12%" zu werfen, verliert den Sinn. Der Kern unterscheidet
die Kategorien; im Bericht ist die Aufschlüsselung noch nicht ausgegeben — heute steht dort die
Gesamtzahl der Fehler.

## Sichtbarkeit

Ein Lauf darf keine zehnminütige Blackbox sein. Während er läuft, sieht man aktuelle RPS, laufende
Anfragen, Fehleranteil und Perzentile — genug, um einen offensichtlich sinnlosen Lauf in der
zweiten Minute abzubrechen statt in der zehnten.
