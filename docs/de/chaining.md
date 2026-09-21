# Was ist Call-Verkettung und wozu?

[← Zur Übersicht](README.md)

> Diese Übersetzung kann hinter dem [russischen Original](../ru/chaining.md) zurückliegen.

> **Status: geplant, funktioniert noch nicht.** Das Beispiel unten ist ein Entwurf des Formats,
> keine funktionierende Konfiguration.

Die Hälfte der Methoden eines echten Dienstes lässt sich nicht mit statischen Daten aufrufen. Um
Überweisungen zu belasten, braucht man existierende Wallets: `Transfer` verlangt `from` und `to`,
und die gibt es nur aus Antworten von `CreateWallet`.

Der übliche Ausweg ist, Testdaten vorab vorzubereiten. Wo sich Daten vorbereiten lassen, ist das
der richtige Weg, und dafür kommt vor der Verkettung die Einspeisung aus einer Datei: ein CSV- oder
JSON-Datensatz, eine Zeile pro Anfrage. Doch nicht alles lässt sich vorbereiten: Einmal-Tokens,
Sitzungen, Last auf den Erstellungsvorgang selbst. Und tausend Überweisungen zwischen denselben
zwei Wallets sind keine Last auf Überweisungen, sondern auf eine Datenbankzeile und ihre Sperre.

Der zweite Ausweg ist ein Skript, das ein Wallet erstellt und gleich davon überweist. Dann misst
man nicht die Überweisung, sondern die Folge „Erstellen plus Überweisen", und die beiden lassen
sich in der Latenz nicht mehr trennen.

Wir lösen das mit Pools. Ein Aufruf wird zum Produzenten erklärt: Aus seinen Antworten wird ein
Feld genommen und in einem benannten Pool gesammelt. Ein anderer Aufruf ist Konsument: Er setzt
Werte aus dem Pool in seine Anfragen ein. Jeder läuft mit eigener RPS und bleibt eine eigene Zeile
im Bericht.

```yaml
calls:
  - method: wallet.v1.WalletService/CreateWallet
    rps: 5
    extract:
      wallets: wallet_id

  - method: wallet.v1.WalletService/Transfer
    rps: 50
    data:
      from: ${pool.wallets}
      to: ${pool.wallets}
      amount: 100
```

Fremde `.proto` muss man dafür nicht anfassen: Die Verknüpfung wird bei uns beschrieben, und
Methoden und Felder werden beim Start anhand der Reflection-Daten geprüft — ein fehlendes Feld ist
ein klarer Fehler vor dem Lauf, keine Müllantworten mitten in der Last.

Eine offene Frage, die mit der Umsetzung geklärt wird: Was tun, wenn der Konsument schneller ist
als der Produzent und der Pool leerläuft — warten, Last senken oder abbrechen. Stillschweigend den
letzten Wert wiederzuverwenden ist die einzige sicher falsche Antwort.

Platz im Plan — nach dem Hochfahren der Last und den Datensätzen, siehe
[Strategie](../strategy.md) (Russisch).
