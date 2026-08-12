# ADR-0007: `errors` 是零依赖叶子包，投影归各传输所有

**状态：** Accepted
**日期：** 2026-08-09
**关系：** 细化 [ADR-0006](./0006-errors-kind-over-transport-code.md) 的「Kind 是唯一真相、各传输单向投影」原则。2026-08-09 的规范复审进一步确认：零第三方依赖只是必要条件，核心还必须不拥有 JSON/Protobuf wire shape，不用 Kind 猜测公开性，也不能依赖全局 codec 注册。当前为规范已接受、实现重新对齐中

## Context

ADR-0006 确立了三层分离：领域语义（`Kind`）、传输编码、诊断信息。但实现时投影全部留在了 `errors` 包内，导致该包同时持有 HTTP、gRPC、protobuf 三种线格式知识。

2026-08-09 实测暴露了后果，**全部本机验证** `[一手数据]`：

```
只 import forge/errors 的最小程序 → 10 MB，31 个 protobuf 包，8 个 grpc 包
对照：bare hello 2.3 MB
```

即**任何使用 Forge 错误模型的程序，无论是否用 HTTP 或 gRPC，都无条件付出约 7.7 MB**。一个只想用错误分类的库也要扛整套 protobuf 反射。

### 成本归属的实测拆解

初判「gRPC 拖进 5 MB」是错的，实测推翻：

| 组合 | 体积 | 边际 |
| --- | --- | --- |
| bare hello | 2.3 MB | — |
| + `grpc/codes` | 2.9 MB | +0.6 |
| + `grpc/status`（无 protobuf） | 7.3 MB | +5.0 |
| net/http + protobuf（无 grpc） | 9.3 MB | — |
| 同上 + `grpc/status` + `codes` | 9.9 MB | **+0.6** |

**那 5 MB 是 protobuf reflection，不是 gRPC。** `grpc/status` 昂贵仅因它内含 `google.rpc.Status` proto 消息。protobuf 已在场时，gRPC 的边际成本只有 0.6 MB。

`grpc/codes` 那 0.6 MB 也非码表本身：`codes.Code` 是 `uint32` 枚举，但 import 它会连带 `grpclog → connectivity → serviceconfig` —— 日志系统与连接状态机，纯 REST 应用永不执行。

### 分层缺陷（比体积更根本）

决策前，`errors` 同时暴露 `Kind.HTTPStatus()` 与 `Kind.GRPCCode()`。一个领域包凭什么知道两种传输的词汇表？最直白的证据是包内定义了 `StatusClientClosed = 499` —— **nginx 的惯例被写进了领域错误包**。

先前为保留 `HTTPStatus()` 给出的理由是「int 不引入依赖」。该理由**不成立**：它用依赖成本回答归属问题，照此逻辑 `Kind.MQTTReasonCode()`（也只是一个 byte）同样该留。成本为零不等于该属于此处。

## Decision

**`errors` 的 import 列表 MUST 只有标准库，且 MUST NOT 实现任何传输 wire format。** `Kind` 回答「这是什么错」；「它在你的协议里叫什么」由协议自己回答。只有 stdlib import 但在核心实现 `MarshalJSON`，依旧是所有权错误，不算完成本 ADR。

三层投影全部外移：

| 移出 | 去向 | 体积影响 |
| --- | --- | --- |
| Protobuf `Status` envelope | 删除；gRPC 使用原生 status details，HTTP 使用 Problem Details | −5 MB |
| `GRPCStatus` / `Kind.GRPCCode` / `kindFromGRPCCode` | `transport/grpc` | −0.6 MB |
| `Kind.HTTPStatus` / `FromHTTPStatus` / `StatusClientClosed` | `transport/http` | 0（分层正确性） |
| JSON marshal / unmarshal | `transport/http` 的独立 Problem DTO | 0（建立真实信息边界） |
| HTTP Protobuf transcoding | `transport/http/transcoding` | 纯 HTTP closure 移除 Protobuf |

外移后的形态：

