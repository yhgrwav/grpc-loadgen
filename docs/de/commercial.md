# Wie sieht die kommerzielle Nutzung aus?

[← Zur Übersicht](README.md)

> Diese Seite beschreibt ein **beabsichtigtes** Modell. Nichts vom kostenpflichtigen Teil
> existiert bisher, und es gibt keinen Zeitplan. Es steht hier, damit es später niemanden
> überrascht.

## Was kostenlos bleibt

Kern und CLI stehen unter der Apache License 2.0 und bleiben offen. Das ist ein vollständiges
Werkzeug, keine Demo: Läufe von einer Maschine, beliebig viele Methoden mit beliebigen Raten,
CI-Schwellwerte, Berichte. Kommerzielle Nutzung ist nicht eingeschränkt — ein Unternehmen kann
die CLI in seine Pipelines einbauen, ohne zu zahlen oder jemanden zu fragen.

## Was kostenpflichtig wird

Eine eigene Control Plane für Teams, denen eine Maschine und ein Lauf nicht mehr reichen:

- **verteilte Läufe** — Last von mehreren Maschinen, synchroner Start, korrekt zusammengeführte
  Histogramme in einem Bericht;
- **Lauf-Historie** — gespeicherte Ergebnisse, Vergleich von Release zu Release, Verfolgung von
  Verschlechterungen über die Zeit;
- **Dashboard** — laufende Tests live und Auswertung abgeschlossener;
- **Integrationen** — Benachrichtigungen, Export in Monitoring-Systeme, rollenbasierter Zugriff.

Vorgesehen ist ein Abonnement je Organisation mit kostenlosem Testzeitraum, abgerechnet über die
Control Plane und nicht über Anfragen oder Maschinen. Für geschlossene Umgebungen wird eine
selbst betriebene Variante geprüft.

## Warum die Grenze dort verläuft

Alles, was von einer einzelnen Maschine aus funktioniert, gehört zum offenen Teil. Bezahlenswert
ist, was Infrastruktur braucht: mehrere Generatoren zu koordinieren, Historie zu speichern, einem
Team eine Oberfläche zu geben.

Technisch ist es dieselbe Engine. Die Control Plane ist eine weitere Hülle um den Kern, neben der
CLI, und die Metriken waren von Anfang an so entworfen, dass sich Ergebnisse mehrerer Maschinen
korrekt addieren.

## Was das für Beitragende bedeutet

Deshalb wird ein Pull Request erst nach Unterzeichnung des [CLA](../../CLA.md) übernommen. Es ist
eine Lizenz, keine Rechteübertragung: Das Urheberrecht am Beitrag bleibt bei der beitragenden
Person, während das Projekt das Recht zur Unterlizenzierung erhält. Ohne das ist der geschlossene
Teil rechtlich unmöglich, und nachträglich lässt es sich nicht heilen — dafür bräuchte es die
Zustimmung aller, die je etwas beigetragen haben.

Wer den offenen Teil nutzt, für den ändert sich nichts: Apache 2.0 ist unwiderruflich erteilt,
und veröffentlichte Versionen bleiben darunter.
