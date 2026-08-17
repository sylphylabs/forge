# ADR-0013：方法级错误声明——marker 认领的 typed 扩展生成精确 OpenAPI responses

**状态：** Accepted
**日期：** 2026-08-18
**决策者：** 项目所有者
**关系：** 建立在 [ADR-0006](./0006-errors-kind-over-transport-code.md)（错误身份 = (domain, reason)，Kind 单向投影传输码）与 [ADR-0012](./0012-transport-projection-gate.md)（只有声明过的身份才出线）之上；调研见 [docs/research/R1-method-error-openapi-precedent.md](../research/R1-method-error-openapi-precedent.md)。

---

## Context

ADR-0006 之后，契约错误在 Protobuf 枚举上声明（值级 `kind`、枚举级 `default_kind`），但「哪个方法会抛哪些错误」不存在任何机器可读的声明。OpenAPI 生成器只能给出笼统的 `default` 兜底 response；想要精确的 4xx/5xx 文档只有一条路——手写 gnostic `Operation` 注解里的状态码字面量字符串。字面量与真实错误声明零联动：写错、写漏、错误改名后过期，全都无声无息。

调研（R1）确认这是业界空白 `[一手：R1]`：grpc-gateway 的 `responses` 是裸字符串 map；AIP-193 只规定单个错误的线格式，没有 per-method 枚举条款；Kratos 的错误枚举注解与其 OpenAPI 生成器互不知晓；connect 生态只有 default 兜底开关。「方法实际可抛的错误枚举 → 精确 per-code responses」没有现成方案。

方案的硬约束：声明必须享受编译器校验（写一个不存在的 reason 必须编译失败），且 forge 不能预知应用的错误枚举类型。

## Decision

**forge api 只定义一个 marker；应用用自己的枚举类型声明扩展字段，marker 认领；生成器按声明产出精确 responses。**

`sylphy/errors/v1/errors.proto` 新增：

```proto
extend google.protobuf.FieldOptions {
  bool throws = 500103;
}
```

应用在自己的包里声明（forge 不预定义这些扩展）：

```proto
extend google.protobuf.MethodOptions {
  repeated MyFailureReason throws = 50000 [(sylphy.errors.v1.throws) = true];
}
extend google.protobuf.ServiceOptions {
  repeated MyFailureReason service_throws = 50001 [(sylphy.errors.v1.throws) = true];
}
```

字段类型是应用自己的错误枚举——编译器保证只能引用真实存在的枚举值，改名即编译失败，这正是字符串 map 方案给不了的。生成器（protoc-gen-openapi）对每个 method 收集 MethodOptions 与所属 service 的 ServiceOptions 上的全部扩展字段，凡其 FieldOptions 携带 `(sylphy.errors.v1.throws) = true` 即认领，service 级与 method 级取并集；每个枚举值经值级 `kind`（缺省回退枚举级 `default_kind`）解析出 Kind，经与运行时错误编码器**同一个**投影（`transport/http.StatusOf`，直接调用已发布 runtime，非复制的映射表）得到状态码，按状态码分组生成 responses：media type `application/problem+json`，schema 引用共享的 ForgeProblem 组件，description 按 `(kind, domain)` 分行列出全部 reason。

**marker 动态解析。** 插件编译依赖的是已发布 forge api 模块，而应用请求里的 marker 与应用扩展都以 descriptor 形态到达。生成器用 CodeGeneratorRequest 自带的 descriptor 集建 `protoregistry.Files` + `dynamicpb.Types` 池，把 options 按池重新反序列化后按全名匹配 `sylphy.errors.v1.throws`。解析不了的 unknown 扩展忽略、不 fail；凡能解析出 marker 的路径全部走到，不静默丢弃。

**VALIDATION_FAILED 自动并入。** 方法请求消息（递归嵌套、防环，含 map value）存在任何 `buf.validate.*` 约束（message/field/oneof 级）时，框架身份 `forge.sylphylabs.io / VALIDATION_FAILED` 并入该方法的 400，与应用声明的 400 reason 并列；无任何 throws 声明仅命中 validation 的方法同样生成 400。生成器选项 `validation_reason`（默认 true）可关闭。依据：validate middleware 的拒绝对每个带约束的方法都真实可达，漏掉它的文档是错的。

