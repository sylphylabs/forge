# ADR-0010: destination 的语义归 adapter，通配符不做统一抽象

**状态：** Accepted
**日期：** 2026-08-10
**关系：** 收敛 [ADR-0009](./0009-message-transport-parity.md) 遗留的第三行不一致（「topic 是散落的字符串」）；**部分 supersede ADR-0009 第 3 项** —— 推翻其 `Nacker` 能力接口，理由见「被否决的方案」末条。ADR-0009 的核心（`Handler` 收敛为 `UnaryHandler`、契约进 proto、能力差异不用 option 表达）**继续有效**。

## Context

ADR-0009 把 `destination` 从 `Handler` 的一等参数移进 context，并记下第三行不一致待偿：契约进 proto 后，`destination` 成为 proto option 里的一个字符串。**但那个字符串在四个 adapter 里根本不是同一种东西** `[一手数据，2026-08-10 逐个 adapter 核实]`：

| Adapter | `Subscribe(ctx, destination, h)` 里 destination 的去向 | 证据 |
| --- | --- | --- |
| **Kafka** | 字面 topic 名，直接给 SDK | `kafka.go:316` `kgo.ConsumeTopics(topic)`，全仓无 `ConsumeRegex` |
| **MQTT** | topic filter，交给 SDK **并**注册进 adapter 自建路由表 | `mqtt.go:432`、`:423` |
| **NATS core** | subject，纯透传 | `nats.go:239` `conn.Subscribe` |
| **JetStream** | **配置 map 查找键**，从不发给 broker | `jetstream.go:269` `s.bindings[destination]` → `{Stream, Consumer}` |
| **RabbitMQ** | **配置 map 查找键**，消费用 `binding.Queue.Name` | `rabbitmq.go:468`、`:650` |

后两个是决定性的。它们的通配符行为是**外部预置拓扑**的属性 —— JetStream 的 consumer `FilterSubject`、RabbitMQ 的 queue `BindingKeys` —— adapter 既看不见也无法校验。`jetstream.go:210` 自陈「It never creates or updates streams or consumers」。

于是「这个 broker 支不支持通配符」对这两个 adapter **不是一个能诚实回答的问题**。

### 通配符语法的实际分歧

| Broker | 分隔符 | 单级 | 多级 | 位置约束 | 匹配方 |
| --- | --- | --- | --- | --- | --- |
| MQTT | `/` | `+` 任意位置 | `#` **仅末尾** | `#` 非末尾时静默不匹配 | **adapter 自己**（`router.go:162-182`） |
| NATS core | `.` | `*` | `>` | `>` 仅末尾 | broker |
| RabbitMQ | `.` | `*` | `#` **可在中间** | 无 | broker（经 `BindingKeys`） |
| Kafka | 无层级语义 | **无** | **无** | — | 不适用 |

MQTT 自建匹配器的原因不是 paho 不够用，而是 `router.go:26-28` 写明的「acknowledgement here depends on the aggregate handler outcome」。

**通配符测试覆盖率 `[一手数据]`：只有 MQTT 有。** `router_test.go:13-39` 13 个用例 + `mqtt_test.go:234` 通配符下投递具体 topic。NATS / JetStream / Kafka **零通配符测试**；RabbitMQ 有具体 routing key 断言但**没有一个测试真的用 `*` / `#` 绑定队列**。

### 静默失败是真实的，不是推演

用户传 `orders.*` 时四个 adapter 的行为**分成两类且不一致**：

| Adapter | 结果 |
| --- | --- |
| NATS core | 正常工作 |
| JetStream / RabbitMQ | **报错** `ErrBindingNotFound` |
| MQTT | `.` 不是它的分隔符 → `orders.*` 是**单个字面层级** → 静默零投递 |
| Kafka | broker 上无此 topic → 静默零投递 |

