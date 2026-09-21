# Welches Problem löst grpc-loadgen?

[← Zur Übersicht](README.md)

> Diese Übersetzung kann hinter dem [russischen Original](../ru/problem.md) zurückliegen.

Ein Lasttest soll eine Frage beantworten: **ab welcher Last hält der Dienst nicht mehr mit, und wo
liegt der Engpass.** Mit dieser Antwort geht man zu den Entwicklern — reparieren, optimieren,
entscheiden, ob das Release rausgeht.

Damit die Antwort stimmt, braucht es zwei Dinge.

**Last, die der Produktion ähnelt.** Einen echten Dienst belastet niemand über einen einzigen
Endpunkt. Ein Zahlungsdienst verarbeitet zu jedem Zeitpunkt Hunderte Saldoabfragen, Dutzende
Überweisungen und eine Handvoll Registrierungen — und er bricht an dieser Mischung, nicht an jeder
Methode einzeln. Ein Dienst, der getrennt 800 RPS Lesen und 50 RPS Schreiben hält, kann an ihrer
Summe kippen — gemeinsamer Verbindungspool, Datenbanksperren, Konkurrenz um denselben Cache. `ghz`
nimmt eine Methode pro Lauf: drei Methoden sind drei Läufe und keine Antwort darauf, was passiert,
wenn sie zusammen laufen. grpc-loadgen beschreibt die ganze Last: eine Liste von Methoden, jede
mit eigener RPS, ein Lauf, ein Bericht.

**Zahlen, denen man trauen kann.** Ein Lastwerkzeug liefert eine Zahl, auf der Entscheidungen
beruhen. Eine falsch gewonnene Zahl ist gefährlicher als keine: Eine fehlende Zahl sieht man, eine
falsche sieht aus wie Wissen. Die teuersten Fehler passieren genau bei Degradierung — der Dienst
wird langsam, das Werkzeug zeigt weiter schöne Zahlen. Die konkreten Arten zu lügen stehen auf
[einer eigenen Seite](pitfalls.md).

Was das Werkzeug nicht tut: Es sieht den Dienst nur von außen — Latenz, Durchsatz, Fehler. CPU und
Speicher des Dienstes sieht es nicht und errät sie nicht aus eigenen Daten. Die kommen aus dem
Monitoring des Dienstes und werden zeitlich mit der Last abgeglichen.
