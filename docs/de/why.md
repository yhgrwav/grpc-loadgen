# Warum dieses Werkzeug?

[← Zur Übersicht](README.md)

grpc-loadgen ruht auf einem Grundsatz: Ein Lasttest ist nur dann etwas wert, wenn man seinem
Ergebnis ohne Einschränkung vertrauen kann. Alles andere im Werkzeug folgt daraus.

## Last, die die Produktion abbildet

Das Werkzeug beschreibt Last so, wie sie tatsächlich auftritt: mehrere Methoden gleichzeitig,
jede mit eigener Rate und eigener Dauer, in einem Lauf und einem Bericht. Erst die Mischung der
Aufrufe bringt Konkurrenz um den Verbindungspool, Datenbanksperren und Cache-Wettbewerb zum
Vorschein — Effekte, die unsichtbar bleiben, solange Methoden einzeln gemessen werden.

## Messung, die Verzerrung widersteht

Die Latenz wird ab dem geplanten Zeitpunkt einer Anfrage gezählt, nicht ab dem Moment, in dem sie
tatsächlich hinausging. Damit ist die Verzögerung vollständig erfasst, einschließlich der Wartezeit
in der Warteschlange des Generators. Friert das Ziel eine Sekunde ein, meldet das Werkzeug
`1005ms` — die Verzögerung, die ein Nutzer erlebt hätte — statt der `5ms`, die lediglich
beschreiben, wie schnell der Dienst nach seiner Erholung antwortete.

Die Last folgt einem Fahrplan: Die eingestellte Rate wird gehalten, unabhängig davon, ob der
Dienst mitkommt. Ein Lauf mit 1000 RPS bleibt von Anfang bis Ende ein Lauf mit 1000 RPS.

## Keine Vorbereitung nötig

Für den Start genügen Adresse und Methodenname. Die Methodenbeschreibung kommt per gRPC Server
Reflection vom Dienst selbst: keine `.proto`-Dateien, keine Codegenerierung, keine Build-Schritte.
Die Interaktion findet auf Protobuf-Ebene statt, deshalb spielen Sprache und Plattform des
getesteten Dienstes keine Rolle.

## Deklarative Beschreibung

Last wird durch eine Konfigurationsdatei definiert, nicht durch ein Programm. Die Datei liegt beim
Code des Dienstes, geht durch das Review und ist auch nach einem halben Jahr klar lesbar. Sie
braucht kein Debugging und kann nicht selbst zur Quelle von Messverzerrungen werden.

## Bereit für Continuous Integration

Schwellwertbedingungen stehen in derselben Datei: Wird ein angegebener Wert überschritten, endet
der Prozess mit Exit-Code `1` und die Pipeline stoppt. Der Bericht geht nach stdout, die Diagnose
nach stderr — das Ergebnis ist maschinell verwertbar, ohne Text mit regulären Ausdrücken zu
zerlegen.

## Rücksicht auf den Menschen davor

Fehlermeldungen nennen Datei, Zeile und Ursache. Ein unbekannter Schlüssel in der Konfiguration
und eine Rate von Null gelten als Fehler und werden nicht stillschweigend akzeptiert: Ein Lauf,
der keine einzige Anfrage gesendet hat, darf nicht wie ein Erfolg aussehen. Der Zustand eines
Laufs ist währenddessen sichtbar — aktuelle Rate, offene Anfragen, Fehleranteil, aktueller p99.

Die Qualität der Bedienoberfläche wird am selben Maßstab gemessen wie die Korrektheit der
Messwerte: Eine unklare Fehlermeldung zählt als Defekt.

## Im Vergleich zu bestehenden Werkzeugen

| Fähigkeit | ghz | k6 | JMeter | grpc-loadgen |
|---|---|---|---|---|
| Mehrere Methoden mit unterschiedlichen Raten in einem Lauf | eine Methode pro Lauf | per Skript | über mehrere Thread-Gruppen | **in der Konfiguration** |
| Latenz ab dem geplanten Zeitpunkt | teilweise | Closed Model per Default | nein | **ja** |
| Lauf ohne `.proto` und Codegenerierung | ja | `.proto` erforderlich | `.proto` erforderlich | **ja** |
| Aufrufe über Antwortdaten verketten | nein | manuell im Skript | manuell | **Stufe 1** |
| Schwellwertbedingungen für CI | teilweise | ja | über Plugins | **ja** |
| Last ohne Programmieren beschreiben | ja | JavaScript-Skript | XML über eine GUI | **ja** |