**外部证据：这不是假想的失败模式。** go-micro 的 `Broker` 接口是同一形状（`topic string` 原样透传、零校验、零能力申报），其 memory broker 订阅 `foo.*` 后发布 `foo.bar` `[一手数据，编译 go-micro v6 实测]`：

```
subscribe err: <nil>
publish err: <nil>  handler invocations: 0
```

同一份代码换 NATS 就能收到。且在那个接口下**不可修复** —— `Publish` 没有「无订阅者匹配」的概念，`Subscribe` 没有「本 broker 兑现不了你的 pattern」的概念。

## Decision

### 1. destination 的语义归 adapter，核心不定义 topic 语法

核心**不**引入 `TopicPattern` 类型、不定义统一分隔符、不定义统一通配符语法、不做跨 broker 转写。

**外部先例一致支持这一条。** 调研六个同类抽象 `[一手数据：读源码/规范原文]`，**没有一个统一了通配符语法**：

| 项目 | 通配符处理 |
| --- | --- |
| Watermill | 无此概念；`docs/` 全文零提及；零校验 |
| gocloud.dev | 全仓库零次 "wildcard"；NATS 意外透传 |
| Spring Cloud Stream | 核心零通配符；Rabbit 走 `bindingRoutingKey`、Kafka 走 `destinationIsPattern`，**语义不兼容** |
| go-micro | 3+ 套语法并存，零校验（见上，已实测静默失败） |
| AsyncAPI 3.0 | 规范沉默 |
| CloudEvents | 拒绝建模 destination |

两个多协议**规范**在多年标准化工作后仍对通配符沉默，这比任何单一实现的选择更有说服力。

### 2. destination 是逻辑名，物理地址可选 —— 对齐 AsyncAPI 3.0

AsyncAPI 在 2.x → 3.0 做过**与本 ADR 相同的重构**：2.6 的 channel map key 就是 topic（"Channels are also known as 'topics', 'routing keys', 'event types' or 'paths'"），3.0 把 key 改为纯逻辑标识符，物理地址移到可选的 `address` 字段：

> `address`: "…typically the 'topic name', 'routing key', 'event type', or 'path'. **When `null` or absent, it MUST be interpreted as unknown.** This is useful when the address is generated dynamically at runtime or can't be known upfront… **instead use bindings** to define them."

**因此 JetStream 与 RabbitMQ 把 destination 当查找键不是缺陷，而是规范明文祝福的 `address: null` 情形。** 两种模型都合法，MUST 在文档中区分：

- **wire address 型**（Kafka / MQTT / NATS core）：destination 即 broker 地址，通配符由 broker 或 adapter 的语法定义
- **logical name 型**（JetStream / RabbitMQ）：destination 经 adapter binding 表解析，通配符在 binding 里声明

### 3. adapter MUST 在注册期拒绝兑现不了的 destination

这是先例留下的空白，也是本 ADR 唯一新增的**约束**（而非文档要求）：

> **adapter MUST NOT 接受一个它必然收不到消息的订阅。** 能在注册期判定的，MUST 在 `Subscribe` 返回 error；MUST NOT 静默返回一个永不投递的 `Subscription`。

落到四个 adapter：

| Adapter | 现状 | 本 ADR 要求 |
| --- | --- | --- |
| JetStream / RabbitMQ | 已 `ErrBindingNotFound` | 维持 |
| MQTT | 透传 | 维持（`/` 分隔下 `+` `#` 由自建路由器兑现），文档写明分隔符 |
| **Kafka** | 静默接受 | **新增**：destination 含 `*` `#` `>` 时返回 error —— 字面匹配下这必然是用户误用了别的 broker 的语法 |

Kafka 的逃生舱是 adapter 自有的 `WithTopicRegex()`：它同时启用 `kgo.ConsumeRegex` 并解除上述校验。

