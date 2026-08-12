# ADR-0006: 错误模型——Kind 取代传输码，codegen 生成值 sentinel

**状态：** Accepted（`Kind.Retryable()` 一条已由 [ADR-0008](./0008-retryability-needs-delivery-evidence.md) supersede；其余继续有效）
**日期：** 2026-08-09
**关系：** 设计文档见 [docs/design/errors.md](../design/errors.md)

## Context

Forge 继承自 Kratos 的错误模型：`Error` 内嵌 proto `Status`，携带一个 **HTTP 状态码**作为分类，proto enum 上标注 `default_code = 500` / `code = 404`，codegen 输出 `IsXxx(err) bool` 与 `ErrorXxx(format, args...)` 两个函数。

2026-08-09 的实测审计（方法与完整输出见工作文档，未入库）发现四类问题，**全部为本机验证，非推理** `[一手数据]`：

**1. typed-nil 触发 panic。** `FromError` 中 `errors.As(err, &se)` 对 `(*Error)(nil)` 匹配成功并返回 nil，后续 `.Code` 解引用崩溃。`Code()` 被全部 10 个 `IsXxx()` 判定函数调用，也被 codegen 生成的每个 `IsXxx()` 调用 —— 任何 `if errors.IsNotFound(err)` 遇 typed-nil 即 panic，且触发点远离故障源。

**2. 状态码跨进程塌缩。** HTTP → gRPC → HTTP 往返实测：12 个映射码无损，但 **418、422 塌缩为 500**。根因是 HTTP 码空间（60+）大于 gRPC 码空间（17），双向映射必然有损。422 是常用码。

**3. 错误链止于 RPC 边界。** 本地 `As()` 可达 `*dbErr`；过一次 RPC 后 `Unwrap` 返回 nil。每一跳把因果链压平成一个 message 字符串。（此项**不打算完全解决** —— 见 Decision 3。）

**4. 聚合错误静默丢数据。** `Join(a, b)` 的 `Code`/`Reason` 只取第一个，第二个在投影到传输层时丢弃，因为一个 wire status 只能命名一个错误。批量校验场景直接受影响。

此外还有一致性问题：全仓 12 处 `errors.Xxx("R", err.Error())` 中 **8 处未补 `.WithCause`**，丢弃错误链；reason 命名 `SCREAMING_SNAKE` 与小写混用（`CODEC` vs `no_available_node`），而 reason 是跨进程机读契约。

**两项初判被实测推翻，记录以免重复调查：** `Is` 无 false positive（语义是「按 code+reason 判等价类」，刻意且正确）；`Clone` 无 metadata aliasing（map 逐键复制）。

## Decision

**根本主张：当前把三件事焊在一个结构体里** —— 领域语义、传输编码、诊断信息。按三层切开。

### 1. `Kind` 取代 HTTP `code` 作为唯一真相

16 个值的闭集，对齐 gRPC canonical codes（两者中较窄的词汇表）。HTTP 与 gRPC 各自**单向投影**，不存在往返，故无损 —— 422 是 `KindOutOfRange` 的 HTTP 投影，不再塌缩。

层次也因此摆正：领域包不再需要在构造错误时知道 HTTP 存在。

顺带得到显式可重试性（`Kind.Retryable()`），调用方不必再把「503 可重试、500 不可」编码在每个调用点。

### 2. codegen 生成值 sentinel，判定回归 stdlib

```proto
enum FailureReason {
  FAILURE_REASON_UNSPECIFIED = 0;
  FAILURE_REASON_NOT_FOUND = 1 [(sylphy.errors.v1.kind) = KIND_NOT_FOUND];
}
```
```go
var ErrNotFound = errors.MustDefine(errors.KindNotFound, "sylphy.test.v1", ...)

if errors.Is(err, v1.ErrNotFound) { ... }   // 只有 stdlib 语义
```

`default_kind` 可省（缺省 `KIND_INTERNAL`）。domain 自 proto package 推导，解决裸 reason 全局撞车。

**删除** 10 个 `IsXxx` 函数及 codegen 的 `IsXxx` —— 它们正是问题 1 的 panic 传播路径。`KindOf(err)` 一个函数足够且天然 nil 安全。

codegen 在**编译期**校验 reason 格式、零值标注、`KIND_UNSPECIFIED`，把一致性问题挡在代码库外。

### 3. cause 不跨进程，靠 trace 关联

cause 常含连接串、内网地址、SQL 片段。审计确认**当前不出网，这是正确属性，MUST 保持**。

