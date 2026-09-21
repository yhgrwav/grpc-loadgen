<div align="center">

# grpc-loadgen

**gRPC-Lasttests, die nicht lügen.**

[Русский](../ru/) · [English](../en/) · [Deutsch](../de/) · [中文](../zh-CN/)

</div>

---

> Diese Übersetzung kann hinter dem [russischen Original](../../README.md) zurückliegen.

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
$ go install github.com/yhgrwav/grpc-loadgen/cmd/grpc-loadgen@latest
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
| `app.tls` | TLS. Weggelassen heißt aktiv; `false` verbindet unverschlüsselt |
| `load.warmup` | Die ersten N Sekunden bleiben aus den Perzentilen — kalte Caches verzerren sie |
| `load.calls[].method` | Vollständiger Methodenname |
| `load.calls[].rps` | Anfragen pro Sekunde für diese Methode |
| `load.calls[].duration` | Wie lange: `30s`, `5m`, `1h` |
| `load.calls[].timeout` | Wie lange auf eine Antwort gewartet wird. Fehlt er — `2s`. Null schaltet nicht ab, sondern ist ein Fehler |
| `load.calls[].data` | Request-Body, siehe unten. Fehlt er — leere Nachricht |

Die Konfiguration wird streng gelesen: ein Tippfehler im Feldnamen oder `rps` gleich null ist ein
Fehler mit Ortsangabe, kein Lauf ohne Last.

**Timeout und Obergrenze für laufende Anfragen.** Hängt der Dienst, hält jede Methode
`rps × timeout` Anfragen in der Schwebe, bis der Timeout greift. Die Summe über alle Methoden darf
`-max-in-flight` (Standard 5000) nicht überschreiten, sonst ist die Obergrenze vor dem ersten
Timeout erschöpft und der Lauf bricht ab, ohne je zu zeigen, dass der Dienst hing. Das wird vor dem
Start geprüft, und der Fehler nennt beide Auswege: welcher Timeout passt und welche Obergrenze
nötig ist.

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
Methode und Feld, vor der ersten Anfrage. Ohne `data` geht eine leere Nachricht raus, und eine
solche Methode braucht keine Reflection.

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

Ist Reflection am Dienst abgeschaltet, startet eine Methode mit `data` nicht — der Fehler sagt es
direkt. Eine Methode ohne `data` funktioniert auch ohne Reflection.

## Ausführen

```console
$ grpc-loadgen -c loadgen.yaml
```

Vor dem Start verbindet sich das Werkzeug mit dem Dienst. Eine unerreichbare Adresse ist sofort
ein Fehler, mit Adresse und Ursache, ohne Lauf und ohne Bericht.

| Flag | Bedeutung |
|---|---|
| `-c` | Pfad zur Konfiguration |
| `-connect-timeout` | Wie lange auf einen Dienst gewartet wird, der die Verbindung annimmt, aber schweigt. Standard `10s`. Abgelehnte Verbindung und falsche Adresse warten nicht |
| `-max-in-flight` | Obergrenze für Anfragen, die auf Antwort warten. Standard `5000` |
| `-fake` | Einen eingebauten Stub statt des Dienstes aus der Konfiguration belasten — um das Werkzeug ohne Dienst anzusehen. Der Bericht ist mit `fake target` markiert |
| `-fake-delay`, `-fake-jitter`, `-fake-fail-ratio` | Verhalten des Stubs. Nur zusammen mit `-fake` |

Im Terminal läuft der Lauf im Vollbild: RPS, laufende Anfragen, Fehler und Perzentile live, `q`
hält an. Ohne Terminal (in CI, bei umgeleiteter Ausgabe) — eine Fortschrittszeile pro Sekunde:

```
32.0s  sent 25600  rps 800  in-flight 47  failed 51  p99 43ms
```

Am Ende ein Bericht je Methode: gesendet, fehlgeschlagen, RPS, p50/p90/p95/p99. Durch den Timeout
abgebrochene Anfragen werden nicht durch eine Zahl ersetzt: Könnten sie den Platz eines Perzentils
einnehmen, wird es als untere Schranke gedruckt, `>2.0s`. Anfragen, die den Dienst nie erreicht
haben, zählen als Fehler, bleiben aber aus den Perzentilen.

Der Bericht geht nach stdout, Fortschritt und Fehler nach stderr.

## Noch nicht vorhanden

Hochfahren von null auf die Ziel-RPS, Pass/Fail-Schwellen und JSON-Bericht für CI, Aufschlüsselung
der Fehler nach Code im Bericht, Export nach Prometheus. Die Reihenfolge steht in der
[Strategie](../strategy.md) (Russisch).

## Mehr erfahren

| Frage | |
|---|---|
| Welches Problem löst grpc-loadgen? | [Lesen](problem.md) |
| Warum dieses Werkzeug? | [Lesen](why.md) |
| Welche Lasttest-Probleme behebt es? | [Lesen](pitfalls.md) |
| Was ist Call-Verkettung und wozu? | [Lesen](chaining.md) |
| Wie sieht die kommerzielle Nutzung aus? | [Lesen](commercial.md) |
| Wo stelle ich Fragen und gebe Rückmeldung? | [Lesen](feedback.md) |

## Mitwirken

Issues und Diskussionen sind willkommen. Pull Requests werden übernommen, sobald das
[CLA](../../CLA.md) unterzeichnet ist — ein Kommentar am Pull Request, automatisch geprüft.
Einstieg: [CONTRIBUTING.md](../../CONTRIBUTING.md).

Das CLA ist eine Lizenz, keine Rechteübertragung: das Urheberrecht am Beitrag bleibt bei Ihnen.

## Lizenz

Apache License 2.0 — siehe [LICENSE](../../LICENSE).