```
errors                       Kind · Error · Public · Violations      无 wire 实现
transport/http               Problem Details · JSON · SSE/WS
transport/http/transcoding   ProtoJSON · path/query · HttpBody · stream field
transport/grpc               code/status · ErrorInfo · BadRequest · RequestInfo(trace)
api/errors/v1                Kind annotations；无 Status envelope
```

HTTP 投影同样外移，尽管它省不了一个字节。**判据是归属，不是成本** —— 这是对 ADR-0006 原则的贯彻，也修正了先前那条不成立的理由。

### 信息边界

`Error` 的 message、metadata 与 violations 是调用方显式声明的公开契约数据；cause 与任意 wrapper 只在本地存在。传输只能消费一个无 cause、拥有独立 map/slice 的 `errors.Public` 快照（`PublicOf` / `FromPublic`）。

**2026-08-10 已实现，并实测了被取代的 policy 模型为何不成立** `[一手数据]`：

```
KindNotFound + metadata{"dsn":"postgres://user:hunter2@10.0.0.1/db"}  → 原样出网
violation description 携带 "pq: duplicate key value violates ..."      → 原样出网
PolicySafe = PolicyVerbose（三者均为可重赋值的导出变量）                 → 脱敏静默失效
```

policy 只读 Kind 猜测 message 来源，对 metadata 与 violation **完全没有观察能力**；且任何依赖库都能重赋值那三个全局变量。`PolicySafe` / `PolicyStrict` / `PolicyVerbose` 与 `Project` 已全部删除。需要不同外部表示的应用提供自定义 transport encoder，而不是让核心猜测。

**身份必须成对。** `FromPublic` 只在 domain 与 reason 同时存在时保留身份；半个身份会让无关失败比较相等，故按匿名处理（Kind 仍保留）。

trace 通过一个透明 Go wrapper 附着，MUST 保留完整 `Unwrap` 链，也 MUST 覆盖普通 Go error。gRPC 使用 Forge 自有 `TraceInfo` detail；trace ID 不是 request ID，不得借用 `google.rpc.RequestInfo.request_id`。

### 实现机制：seam 而非条件编译

`transport/http` 定义一个 `Schema` 结构（`transport/http/schema.go`），字段为函数值；`transcoding` 在 `init()` 中经 `RegisterSchema` 装入。核心的每个 schema 相关分支先问 seam「你处理了吗」，未装载则走值形态的回退路径。

生成代码 import `transcoding` 并引用其 `SupportPackageIsVersion1` —— **引用即链接**，应用无需显式声明。链接器按包裁剪：不 import 就不付。

选 seam 而非 build tag 的理由已在「被否决的方案」列出；此处补一条实现期发现：**`transcoding` import `transport/http`，故核心的 internal test 无法反向 import 它**（import cycle）。需要 schema 行为的测试放 `package http_test` 外部测试包。

### HTTP reader 规则

状态行权威，body 只作细化。三条规则于 2026-08-10 落地，每条都对应一个**实测缺陷**而非理论风险 `[一手数据]`：

| 规则 | 修复前的实测行为 |
| --- | --- |
| 仅解析 `application/problem+json`（允许参数） | `text/html` 的 nginx 错误页被当作 Forge 错误解析 |
| body 上限 `MaxProblemBytes` = 64 KiB | 8 MiB 的 message 被整个接受 |
| kind 与状态行矛盾则作废整个 body | 503 + 陈旧的 `NOT_FOUND` body **匹配上 NotFound sentinel**，调用方停止重试瞬时故障 |

第三条最关键：陈旧中间层可以用新状态码回放旧 body，信 body 会把可重试故障判成不可重试。

未知 kind **不**作废 body —— 对端可能运行更新版本，故保留其余合法公开字段，仅分类回退到状态行。

### 不采用 RFC 9457 的散文成员

错误响应使用 `application/problem+json` **媒体类型**（代理与浏览器识别它），但不发送 `type` / `title` / `detail` / `status`。

`title` 与 `detail` 是 RFC 9457 为「没有机读身份」的场景准备的散文；本契约已有 `kind`（分类）+ `reason`（身份）+ `message`（给人读），再加两个会形成两套重叠词汇，读者需要猜哪个权威。`type` 恒为 `about:blank`，零信息量。