因此 `Wrap` 是**本地关系**：远端错误 `Unwrap` 返回 nil，`errors.As` 取不到远端类型（避免「我以为拿到了 `*dbErr`」的类型幻觉）。跨进程排查靠 `TraceID`。

**不序列化因果摘要。** 实现期曾做过可选的 `DebugInfo` 投影（默认关闭、20 帧上限），随后移除 —— 见「被否决的方案」。跨进程排查只有一条路径：trace_id。

### 4. 脱敏是使用者的决定，框架给保守默认

`Policy` 接口 + 三档预设（Safe/Verbose/Strict）+ 自定义逃生舱。

默认 `PolicySafe` 只对 `KindInternal`/`KindDataLoss`/`KindUnknown` withhold message —— 即「message 内容使用者最不可能预料」的那类（它来自被 `Wrap` 的东西）；`KindNotFound` 之类由服务自己写的 message 原样保留，不牺牲调试体验。trace_id 与 reason 始终保留。

### 5. 聚合错误一等公民

`Violations` 映射到 `errdetails.BadRequest.FieldViolations`，全部条目过线。`Join` **不**承担聚合语义 —— 与其静默丢数据，不如让聚合有显式类型。

## 被否决的方案

**保留 HTTP code，仅在 metadata 加原始码作为逃生舱。** 改动最小，能缓解 422 塌缩。否决理由：不解决层次倒置，且逃生舱是「知道的人才会用」的隐性契约，新写的代码仍会踩塌缩。

**保留 `Error` 内嵌 proto message。** 否决理由：线格式反向决定内存模型。实现中这条得到额外印证 —— 仓库 json codec 是 `encoding/json` 而非 protojson，内嵌时错误体 JSON 会输出 `{"kind":6}`（枚举序号，对客户端无意义且重排即静默改变语义）。放弃内嵌后可显式实现 `MarshalJSON` 输出 `{"kind":"NOT_FOUND"}`。**这个收益在决策时未预见到。**

**默认 `PolicyVerbose`（一切由使用者显式开启脱敏）。** 否决理由：默认值决定绝大多数部署的行为，把「不泄漏」设为需要主动开启的选项，等于让忘记配置的人承担后果。

**把因果链摘要进 `DebugInfo` 随错误发送。** 此方案一度实现（默认关闭 + 20 帧上限），随后移除。否决理由有三：(1) 它与 trace 解决同一问题，而 trace 后端持有同一信息的更完整形态（未截断、有结构、含周边 span 与时序），`DebugInfo` 只是其劣化副本；(2) 它为「cause 不出网」这个已验证的安全属性开了一个口子，安全性从此依赖一个布尔开关不被设错；(3) 8KB trailer 上限意味着这条通道**本就不适合**承载诊断信息 —— 实测 200 帧会直接失败 RPC 而非降级。一个需要「小心别放太多」的通道不该是诊断的正式路径。

同一个问题应当只有一个答案：**跨进程因果关系靠 trace_id。** 配套地，tracing middleware 现在会自动为出站错误打上 trace ID，否则「靠 trace」只是空话。

## Consequences

**破坏性变更。** 删除 10 个构造器 + 10 个判定函数；proto `Status` 字段全换（`code`→`kind`，新增 `domain`/`trace_id`/`violations`）；`default_code`/`code` 扩展改为 `default_kind`/`kind`。需配套迁移指南与 `COMPATIBILITY.md` 更新。

**对跨公网调用方的直接影响：** 在不可靠链路两端，`Domain` 让两侧同名 reason 可区分，无需靠约定避让。（同段原先还声称 `Kind.Retryable()` 让重试语义成为显式契约，该论断已由 [ADR-0008](./0008-retryability-needs-delivery-evidence.md) 推翻。）

**已知未解决：** 跨进程因果链不可见是**刻意的**（安全优先），代价是排查依赖 trace 基础设施。若 trace 未接入，远端错误只有 kind + reason 可依。

**顺带修正**（非本 ADR 核心，但同批落地）：circuitbreaker 原先把调用方错误计入熔断，现显式排除；retry 的 `DefaultRetryable` 刻意窄于 `IsRetryable`（排除限流与冲突，无退避重试限流会加剧过载）；jwt 10 个 sentinel 原共用一个 reason，现各自独立。

**测试卫生：** 同批为 `contrib/polaris` 的 5 个集成测试加了 `FORGE_POLARIS_INTEGRATION` 环境变量门控。它们此前**长期失败**（需本地 :8090 Polaris），把缺失依赖报告成代码故障，且长红的套件会失去被阅读的价值。
