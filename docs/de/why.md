# Warum dieses Werkzeug?

[← Zur Übersicht](README.md)

**Ein Lauf statt drei.** Verschiedene Raten für verschiedene Methoden, gleichzeitig. `ghz` nimmt
eine Methode pro Lauf und kann eine Mischung schlicht nicht ausdrücken. `k6` kann es, aber über
ein JavaScript-Programm, das geschrieben und gepflegt werden will.

**Zahlen, denen man glauben kann.** Die Latenz wird ab dem Zeitpunkt gemessen, für den die
Anfrage *geplant* war, nicht ab dem Zeitpunkt, an dem sie tatsächlich rausging. Der Unterschied
ist nicht kosmetisch: friert ein Dienst eine Sekunde ein, meldet die naive Messung `p99 = 5ms`
statt ehrlicher `1005ms`. Details in [der Problemliste](pitfalls.md).

**Null Vorbereitung.** Kein `.proto`, kein Codegen, keine Skripte. Adresse und Methodenname
genügen — die Beschreibung holt sich das Werkzeug per Server Reflection beim Dienst. Die
Implementierungssprache des Dienstes spielt keine Rolle.

**Eine Konfiguration, kein Programm.** Last wird deklarativ in einer Datei beschrieben, die neben
dem Code liegt und sich auch nach einem halben Jahr noch lesen lässt. In `k6` ist es ein Skript —
und ein Skript ist Code, den man debuggt, reviewt und irgendwann repariert, wenn er selbst zum
Engpass wird.

**Für CI gemacht.** `p99 < 200ms` setzen, und eine Überschreitung lässt die Pipeline über den
Exit-Code scheitern. Kein Parsen der Ausgabe mit regulären Ausdrücken, kein Bash-Kleber.

**Für Menschen gebaut.** Ein Konfigurationsfehler nennt Datei, Zeile und die konkrete Ursache.
Ein Tippfehler im Feldnamen ist ein Fehler und kein stiller Leerlauf mit grünem Bericht. Während
des Laufs sieht man, was geschieht, statt erst Stille und dann eine Zahlenwand. Eine schlechte
Fehlermeldung gilt uns als Defekt, nicht als Kleinigkeit.

| | ghz | k6 | JMeter | grpc-loadgen |
|---|---|---|---|---|
| Verschiedene Raten je Methode in einem Lauf | nein | per Skript | umständlich | **ja** |
| Latenz ohne Coordinated Omission | teilweise | Closed Model per Default | nein | **ja** |
| Ohne `.proto` und Codegen | ja | nein | nein | **ja** |
| Datengetriebene Verkettung von Aufrufen | nein | manuell im Skript | manuell | **Stufe 1** |
| Schwellwerte für CI | teilweise | ja | über Plugins | **ja** |
| Konfiguration statt Code | ja | nein | XML in einer GUI | **ja** |
