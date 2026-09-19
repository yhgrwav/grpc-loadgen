# Was ist Call-Verkettung und wozu?

[← Zur Übersicht](README.md)

Die Hälfte der Methoden eines echten Dienstes lässt sich nicht mit statischen Daten aufrufen. Um
Überweisungen zu belasten, braucht es existierende Konten: `Transfer` will `from` und `to`, und
die kommen nur aus Antworten von `CreateWallet`.

Der übliche Behelf ist, Testdaten vorab anzulegen und fest in die Konfiguration zu schreiben. Das
trägt bis zur ersten unangenehmen Frage: Tausend Überweisungen zwischen denselben zwei Konten
sind keine Last auf Überweisungen, sondern Last auf eine Datenbankzeile und deren Sperre. Ein
realistisches Bild entsteht erst mit einem Strom unterschiedlicher Entitäten.

Der andere Behelf ist ein Skript, das ein Konto anlegt und sofort davon überweist. Dann misst man
die Abfolge „Anlegen plus Überweisen", und ihre Anteile an der Latenz sind nicht mehr trennbar.

Wir lösen das über Pools. Ein Aufruf wird zum Produzenten erklärt: Aus seinen Antworten wird ein
Feld entnommen und in einem benannten Pool gesammelt. Ein anderer Aufruf ist Konsument und füllt
seine Anfragen aus diesem Pool. Jeder behält seine eigene Rate und bleibt eine eigene Zeile im
Bericht.

```yaml
calls:
  - method: wallet.v1.WalletService/CreateWallet
    rps: 5
    extract:
      wallets: wallet_id

  - method: wallet.v1.WalletService/Transfer
    rps: 50
    payload:
      from: ${pool.wallets}
      to: ${pool.wallets}
      amount: 100
```

Niemandes `.proto` muss dafür angefasst werden: Die Verknüpfung wird bei uns beschrieben, während
die Existenz von Methoden und Feldern beim Start gegen die Reflection-Daten geprüft wird — ein
fehlendes Feld ergibt einen klaren Fehler vor dem Lauf statt Müllantworten mittendrin.

Eine Frage bleibt offen, bis die Implementierung sie beantwortet: Was tun, wenn der Konsument
schneller ist als der Produzent und der Pool leerläuft — warten, Last senken oder abbrechen. Den
letzten Wert stillschweigend wiederzuverwenden ist die einzige sicher falsche Antwort.

Status: Stufe 1, sobald die Engine läuft.
