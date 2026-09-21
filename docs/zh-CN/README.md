<div align="center">

# grpc-loadgen

**不会撒谎的 gRPC 压力测试工具。**

[Русский](../ru/) · [English](../en/) · [Deutsch](../de/) · [中文](../zh-CN/)

</div>

---

> 本译文可能落后于[俄文原文](../../README.md)。

> **早期阶段。** 已可用：对真实服务施加 unary 负载，一次运行中多个方法各自的 RPS，
> 从配置生成请求体，控制台报告。尚未实现：逐步加压、用于 CI 的通过/失败阈值、JSON 报告、
> 指标导出。以下只描述已经可用的功能。

本工具回答人们做压测时要问的问题：**服务在多大负载下开始扛不住，瓶颈在哪里。**
为此它施加接近生产的负载——多个方法同时运行，每个方法各自的 RPS——并保证在服务劣化时
数字依然不撒谎。不需要 `.proto`，不需要代码生成，不需要脚本。

项目建立在三条优先级之上，顺序如下。

**测量的准确性**——报告中的数字与实际发生的情况一致，包括服务劣化期间。

**易用性。** 这一领域默认认为，为工程师打造的工具可以难用。我并不这么认为：
界面是本产品的主要优势之一，含义不清的错误提示与错误的数字同样是缺陷。

**速度**——压测机永远不会成为瓶颈。

## 安装

```console
$ go install github.com/yhgrwav/grpc-loadgen/cmd/grpc-loadgen@latest
```

## 配置

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

方法名为全名：`包.服务/方法`。工具通过 gRPC server reflection 向服务本身获取方法描述，
因此无需放置任何 `.proto`。

| 字段 | 作用 |
|---|---|
| `name` | 可选。运行在标题栏中的名称。未填写时——若所有方法属于同一服务则用服务名，否则用配置文件名 |
| `app.target` | 服务地址：`ip` 和 `port` |
| `app.tls` | TLS。未填写即开启；`false` 表示不加密连接 |
| `load.warmup` | 前 N 秒不计入百分位：冷缓存会扭曲它们 |
| `load.calls[].method` | 方法全名 |
| `load.calls[].rps` | 该方法每秒请求数 |
| `load.calls[].duration` | 持续多久：`30s`、`5m`、`1h` |
| `load.calls[].timeout` | 等待响应多久。未填写为 `2s`。填零不会关闭超时，而是报错 |
| `load.calls[].data` | 请求体，见下文。未填写则发送空消息 |

配置按严格模式读取：字段名拼错或 `rps` 为零都会报错并指出位置，而不是跑一个没有负载的运行。

**超时与在途请求上限。** 服务卡住时，每个方法会有 `rps × timeout` 个请求在途，直到超时触发。
所有方法之和不得超过 `-max-in-flight`（默认 5000），否则上限会在第一个超时之前耗尽，
运行失败，却从未显示服务卡住了。这一点在启动前检查，错误信息会给出两条出路：
多大的超时能放下，需要多大的上限。

### 请求体

```yaml
    - method: wallet.v1.WalletService/GetBalance
      rps: 800
      duration: 1m
      data:
        wallet_id: "w-123"
        currency: USD                         # 枚举按名称
        filter:
          since: "2026-01-01T00:00:00Z"       # google.protobuf.Timestamp
```

`data` 是按消息结构书写的普通 YAML。工具通过 server reflection 从服务获取 schema，
不需要 `.proto`。请求体在启动前构建一次，其中任何错误——未知字段、类型不符、方法不存在——
都会在第一个请求之前立即报出，并指明方法和字段。没有 `data` 时发送空消息，
这样的方法也不需要 reflection。

取值规则即 protobuf 的标准 JSON 规则（`protojson`）：

- 字段名与 `.proto` 中一致（`wallet_id`）或用 JSON 形式（`walletId`）；
- 枚举按名称；`Timestamp`、`Duration` 按其格式写成字符串；
- `int64` 和 `uint64` 精确送达，包括大于 2^53 的值（snowflake ID、以最小单位计的金额）：
  数字全程不经过 float；
- 以零开头的数字（`0123`、`007`）是配置错误：YAML 会把它读成八进制。需要保留零时，
  写成字符串 `"0123"`；
- **`bytes` 用 base64 字符串。** `signature: abcd` 不是四个字节 `abcd`，而是另外三个字节：
  `abcd` 本身就是合法的 base64，不会报错。四个字节 `abcd` 应写作 `signature: YWJjZA==`。

若服务关闭了 reflection，带 `data` 的方法不会启动——错误信息会直接说明。
不带 `data` 的方法无需 reflection 也能运行。

## 运行

```console
$ grpc-loadgen -c loadgen.yaml
```

启动前工具会先连接服务。地址不可达会立即报错，给出地址和原因，不运行也不出报告。

| 参数 | 作用 |
|---|---|
| `-c` | 配置文件路径 |
| `-connect-timeout` | 服务接受了连接但不响应时等待多久。默认 `10s`。连接被拒或地址错误不等待 |
| `-max-in-flight` | 等待响应的请求上限。默认 `5000` |
| `-fake` | 压测内置桩服务而非配置中的服务——无需服务即可试用工具。报告标记为 `fake target` |
| `-fake-delay`、`-fake-jitter`、`-fake-fail-ratio` | 桩服务的行为。仅与 `-fake` 一起使用 |

在终端中运行为全屏界面：实时显示 RPS、在途请求、错误和百分位，按 `q` 停止。
没有终端时（CI 中、输出被重定向）——每秒一行进度：

```
32.0s  sent 25600  rps 800  in-flight 47  failed 51  p99 43ms
```

结束时按方法给出报告：发送数、失败数、RPS、p50/p90/p95/p99。被超时中断的请求不会用一个数字
代替：如果它们可能占据某个百分位的位置，该百分位会以下界形式打印，`>2.0s`。
未到达服务的请求计为失败，但不计入百分位。

报告输出到 stdout，进度和错误输出到 stderr。

## 尚未实现

从零逐步加压到目标 RPS、用于 CI 的通过/失败阈值与 JSON 报告、报告中按错误码细分失败、
导出到 Prometheus。先后顺序见[策略](../strategy.md)（俄文）。

## 深入了解

| 问题 | |
|---|---|
| grpc-loadgen 解决什么问题？ | [阅读](problem.md) |
| 为什么选择它？ | [阅读](why.md) |
| 它修正了压测中的哪些问题？ | [阅读](pitfalls.md) |
| 什么是调用串联，为什么重要？ | [阅读](chaining.md) |
| 商业使用会是什么形态？ | [阅读](commercial.md) |
| 在哪里提问和反馈？ | [阅读](feedback.md) |

## 参与贡献

欢迎提 issue 和参与讨论。作者签署 [CLA](../../CLA.md) 后即可合入 pull request——
只需在 PR 中留一条评论，由机器人自动校验。上手方式见
[CONTRIBUTING.md](../../CONTRIBUTING.md)。

CLA 是一份许可，而非权利转让：贡献的著作权仍归你所有。

## 许可证

Apache License 2.0 —— 参见 [LICENSE](../../LICENSE)。
