# Warum dieses Werkzeug?

[← Zur Übersicht](README.md)

> Diese Übersetzung kann hinter dem [russischen Original](../ru/why.md) zurückliegen.

grpc-loadgen ruht auf einem Grundsatz: Ein Lasttest ist nur dann etwas wert, wenn man seinem
Ergebnis ohne Einschränkung vertrauen kann. Alles andere im Werkzeug folgt daraus.

Die Prioritäten sind geordnet: **Korrektheit der Messung**, dann **Bedienbarkeit**, dann
**Geschwindigkeit**. Im Konflikt gewinnt die erste — eine Optimierung, die die Genauigkeit
verschlechtert, wird abgelehnt.

## Last wie in der Produktion

Das Werkzeug beschreibt Last so, wie sie wirklich auftritt: mehrere Methoden gleichzeitig, jede
mit eigener Rate und Dauer, alles in einem Lauf und einem Bericht. Gerade an der Mischung zeigen
sich Konkurrenz um den Verbindungspool, Datenbanksperren und Kämpfe um den Cache — Effekte, die man
nie sieht, solange Methoden einzeln gemessen werden.

## Messung, die sich nicht verzerren lässt

Die Latenz zählt ab dem Zeitpunkt, für den die Anfrage geplant war, nicht ab dem, zu dem sie
gesendet werden konnte. So wird die ganze Verzögerung erfasst, einschließlich der Wartezeit auf der
Seite des Generators. Hängt das Ziel eine Sekunde, meldet das Werkzeug `1005ms` — die Verzögerung,
die ein echter Nutzer gesehen hätte — statt `5ms`, die nur zeigen, wie schnell die Antworten nach
der Erholung kamen.

Die Last folgt einem Zeitplan: Die angeforderte RPS wird gehalten, ob der Dienst mitkommt oder
nicht.

Eine durch den Timeout abgebrochene Anfrage wird nicht durch eine Zahl ersetzt. Bekannt ist nur,
dass sie länger als der Timeout dauerte, und könnten solche Anfragen den Platz eines Perzentils
einnehmen, druckt der Bericht statt einer Zahl eine untere Schranke: `p99 >2.0s`.

## Ohne Vorbereitung

Adresse des Dienstes und Methodenname genügen zum Start. Die Beschreibung der Methode holt das
Werkzeug per gRPC Server Reflection vom Dienst selbst: keine `.proto`-Dateien, kein Codegen, keine
Build-Schritte. Der Request-Body steht als gewöhnliches YAML in der Konfiguration und wird vor dem
Start nach dem Schema des Dienstes gebaut.

## Deklarative Beschreibung

Die Last wird durch eine Konfigurationsdatei festgelegt, nicht durch ein Programm. Die Datei liegt
beim Code des Dienstes, durchläuft das Code-Review und bleibt auch nach einem halben Jahr lesbar.
Sie braucht kein Debugging und kann selbst keine Quelle von Messfehlern werden.

## Sorgfalt für den Menschen

Fehlermeldungen nennen Ort und Ursache. Ein unbekannter Schlüssel in der Konfiguration oder eine
Rate von null sind Fehler und werden nicht stillschweigend hingenommen: Ein Lauf, der keine einzige
Anfrage gesendet hat, darf nicht erfolgreich aussehen. Der Zustand des Laufs ist live sichtbar —
aktuelle Rate, laufende Anfragen, Fehleranteil, Perzentile.

Die Qualität der Oberfläche steht gleichrangig neben der Korrektheit der Messung: Eine unklare
Fehlermeldung gilt als Defekt.

## Im Vergleich mit bestehenden Werkzeugen

| Fähigkeit | ghz | k6 | JMeter | grpc-loadgen |
|---|---|---|---|---|
| Mehrere Methoden mit unterschiedlicher Rate in einem Lauf | eine Methode pro Lauf | per Skript | über mehrere Thread-Gruppen | **in der Konfiguration** |
| Latenz ab dem geplanten Zeitpunkt | teilweise | standardmäßig Closed Model | nein | **ja** |
| Ohne `.proto` und Codegen | ja | `.proto` nötig | `.proto` nötig | **ja** |
| Last ohne Programmierung beschrieben | ja | JavaScript-Skript | XML über GUI | **ja** |
| Hochfahren der Last | ja | ja | ja | geplant |
| Schwellen für CI | teilweise | ja | über Plugins | geplant |
| Verkettung von Aufrufen über Antwortdaten | nein | per Hand im Skript | per Hand | geplant |