`status` 被否决的理由更强：在 body 内复制状态行，正好制造上表第三条要拒绝的那种矛盾。状态行已经权威，第二份副本只能冗余地一致、或有害地不一致。

### HTTP 与 codec 边界

所有 HTTP 错误固定为 RFC 9457 `application/problem+json`，不参与成功响应的内容协商。空 body、错误 content type、畸形/超限 JSON 与 status/body 冲突均回退到实际 HTTP status；未来未知 Kind 使用 status 分类但保留其余合法公开字段。这样既修复当前空 body 被解成零值 `Error` 的回归，也消除 JSON 与 ProtoJSON 分别输出 `NOT_FOUND` / `KIND_NOT_FOUND` 的双 wire contract。

HTTP client/server 各自拥有不可变 codec 集。`transport` 不 blank-import codec，codec 包不靠 `init` 修改进程全局。生成的 `_http.pb.go` 显式 import `transport/http/transcoding`；后者完整拥有 `proto.Message`、ProtoJSON、binary Protobuf、Google path/query、`HttpBody` 和 stream-field binding。只搬一个 `MessageDecoder` 不构成真实包边界。

### 实施状态（2026-08-09）

第一阶段完成，实测 `[一手数据]`：`go list -deps ./errors` 为 `protobuf=0 grpc=0`；`go list -deps ./transport/http` 为 `protobuf=36 grpc=0`。

**gRPC 依赖已彻底移除，protobuf 未移除** —— 后者的来源不是 `errors`，而是 `transport/transport.go` 的 blank import 加上 `path.go`/`protojson.go`/`codec.go`/`stream.go` 自身对 proto 反射的需求（AIP 路径模板要反射消息结构）。这是第二阶段的范围。

**实现期发现三处行为回归，均为集成测试捕获，非编译错误** `[一手数据]`：

1. 客户端错误转换放在 middleware 链**外**，retry 看到的仍是原始 status，`KindOf` 认不出 → **重试静默失效**；
2. 客户端转换用 Forge 错误**替换**原错误，`status.Code(err)` 失效 → otel A66 指标测试失败；现改为 `statusError` 同时承载两种视图；
3. HTTP `decodeError` 对空 body 无条件返回成功 → 无 body 的 503（代理常见响应）解成零值错误，`KindUnavailable` 退化为 `KindUnknown` → **重试再次失效**。

三者都不会编译失败。**「拆包正确」不等于「行为不变」，验收 MUST 含跨传输集成测试。**

### 连带效果：新传输不必改核心

现在每支持一种新传输，就要往 `errors` 加一个方法。外移后，MQTT / CoAP / 自定义协议各自定义映射即可，与 HTTP、gRPC 平级。这是判据一（深接口）与判据三（替换测试）的直接兑现。

HTTP 侧还额外获得表达空间：`KindAlreadyExists` 与 `KindConflict` 当前都映射 409，这个取舍归 HTTP 层后可以提供选项（部分 API 期望 422），塞在 `errors` 里只能一刀切。

## 被否决的方案

**保留 `GRPCStatus() *status.Status` 在 `errors`（仅移走 protobuf）。** 一度倾向此方案，理由是该方法实现的是 grpc-go 的公开扩展点（按精确签名做类型断言），移走会失去生态互操作 —— 与 `transport/http/stream.go` 当初「为省事内嵌 gRPC 接口」性质不同，后者是耦合，前者是协议实现。

最终否决：只移 protobuf 而留 `status`，`status` 仍拖 protobuf，等于白移。互操作性问题**不消失但换位置** —— 由 `transport/grpc` 的拦截器承担（该处已存在 `projectError`，所有出站错误流经它，是自然承接点而非硬塞）。

**保留 `Kind.HTTPStatus()`（只移 gRPC 与 protobuf）。** 否决理由见 Context：成本为零不构成归属理由。保留它会让「各传输单向投影」这条原则只对昂贵的依赖生效，变成成本驱动而非设计驱动。

