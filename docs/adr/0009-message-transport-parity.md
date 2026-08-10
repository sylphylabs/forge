# ADR-0009: 消息传输与 HTTP/gRPC 对齐

**状态：** Accepted
**日期：** 2026-08-10
**关系：** 兑现 [ADR-0002](./0002-transporter-describes-transport-commonality.md) 的「`Transporter` 描述传输共性」

## Context

`transport/message` 定义了自己的 `Handler` 与 `Middleware`，签名与 `middleware` 包的 `UnaryMiddleware` / `StreamMiddleware` 三者互不兼容：

```go
// transport/message
type Handler    func(context.Context, string, *Message) error
type Middleware func(Handler) Handler

// middleware（HTTP 与 gRPC 用）
type UnaryHandler  func(ctx context.Context, req any) (any, error)
type StreamHandler func(request any, stream ServerStream) error
```

**后果是消息消费者拿不到框架的中间件** `[一手数据]`，2026-08-10 实测：

| 传输 | 可用中间件 |
| --- | --- |
| HTTP / gRPC | recovery、logging、ratelimit、timeout、circuitbreaker、validate、selector、retry、metadata、governance —— **10 个** |
| 消息消费 | 仅 `contrib/otel/message` 一个 tracing —— **1 个** |

**worker 因此没有 recovery**：一条畸形消息 panic 会带走整个消费进程。而这恰是消息场景最需要的防护 —— 投递内容由外部系统决定，不由本服务控制。

### 不一致的完整面貌

中间件只是表征。三个传输在**三个层面**都不对齐：

| | gRPC | HTTP | 消息 |
| --- | --- | --- | --- |
| 契约声明在 proto | ✅ | ✅ | ❌ topic 是散落的字符串 |
| 生成 `RegisterXServer` | ✅ | ✅ | ❌ 手写 `srv.Handle` + 手动 `proto.Unmarshal` |
| 可用中间件 | 10 | 10 | 1 |

第三行是前两行的**后果**：没有统一的 handler 形状，中间件就无从统一。分开处理会得到半成品 —— 只收敛签名则契约仍在 proto 之外；只做生成器则生成出的 handler 依旧用不了中间件。

### 三个 broker adapter 提供的实测约束

2026-08-10 交付 Kafka / MQTT 5 / RabbitMQ 三个 adapter，其报告暴露了一个结构性事实 `[一手数据]`：

| Broker | handler 返回 error 时可用的手段 |
| --- | --- |
| **Kafka** | **无 nack** —— 只能不推进 offset |
| **MQTT 5** | **无 nack** —— 只能不发 PUBACK；QoS 0 时完全无效 |
| NATS JetStream | nack / term |
| RabbitMQ | nack + requeue 可选 |

这证明「ack 决策交给 adapter」的原有设计是对的：核心若假定所有 broker 都能 nack，Kafka 与 MQTT 就无法实现。但也暴露代价 —— **同一份 handler 在不同 broker 上的可靠性完全不同，而契约无处表达这一点**。

## Decision

**消息传输与 HTTP/gRPC 在三个层面对齐。** 三项互相依赖，MUST 一并落地。

### 1. `Handler` 收敛为 `middleware.UnaryHandler`

```go
// 之前
type Handler func(context.Context, string, *Message) error
// 之后：消息 handler 就是一个 UnaryHandler，req 为 *Message
```

`destination` 不再是一等参数，改由 `transport.FromServerContext(ctx).Operation()` 读取。

**这不是牺牲清晰换统一。** 实测确认 `message.Transport` 已经实现 `Transporter`，且 `Operation()` 返回的正是**实际投递的 destination**（通配符订阅下与订阅模式不同）`[一手数据]`：

```go
// transport/message/transport.go:34
// Operation returns the concrete destination that delivered the message, which
func (tr *Transport) Operation() string { return tr.destination }
```

HTTP 与 gRPC 的 middleware 本来就从 context 取 operation。收敛后**三个传输取路由信息的方式一致**，比现状更一致，而非更少信息。

`UnaryHandler` 返回 `(any, error)` 而消息无返回值。实测确认 `logging` / `ratelimit` / `recovery` 等**只透传 `reply` 而不检查它** `[一手数据]`，故消息 handler 返回 `(nil, err)` 可直接复用全部 10 个中间件。

`message.Middleware` 与其 `Chain` 删除；`Server` 接受 `middleware.UnaryMiddleware`。

### 2. `protoc-gen-go-message`：契约进 proto

与既有三个生成器同构 —— 读 `MethodOptions` 扩展决定是否生成，机制与 `protoc-gen-go-http` 读 `google.api.http` 完全一致 `[一手数据]`：

```proto
service OrderEvents {
  rpc OnOrderCreated(OrderCreated) returns (google.protobuf.Empty) {
    option (sylphy.message.v1.subscribe) = { destination: "order.created" };
  }
}
```

