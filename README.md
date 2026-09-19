<div align="center">

# grpc-loadgen

**Нагрузочное тестирование gRPC, которое не врёт.**

[Русский](docs/ru/) · [English](docs/en/) · [Deutsch](docs/de/) · [中文](docs/zh-CN/)

</div>

---

> **Ранний WIP.** Движок в разработке, запускать пока нечего. Всё ниже — зафиксированный
> интерфейс, к которому идём.

Один прогон. Сколько угодно методов, у каждого свой RPS. Латенси, которой можно верить.
Ни `.proto`, ни кодогенерации, ни скриптов.

```console
$ grpc-loadgen run -c loadgen.yaml
```

## Установка

```console
$ go install github.com/yhgrwav/grpc-loadgen/cmd/grpc-loadgen@latest
```

## Конфиг

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

Имя метода — полное: `пакет.Сервис/Метод`. Описание метода инструмент получает у самого
сервиса через gRPC server reflection, поэтому класть куда-то `.proto` не нужно.

| Поле | Что делает |
|---|---|
| `app.target` | Адрес сервиса: `ip` и `port` |
| `app.tls` | TLS. Не указан — включён. `false` — подключение без шифрования |
| `load.warmup` | Первые N секунд не попадают в отчёт: холодные кеши портят percentiles |
| `load.calls[].method` | Полное имя метода |
| `load.calls[].rps` | Запросов в секунду для этого метода |
| `load.calls[].duration` | Сколько его долбить: `30s`, `5m`, `1h` |

## Запуск

```console
$ grpc-loadgen run -c loadgen.yaml
```

Пока идёт прогон, видно, что происходит:

```
running  32s/60s │ sent 25 600 │ in-flight 47 │ err 0.2% │ p99 43ms
```

В конце — отчёт по каждому методу: throughput, percentiles, разбивка ошибок.

## CI

Добавь пороги — и прогон сам решит, упал билд или нет:

```yaml
thresholds:
  - metric: p99
    less_than: 200ms
  - metric: error_rate
    less_than: 0.01
```

Порог превышен — отчёт печатается и процесс выходит с кодом `1`, пайплайн падает. Порогов нет
— код `0`.

```yaml
- name: Load test
  run: grpc-loadgen run -c loadgen.yaml
```

Отчёт идёт в stdout, прогресс и ошибки — в stderr. `grpc-loadgen run -c loadgen.yaml > report.json`
даёт чистый файл.

## Дальше

**[Документация →](docs/ru/)** — чем отличается от ghz и k6, какие проблемы нагрузочного
тестирования закрывает, как будет устроено связывание вызовов, модель коммерческого
использования.

## Контрибьютинг

Issues и обсуждения приветствуются. Pull request'ы принимаются после подписания [CLA](CLA.md) —
одна строчка комментарием в PR, проверяется автоматически. Как начать — в
[CONTRIBUTING.md](CONTRIBUTING.md).

CLA — это лицензия, а не передача прав: авторские права остаются у вас.

## Лицензия

Apache License 2.0 — см. [LICENSE](LICENSE).
