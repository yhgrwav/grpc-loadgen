<div align="center">

# grpc-loadgen

**gRPC-Lasttests, die nicht lügen.**

[Русский](../ru/) · [English](../en/) · [Deutsch](../de/) · [中文](../zh-CN/)

</div>

---

> **Früher WIP.** Die Engine entsteht gerade, es gibt noch nichts auszuführen. Alles Folgende ist
> die Schnittstelle, auf die wir hinarbeiten.

Ein Lauf. Beliebig viele Methoden, jede mit eigener Rate. Kein `.proto`, kein Codegen, keine
Skripte.

Das Projekt ruht auf drei Prioritäten, in dieser Reihenfolge: **Korrektheit der Messung** — die
Zahlen entsprechen dem, was tatsächlich geschah, auch während der Dienst schwächelt;
**Bedienbarkeit** — Konfiguration, Fehlermeldungen und Bericht sind für Menschen gemacht;
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

Methodennamen werden vollständig angegeben: `paket.Dienst/Methode`. Die Methodenbeschreibung holt
sich das Werkzeug per gRPC Server Reflection beim Dienst selbst — es muss also nirgendwo ein
`.proto` liegen.

| Feld | Bedeutung |
|---|---|
| `app.target` | Adresse des Dienstes: `ip` und `port` |
| `app.tls` | TLS. Weggelassen heißt aktiv; `false` verbindet unverschlüsselt |
| `load.warmup` | Die ersten N Sekunden bleiben aus dem Bericht — kalte Caches verzerren Perzentile |
| `load.calls[].method` | Vollständiger Methodenname |
| `load.calls[].rps` | Anfragen pro Sekunde für diese Methode |
| `load.calls[].duration` | Wie lange: `30s`, `5m`, `1h` |

## Ausführen

```console
$ grpc-loadgen run -c loadgen.yaml
```

Während des Laufs ist sichtbar, was passiert:

```
running  32s/60s │ sent 25 600 │ in-flight 47 │ err 0.2% │ p99 43ms
```

Am Ende: Durchsatz, Perzentile und aufgeschlüsselte Fehler, je Methode.

## CI

Schwellwerte ergänzen — und der Lauf entscheidet selbst über den Build:

```yaml
thresholds:
  - metric: p99
    less_than: 200ms
  - metric: error_rate
    less_than: 0.01
```

Ein verletzter Schwellwert druckt den Bericht und beendet den Prozess mit Code `1`, die Pipeline
schlägt fehl. Ohne Schwellwerte: Code `0`.

Der Bericht geht nach stdout, Fortschritt und Fehler nach stderr; so liefert
`grpc-loadgen run -c loadgen.yaml > report.json` eine saubere Datei.

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