**default response 语义不变。** 精确 responses 与 `default` 兜底共存；显式手写的 response 内容永不被覆盖（沿用既有规则），但见下面的 fail 清单第 g 条——同码共存不是覆盖问题，是双源问题。

### 生成期 fail 清单

每条违反都是 plugin error 带定位诊断，各有专属测试：

| # | 违反 | 理由 |
| --- | --- | --- |
| a | marker 挂在非 MethodOptions/ServiceOptions 扩展的字段上 | 死注解比报错更贵；全池扫描而非按需 |
| b | 被 marker 标记的扩展字段不是 repeated enum | 声明形态是契约的一部分 |
| c | throws 引用枚举零值 | 零值命名「无错误」（与 ADR-0006 一致） |
| d | 值解析不到 kind（无值级 `kind` 且无枚举级 `default_kind`） | 无 Kind 即无状态码投影 |
| e | kind 投影出的状态码不在 4xx/5xx | 声明的错误映到成功码是自相矛盾 |
| f | 同一方法（含 service 级并集）同一身份声明两次 | 重复是复制粘贴事故的信号 |
| g | 声明产出的状态码与该方法手写的同码 gnostic response 共存 | 同一事实两个来源必然漂移；声明是唯一源，字面量删除 |
| h | （非 fail）解析不了 descriptor 的 unknown 扩展忽略，可解析的 marker 全部走到 | 部分可见的输入不该整体失败，但可见的声明不许丢 |

## 被否决的方案

**grpc-gateway 式字符串状态码 map。** 否决：与真实错误声明零联动是本 ADR 要消灭的现状，不是可选方案。写 `"404"` 的手和抛 `NOT_FOUND` 的代码之间没有任何编译器或生成器在场。

**forge 预定义 typed 扩展（`extend MethodOptions { repeated ??? throws }`）。** 否决：protobuf 没有泛型，forge 侧字段类型只能二选一——定死某个具体枚举（应用用不了自己的枚举）或退化为 string/int32（失去编译器校验，回到字符串 map）。marker 认领是唯一让「字段类型 = 应用自己的枚举」与「生成器可发现」同时成立的路，代价只是应用多写一行 marker。

**按类型巧合推断（扩展字段恰好是带 kind 注解的枚举即认领）。** 否决：应用完全可能有恰好同形但语义无关的扩展；隐式认领把巧合变成契约。marker 是显式的 opt-in。

**生成器内建 Kind→HTTP 映射表。** 否决：cmd 模块能直接依赖已发布 runtime 的 `StatusOf`，同一个函数就是投影闸（ADR-0012 语境下的唯一投影）；复制映射表是第二真相源。生成器另有契约测试锁死全部 16 个 Kind 经该投影落在错误码域内。

## Consequences

- **手写错误 response 字面量在带声明的方法上从可用变为构建失败**（fail g）。修复路径是删字面量、补声明——方向与 ADR-0012「修复路径是声明进 proto」完全一致。
- **文档从此与声明同源。** 错误枚举改名、改 kind，重新生成即反映；不改声明则编译失败挡住。
- **声明 ≠ 运行时保证。** 本 ADR 只管文档与声明一致；「运行期出线 reason ∈ 该方法声明集」的断言与 CI OpenAPI diff 是后续闸门（见 openapi-3.2.md Next Gates）。
- **应用扩展号段自管。** 50000–99999 是组织内部自用号段 `[一手：R1]`，应用在自己包内选号，forge 只占已登记的 500101–500103。
- 新增测试：`cmd/protoc-gen-openapi/throws_test.go`（并集、validation 并入、开关、fail 清单逐条）、`cmd/internal/openapi/generator/throws_test.go`（投影与运行时一致、4xx/5xx 守卫）。