**用 build tag 或运行时开关实现可选。** 否决：build tag 造成两套语义与两套测试矩阵；运行时开关根本不触发链接器裁剪。**Go 中唯一有效的可选机制是包边界。**

## Consequences

**破坏性变更。** `Kind.HTTPStatus`、`Kind.GRPCCode`、`Error.GRPCStatus`、`Error.ToProto`、`FromProto`、`FromHTTPStatus`、`StatusClientClosed` 全部换包。实测外部调用点仅 2 处，均在 `transport/http` 内 —— 迁移面小于表面。

**行为变化，非纯搬迁：`errors.KindOf(err)` 不再识别任何外部错误。** `errors` 不认识 gRPC status 或 HTTP response，识别职责整体移交传输层。gRPC 客户端必须在 unary、stream setup、`SendMsg` 与 `RecvMsg` 的 middleware 之前还原 Forge error，并保留原 `GRPCStatus`。

**`Error.Is` 比较 domain + reason，MUST NOT 比较 Kind。** 复审期曾提议改为 Kind + domain + reason，经实测否决 `[一手数据]`：

```
服务把某错误从 KindNotFound 重分类为 KindFailedPrecondition，reason/domain 不变
  当前语义： Is(v2, v1) = true    —— 客户端匹配继续有效
  加入 Kind： Is(v2, v1) = false  —— 所有客户端静默失效
```

Kind 是**可变的分类**（HTTP 409 vs 422 之争即例），domain+reason 是**稳定的身份**。把可变量纳入匹配，等于让每次分类调整都成为破坏性变更 —— 这与 ADR-0006 排除 message 的理由同构：会变的东西不参与身份判定。

提议的动机是「HTTP/gRPC 状态与 identity 冲突时误匹配 sentinel」。该场景实测不存在：仅凭状态码分类的远端错误 identity 为空，与任何 sentinel 的 `Is` 已经返回 false。匿名错误之间按 Kind 相等（`Is(Of(KindNotFound), Of(KindNotFound)) = true`），这是刻意的 —— 它们本就没有身份可比。

**重试性的归属待定，当前保留 `Kind.Retryable`。** 复审期提议删除，理由是只有传输能证明「请求未发送」。该理由对**自动重试**成立，但 `Kind.Retryable` 描述的是「这类失败是否值得重试」，是分类属性而非执行判断 —— `middleware/retry` 的 `DefaultRetryable` 已经刻意窄于它（排除限流与冲突，并要求幂等声明），两者是不同层次的问题。

删除它会让调用方退回手写状态码列表，正是 ADR-0006 要消除的东西。归属问题另立条目讨论，MUST NOT 在未定案时半删。

**gRPC detail 不得随 cause 深度增长。** 复审期曾提议 4096-byte status budget 加 6144-byte trailer budget，因**缺乏实测依据**未采纳：两个数字的推导过程未给出，而 trailer 上限本身依 server 配置可变（grpc-go 默认 8 KiB）。

已落地的是可检验的不变量 `[一手数据]`：`TestWireSizeIsIndependentOfCauseDepth` 断言 500 层深的 cause 链与浅层链产生**逐字节相同**的 status，且编码后远低于 8 KiB。这直接消除了「无界 detail 撑爆 trailer」这一真实风险 —— 因为 cause 根本不过线（见上文信息边界）。

若将来出现 violations 或 metadata 无界增长的实例，再依实测定预算；在那之前，加两个来源不明的魔数只会制造无法验证的约束。

**收益（第一阶段实测，第二阶段仍为目标）：**

| 场景 | 决策前 | 当前 | 第二阶段目标 |
| --- | --- | --- | --- |
| 纯 REST / 嵌入式 | 16 MB | 约 16 MB，`protobuf=36 grpc=0` | ~2.6 MB，`protobuf=0 grpc=0` |
| 只用 `errors` 的库 | 10 MB | 约 2.6 MB，`protobuf=0 grpc=0` | 已达到依赖目标 |
| gRPC / 生成代码服务 | 16 MB | 约 16 MB | 16 MB |

第三行是可选化该有的形状：**用得着的人一分不省，也一分不多付。**

