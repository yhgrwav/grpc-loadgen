<div align="center">

<h1><img src="../../assets/logo.png" width="360" alt="LeetTest"></h1>

**gRPC-Lasttests, die nicht lügen.**

[Русский](../ru/) · [English](../en/) · [Deutsch](../de/) · [中文](../zh-CN/)

[![CI](https://github.com/yhgrwav/leettest/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/yhgrwav/leettest/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/yhgrwav/leettest.svg)](https://pkg.go.dev/github.com/yhgrwav/leettest)
[![Go version](https://img.shields.io/github/go-mod/go-version/yhgrwav/leettest)](../../go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](../../LICENSE)

</div>

---

> Übersetzt aus [README.md](../../README.md) bei 4bcef1e, 2026-09-25. Weichen die beiden ab, gilt
> das russische.

> **Frühe Phase.** Funktioniert bereits: Unary-Last gegen einen echten Dienst, mehrere Methoden
> mit eigener RPS in einem Lauf, Request-Body aus der Konfiguration, Bericht in der Konsole. Noch
> nicht: Hochfahren der Last, Pass/Fail-Schwellen für CI, JSON-Bericht, Metrik-Export. Alles
> Folgende beschreibt nur, was schon funktioniert.

Das Werkzeug beantwortet die Frage, mit der man zu einem Lasttest kommt: **ab welcher Last hält
der Dienst nicht mehr mit, und wo liegt der Engpass.** Dafür erzeugt es Last, die der Produktion
ähnelt — mehrere Methoden gleichzeitig, jede mit eigener RPS —, und misst so, dass die Zahlen
auch bei Degradierung des Dienstes nicht lügen. Kein `.proto`, kein Codegen, keine Skripte.

Das Projekt ruht auf drei Prioritäten, in dieser Reihenfolge.

**Korrektheit der Messung** — die Zahlen entsprechen dem, was tatsächlich geschah, auch während
der Dienst degradiert.

**Bedienbarkeit.** In dieser Kategorie gilt es als selbstverständlich, dass ein Werkzeug für
Ingenieure unbequem sein darf. Ich sehe das anders: Die Oberfläche ist einer der wichtigsten
Vorteile des Produkts, und eine unklare Fehlermeldung ist ebenso ein Defekt wie eine falsche Zahl.

**Geschwindigkeit** — der Generator wird nie zum Engpass.

## Installation

```console
$ go install github.com/yhgrwav/leettest/cmd/leettest@latest
```

## Konfiguration

```yaml
app:
  target:
    ip: localhost
    port: 50051
  tls: false

load:
  warmup: 5s
  calls:
    - method: wallet.v1.WalletService/GetBalance
      rps: 800
      duration: 1m

    - method: wallet.v1.WalletService/Transfer
      rps: 50
      duration: 1m
```

Der Methodenname ist vollständig: `paket.Dienst/Methode`. Die Beschreibung der Methode holt das
Werkzeug per gRPC Server Reflection vom Dienst selbst, eine `.proto` muss nirgends abgelegt
werden.

| Feld | Bedeutung |
|---|---|
| `name` | Optional. Name des Laufs in der Kopfzeile. Fehlt er — der Dienstname, wenn alle Methoden zu einem gehören, sonst der Dateiname der Konfiguration |
| `app.target` | Adresse des Dienstes: `ip` und `port` |
| `app.tls` | TLS. Weggelassen — aktiv. `false` — unverschlüsselte Verbindung |
| `app.ca` | PEM-Datei mit Zertifikaten, mit denen der Dienst statt mit den Systemzertifikaten geprüft wird. Braucht TLS |
| `app.cert`, `app.key` | Client-Zertifikat und sein Schlüssel in PEM, für einen Dienst mit mTLS. Nur zusammen, braucht TLS |
| `app.server_name` | Name, gegen den das Zertifikat des Dienstes geprüft wird, wenn es die Adresse aus `target` nicht nennt. Braucht TLS |
| `app.metadata` | Header jedes Aufrufs: `authorization`, `x-api-key` usw. `${NAME}` wird aus einer Umgebungsvariable genommen |
| `app.max_response_size` | Die größte Antwort, die ein Aufruf annimmt: `16MiB`, `512KB`. Die Einheit ist Pflicht (`MB` = 10⁶ Bytes, `MiB` = 2²⁰), unter 2 GiB. Fehlt es — 4 MiB, wie bei gRPC. Eine größere Antwort ist eine abgewiesene Anfrage, keine Überlastung des Ziels |
| `load.warmup` | Die ersten N Sekunden bleiben aus den Perzentilen und aus `sent`: kalte Caches verzerren sie. Die Aufrufe der Aufwärmphase gehen an das Ziel; der Bericht druckt sie als Zeile `warm-up N sent (M failed), excluded from stats` — `sent` plus diese Zeile ergeben alle ausgegangenen Aufrufe. Das Ziel hat sie alle erhalten, außer den als unreachable gezählten; `cut off` und Aufrufe mit Timeout haben es womöglich nur teilweise erreicht: Ein Ziel, das sein HTTP/2-Fenster (Flow Control) nicht öffnet, bekommt nur die Header, und seine Zähler sehen den Aufruf womöglich nicht. Zählt zu `duration`, kürzer als jeder Aufruf |
| `load.calls[].method` | Vollständiger Methodenname |
| `load.calls[].rps` | Anfragen pro Sekunde für diese Methode |
| `load.calls[].duration` | Wie lange sie belastet wird: `30s`, `5m`, `1h` |
| `load.calls[].timeout` | Wie lange auf eine Antwort gewartet wird. Fehlt er — `2s`. Null schaltet nicht ab, sondern ist ein Fehler |
| `load.calls[].data` | Request-Body, siehe unten. Fehlt er — leere Nachricht |

Die Konfiguration wird streng gelesen: ein Tippfehler im Feldnamen ist ein Fehler mit
Zeilennummer, ein falscher Wert ein Fehler mit Nummer und Methode des Aufrufs, kein Lauf mit leerer
Last. `rps` ist eine ganze Zahl: `10.5` wird abgelehnt, nicht stillschweigend auf zehn gerundet.
`warmup` muss kürzer sein als jeder Aufruf, sonst bliebe von dem Aufruf keine einzige gemessene
Anfrage übrig.

**Timeout und Obergrenze für laufende Anfragen.** Hängt der Dienst, hält jede Methode
`rps × timeout` Anfragen in der Schwebe, bis der Timeout greift, plus eine Reserve: eine Anfrage an
der Fenstergrenze und `rps × 100 ms` dafür, dass der Generator einen Platz etwas nach der Deadline
freigibt. Die Summe über alle Methoden darf `-max-in-flight` (Standard 5000) nicht überschreiten.
Das wird vor dem Start geprüft, und der Fehler nennt beide Auswege: welcher Timeout passt und
welche Obergrenze nötig ist. Deshalb stößt ein hängender Dienst nicht an die Obergrenze: Der Lauf
erreicht sein Ende, und der Bericht sagt, wie viele Aufrufe innerhalb des Timeouts keine Antwort
bekamen und bei welchem geplanten Aufruf das Ziel zuletzt geantwortet hat. Ist die Obergrenze
trotzdem erschöpft, wurden Plätze mehr als 100 ms über ihre Deadline gehalten — das ist der
Generator (zu wenig CPU) oder der Sender, und der Bericht erklärt den Lauf für ungültig. Daneben
steht, wie viele Plätze in diesem Moment über ihre Deadline gehalten wurden. Wie viele davon über
die Reserve hinausgingen, zählt das Werkzeug nicht: Für das Urteil genügt einer.

### Zugang zum Dienst

```yaml
app:
  target:
    ip: 10.0.3.17
    port: 443
  ca: certs/ca.pem            # Pfade relativ zur Konfigurationsdatei
  cert: certs/client.pem
  key: certs/client.key
  server_name: payments.internal
  metadata:
    authorization: Bearer ${PAYMENTS_TOKEN}
    x-api-key: ${PAYMENTS_KEY}
```

**Geheimnisse.** `${NAME}` wird aus einer Umgebungsvariable eingesetzt, auch mitten im Wert:
`Bearer ${TOKEN}`. Eine nicht gesetzte oder leere Variable ist ein Fehler vor dem Start, mit ihrem
Namen. Sonst ginge `Bearer ` ohne Token hinaus, und der Lauf zeigte 100 % Fehler als Schuld des
Ziels. Um `${` wörtlich zu schreiben, verdoppelt man das Dollarzeichen: `$${`. Ein einzelnes `$`
bleibt, wie es ist. Die Ersetzung wirkt nur in `app.metadata`: In `data`, `target` und allen
anderen Feldern geht `${NAME}` so hinaus, wie es geschrieben ist. Es gibt kein Flag für Header,
daher landet das Token weder in `ps` noch in der Shell-History. Header-Werte druckt das Werkzeug
nirgends: nicht im Bericht, nicht während des Laufs, nicht in Fehlern.

**Header.** Namen werden kleingeschrieben, wie HTTP/2 sie ohnehin überträgt. Ein
Konfigurationsfehler vor dem Start: ein Schlüssel mit `grpc-` (von gRPC reserviert),
Pseudo-Header wie `:path`, ein Schlüssel mit `-bin` (binäre Header werden noch nicht unterstützt),
ein Wert außerhalb von druckbarem ASCII. Dieselben Header und dasselbe Zertifikat gehen in die
Prüfung der Methoden vor dem Start. Antwortet das Ziel darauf mit `Unauthenticated`, während
Header gesetzt sind, startet der Lauf nicht: „target rejected credentials" — mit einem falschen
Token hätte er einen Konfigurationsfehler als Ergebnis des Ziels gezeigt. `PermissionDenied`
hält den Lauf nicht an: Das Token wurde angenommen und kann für Aufrufe gelten, ohne Zugang zur
Reflection zu geben; die Methoden werden als ungeprüft markiert. `Unauthenticated` ohne Header hält
ihn ebenfalls nicht an, und ein Hinweis sagt: „target requires credentials; app.metadata is not
set".

**Zertifikate.** `ca` ersetzt die Systemwurzeln, statt sie zu ergänzen. Die Prüfung des
Zertifikats des Ziels lässt sich durch nichts abschalten. `server_name` ändert nur den Namen, gegen
den das Zertifikat geprüft wird und der in SNI geht; `:authority` bleibt die Adresse aus `target`,
sodass ein Ziel, das danach routet, dieselbe Anfrage sieht. Ein passwortgeschützter Schlüssel wird
nicht unterstützt: Er muss vorher entschlüsselt werden.

**Eine Verbindung, die Service Config des Ziels wird ignoriert.** Der Generator hält eine
Verbindung zu einer Adresse. Liefert DNS mehrere Adressen, wird nur ein Backend belastet: Es gibt
keine Lastverteilung zwischen ihnen. Der Bericht zeigt das nicht: Wie viele Adressen DNS geliefert
hat, weiß er nicht. Die Service Config, die das Ziel über seinen Resolver ausgibt, wird nicht
angewendet: Sie könnte eine Verbindung zu jeder Adresse öffnen, unseren Timeout verkürzen, Aufrufe
auf einer ausgefallenen Verbindung halten statt sie scheitern zu lassen, oder die Antwortgröße
begrenzen — also das ändern, was gemessen wird. Aufrufe werden auch nicht wiederholt.

**Fehler vor dem Start.** Alles, was sich vor dem Lauf wissen lässt, bedeutet Exit-Code 1 und
keinen einzigen Aufruf an das Ziel: Datei nicht gefunden oder kein PEM, Zertifikat passt nicht zum
Schlüssel, Zertifikat des Ziels abgelaufen oder nennt die Adresse nicht (Hinweis: `server_name`),
das Ziel schließt die Verbindung gleich nach dem Handshake (es hat das Client-Zertifikat nicht
angenommen), TLS ist an, das Ziel hat keins, oder umgekehrt.

### Request-Body

```yaml
    - method: wallet.v1.WalletService/GetBalance
      rps: 800
      duration: 1m
      data:
        wallet_id: "w-123"
        currency: USD                         # Enum per Name
        filter:
          since: "2026-01-01T00:00:00Z"       # google.protobuf.Timestamp
```

`data` ist gewöhnliches YAML in Form der Nachricht. Das Schema holt das Werkzeug per Server
Reflection vom Dienst; keine `.proto` nötig. Der Body wird einmal vor dem Start gebaut, und jeder
Fehler darin — unbekanntes Feld, falscher Typ, fehlende Methode — zeigt sich sofort, mit Namen von
Methode und Feld, vor der ersten Anfrage. Ohne `data` geht eine leere Nachricht hinaus, und eine
solche Methode braucht keine Reflection.

**Ein Body pro Methode.** Alle Aufrufe einer Methode gehen mit demselben `data` hinaus. Für einen
Schreibvorgang mit Idempotenzschlüssel heißt das: Der erste Aufruf legt den Datensatz an, alle
weiteren nehmen den Wiederholungspfad — das Ziel gibt die schon gespeicherte Antwort zurück.
Gemessen wird dieser Pfad, nicht das Anlegen des Datensatzes. Unterschiedliche Daten pro Aufruf
sind geplant.

Die Werteregeln sind die Standard-JSON-Regeln für protobuf (`protojson`):

- Feldnamen wie in der `.proto` (`wallet_id`) oder in JSON-Form (`walletId`);
- Enums per Name; `Timestamp` und `Duration` als Zeichenketten in ihrem Format;
- `int64` und `uint64` kommen exakt an, auch über 2^53 (Snowflake-IDs, Beträge in kleinsten
  Einheiten): keine Zahl läuft durch float;
- eine Zahl mit führender Null (`0123`, `007`) ist ein Konfigurationsfehler: YAML würde sie oktal
  lesen. Braucht man die Null, schreibt man sie als Zeichenkette, `"0123"`;
- **`bytes` als Base64-Zeichenkette.** `signature: abcd` sind nicht die vier Bytes `abcd`, sondern
  drei andere: `abcd` ist selbst gültiges Base64, und es gibt keinen Fehler. Die vier Bytes `abcd`
  schreibt man als `signature: YWJjZA==`.

Jede Methode aus der Konfiguration wird vor dem Start am Dienst geprüft: ein Tippfehler im Namen
oder eine Methode, die es dort nicht gibt, ist ein Fehler vor der ersten Anfrage, kein Lauf, in dem
alle Aufrufe mit `Unimplemented` scheitern.

Ist Reflection am Dienst abgeschaltet, startet eine Methode mit `data` nicht — der Fehler sagt es
direkt. Eine Methode ohne `data` funktioniert auch ohne Reflection: Es gibt nichts, womit sie
geprüft werden könnte, und eine Warnung sagt das — vor dem Lauf auf stderr und noch einmal als
Zeile im Bericht, weil die Vollbildansicht die erste überschreibt. Die Warnung sagt, warum die
Prüfung nicht möglich war: Reflection ist aus, oder sie hat abgelehnt oder gar nicht geantwortet —
das sind verschiedene Dinge.

## Ausführen

```console
$ leettest -c leettest.yaml
```

Vor dem Start verbindet sich das Werkzeug mit dem Dienst. Eine unerreichbare Adresse ist sofort
ein Fehler, mit Adresse und Ursache, ohne Lauf und ohne Bericht.

**Wo ausführen.** Stellen Sie den Generator neben das Ziel: ins selbe Netz, auf dieselbe Maschine
oder in denselben Cluster. Alles, was dazwischen liegt, geht in die Latenz ein und sieht aus wie
Zeit des Ziels. Auf unserem Prüfstand zeigte der Generator bei 1000 RPS auf dem Host über die
Portweiterleitung von Docker Desktop p99 38 ms, im Docker-Netz 2 ms: dasselbe Ziel, im ersten Fall
wurde das Docker-Netz gemessen, nicht das Ziel.

| Flag | Bedeutung |
|---|---|
| `-c` | Pfad zur Konfiguration |
| `-connect-timeout` | Wie lange auf einen Dienst gewartet wird, der die Verbindung annimmt, aber schweigt. Standard `10s`. Abgelehnte Verbindung und falsche Adresse warten nicht |
| `-max-in-flight` | Obergrenze für Anfragen, die auf Antwort warten. Standard `5000` |
| `-fake` | Einen eingebauten Stub statt des Dienstes aus der Konfiguration belasten — um das Werkzeug ohne Dienst anzusehen. Der Bericht ist mit `fake target` markiert |
| `-fake-delay`, `-fake-jitter`, `-fake-fail-ratio` | Verhalten des Stubs. Nur zusammen mit `-fake` |
| `-version` | Version drucken und beenden. Ein Build aus den Quellen druckt den Commit |

Im Terminal läuft der Lauf im Vollbild: RPS, laufende Anfragen, Fehler und Perzentile live, `q`
hält an. Ohne Terminal (in CI, bei umgeleiteter Ausgabe) — eine Fortschrittszeile pro Sekunde:

```
32.0s  sent 25600  rps 800  in-flight 47  failed 51  not-sent 0  p99 43ms
```

Am Ende ein Bericht je Methode: gesendet, fehlgeschlagen, `sent/s`, p50/p90/p95/p99. `sent/s` sind
die gesendeten Anfragen geteilt durch die Sendezeit: Aufwärmphase und das Warten auf die letzten
Antworten nach dem Ende des Plans sind nicht enthalten, daher drückt ein Ziel, das am Ende des
Laufs hängt, diese Zahl nicht. Durch den Timeout abgebrochene Anfragen werden nicht durch eine Zahl
ersetzt: Könnten sie den Platz eines Perzentils einnehmen, wird es als untere Schranke gedruckt,
`>2.0s`. Anfragen, die den Dienst nie erreicht haben, zählen als Fehler, bleiben aber aus den
Perzentilen. Eine Anfrage, die hinausging, aber keinen Status bekam — die Gegenseite hat den Stream
zurückgesetzt oder die Verbindung abgebrochen —, wird getrennt gezählt, als `cut off`: Das Ziel oder
ein Proxy könnte sie verarbeitet haben; bei einem Schreibvorgang ist das ein Anlass, nach Dubletten
zu suchen.

Fehler werden getrennt: Die Zeile `error status` zeigt, wie oft ein Fehlerstatus zurückkam
(`RESOURCE_EXHAUSTED`, `UNAVAILABLE`, `INTERNAL` und andere) und nach welcher Zeit. Den Status kann
statt des Ziels ein Proxy davor geschickt haben: nginx ohne lebendes Backend antwortet
`UNAVAILABLE`, und der Client kann das eine nicht vom anderen unterscheiden. Die Zeile `rejected`
zeigt, wie oft ein Aufruf bei jedem Tempo gescheitert wäre: eine falsche Anfrage (keine solche
Methode, falsches Argument, Body passt nicht zum Schema) oder eine Nachricht, die nicht passte —
eine Antwort, die das Limit des Clients vollständig abgewiesen hat (`app.max_response_size`,
standardmäßig 4 MiB; von der Antwort kommt nichts an; ein Hinweis unter dem Bericht nennt das
Limit des Laufs), oder eine Anfrage, die das Ziel oder ein Proxy davor zu groß fand (deren Limit
kennen wir nicht). Letzteres hängt nicht von der Last ab: Ein solcher Aufruf scheitert bei jeder
RPS. Sind alle gemessenen Aufrufe einer Methode abgewiesen, wird der Lauf für ungültig erklärt, und
das Urteil nennt diese Methode: Bei einem Tippfehler in einer von drei Methoden läge der Anteil am
Lauf bei 33 %, und es gäbe gar kein Urteil.

Unter dem Bericht werden die gescheiterten Aufrufe jeder Methode nach gRPC-Code aufgeschlüsselt,
vom häufigsten zum seltensten, in zwei Zeilen. „sent by the target" — der Status kam über das Netz:
vom Ziel oder von einem Proxy davor, der Client kann das nicht unterscheiden. „set by the client,
no status came back" — es gab keinen Status, der Client hat den Code selbst gesetzt: niemand hat
geantwortet, die Gegenseite hat den Stream zurückgesetzt, unsere Deadline ist abgelaufen. Derselbe
Code kann in beiden Zeilen stehen. `DeadlineExceeded` vom Ziel ist meist unsere eigene Deadline:
Sie geht im Header `grpc-timeout` an das Ziel, und es kann den Aufruf vor unserem Timer beenden.
Die Kategorie sagt, wessen Schuld es ist, der Code, wonach man in den Logs des Ziels sucht.

Die Last läuft über eine Verbindung. Der Bericht druckt, wie oft sie neu aufgebaut wurde und
welches Limit gleichzeitiger Streams (`MAX_CONCURRENT_STREAMS`) das Ziel angekündigt hat, oder dass
es keins angekündigt hat. Ein Aufruf zählt ab seinem geplanten Zeitpunkt, und alles, was er auf der
Client-Seite gewartet hat, steckt in seiner Latenz: der verspätete Generator, das Warten auf eine
bereite Verbindung, das Warten auf einen freien Stream. Ändert sich ohne diese Wartezeiten das
gedruckte p99 mindestens einer Methode oder ist ein Teil der Aufrufe nie hinausgegangen, fällt der
Bericht ein Urteil und nennt die Ursache, die im p99-Schwanz am häufigsten vorkommt: Der Generator
war zu spät, es gab keine Verbindung zum Ziel, die Streams waren erschöpft. Im letzten Fall ist die
Kapazität des Ziels oberhalb von „Limit × Verbindungen" laufenden Aufrufen nicht gemessen. Für jede
betroffene Methode wird p99 ohne die Wartezeiten auf der Client-Seite gedruckt — die Zeit, die der
Aufruf beim Ziel verbrachte. Wurde vor dem Aufruf auf den Resolver gewartet, landen auch die
Interceptors des Aufrufers im Warten auf die Verbindung, daher kann diese Zahl etwas zu niedrig
ausfallen. Nie gesendete Aufrufe werden nach Ursache aufgeteilt: auf einen Stream gewartet, auf die
Verbindung gewartet, Generator zu spät.

Der Bericht geht nach stdout, Fortschritt und Fehler nach stderr.

**Anhalten** (`q` in der Vollbildansicht, Ctrl+C ohne sie):

- der erste Druck — neue Anfragen gehen nicht mehr hinaus, laufende Anfragen laufen bis zu ihrem
  Timeout weiter und landen wie üblich im Bericht;
- der zweite — laufende Anfragen werden abgebrochen und getrennt als abgebrochen gezählt: kein
  Fehler des Dienstes, sondern eine untere Schranke der Zeit; der Bericht wird gedruckt;
- der dritte — sofortiges Beenden, ohne Bericht.

In der Vollbildansicht führt die oberste Zeile durch diese Stufen: Solange laufende Anfragen zu
Ende laufen, zeigt sie, wie viele übrig sind und dass `q` sie ein weiteres Mal abbricht; nach dem
Abbruch, dass `q` ein weiteres Mal ohne Bericht beendet.

SIGTERM (`docker stop`, Kubernetes, ein abgebrochener CI-Job) bricht laufende Anfragen sofort ab
und druckt den Bericht, ohne das sanfte Anhalten: Der Orchestrator beendet den Prozess wenige
Sekunden später, und der Bericht muss es noch hinausschaffen. Läuft bereits ein Abbruch, unterbricht
SIGTERM ihn nicht.

Ein angehaltener Lauf ist im Bericht als unvollständig markiert: Seine Zahlen sind ehrlich, decken
aber weniger ab als geplant.

**Exit-Codes.** Schwellen „hält / hält nicht" gibt es noch nicht, daher gibt ein Lauf, der sein
Ende erreicht, `0` zurück, wie auch immer das Ziel geantwortet hat: **`0` heißt nicht „der Dienst
ist gesund"**. Ein Lauf, in dem 100 % der Aufrufe keine Antwort bekamen, gibt ebenfalls `0` zurück
— und der Bericht sagt das direkt. Eine Schwelle und ein Code ungleich null dafür kommen in Phase
2. Die Codes sind gerangt, nicht addiert: Ein ungültiger Lauf (`2`) wiegt schwerer als ein
unvollständiger (`3`), und `130` und `143` sind das Beenden ohne Bericht, daher ist ein Anhalten,
das einen Bericht gedruckt hat, immer `3`.

| Code | Was geschah |
|---|---|
| `0` | Plan ausgeführt, Bericht vollständig |
| `1` | Der Lauf fand nicht statt: Flags, Konfiguration, Verbindung. Kein Bericht |
| `2` | Der Lauf ist ungültig: Er stieß an die In-Flight-Obergrenze, oder das Ziel wies alle Aufrufe einer Methode als falsche Anfrage ab. Es gibt einen Bericht, aber seine Zahlen handeln nicht vom Ziel |
| `3` | Der Lauf wurde vor dem Plan angehalten. Es gibt einen Bericht, und er deckt nur ab, was durchkam |
| `130` | Notausstieg ohne Bericht nach Ctrl+C |
| `143` | Notausstieg ohne Bericht nach SIGTERM |

## Noch nicht vorhanden

Hochfahren von null auf die Ziel-RPS, Pass/Fail-Schwellen und JSON-Bericht für CI, Export nach
Prometheus.

## Mehr erfahren

| Frage | |
|---|---|
| Welches Problem löst LeetTest? | [Lesen](problem.md) |
| Warum dieses Werkzeug? | [Lesen](why.md) |
| Welche Lasttest-Probleme behebt es? | [Lesen](pitfalls.md) |
| Was ist Call-Verkettung und wozu? | [Lesen](chaining.md) |
| Wie sieht die kommerzielle Nutzung aus? | [Lesen](commercial.md) |
| Wo stelle ich Fragen und gebe Rückmeldung? | [Lesen](feedback.md) |

**[Vergleich mit ghz auf einem Prüfstand →](../compare-ghz.md)** (englisch) — Tabellen und der
Befehl zum Nachstellen.

## Mitwirken

Issues und Diskussionen sind willkommen. Pull Requests werden übernommen, sobald das
[CLA](../../CLA.md) unterzeichnet ist — eine Zeile als Kommentar im Pull Request, automatisch
geprüft. Einstieg: [CONTRIBUTING.md](../../CONTRIBUTING.md).

Das CLA ist eine Lizenz, keine Rechteübertragung: das Urheberrecht bleibt bei Ihnen.

## Lizenz

Apache License 2.0 — siehe [LICENSE](../../LICENSE).
