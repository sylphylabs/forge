# ADR-0008: 可重试性需要投递证据，Kind 不足以判定

**状态：** Accepted
**日期：** 2026-08-10
**关系：** 部分 supersede [ADR-0006](./0006-errors-kind-over-transport-code.md) —— 仅推翻其「顺带得到显式可重试性（`Kind.Retryable()`）」一条，ADR-0006 的核心（Kind 取代 HTTP 码作为唯一真相、codegen 生成值 sentinel、cause 不跨进程）**继续有效**。

## Context

ADR-0006 在确立 `Kind` 模型时顺带引入了 `Kind.Retryable() bool` 与 `errors.IsRetryable(err) bool`，理由是「调用方不必再把『503 可重试、500 不可』编码在每个调用点」。

**这个抽象不可能正确。** 重试是否安全取决于三个变量：

| 变量 | `Kind` 能回答吗 |
| --- | --- |
| 错误的类别 | ✅ 能 |
| **请求是否已到达服务端并执行** | ❌ **不能** |
| **操作本身是否幂等** | ❌ **不能** |

第二项是决定性的。`KindUnavailable` 既可能产生于连接尚未建立（重试安全），也可能产生于服务端执行完毕、响应回传途中被负载均衡器掐断（重试即重复执行）。**同一个 Kind 覆盖两种相反的安全性。** 第三项是调用点的语义，错误类型无从知晓。

因此 `IsRetryable(err) == true` 在「非幂等操作 + 服务端可能已执行」场景下是错误断言，而其签名诱导调用方直接使用。

### 三条实证 `[一手数据，2026-08-10 核实]`

1. **函数注释自认不足。** `Kind.Retryable` 的 doc comment 写着「Retryable describes the Kind, not the operation: retrying a non-idempotent call is still the caller's judgement」。**一个必须靠注释警告「别直接信我」的布尔函数，正是它不该存在的证据。**

2. **唯一该用它的消费方拒绝使用。** `middleware/retry` 的 `DefaultRetryable` 自行实现判定，排除 `KindResourceExhausted` 与 `KindConflict`（无退避重试限流会加剧过载）。ADR-0006 自己把这个差异记为「顺带修正」，但未作为裁决。结果是同仓两套「可重试」定义并存：

   | 判定源 | 认定可重试的 Kind |
   | --- | --- |
   | `Kind.Retryable()` | Unavailable、ResourceExhausted、Conflict、DeadlineExceeded |
   | `retry.DefaultRetryable` | Unavailable、dial 错误、DeadlineExceeded（仅在幂等声明下） |

3. **设计文档描述了一个不存在的机制。** `docs/design/retry.md` 曾描述 `transport.MarkNotSent` / `transport.WasNotSent` 与一套五步判定序，两个函数在全仓零命中。文档写的是意图，代码实现的是另一回事。

### 判据检验

- **判据一（深度）**：`Kind.Retryable` 未撬动任何实现空间，只是给一个 switch 换名字。
- **判据四（第二实现）**：正确消费方为**零** —— 唯一的潜在消费方选择了不用。
- **判据五（错误前移）**：反向违反。把「看似可用、实际不足」的判断留到运行期，故障表现为重复扣款一类最难追查的形态。
- **判据六（拒绝测试）**：可重试性是调用点策略，不是错误类型属性。框架应给机制（Kind + 幂等声明 + 投递证据），策略归调用方 —— 与 ADR-0004 把 tenant 逐出核心同线。
- **无过渡态（elegance §4）**：两套判定并存且均未被裁决为长期形态。

## Decision

**三项，构成一个整体，缺一不可。**

### 1. 删除 `Kind.Retryable` 与 `errors.IsRetryable`

`errors` 包只描述**发生了什么**，不建议**该怎么办**。调用方需要重试判定时，组合 Kind、幂等声明与投递证据自行决定，或使用 `middleware/retry` 的默认判定。

### 2. 引入投递证据：`transport.NotSent`

传输层是唯一知道「请求有没有离开本机」的层。新增最小机制标记这一事实：

```go
// transport 包
func MarkNotSent(err error) error   // 传输层在确知请求未发出时包装错误
func WasNotSent(err error) bool     // 判定链上是否带有该标记
```

**MUST 保守**：仅在传输层能证明请求未被发出时标记。已发出但未收到响应 MUST NOT 标记 —— 无证据即视为「可能已执行」。

投递点（实现时按实际代码确认）：
- HTTP：dial 阶段失败、连接池取连接失败 —— 请求字节未写出
- gRPC：`Unavailable` 且流未曾发送任何字节

### 3. `DefaultRetryable` 收紧为「证据或声明」

```
未声明幂等 → 仅当 WasNotSent(err) 为真时重试
已声明幂等（retry.Idempotent(ctx)）→ 按 Kind 判定，沿用现有集合
两者皆无 → 不重试
```

`KindUnavailable` **不再无条件重试** —— 它是本 ADR 修正的主要不安全默认。

## Consequences

**破坏性变更。** 删除两个公开函数；`middleware/retry` 默认行为收紧，此前被自动重试的部分 `Unavailable` 错误不再重试。需更新 `COMPATIBILITY.md` 双语版与 `docs/design/retry.md`。

**默认更保守，代价是需要显式声明。** 幂等操作要拿回自动重试，须在调用点声明 `retry.Idempotent(ctx)`。这是刻意的：**让安全成为默认，让便利需要一次明示**。

**提升了 T9.2 的价值定位。** 幂等中间件不再是「锦上添花的去重」，而是**非幂等操作获得重试保护的唯一正确路径** —— 服务端幂等保证使得「声明幂等」这一动作有实质支撑，而非调用方的一句空话。

**`MarkNotSent` 的成本落在传输层。** 每个传输需要判断自身的「未发出」边界；判断不了的传输不标记即可，退化为「不重试」，安全。

**对跨公网调用方的直接影响：** ADR-0006 曾记「`Kind.Retryable()` 让不可靠链路上的重试语义成为显式契约」。该论断作废 —— 公网链路恰恰是 `Unavailable` 二义性最强的场景（连接失败与响应途中断连难以区分）。正确的显式契约是投递证据加幂等声明。

**已知未解决：** 若某传输无法判定「未发出」，其非幂等调用将完全失去自动重试。这是刻意的保守，不视为缺陷。