`errors` 因此成为任何 Go 项目都可用的零依赖错误库，不限于 Forge 使用者。这一价值高于节省的字节数。

**验收标准（无法糊弄）。** 分两阶段，第一阶段已达成：

*第一阶段 —— errors 成为叶子包（2026-08-09 已完成）*

1. [x] `errors` 只含语义值与快照：无 JSON/Protobuf 实现，无 policy，import 列表只含标准库 —— 由 `TestPackageImportsStandardLibraryOnly` 守卫，任何新增第三方 import 直接测试失败；
2. [x] 只用 `errors` 的库：`go list -deps` 中 protobuf 与 grpc 包数**均为 0**（10 MB → 2.6 MB）；
3. [x] gRPC 服务行为零变化，含 grpc-go 经 `GRPCStatus` 的自动识别；
4. [x] gRPC unary 与 stream 在 middleware **之前**恢复 Forge error —— 否则 retry/breaker 按 Kind 的判定失效（实现期真实回归，见下）；
5. [x] root、`api`、`cmd` 与全部 22 个 `contrib` module 分别 build/vet/test 通过。

*第二阶段 —— 纯 HTTP 闭包移除 Protobuf（2026-08-09 已完成）*

6. [x] `transport/http` 的 ProtoJSON、path/query binding、`HttpBody`、stream field 移入 `transport/http/transcoding`；
7. [x] `transport` 不再 blank-import proto codec —— 改由 `transcoding` 注册，因为它才是需要它们的一方；
8. [x] 纯 REST 应用 `go list -deps` 中 protobuf 与 grpc 包数**均为 0**（16 MB → 11 MB）。

实测 `[一手数据]`：

| 应用形态 | 二进制 | protobuf | grpc |
| --- | --- | --- | --- |
| 纯 REST（`transport/http`，无生成代码） | **11 MB** | **0** | **0** |
| 只用 `errors` 的库 | 2.6 MB | 0 | 0 |
| 生成代码 + gRPC（import `transcoding`） | 18 MB | 40 | 75 |

第三行是可选化该有的形状 —— **用得着的人一分不省，也一分不多付**。

*第三阶段 —— 信息边界与 reader 规则（2026-08-10 已完成）*

9. [x] `Public` / `PublicOf` / `FromPublic` 落地；三个 policy 与 `Project` 删除；
10. [x] 身份成对校验：半个身份按匿名处理；
11. [x] HTTP reader 三条规则（媒体类型、64 KiB 上限、状态冲突作废）各配回归测试；
12. [x] 错误响应固定 `application/problem+json`，不参与内容协商；SSE / WebSocket 错误帧共用同一文档。

**`Define` 收敛为 `MustDefine`（2026-08-09）。** sentinel 是 `init()` 期构建的包级状态，此时没有调用方可以处理错误，非法声明属编程错误而非运行时状况。曾实现「`Define` 返回 error + `MustDefine` 包装」的双 API，迁移全部 14 个调用点后实测**零处使用返回 error 的形式** —— 遂删除，只留 panic 形式。先例：`encoding.RegisterCodec` 对 nil/无名 codec 同样 panic。

校验内容：domain 与 reason 非空、reason 为 `SCREAMING_SNAKE_CASE`。**校验在 `errors` 包而非仅在 generator**，因为手写 sentinel 会绕过 generator。无身份的 sentinel 若流到线上，`Is` 会仅凭 Kind 与无关错误匹配。

**已知风险：** 若 proto 判断放错位置，会得到「包分开了、核心仍耦合」的假拆分。

2026-08-09 实现期已踩过一次并回退 `[一手数据]`：曾让 `encoding/proto` 实现 `SchemaValue`/`DecodeError` 能力接口，结果 **9 个 contrib module 编译失败** —— `encoding/proto` 因此依赖 `api` module，每个注册 codec 的模块都被迫承担错误契约依赖。教训是**能力接口 MUST NOT 让底层去认识上层类型**；该判断现位于 `transport/http`，它本就同时依赖两者。

判断真假拆分的唯一标准是验收条件 4。
