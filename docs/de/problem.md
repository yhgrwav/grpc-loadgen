# Welches Problem löst grpc-loadgen?

[← Zur Übersicht](README.md)

Kein echter Dienst wird über einen einzigen Endpunkt belastet. In einem Zahlungsdienst laufen zu
jedem Zeitpunkt Hunderte Kontostandsabfragen, Dutzende Überweisungen und eine Handvoll
Registrierungen — und er bricht an dieser Mischung, nicht an einer einzelnen Methode.

Die üblichen Werkzeuge funktionieren anders. `ghz` nimmt eine Methode pro Lauf: drei Methoden
bedeuten drei Läufe, drei Berichte und keinerlei Antwort darauf, was geschieht, wenn sie
gleichzeitig laufen. Ein Dienst, der einzeln 800 RPS Lesezugriffe und 50 RPS Schreibzugriffe
verträgt, kann an deren Summe scheitern — gemeinsamer Verbindungspool, Sperren in der Datenbank,
Konkurrenz um denselben Cache. Getrennte Läufe zeigen das nie.

grpc-loadgen beschreibt die Last als Ganzes: eine Liste von Methoden, jede mit eigener Rate und
Dauer, in einem Lauf und einem Bericht. Das ist der eigentliche Unterschied — nicht „schneller"
oder „bequemer", sondern die Möglichkeit, eine Frage zu stellen, die andere Werkzeuge nicht
ausdrücken können.

Die andere Hälfte des Problems ist das Vertrauen in die Zahlen. Ein Lastwerkzeug liefert einen
Wert, an dem eine Release-Entscheidung hängt. Ein falsch ermittelter Wert ist gefährlicher als
gar keiner: eine fehlende Zahl sieht man, eine falsche sieht aus wie Wissen. Die konkreten
Formen dieser Lüge haben [eine eigene Seite](pitfalls.md).
