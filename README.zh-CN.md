# grpc-loadgen

[Русский](README.md) · [English](README.en.md) · [Deutsch](README.de.md) · **中文**

面向 gRPC 服务的声明式压力测试工具。

> **状态：早期 WIP。** 仓库目前只是骨架，压测引擎尚未实现。
> 下文所述功能均不可用；此处给出 CLI 与配置形态，只为先把使用体验定下来。

## 要解决的问题

对真实服务做压测时，现有工具各自都缺一块：

| | 同一次运行中多个方法使用**不同** RPS | 调用之间的依赖 | 不受 coordinated omission 影响的延迟 | 无需代码生成 / 无需 `.proto` |
|---|---|---|---|---|
| `ghz` | 不支持——每次运行一个方法 | 不支持 | 部分支持 | 支持（reflection） |
| `k6`（grpc） | 需写脚本，默认 closed model | 手工处理 | closed model 会掩盖卡顿 | 需要 `.proto` |
| jmeter-grpc-request | 笨重 | 手工处理 | 不支持 | 需要 `.proto` |
| **grpc-loadgen** | 支持 | 计划中（阶段 1） | 设计之初即支持 | 支持（reflection） |

具体场景：同时以 800 RPS 打 `GetBalance`、50 RPS 打 `Transfer`、5 RPS 打 `CreateWallet`，
针对同一个服务，一次运行，一份报告。

## 设计决策

**Open model。** 目标速率是一张时刻表，而不是并发度：请求按时钟发出，不管先前的请求是否已经返回。
Closed model（N 个虚拟用户，各自等待响应）恰恰会在服务开始劣化时自我限流——而那正是你想测量的时刻。

**延迟从 `scheduledAt` 起算。** 设目标 1000 RPS（每毫秒一个请求），服务卡死一秒。
这一秒内本应发出 1000 个请求。
若从实际发送时刻起算：它们都在服务恢复后才发出，各耗时 5 毫秒，报告给出 `p99 = 5ms`——整段故障消失了。
若从请求被*计划*的时刻起算：本该在 `t=0` 发出的请求实际在 `t=1000ms` 才发出，`t=1005ms` 收到响应，
延迟为 `1005ms`。同一次运行，两份报告，只有一份是真的。
这一点从第一个提交起就写进内核，而不是事后补上。

**显式的 in-flight 上限。** 在 open model 下，目标服务一旦卡住，挂起的请求会不断堆积，
最终压测机先于被测服务崩溃。触及上限会被记为一次运行错误——绝不会悄悄降低施加的负载，
那样会在无人察觉的情况下让结果失去意义。

**可合并的指标。** 记录发生在 hot path 上：使用 HDR 直方图与分片计数器，而不是在切片外面套一把互斥锁。
分位数由合并后的直方图算出，绝不对分位数求平均——对 p99 取平均毫无意义，
而分布式运行（阶段 3）必须合并的正是分布本身。

**通过 gRPC server reflection 获取描述符。** 无需代码生成，也不必把 `.proto` 放到任何地方。
一切工作都在 protobuf wire 层面完成，因此被测服务用什么语言实现并不重要。
解析 `.proto`（进程内完成，不依赖外部 `protoc`）是阶段 2 的兜底方案，用于关闭了 reflection 的服务。

**MVP 仅支持 unary RPC。** 流式 RPC 在 roadmap 中。

## 预期用法

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

thresholds:          # 可选；超标 -> 退出码 1
  - metric: p99
    less_than: 200ms
  - metric: error_rate
    less_than: 0.01
```

```console
$ grpc-loadgen run -c loadgen.yaml
```

阈值正是让该工具能用于 CI 的关键：正常情况下输出报告并以 0 退出，超标则输出报告并以 1 退出，
从而让流水线失败。

## Roadmap

- **阶段 0（MVP）** —— 不含调用间依赖的压测引擎：reflection、以 `{method, rps, duration}` 列表描述的配置、每个方法的静态 payload、含分位数的报告、可选阈值。
- **阶段 1** —— 调用之间的依赖图：存活实体池（先创建钱包，再在转账中复用其 id）、请求流之间的生产者/消费者、池耗尽时的明确行为（backpressure）。
- **阶段 2** —— 以 `.proto` 作为描述符来源、流式 RPC、用于自动关联的自定义 protobuf 选项、容器化。
- **阶段 3** —— control plane：分布式运行、跨机器的直方图聚合、worker 启动同步、仪表盘。独立的闭源仓库。

## 目录结构

```
cmd/grpc-loadgen/   CLI 入口
pkg/engine/         调度器、限速、in-flight 上限、worker 池
pkg/descriptor/     方法描述符解析（reflection）
pkg/metrics/        直方图、分片计数器、分位数
pkg/config/         配置解析与校验
internal/           私有辅助代码
```

库内核对 YAML、CLI 以及未来的 control plane 一无所知。交付方式依赖内核，内核不依赖任何交付方式。

## 参与贡献

**欢迎提 issue 和参与讨论。暂不接受 pull request**——CLA 机器人尚未配置，
而未经 CLA 合入的代码日后无法重新授权。请改为创建 issue。

依赖只允许使用宽松许可证（Apache-2.0 / MIT / BSD / ISC）。出现 copyleft 依赖时 CI 会失败。

## 许可证

Apache License 2.0 —— 参见 [LICENSE](LICENSE)。