生成与另外两个传输同形的注册函数：

```go
type OrderEventsMessageServer interface {
	OnOrderCreated(context.Context, *OrderCreated) error
}

func RegisterOrderEventsMessageServer(s *message.Server, srv OrderEventsMessageServer)
```

**`destination` MUST 可在注册期覆盖**，因为同一契约在不同环境的 topic 前缀不同。proto 里的值是默认而非硬编码。

`returns (google.protobuf.Empty)` 是 proto `rpc` 语法的形式要求，非语义 —— 生成的接口方法只返回 `error`。

### 3. ack 能力用可选接口表达，不用 option

差异分两类，处理方式不同：

| 类型 | 例子 | 表达方式 |
| --- | --- | --- |
| **策略差异** | 失败时重投递还是丢弃 | ✅ 构造 option（三个 adapter 已各有 7–15 个） |
| **能力差异** | 有无 nack | ❌ option 变不出协议不存在的能力 |

后者用可选能力接口，沿用 T8.4 已确立的原子化模式：

```go
// 仅由支持负确认的 adapter 实现。
type Nacker interface {
	Nack(ctx context.Context, msg *Message, requeue bool) error
}
```

Kafka 与 MQTT 不实现它，消费方类型断言。**核心因此不必假装所有 broker 语义相同**，而调用方能在编译期发现自己依赖的能力是否存在。

## 被否决的方案

**保留独立 `Handler` 签名，为消息各写一份中间件。** 一度倾向此案，理由是消息的 ack 语义与请求/响应不同（`recovery` 对 unary 是「返回 500」，对消息是「nack 还是丢弃」）。

否决：实测表明中间件**不检查 `reply`**，语义差异实际落在 ack 决策上，而那已由第 3 项的能力接口承接。各写一份会让每个新中间件都要实现两遍，且两份必然漂移 —— 这正是 T1 消除「四套互不兼容中间件」时的原始动机。

**把 destination 保留为一等参数，只统一 `Chain` / `Compose` 组合器。** plan 中的原倾向。否决：组合器统一而签名不统一，等于中间件仍不能复用，只解决了表面。且实测证明 destination 经 `Transporter` 已经可得，保留参数是冗余而非清晰。

**用 option 表达 ack 差异。** 否决：Kafka 的 ack 相关 option 数为 **0** `[一手数据]`，因为协议没有 nack。option 只能配置存在的行为。

**不做生成器，让应用手写 `srv.Handle`。** 否决：那正是第 1 行不一致的来源 —— topic 成为散落的字符串常量，跨语言消费者无从共享契约，且每个 handler 都要手写 `proto.Unmarshal` 样板。

## Consequences

**破坏性变更。** `message.Handler`、`message.Middleware`、`message.Chain`、`Server.Handle` 的签名全部改变；四个 adapter（kafka / mqtt / nats / rabbitmq）的 `Subscribe` 需适配。`contrib/otel/message` 的 `Consumer` 中间件改为 `UnaryMiddleware`。

**收益：消息消费者立即获得 10 个中间件**，其中 recovery 修复「一条畸形消息带走整个 worker」这一真实缺陷。

**新增 `protoc-gen-go-message`** 与 `api/proto/sylphy/message/v1/` 注解 schema，需纳入 `buf.gen.yaml` 与 `modules.json`。

**已知未决：** `Handler` 仍拿不到投递计数与 `Redelivered` 标志（RabbitMQ adapter 报告），故毒消息限制仍是队列级策略。是否将其纳入契约留待有实例需求时再议 —— 现在纳入等于为三个 broker 中只有一个支持的能力扩宽核心。

**验收标准：**

1. [x] `middleware/` 下 10 个中间件全部可用于消息消费，各有测试 —— `transport/message/middleware_parity_test.go`；
2. [x] `transport/message` 不再导出自有 `Middleware` / `Chain`；
3. [x] 生成的 `RegisterXMessageServer` 与 HTTP/gRPC 同形，destination 可覆盖 —— `cmd/protoc-gen-go-message`，按 operation 或 prefix 覆盖，显式覆盖优先于 prefix；
4. [~] **已由 [ADR-0010](./0010-destination-is-adapter-defined.md) 推翻。** `Nacker` 已删除：全仓零实现、零类型断言，而 RabbitMQ 与 NATS JetStream 已用 `WithErrorClassifier` 实现同一能力且严格更优（在 adapter 内部、delivery settle 处运行，能表达 `NakWithDelay` / `Term`）。能力仍由 adapter 是否实现表达，只是不再有这个未被使用的接口；
5. [x] 四个 adapter 与 root、api、cmd 分别 build / vet / test 通过 `[2026-08-10 实测]`。
