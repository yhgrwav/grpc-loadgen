<div align="center">

<h1><img src="../../assets/logo.png" width="360" alt="LeetTest"></h1>

**不会撒谎的 gRPC 压力测试工具。**

[Русский](../ru/) · [English](../en/) · [Deutsch](../de/) · [中文](../zh-CN/)

[![CI](https://github.com/yhgrwav/leettest/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/yhgrwav/leettest/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/yhgrwav/leettest.svg)](https://pkg.go.dev/github.com/yhgrwav/leettest)
[![Go version](https://img.shields.io/github/go-mod/go-version/yhgrwav/leettest)](../../go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](../../LICENSE)

</div>

---

> 译自 4bcef1e（2026-09-25）时的 [README.md](../../README.md)。两者不一致时，以俄文版为准。

> **早期阶段。** 已可用：对真实服务施加 unary 负载，一次运行中多个方法各自的 RPS，
> 从配置生成请求体，控制台报告和供脚本使用的 JSON。尚未实现：逐步加压、用于 CI 的通过/失败阈值、
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
$ go install github.com/yhgrwav/leettest/cmd/leettest@latest
```

需要 Go 1.26 或更高版本。

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

方法名为全名：`包.服务/方法`。工具通过 gRPC server reflection 从服务本身获取方法描述，
因此不需要在任何地方放置 `.proto`。

| 字段 | 作用 |
|---|---|
| `name` | 可选。报告标题中运行的名称。省略时：若所有方法属于同一服务，则为服务名，否则为配置文件名 |
| `app.target` | 服务地址：`ip` 和 `port` |
| `app.tls` | TLS。省略即开启。`false`——不加密的连接 |
| `app.ca` | PEM 证书文件，用它代替系统根证书校验服务。需要 TLS |
| `app.cert`、`app.key` | PEM 格式的客户端证书及其私钥，用于启用 mTLS 的服务。必须同时给出，需要 TLS |
| `app.server_name` | 当服务证书不包含 `target` 中的地址时，用于校验证书的名称。需要 TLS |
| `app.metadata` | 每次调用的请求头：`authorization`、`x-api-key` 等。`${NAME}` 取自环境变量 |
| `app.max_response_size` | 一次调用接受的最大响应：`16MiB`、`512KB`。必须带单位（`MB` = 10⁶ 字节，`MiB` = 2²⁰），小于 2 GiB。省略时为 4 MiB，与 gRPC 相同。更大的响应算作被拒绝的请求，而不是目标过载 |
| `load.warmup` | 前 N 秒不计入百分位和 `sent`：冷缓存会扭曲它们。预热期间的调用确实会发往目标；报告将其打印为一行 `warm-up N sent (M failed), excluded from stats`——`sent` 加上这一行等于压测机尝试发送的所有调用。除计为 unreachable 和 client error 的调用外，目标都收到了；`cut off` 和超时的调用可能只有一部分到达了目标：不打开 HTTP/2 窗口（flow control）的目标只会收到请求头，它的计数器可能看不到这次调用。计入 `duration`，必须短于每个调用 |
| `load.calls[].method` | 方法全名 |
| `load.calls[].rps` | 该方法每秒请求数 |
| `load.calls[].duration` | 对它施压多久：`30s`、`5m`、`1h` |
| `load.calls[].timeout` | 等待响应的时长。省略时为 `2s`。0 不表示关闭，而是报错 |
| `load.calls[].data` | 请求体，见下文。省略时为空消息 |

配置按严格模式读取：字段名拼写错误会报错并给出行号，取值错误会报错并给出调用序号和方法，
而不是以空负载运行。`rps` 必须是整数：`10.5` 会被拒绝，而不是悄悄四舍五入为 10。
`warmup` 必须短于每个调用，否则该调用连一个被测量的请求都不会剩下。

**超时与在途请求上限。** 如果服务卡死，每个方法会有 `rps × timeout` 个请求处于在途状态，
直到超时触发，另加一段余量：窗口边界上的一个请求，以及 `rps × 100 ms`，用于压测机在截止时间
稍后才释放槽位。所有方法之和不得超过 `-max-in-flight`（默认 5000）。这会在启动前检查，
错误信息给出两条出路：多大的超时能放得下，需要多大的上限。因此卡死的服务不会触及上限：
运行会走到终点，报告会说明有多少调用在超时内没有得到响应，以及目标最后一次响应的是计划中的
哪个调用。如果上限仍然用尽，说明槽位被占用超过其截止时间 100 ms 以上——这是压测机（CPU
不足）或发送端的问题，报告会宣布本次运行无效。旁边会打印此刻有多少槽位已超过其截止时间仍被
占用。其中有多少超出了余量，工具不统计：一个就足以作出判定。

### 访问服务

```yaml
app:
  target:
    ip: 10.0.3.17
    port: 443
  ca: certs/ca.pem            # 路径相对于配置文件
  cert: certs/client.pem
  key: certs/client.key
  server_name: payments.internal
  metadata:
    authorization: Bearer ${PAYMENTS_TOKEN}
    x-api-key: ${PAYMENTS_KEY}
```

**密钥。** `${NAME}` 从环境变量替换，也可以位于值的中间：`Bearer ${TOKEN}`。变量未设置或
为空时，启动前报错并给出变量名。否则会发出没有令牌的 `Bearer `，运行会把 100% 的失败显示为
目标的过错。要按字面写 `${`，把美元符号写两次：`$${`。单个 `$` 保持原样。替换只在
`app.metadata` 中生效：在 `data`、`target` 及其他字段中，`${NAME}` 按原文发出。请求头没有
命令行参数，因此令牌不会出现在 `ps` 或 shell 历史中。工具在任何地方都不打印请求头的值：
报告中、运行过程中、错误信息中都不会。

**请求头。** 名称会转为小写，HTTP/2 本来就这样传输。以下情况在启动前报配置错误：以 `grpc-`
开头的键（由 gRPC 保留）、`:path` 之类的伪头、以 `-bin` 结尾的键（暂不支持二进制请求头）、
超出可打印 ASCII 的值。启动前的方法检查也使用同样的请求头和证书。如果设置了请求头而目标
对该检查回复 `Unauthenticated`，运行不会开始：「target rejected credentials」——用错误的令牌，
它会把配置错误显示为目标的结果。`PermissionDenied` 不会阻止运行：令牌已被接受，可能对调用
有效，只是不允许访问 reflection；这些方法会被标记为未检查。未设置请求头时的 `Unauthenticated`
同样不会阻止运行，并会给出提示：「target requires credentials; app.metadata is not set」。

**证书。** `ca` 替换系统根证书，而不是追加。对目标证书的校验无法以任何方式关闭。
`server_name` 只改变校验证书所用的名称以及写入 SNI 的名称；`:authority` 仍是 `target` 中的
地址，因此依据它路由的目标看到的是同样的请求。不支持带密码的私钥：请事先解密。

**一个连接，忽略目标的 service config。** 压测机只保持到一个地址的一个连接。如果 DNS 返回
多个地址，只有一个后端承受负载：它们之间没有负载均衡。报告不会显示这一点：它不知道 DNS
返回了多少个地址。目标通过解析器下发的 service config 不会被应用：它可能为每个地址打开连接、
缩短我们的超时、在失效的连接上挂起调用而不是让其失败，或者限制响应大小——也就是改变被测量的
内容。调用也不会重试。

**启动前的错误。** 凡是运行前就能知道的问题，都以退出码 1 结束，且不向目标发出任何调用：
文件不存在或不是 PEM，证书与私钥不匹配，目标证书已过期或不包含该地址（提示：`server_name`），
目标在握手后立即关闭连接（没有接受客户端证书），开启了 TLS 而目标没有，或者相反。

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

`data` 是按消息结构书写的普通 YAML。工具通过 server reflection 从服务获取 schema，不需要
`.proto`。请求体在启动前构建一次，其中的任何错误——未知字段、类型不对、方法不存在——都会立刻
显示，并带上方法名和字段名，早于第一个请求。没有 `data` 时发送空消息，这样的方法不需要
reflection。

**每个方法一个请求体。** 一个方法的所有调用都使用同一个 `data`。对于带幂等键的写操作，这意味着
第一次调用创建记录，之后的调用都走重复路径：目标返回已保存的响应。测量的是这条路径，而不是
创建记录。每次调用使用不同数据已在计划中。

取值规则是 protobuf 的标准 JSON 规则（`protojson`）：

- 字段名与 `.proto` 中相同（`wallet_id`），或使用 JSON 形式（`walletId`）；
- 枚举按名称；`Timestamp`、`Duration` 按各自格式写成字符串；
- `int64` 和 `uint64` 精确传递，包括大于 2^53 的值（snowflake ID、以最小单位计的金额）：
  数字在任何环节都不经过 float；
- 带前导零的数字（`0123`、`007`）是配置错误：YAML 会把它读作八进制。需要保留零就写成
  字符串，`"0123"`；
- **`bytes` 写成 base64 字符串。** `signature: abcd` 不是四个字节 `abcd`，而是另外三个字节：
  `abcd` 本身就是合法的 base64，不会报错。四个字节 `abcd` 应写作 `signature: YWJjZA==`。

配置中的每个方法都会在启动前向服务核对：方法名拼写错误或服务不存在的方法，会在第一个请求前
报错，而不是运行一场所有调用都以 `Unimplemented` 失败的测试。

如果服务关闭了 reflection，带 `data` 的方法不会启动——错误信息会直接说明。不带 `data` 的方法
没有 reflection 也能运行：无法检查它，工具会打印警告——运行前输出到 stderr，并在报告中再给出
一行，因为全屏界面会覆盖前者。警告会说明为何无法检查：reflection 已关闭，或者它拒绝了，
或者根本没有响应——这是不同的情况。

## 运行

```console
$ leettest -c leettest.yaml
```

启动前工具会连接服务。地址不可达会立即报错，给出地址和原因，不会运行，也没有报告。

**在哪里运行。** 把压测机放在目标旁边：同一网络、同一台机器或同一集群中。两者之间的一切都会
计入延迟，看起来像目标的耗时。在我们的测试台上，1000 RPS 时，主机上的压测机经由 Docker
Desktop 端口转发测得 p99 38 ms，而在 Docker 网络内部为 2 ms：目标相同，前一种情况测到的是
Docker 的网络，而不是目标。

| 参数 | 作用 |
|---|---|
| `-c` | 配置文件路径 |
| `-connect-timeout` | 对已接受连接但不响应的服务等待多久。默认 `10s`。连接被拒绝和地址错误不会等待 |
| `-max-in-flight` | 等待响应的请求上限。默认 `5000` |
| `-fake` | 对内置桩服务施压，而不是配置中的服务——无需服务即可体验工具。报告会标注 `fake target` |
| `-fake-delay`、`-fake-jitter`、`-fake-fail-ratio` | 桩服务的行为。只能与 `-fake` 一起使用 |
| `-version` | 打印版本并退出。从源码构建的版本打印提交号 |
| `-output` | stdout 中的报告格式：`text`（默认）或供脚本和 CI 使用的 `json` |

在终端中运行时以全屏界面显示：实时的 RPS、在途请求、错误和百分位，按 `q` 停止。没有终端时
（在 CI 中、输出被重定向时）每秒打印一行进度：

```
32.0s  sent 25600  rps 800  in-flight 47  failed 51  not-sent 0  p99 43ms
```

结束时按方法给出报告：已发送、失败数、`sent/s`、p50/p90/p95/p99。`sent/s` 是已发送请求数除以
发送时长：预热以及计划结束后等待最后响应的时间都不计入，因此目标在运行末尾卡住不会拉低这个
数字。因超时而中断的请求不会被某个数值替代：如果它们可能占据某个百分位的位置，该百分位会
打印为下界，`>2.0s`。未到达服务的请求计为失败，但不计入百分位。已经发出却没有收到状态的
请求——对端重置了流或断开了连接——单独计为 `cut off`：目标或代理可能已经处理了它，对于写操作
这是检查重复数据的理由。

成功以外的响应按行分开，每行有自己的百分位。状态可能不是目标本身发来的，而是它前面的代理：
没有存活后端的 nginx 会回复 `UNAVAILABLE`，客户端无法区分两者。

- `request error`：在任何速率下都会失败的调用。错误的请求（没有该方法、参数错误、请求体不符合
  schema），或者被目标或其前面的代理认为过大的请求（它们的限制我们无从得知）。
- `overload`：状态表示“过载或不可用”，即 `RESOURCE_EXHAUSTED` 或 `UNAVAILABLE`。这是状态本身的
  说法，而不是对目标的诊断：没有存活后端的代理也会发出同样的 `UNAVAILABLE`。
- `failure`：状态表示调用出错：`INTERNAL`、`UNKNOWN`、`DATA_LOSS`、对端发来的 `CANCELLED`，以及
  `ABORTED`。`ABORTED` 是并发修改之间的冲突（事务回滚、乐观锁失败），而不是容量不足：负载会让它
  更频繁，但在两行相同的数据上，任何速率下都会出现。
- `bad response`：响应到达了，但客户端没有接受。要么超过了客户端的限制（`app.max_response_size`，
  默认 4 MiB；响应内容一点也不会到达），要么用客户端没有声明的编码压缩。后者是对端违反了协议：
  gRPC 只允许服务器使用客户端在 `grpc-accept-encoding` 中列出的编码进行压缩。
- `client error`：客户端自己拒绝发送。请求无法编码，或者编解码器、拦截器出错。这个调用从未到达
  目标。

过大的请求是通过 grpc-go 的错误文本识别的。其他实现上的目标（Envoy、Java）措辞不同，它的拒绝会
被归入 `overload`，而不是 `request error`。

如果某个方法所有被测量的调用都是 `request error`、`client error` 或 `bad response`，本次运行被宣布
无效（退出码 2），提示会写明方法、原因以及该怎么做：三个方法中有一个拼写错误时，按整次运行算的
占比只有 33%，根本不会有判定。

| 状态码 | 状态经网络传回 | 客户端设置的状态码，请求已发出 | 客户端设置的状态码，请求未发出 |
|---|---|---|---|
| `INVALID_ARGUMENT`、`NOT_FOUND`、`ALREADY_EXISTS`、`PERMISSION_DENIED`、`UNAUTHENTICATED`、`FAILED_PRECONDITION`、`OUT_OF_RANGE`、`UNIMPLEMENTED` | `request error` | `cut off` | `client error` |
| `RESOURCE_EXHAUSTED` | `overload`；grpc-go 的 “larger than max” 为 `request error` | `cut off`；超过我们限制的响应为 `bad response` | `client error` |
| `UNAVAILABLE` | `overload` | `cut off` | `unreachable` |
| `CANCELLED`、`UNKNOWN`、`INTERNAL`、`DATA_LOSS`、`ABORTED` | `failure` | `cut off`；无法解压的响应为 `bad response` | `client error` |
| `DEADLINE_EXCEEDED` | 超时 | 超时 | 超时，未发出 |

被我们自己的停止（Ctrl+C、SIGTERM）中断的调用，无论状态码是什么，都是 `aborted`：不是目标的拒绝。

报告下方按 gRPC 状态码列出每个方法的失败调用，从多到少，分两行。「sent by the target」——
状态经网络传回：来自目标或它前面的代理，客户端无法区分。「set by the client」——状态码由客户端
自己设置：无人响应、对端重置了流、我们的截止时间已到，或者客户端没有接受已到达的响应（这时目标的
OK 状态可能已经到达，但最终的状态码是客户端的）。
同一个状态码可能同时出现在两行中。来自目标的 `DeadlineExceeded` 通常就是我们自己的截止时间：
它通过 `grpc-timeout` 请求头发给目标，目标可能在我们的计时器之前结束调用。类别说明是谁的
过错，状态码说明该在目标日志中找什么。

负载通过一个连接发送。报告会打印它重连了多少次，以及目标声明的并发流上限
（`MAX_CONCURRENT_STREAMS`），或者说明目标没有声明。调用从其计划时刻开始计时，它在客户端
一侧的所有等待都计入延迟：压测机的延迟、等待就绪连接、等待空闲流。如果去掉这些等待后至少
一个方法打印的 p99 发生变化，或者有部分调用始终没有发出，报告会作出判定，并指出在 p99 尾部
最常见的原因：压测机迟了、与目标没有连接、流用尽了。在最后一种情况下，目标在超过
「上限 × 连接数」个在途调用时的容量没有被测量。对每个受影响的方法，会打印去掉客户端等待后
的 p99——调用在目标处花费的时间。如果调用前等待过解析器，调用方的拦截器也会计入连接等待，
因此这个数字可能略微偏低。未发出的调用按原因拆分：等待流、等待连接、压测机迟了。

报告输出到 stdout，进度和错误输出到 stderr。

**停止**（全屏界面中按 `q`，否则按 Ctrl+C）：

- 第一次按——不再发出新请求，在途请求运行到各自超时，并照常计入报告；
- 第二次——中断在途请求，单独计为被中断：这不是服务的失败，而是时间的下界；打印报告；
- 第三次——立即退出，不打印报告。

在全屏界面中，顶部一行会引导完成这些步骤：在途请求收尾时，它显示还剩多少，以及再按一次 `q`
会中断它们；中断之后，显示再按一次 `q` 会不带报告退出。

SIGTERM（`docker stop`、Kubernetes、取消 CI 任务）会立即中断在途请求并打印报告，跳过温和停止：
编排器几秒后就会杀掉进程，报告必须来得及输出。如果中断已在进行，SIGTERM 不会打断它。

被停止的运行在报告中标记为不完整：数字是真实的，但覆盖的范围小于计划。

**退出码。** 目前还没有「扛得住 / 扛不住」的阈值，因此走到终点的运行无论目标如何响应都返回
`0`：**`0` 并不表示「服务健康」**。100% 调用没有得到响应的运行同样返回 `0`——报告会直接说明
这一点。阈值及对应的非零退出码将在第 2 阶段提供。退出码按优先级排序，而不是相加：无效运行
（`2`）优先于不完整运行（`3`），而 `130` 和 `143` 表示不带报告退出，因此打印了报告的停止
总是 `3`。

| 退出码 | 发生了什么 |
|---|---|
| `0` | 计划执行完毕，报告完整 |
| `1` | 运行未进行：参数、配置、连接问题。没有报告 |
| `2` | 运行无效：触及在途上限，或者某个方法所有被测量的调用都是 `request error`、`client error` 或 `bad response`。有报告，但其中的数字与负载无关 |
| `3` | 运行在计划结束前被停止。有报告，只覆盖已经完成的部分 |
| `130` | Ctrl+C 后紧急退出，没有报告 |
| `143` | SIGTERM 后紧急退出，没有报告 |

退出码 `4` 预留给阈值。

**供脚本和 CI 使用的 JSON。** 使用 `-output json` 时，stdout 中恰好是一个 JSON 对象加一个换行：
进度、实时界面、警告和错误都输出到 stderr，因此 stdout 可以直接交给 `jq`。只有确实进行了的运行
（退出码 `0`、`2`、`3`）才会打印这个对象；退出码为 `1`、`130` 和 `143` 时 stdout 为空。
字段 `outcome`（`complete`、`invalid` 或 `incomplete`）始终与退出码一致。

模式通过 `schema_version` 进行版本管理，当前为 `1`，它是契约；屏幕上的文字不是，在 1.0 之前
可能变化：请解析 JSON，而不是屏幕。规则如下：

- 新增字段不改变版本；字段改名、删除或改变类型会提高 `schema_version`；
- 枚举的取值（`outcome`、`unchecked[].reason`、`failure_codes` 中的状态码）可以扩展而不改变版本；
- 使用方必须跳过未知字段，并在遇到未知枚举值时正常处理，不能出错。

单位写在字段名中：`_us` 为整数微秒，`_s` 为整数秒；速率 `rps` 是唯一的小数。运行没有产生的值是
`null`，而不是 `0`：没有观测值的百分位、没有调用的方法的速率、目标没有声明的流上限。百分位是一个
对象 `{"us": 1234, "lower_bound": false}`：`lower_bound: true` 时它是下界（尾部超过了超时时间，
屏幕上显示为 `>5.00s`），而不是一个值。延迟是直方图的值，以整数微秒给出，不做屏幕上的舍入；
直方图保留 3 位有效数字（相对误差不超过 0.1%）。计数是精确的。
时间从 `started_at`（RFC 3339，UTC）即计划开始时刻算起，包含预热；`duration_us` 也包含预热。
某一秒中的 `in_flight` 表示该秒结束时在途的调用数。`failure_codes` 中的状态码使用规范名称
（`UNAVAILABLE`）。`unchecked[].error` 是给人看的文字，会随 grpc-go 变化；脚本请使用 `reason`。
核对方法：运行的总数等于各方法总数之和；在一个方法的各秒中，`Σ begun` 加上 `outside_timeline`
等于该方法的全部调用，包括预热。

决策以字段给出，而不是文字：`invalid_reasons`（`in_flight_cap`、`nothing_measured`）、
`methods[].invalid_reason`（`request_error`、`client_error`、`bad_response`、`mixed` 或 `null`）、
`tail_wait_cause`（`generator`、`stream`、`connection` 或 `null`，与屏幕上的判定规则相同），以及
`client_waits` 中按原因的数字。`notes` 是给人看的提示文字：措辞会随意调整，请不要解析它。

## 尚未实现

从零逐步加压到目标 RPS、用于 CI 的通过/失败阈值、导出到 Prometheus。

## 延伸阅读

| 问题 | |
|---|---|
| LeetTest 解决什么问题？ | [阅读](problem.md) |
| 为什么要用这个工具？ | [阅读](why.md) |
| 它能解决哪些压测中的问题？ | [阅读](pitfalls.md) |
| 什么是调用链，为什么需要它？ | [阅读](chaining.md) |
| 商业使用是什么样的？ | [阅读](commercial.md) |
| 在哪里提问或反馈？ | [阅读](feedback.md) |

**[在同一测试台上与 ghz 的对比 →](../compare-ghz.md)**（英文）——表格和复现命令。

## 参与贡献

欢迎提交 issue 和参与讨论。签署 [CLA](../../CLA.md) 后即可接受 pull request——在 PR 中留下
一行评论，自动校验。如何开始，见 [CONTRIBUTING.md](../../CONTRIBUTING.md)。

CLA 是一份许可，而不是权利转让：版权仍归你所有。

## 许可证

Apache License 2.0——见 [LICENSE](../../LICENSE)。