**不能用 `WithSubscriberClientOptions(kgo.ConsumeRegex())` 当逃生舱** `[一手数据，实现时发现]`。本 ADR 起草时如此设想，但那条路径穿不过校验：真实正则如 `orders\..*` 含 `*`，会被拒。而 `kgo.Opt` 是不透明接口（`apply(*cfg)` 与 `cfg` 均未导出），adapter **无法检测** `ConsumeRegex()` 是否被透传进来。因此校验与豁免 MUST 由同一个 option 拥有，否则两者会不一致 —— 一个 adapter 无从知晓自己该不该校验。

校验字符集为 `*` `#` `>`，**不含** MQTT 的 `+`。`+` 在 Kafka topic 名里合法，且它仅在 filter 语境下才是通配符；那三个字符在 Kafka topic 名里本就非法，其出现即是「借用了别的 broker 的语法」的证据。

**明确拒绝 gocloud.dev 的相反选择。** 它把「open 必须成功、首次 Send 才失败」写进了 conformance 测试（`drivertest.go:194-195`，`TestNonExistentTopicSucceedsOnOpenButFailsOnSend`）。消费侧没有「首次 Send」这个时刻 —— 一个永不投递的订阅**不会**产生任何后续信号，所以延迟报错在这里等于永不报错。

### 4. 不设 `Capabilities()` 之类的能力查询 API

沿用仓库既有模式（`docs/design/transport-capability-interfaces.md:24-26`）：能力是**可选单方法接口 + 类型断言**，不是一张能力表。

调研中**没有一个先例提供运行时能力协商** —— gocloud.dev 仅有一个 `CanNack()` 且其否定路径是 **panic**（`pubsub.go:209-211`），go-micro 的唯一判别器 `String()` 甚至不唯一（kafka 与 segmentio 同名）。能力查询 API 是个反复被绕过的形状。

## 被否决的方案

**核心定义 `TopicPattern`（层级化 + 末尾多级通配符），adapter 声明能力，不支持则注册期硬失败。** 本 ADR 起草时的初始倾向。否决理由有二：

1. 它对**一半 adapter 无意义**。JetStream 与 RabbitMQ 的 destination 不是 wire address，一个 `TopicPattern` 传进去仍然只是 map key，抽象不产生任何约束力。
2. 它是 MQTT/NATS/RabbitMQ 的**最小公倍数而非最大公约数**：RabbitMQ 允许 `#` 在中间，MQTT 不允许；统一取交集会让 RabbitMQ 用户无法表达合法模式，取并集则 MQTT 静默不匹配。**而这个抽象的全部价值本来就是消除静默不匹配。**

**统一转写（把 `a.b.*` 按 broker 翻译成 `a/b/+`）。** 否决：Kafka 无通配符，转写目标不存在；且 Spring Cloud Stream 的实证显示，即使同为「通配符」，AMQP `#` 是 exchange 对每条消息 routing key 求值，Kafka 的 pattern 是对 **topic 名集合**求正则、默认 5 分钟元数据刷新才生效 —— **同一意图、不同命名空间、不同失败模式**，转写会制造更难查的错觉。

**保留 ADR-0009 的 `Nacker` 能力接口。** 否决 —— 这是本 ADR 对 ADR-0009 的部分 supersede。ADR-0009 第 3 项的推理正确（能力差异不能用 option 表达），但**结论落错了对象** `[一手数据]`：

- `Nacker` 全仓零实现、零类型断言（`grep -rn "Nacker" --include=*.go` 仅命中其自身定义与注释 3 行）
- RabbitMQ 与 JetStream **已用 `WithErrorClassifier` 实现了同一能力**，且真的调用 `delivery.Nack(false, requeue)`（`rabbitmq.go:620-622`）与 `Nak()`/`NakWithDelay()`/`Term()`（`jetstream.go:315-322`）

classifier 不是「用 option 表达能力」，而是「用 option 表达**策略**」—— 能力仍由 adapter 是否实现表达。ADR-0009 自己那张「策略差异 vs 能力差异」的表切分是对的，只是把 classifier 误判成了后者。

