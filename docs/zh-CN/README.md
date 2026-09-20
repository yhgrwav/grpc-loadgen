<div align="center">

# grpc-loadgen

**不会撒谎的 gRPC 压力测试工具。**

[Русский](../ru/) · [English](../en/) · [Deutsch](../de/) · [中文](../zh-CN/)

</div>

---

> **早期 WIP。** 引擎仍在开发中，目前还无法运行。以下内容是我们正在实现的目标形态。

一次运行，任意多个方法，每个方法各自的 RPS。不需要 `.proto`，不需要代码生成，不需要脚本。

项目建立在三条优先级之上，顺序如下：**测量的准确性**——报告中的数字与实际发生的情况一致，
包括服务劣化期间；**易用性**——配置、错误提示与报告都为人而设计；**速度**——压测机永远不会成为瓶颈。

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

方法名使用完整形式：`包名.服务/方法`。工具通过 gRPC server reflection 直接向服务本身获取方法定义，
因此不必在任何地方放置 `.proto` 文件。

| 字段 | 作用 |
|---|---|
| `app.target` | 服务地址：`ip` 与 `port` |
| `app.tls` | TLS。不填即启用；填 `false` 则使用不加密连接 |
| `load.warmup` | 前 N 秒不计入报告——冷缓存会扭曲分位数 |
| `load.calls[].method` | 完整方法名 |
| `load.calls[].rps` | 该方法每秒请求数 |
| `load.calls[].duration` | 持续多久：`30s`、`5m`、`1h` |

## 运行

```console
$ grpc-loadgen run -c loadgen.yaml
```

运行期间可以随时看到当前状况：

```
running  32s/60s │ sent 25 600 │ in-flight 47 │ err 0.2% │ p99 43ms
```

结束时按方法输出吞吐量、分位数与错误分类。

## 用于 CI

加上阈值，本次运行就能自行决定构建是否通过：

```yaml
thresholds:
  - metric: p99
    less_than: 200ms
  - metric: error_rate
    less_than: 0.01
```

阈值被突破时打印报告并以退出码 `1` 结束，使流水线失败；未设置阈值则退出码为 `0`。

报告写入 stdout，进度与错误写入 stderr，因此
`grpc-loadgen run -c loadgen.yaml > report.json` 会得到一个干净的文件。

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
