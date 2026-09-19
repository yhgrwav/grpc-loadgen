# grpc-loadgen

[Русский](README.md) · [English](README.en.md) · **Deutsch** · [中文](README.zh-CN.md)

Deklaratives Lasttesten für gRPC-Dienste.

> **Status: früher WIP.** Das Repository ist ein Gerüst — die Engine ist noch nicht implementiert.
> Nichts davon funktioniert heute; CLI- und Konfigurationsform stehen hier, um die UX festzuhalten.

## Das Problem

Bestehende Werkzeuge lassen jeweils etwas offen, sobald ein realer Dienst unter Last soll:

| | mehrere Methoden mit **unterschiedlichem** RPS in einem Lauf | Abhängigkeiten zwischen Aufrufen | Latenz ohne Coordinated Omission | ohne Codegen / ohne `.proto` |
|---|---|---|---|---|
| `ghz` | nein — eine Methode pro Lauf | nein | teilweise | ja (Reflection) |
| `k6` (grpc) | per Skript, standardmäßig Closed Model | manuell | Closed Model verdeckt Stillstände | `.proto` nötig |
| jmeter-grpc-request | umständlich | manuell | nein | `.proto` nötig |
| **grpc-loadgen** | ja | geplant (Stufe 1) | ja, by design | ja (Reflection) |

Konkret: `GetBalance` mit 800 RPS, `Transfer` mit 50 RPS und `CreateWallet` mit 5 RPS —
gleichzeitig, gegen denselben Dienst, in einem Lauf und einem Bericht.

## Entwurfsentscheidungen

**Open Model.** Eine Zielrate ist ein Fahrplan, kein Nebenläufigkeitsgrad: Anfragen gehen nach
der Uhr raus, unabhängig davon, ob frühere schon beantwortet sind. Ein Closed Model (N virtuelle
Nutzer, die je auf eine Antwort warten) drosselt sich selbst genau dann, wenn der Dienst zu
degradieren beginnt — also genau im Moment, den man messen wollte.

**Latenz wird ab `scheduledAt` gemessen.** Bei 1000 RPS Ziel (eine Anfrage pro Millisekunde)
friere der Dienst für eine Sekunde ein. In dieser Sekunde waren 1000 Anfragen fällig.
Ab tatsächlichem Sendezeitpunkt gemessen, gehen sie alle nach dem Einfrieren raus, brauchen je
5 ms, und der Bericht meldet `p99 = 5ms` — der Ausfall verschwindet. Ab dem *geplanten*
Zeitpunkt gemessen: die für `t=0` fällige Anfrage ging um `t=1000ms` raus und wurde um
`t=1005ms` beantwortet — `1005ms`. Derselbe Lauf, zwei Berichte; nur einer davon stimmt.
Das steckt ab dem ersten Commit im Kern, nicht nachträglich angeflanscht.

**Explizite In-flight-Obergrenze.** Unter einem Open Model häuft ein hängender Zieldienst
offene Anfragen an, bis der Generator vor dem getesteten Dienst stirbt. Das Erreichen der
Obergrenze wird als Lauffehler gemeldet — niemals als stilles Absenken der angebotenen Last,
das die Ergebnisse unbemerkt entwerten würde.

**Zusammenführbare Metriken.** Die Erfassung liegt auf dem Hot Path: HDR-Histogramme und
Sharded Counters statt eines Mutex um ein Slice. Perzentile werden aus zusammengeführten
Histogrammen berechnet und nie gemittelt — ein Mittelwert über p99-Werte sagt nichts aus, und
verteilte Läufe (Stufe 3) müssen Verteilungen zusammenführen.

**Deskriptoren über gRPC Server Reflection.** Kein Codegen, kein `.proto`, das irgendwo liegen
muss. Gearbeitet wird auf Protobuf-Wire-Ebene, die Implementierungssprache des getesteten
Dienstes ist daher egal. `.proto`-Parsing (im Prozess, ohne externes `protoc`) ist der Fallback
in Stufe 2 für Dienste mit deaktivierter Reflection.

**Im MVP nur Unary RPC.** Streaming steht auf der Roadmap.

## Geplante Nutzung

```yaml
# loadgen.yaml
target:
  address: localhost:50051
  plaintext: true

defaults:
  duration: 60s
  max_in_flight: 5000

calls:
  - method: wallet.v1.WalletService/GetBalance
    rps: 800
    payload:
      wallet_id: "w-0001"

  - method: wallet.v1.WalletService/Transfer
    rps: 50
    payload:
      from: "w-0001"
      to: "w-0002"
      amount: 100

thresholds:          # optional; Verletzung -> Exit-Code 1
  - metric: p99
    less_than: 200ms
  - metric: error_rate
    less_than: 0.01
```

```console
$ grpc-loadgen run -c loadgen.yaml
```

Schwellwerte machen das Werkzeug CI-tauglich: normal Bericht und Exit 0, bei Verletzung Bericht
und Exit 1, damit die Pipeline fehlschlägt.

## Roadmap

- **Stufe 0 (MVP)** — Lastengine ohne Abhängigkeiten zwischen Aufrufen: Reflection, Konfiguration als Liste `{method, rps, duration}`, statische Payloads, Bericht mit Perzentilen, optionale Schwellwerte.
- **Stufe 1** — Abhängigkeitsgraph zwischen Aufrufen: Pool lebender Entitäten (Wallets anlegen, ihre IDs in Transfers wiederverwenden), Producer/Consumer zwischen Anfrageströmen, definiertes Verhalten bei leerem Pool (Backpressure).
- **Stufe 2** — `.proto` als Deskriptorquelle, Streaming-RPCs, eigene Protobuf-Optionen für automatische Verknüpfung, Containerisierung.
- **Stufe 3** — Control Plane: verteilte Läufe, Histogramm-Aggregation über mehrere Maschinen, Startsynchronisation der Worker, Dashboard. Eigenes, geschlossenes Repository.

## Aufbau

```
cmd/grpc-loadgen/   CLI-Einstiegspunkt
pkg/engine/         Scheduler, Rate Limiting, In-flight-Obergrenze, Worker-Pool
pkg/descriptor/     Auflösung von Methodendeskriptoren (Reflection)
pkg/metrics/        Histogramme, Sharded Counters, Perzentile
pkg/config/         Parsen und Validieren der Konfiguration
internal/           private Helfer
```

Der Bibliothekskern weiß nichts von YAML, CLI oder einer künftigen Control Plane. Die
Auslieferungswege hängen vom Kern ab; der Kern von keinem von ihnen.

## Mitwirken

Issues und Diskussionen sind willkommen. Pull Requests werden angenommen, sobald die Autorin
oder der Autor das [CLA](CLA.md) unterzeichnet hat — ein Kommentar am Pull Request, automatisch
geprüft. Einstieg: [CONTRIBUTING.md](CONTRIBUTING.md).

Das CLA ist eine Lizenz, keine Rechteübertragung: das Urheberrecht am Beitrag bleibt bei der
beitragenden Person. Es räumt der Projektleitung das Recht ein, das Projekt zu unterlizenzieren
und umzulizenzieren, damit ein späterer Lizenzwechsel nicht die Zustimmung aller früheren
Beitragenden erfordert.

Abhängigkeiten nur unter permissiven Lizenzen (Apache-2.0 / MIT / BSD / ISC). Bei Copyleft
schlägt die CI fehl.

## Lizenz

Apache License 2.0 — siehe [LICENSE](LICENSE).