而且 classifier 严格更优：它在 adapter 内部、错误发生的那一刻运行，能保证 delivery 必被 settle（`rabbitmq.go:626-628` 的 default 分支注释：未知 disposition 也必须 nack，否则 prefetch slot 泄漏到 channel 关闭）。`Nacker` 是暴露给调用方的接口，调用方忘了调就会挂住 prefetch。且 `Nack(ctx, msg, requeue bool)` 表达不了 `NakWithDelay` 与 `Term` 的区别 —— JetStream 的 `Retry`/`Terminate` 与 RabbitMQ 的 `Drop`/`Requeue` 本就不是同一组语义。

**让物理名默认等于逻辑名（proto 里 destination 缺省取方法名）。** 否决，有直接反面证据：Spring Cloud Stream [issue #2967](https://github.com/spring-cloud/spring-cloud-stream/issues/2967) 中用户静默丢消息，维护者确认根因是 `destination` 默认取 binding name，**两者常常重合，用户建立错误心智模型，等名字分叉才炸**。`destination` MUST 显式写出。

## Consequences

**核心零变更。** 本 ADR 不新增任何核心类型或接口 —— 这是它相对初始方案的主要收益。

**Kafka adapter 新增注册期校验**，属破坏性变更：此前静默接受的含通配符 destination 现在返回 error。这正是意图 —— 那些订阅本来就收不到消息。

**四个 adapter README MUST 记录**：destination 属 wire address 型还是 logical name 型、分隔符、通配符语法与位置约束、以及通配符在何处声明。这是本 ADR 的主要交付物 —— 六个先例中有四个把这件事交给文档，而其中做得最差的（go-micro）产生了可复现的静默失败。

**补齐通配符测试**：NATS core 的 `*`/`>` 透传、RabbitMQ 用 `*`/`#` 绑定队列、Kafka 的新校验各需测试。MQTT 已有覆盖。

**已知未决：** 跨 broker 可移植的订阅契约仍不存在 —— 同一份 proto 在 MQTT 与 NATS 下需要不同的 destination 字符串。AsyncAPI 用 protocol-keyed `bindings` 解决（amqp 有 `is: queue|routingKey` 判别式联合，kafka 有 `topic` 作为 "if different from channel name" 的 address override）。**是否在 proto option 中引入 per-protocol bindings 留待有实例需求时再议** —— 现在引入等于为一个尚无人提出的部署形态扩宽 schema，与 ADR-0009 对 `Redelivered` 标志的处置一致。

**验收标准：**

1. [x] Kafka `Subscribe` 拒绝含 `*` / `#` / `>` 的 destination，有测试 —— `ErrWildcardSubscribe`，`TestSubscribeRejectsWildcardTopics`（4 子例）+ `TestSubscribeAcceptsLiteralTopics` + `TestTopicRegexLiftsTheWildcardRejection`；
2. [x] 四个 adapter README 各记录其 destination 模型、分隔符、通配符语法与声明位置 —— JetStream 独立成节于 `nats/jetstream/README.md`，父 README 指向它；
3. [x] NATS core 通配符透传、RabbitMQ `*`/`#` 绑定各有测试 —— `TestWildcardSubjectsPassThroughToTheServer`、`TestWildcardTokenDepthIsEnforcedByTheServer`、`TestWildcardBindingKeysBindAgainstATopicExchange`、`TestWildcardBoundQueueDeliversConcreteRoutingKeys`、`TestWildcardDestinationIsNotABinding`。全部进程内运行，不需真 broker，默认不 skip；
4. [x] `transport/message` 无新增类型（本 ADR 不扩核心）—— `git diff -- transport/` 为空；`WithTopicRegex` 是 adapter 局部 option（一个 bool 字段），非核心类型或能力接口；
5. [x] `message.Nacker` 已删除，四个 adapter 与 root 全部 build / vet / test 通过 `[2026-08-10 实测]`。
