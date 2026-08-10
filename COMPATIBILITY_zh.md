# Forge 与 Forge 兼容性说明

状态：预发布

最后核对：2026 年 8 月 9 日

本文是 [`COMPATIBILITY.md`](COMPATIBILITY.md) 的中文翻译；如果两份文档存在
歧义，以英文规范版本为准。

Forge 是 `go-kratos/kratos` 的独立 fork，不是 Kratos v3 的直接替代品，
也不承诺与未来 Forge 版本保持源码、行为或发布兼容。

Forge 也不会仅为了兼容 Forge 而保留 API。如果已有更清晰或更高效的替代
方案，并且变更有充分技术依据，就可以移除旧 API。这类移除属于有意的破坏性
更新，必须同时提供可执行的迁移说明。

本文只记录已经被 Forge 接受并完成验证的有意差异。尚未完成的工作不是
兼容性事实；拟采用、待重做或已拒绝的上游变更记录在
[`docs/upstream-adoptions.md`](docs/upstream-adoptions.md)。

## 对比基线

- 上游仓库：`https://github.com/go-kratos/kratos`
- 初始上游 commit：`668db92c2c001e9552594ba5a8aede8456af6d7e`
- 初始上游版本线：Kratos v3
- Forge 版本线：v0，目前尚未正式发布

除非条目显式链接了更新的上游 revision，否则本文均以上述 commit 为比较基线。

## 差异摘要

| 领域 | Kratos v3 基线 | Forge | 影响 |
| --- | --- | --- | --- |
| 根 module | `github.com/go-kratos/kratos/v3` | `github.com/sylphylabs/forge` | 源码不兼容 |
| 版本线 | v3 | v0 预发布 | 发布不兼容 |
| 最低 Go 版本 | Go 1.25 | Go 1.27 | 构建要求变化 |
| Module 数量 | 28，包含 `cmd/forge` | 27 | 发布和工具变化 |
| 异步消息 Transport | 没有协议中立的异步契约 | 根 module 提供 `transport/message`；broker SDK 适配器仍是可选嵌套 module | 新增 API；不承诺 broker wire 兼容 |
| 项目 CLI | `cmd/forge` | 已移除 | 工作流不兼容 |
| Protobuf generator | Forge module 路径 | Forge module 路径 | 安装路径变化 |
| Contrib provider SDK | 旧 provider major 与已归档直接依赖 | 当前稳定 major 与受维护的标准替代 | 源码与依赖图变化 |
| UUID 生成 | `google/uuid` 与 `gofrs/uuid` | 标准库 `uuid` | 源码与生成 ID 行为变化 |
| HTTP protobuf 生成 | Open API 字段访问 | Editions 2023 Open/Opaque API accessor | 新增生成能力 |
| Google HTTP transcoding | 路由、body、query 分别解析且仅部分支持 | 共享路径语法、严格生成校验、ProtoJSON 投影和 additional bindings | API 与 wire 行为不兼容 |
| HTTP router | Gorilla mux | 标准库 `http.ServeMux` 路由树 | 行为不兼容 |
| HTTP client 路径 | endpoint base path 和转义变量可能丢失 | 保留 base path、按 AIP 规则转义，生成 client 复用已编译路径计划 | 正确性、生成代码与性能变化 |
| HTTP middleware 配置 | serving 期间仍可修改 middleware | 首次 `Start` 或 `ServeHTTP` 时冻结；生成 unary handler 预组合 middleware chain | 行为与生成代码变化 |
| 未匹配 HTTP 路由 | 可能落入 `http.DefaultServeMux` | 显式处理 404/405 | 行为与安全变化 |
| HTTP stream | 请求 timeout 可能取消 SSE/WebSocket | 分离请求 timeout，保留显式 stream deadline | 行为变化 |
| WRR selector | 稳态清理会扫描节点集合 | 先以 O(1) 判断是否存在过期项 | 仅性能变化 |
| P2C selector | 每个 balancer 使用带锁随机源 | 使用并发安全的 `math/rand/v2` 顶层随机源 | 仅性能变化 |
| App shutdown | 重复 stop 与阶段错误语义不完整 | 幂等 stop、合并错误、受限 after-stop | API 与行为变化 |
| Transport 能力接口 | `transport.Server` 加 `Endpointer` | 可选的 `Healthzer` 与 `GracefulStopper` 接口；App 优先排空并聚合就绪状态 | 新 API |
| Config watch | reload 期间可能观察到部分新旧值 | 原子发布完整、已解析的 snapshot | 行为变化 |
| OTel 属性 | 旧 semconv，transport 属性混用 | semconv v1.41，按 transport 区分 | 遥测 schema 变化 |
| OTel Metrics | 使用自定义名称、`code` 与 `reason` 的通用 unary middleware | HTTP semconv v1.41 duration histogram 与 grpc-go A66 duration metrics | 源码与遥测 schema 不兼容 |
| 错误分类 | 错误上存 HTTP 状态码，再映射到 gRPC | 与传输无关的 `Kind`，向每个 transport 单向投影 | 源码、线格式与行为不兼容 |
| 错误构造 | `errors.BadRequest(reason, msg)` 与生成的 `ErrorXxx(format, args...)` | `errors.New(Kind)` 与生成的 sentinel 值 | 源码与生成代码不兼容 |
| 错误判定 | 生成的 `IsXxx(err)`，比较结构体字段 | 对生成 sentinel 用 `errors.Is`，另有 `errors.KindOf` | 源码不兼容 |
| 错误 annotation | `default_code` 与 `code`，携带 HTTP 状态码 | `default_kind` 与 `kind`，携带 `Kind` | Protobuf 契约不兼容 |
| 重试判定 | `errors.IsRetryable` 与 `Kind.Retryable` 孤立地按 Kind 分类 | 已移除；判定需要投递证据或幂等声明 | 源码不兼容 |
| 客户端重试默认行为 | `KindUnavailable` 被无条件重试 | 仅在有 `transport.WasNotSent` 证据或 `retry.Idempotent(ctx)` 时重试 | 行为不兼容 |
| 聚合错误 | `errors.Join` 过线时静默丢弃除第一个外的全部错误 | 显式 `errors.Violations`，投影到 `errdetails.BadRequest` | 新增 API |
| 错误响应格式 | 参与内容协商：JSON 写 `NOT_FOUND`，ProtoJSON 写 `KIND_NOT_FOUND` | 统一为 `application/problem+json`，不参与协商 | 线格式不兼容 |
| 错误披露 | 所有 message 原样过线 | 只有 `errors.Public` 过线：调用方显式声明的内容，绝不含 cause | 源码与行为不兼容 |
| 负载均衡策略 | 进程级全局 `selector.SetGlobalSelector`，默认值取决于哪个 transport 先被链接 | 每客户端 `WithSelector`，`wrr` 默认值由各 transport 自己拥有 | 源码不兼容 |
| HTTP 客户端流 | 单一 `ClientStream` 接口；只收不发的流用运行时错误应付发送方法 | 核心 `ClientStream` + `SendingClientStream` 能力接口 | 源码不兼容 |
| 错误响应读取 | 任何 body 都解析，无大小限制，不校验状态码 | 仅 Problem 媒体类型、64 KiB 上限、与状态码矛盾的 body 一律丢弃 | 行为变化 |
| HTTP/gRPC 状态码转换 | `transport/http/status` 双向转换 | 已移除；各 transport 单向投影 `Kind` | 源码不兼容 |
| HTTP transport 的 Protobuf 依赖 | `transport/http` 无条件链接 Protobuf runtime | 移入 `transport/http/transcoding`；纯 JSON 服务既不链接 Protobuf 也不链接 gRPC | 源码不兼容；生成代码 import 路径变化 |
| Codec 注册 | `transport` blank-import 全部 codec | 只注册无需 schema 的 codec；Protobuf codec 由 `transcoding` 注册 | 未注册 content type 的行为变化 |

## 仓库身份与版本

所有 Forge module 和生成的 Go package 都使用
`github.com/sylphylabs/forge` 前缀。Forge 当前处于 v0 版本线，因此路径
不带上游的 `/v3` 后缀。

contrib 与 generator module 同样遵循这一规则，例如：

```text
github.com/go-kratos/kratos/contrib/middleware/jwt/v3
github.com/sylphylabs/forge/contrib/middleware/jwt

github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v3
github.com/sylphylabs/forge/cmd
```

首次发布前，仓库内部 module 使用临时的 `v0.0.0` requirement 和相对路径
`replace`。发布时必须为每个发布的嵌套 module 创建带 module 前缀的 tag；根
module tag 不会自动发布嵌套 module。

## 工具链

Forge 要求 Go 1.27。在 final 工具链发布前，开发与 CI 使用 Go 1.27 RC2。
上游基线要求 Go 1.25，因此仍需停留在 Go 1.25 或 Go 1.26 的项目无法直接迁移。

仓库当前包含 27 个 Go module。根目录的 `go test ./...` 不会覆盖嵌套 module，
完整验证方式见 [`DEVELOPMENT.md`](DEVELOPMENT.md)。

## 项目 CLI 与代码生成

Forge 不提供通用 `forge` CLI。被移除的 module 曾包含项目脚手架、源码
生成封装、应用运行器、升级器和 changelog 辅助功能。

上游脚手架会复制模板，并对复制后的所有文件执行不受约束的字节替换。该操作
可能修改 protobuf raw descriptor，却不更新其中编码的长度，最终造成初始化
panic。Forge 不保留或修补这种隐式改写源码的工作流。

| 已移除命令 | 替代方案 |
| --- | --- |
| `forge new` | 创建普通 Go module，或使用可审查的仓库模板 |
| `forge run` | `go run ./cmd/server -conf ./configs` |
| `forge proto ...` | 项目自身的 Buf 或 `protoc` 配置 |
| `forge upgrade` | `go get`、`go install` 与 `go mod tidy` |
| `forge changelog` | Git 历史与 GitHub release notes |

三个原子化 Forge Protobuf 命令共享一个确定性的源码 module：

```text
github.com/sylphylabs/forge/cmd/protoc-gen-go-errors
github.com/sylphylabs/forge/cmd/protoc-gen-go-http
github.com/sylphylabs/forge/cmd/protoc-gen-go-middleware
```

每条命令都声明支持到 protobuf Edition 2024，并且只生成自己拥有的
`_errors.pb.go`、`_http.pb.go` 或 `_middleware.pb.go` 产物。项目不保留
`protoc-gen-go-forge` 转发命令。真实 `protoc` fixture 已经对 Edition 2023
Open/Opaque API 的 message、scalar、repeated、map、显式 presence 与 oneof 字段
完成编译和执行验证。

Inline unary `google.api.HttpRule` 在生成、client 展开、ServeMux 注册和 server
提取阶段使用同一个 Google 路径模板实现。Primary binding 定义生成的 Go client
方法；一层 `additional_bindings` 全部注册为可选 server 路由。嵌套 additional
binding 会导致生成失败。

Google transcoding 的 JSON 对外使用 `application/json`，但 wire 语义遵循
protobuf JSON。整条 message 以及命名 `body`/`response_body` 投影均正确处理 enum
名称、标准 base64 bytes、字符串形式的 64 位整数、非有限浮点数、message、
repeated、map 和 Open/Opaque API。`google.api.HttpBody` 继续携带自身声明的媒体
类型和原始 bytes。

请求字段只分类一次：path 字段不会进入 body 或 query，命名 body 的所有后代不会
进入 query，`body: "*"` 不生成 query。支持嵌套 query leaf 与重复 scalar key；
query 位置的 map 和 repeated message 会在生成期失败。生成的 server 按 body、
query、path 顺序绑定，因此 URL path 始终具有最终优先级。

非法 path 字段、非法 body 投影、`response_body: "*"`、重复或冲突 binding 和
含糊的 custom 声明都会让生成失败，且不会留下部分生成文件。若要编码整条响应，
应省略 `response_body`。

生成的 HTTP 文件要求 `transport/http.SupportPackageIsVersion5`。Version 4 为每个
固定 client binding 引入并发安全的 `CompiledPath`；version 5 进一步预组合 unary
server middleware。因此模板解析、descriptor 校验、middleware 匹配与 handler
组合都不再发生在各自的稳态请求路径中。已经移除过时的 version 3 和 4
sentinel；升级 runtime 前必须使用当前 generator 重新生成 HTTP 文件。

`transport/http.BuildPath` 返回 `(string, error)`，继续作为真正动态模板的便捷
API。手写代码如果反复使用固定模板，应通过 `CompilePath` 或 `MustCompilePath`
只编译一次，再为每个请求调用 `CompiledPath.Build`。生成 client 与手写 client
都会在网络请求前返回展开错误。Primary `custom.kind: "*"` 返回
`ErrUnspecifiedHTTPMethod`；primary 路径中的裸 `*` 或 `**` 返回
`ErrUnboundPathWildcard`。Server 与原始 HTTP client 仍可使用这两类规则。

外部 `google.api.Service` 配置与 `fully_decode_reserved_expansion` 尚未实现，
仍属于 [`docs/design/google-http-transcoding.md`](docs/design/google-http-transcoding.md)
定义的独立 Phase 2。

## 生成式 Server Middleware

语义含糊的 `middleware.Handler`、`middleware.Middleware` 与
`middleware.Chain` 已分别替换为 `UnaryHandler`、`UnaryMiddleware` 与
`ChainUnary`。Streaming 使用独立的 `ServerStream`、`StreamHandler`、
`StreamMiddleware` 与 `ChainStream` 生命周期协议。

HTTP 与 gRPC server 的 selector middleware 已移除。`http.Middleware`、
`grpc.Middleware`、`grpc.StreamMiddleware`、`Server.Use`、
`Server.WrapMiddleware` 与 `http.Context.Middleware` 均不保留兼容 alias。
重新生成的代码会提供 service 专属 middleware plan，以及
`Wrap<Service>HTTPServer` / `Wrap<Service>GRPCServer` 构造函数。构造函数会在注册前
快照并组合 service 与 method middleware，请求路径不再执行 selector 查找或 chain
构造。

`middleware/selector.Server` 也已移除。生成式 server plan 不替代 client 侧
operation 选择，因此 `middleware/selector.Client` 继续保留。Transport 原生的
server 行为应使用 HTTP filter 或 gRPC interceptor；其他 client middleware option
独立存在，不受本次 server API 替换影响。

隔离的前后性能数据记录在
[`docs/benchmarks/http-middleware-2026-07-23.md`](docs/benchmarks/http-middleware-2026-07-23.md)。

## Contrib Provider 依赖

Forge contrib module 使用当前稳定的 Apollo v5、Consul API v2 与 Nacos SDK
v2 module path。使用 SDK 类型构造 Consul 或 Nacos provider 的应用需要修改
import，因此属于源码不兼容：

```text
github.com/hashicorp/consul/api
github.com/hashicorp/consul/api/v2

github.com/nacos-group/nacos-sdk-go/clients/config_client
github.com/nacos-group/nacos-sdk-go/v2/clients/config_client
```

直接依赖的已归档 `json-iterator/go`、`golang/mock` 与 PGV 已分别由
`encoding/json`、`go.uber.org/mock` 和 Protovalidate 测试 fixture 替代。旧请求只要
实现 `Validate() error` 仍然兼容，不需要 PGV runtime module。

部分当前 provider SDK 仍会编译已归档的传递依赖。其归属和替换条件记录在
[`docs/dependency-maintenance.md`](docs/dependency-maintenance.md)；Forge
并不宣称所有传递仓库仍处于维护状态。

## UUID 生成

Forge 直接生成的所有 UUID 均使用 Go 1.27 标准库 `uuid`。根 module 与
contrib provider 不再直接依赖 `github.com/google/uuid` 或
`github.com/gofrs/uuid`。

Resolver selector key 与 provider instance ID 仍使用 UUIDv4。默认 application
instance ID 从 Google UUIDv1（`google/uuid.NewUUID`）改为标准库默认
（`uuid.New`），在 Go 1.27 中即 UUIDv4。需要稳定 ID 或指定 UUID version 的服务
仍应显式传入 application ID。

Provider SDK 仍可能传递依赖其自身的 UUID package。Forge 不暴露这些类型，
也不会为了隐藏上游依赖再引入第二套直接 UUID 实现。

## HTTP 路由

Forge 使用基于标准库 `http.ServeMux` 的路由树替换
`github.com/gorilla/mux`。生成的 Google AIP 路径会编译为标准库方法与路径
pattern，匹配变量仍可通过 `transport/http.Context.Vars()` 和
`http.Request.PathValue()` 获取。

公开行为差异包括：

- literal 和更具体的 pattern 按 `http.ServeMux` 优先级获胜，与注册顺序无关；
- 冲突 pattern 在注册时 panic，不再静默选择第一个路由；
- 支持 AIP 变量、末尾 `**`、末尾 custom verb 和单路径段旧式正则；
- 拒绝跨多个路径段的任意 Gorilla 正则，应改写为 AIP 模板；
- 已移除继承自 Forge 且不产生效果的 `StrictSlash` 选项；从 server 构造中删除
  该选项，路径清理和尾部斜杠重定向遵循标准库；
- `HandlePrefix` 使用路径段前缀语义，而不是任意字符串前缀；
- 未匹配请求不会落入进程级 `http.DefaultServeMux`；确有需要时应将其显式传给
  `NotFoundHandler`；
- method 不匹配时使用配置的 method-not-allowed handler，不依赖 Gorilla matcher
  注册顺序。
- 单路径段变量会对 slash 与 URL delimiter 做百分号编码；只有 AIP 多路径段模板
  保留结构性 slash；
- server 提取多路径段变量时原样保留 `%2F` 和 `%2f`，单路径段变量则完整解码；
- 直接 HTTP client endpoint 保留配置的 base path，discovery service name 与
  endpoint path prefix 分离。

Router benchmark 与验收规则见
[`docs/design/performance.md`](docs/design/performance.md)。

## HTTP Stream

SSE 与 WebSocket server stream 会保留请求 context 的值，但移除 deadline 和
cancel。因此 unary HTTP server timeout 不会结束长连接。Stream 生命周期由 I/O
错误、连接关闭以及显式 `SetReadDeadline` 或 `SetWriteDeadline` 控制。

如果业务要求最大连接时长，必须显式配置；server 的 unary timeout 不再代表
stream 生命周期上限。

## Selector 性能

Selector API 和选择结果保持不变，但稳态成本不同：

- WRR 只有在 current-weight map 大于活动节点数时才构造清理集合；
- P2C 使用并发安全的 `math/rand/v2` 顶层函数，移除每个 balancer 的随机数锁。

采用的上游 revision、不变量测试和 benchmark 结果见
[`docs/benchmarks/selectors-2026-07-22.md`](docs/benchmarks/selectors-2026-07-22.md)。

## 应用生命周期

`App.Stop` 现在幂等。Before-stop、注销、server stop 与 after-stop 阶段会在安全
范围内继续执行；多个失败通过 `errors.Join` 返回，不再由后发生的错误覆盖先前
错误。`AfterStopTimeout` 配置受限的 after-stop 阶段，默认十秒。After-stop
callback 保留 application context 的值，但不继承其取消状态。

`transport.Healthzer` 与 `transport.GracefulStopper` 是 `transport.Server` 之外
的可选能力接口。停机时 `App` 会在 `StopTimeout` 内优先排空实现了
`GracefulStopper` 的 server，排空被放弃或失败时回退到 `Stop`；只实现
`Start`/`Stop` 的 server 收到的仍是与之前相同的单次 `Stop` 调用。
`App.Healthz` 报告所有实现 `Healthzer` 的 server 是否都能接收新请求，
`transport/http/healthz.NewHandler` 将其作为 HTTP readiness 探针提供，不会自动
注册任何路由。gRPC server 的内部 health service 在 `Start` 恢复之前报告
`NOT_SERVING`。

## Config Reload

Watch event 会重新构建所有 config source，解析跨 source placeholder，然后原子
发布完整 reader snapshot。Reload 期间不再向 reader 暴露新旧 source 混合状态。
File watcher 会直接跳过隐藏文件，不再把它们作为 reload error 上报。

## 可观测性

OpenTelemetry tracing 使用 semantic conventions v1.41。HTTP 与 gRPC 属性分开
发出，peer port 使用整数，gRPC method 会被严格校验，同时保留非法原始 method
便于诊断。原自定义 `rpc.status_code` 字段改为 `forge.error.kind` 与
`forge.error.reason`，避免与标准 RPC semantic attribute 冲突，且用名称而非编号
表达失败类别。

Metrics 改为按 transport 区分且首版只提供 duration histogram。HTTP 使用 v1.41
Stable `http.server.request.duration` 与 `http.client.request.duration`；gRPC 直接
使用 grpc-go 的 gRFC A66 `grpc.client.call.duration`、
`grpc.client.attempt.duration` 与 `grpc.server.call.duration`，不会同时发送 RC
`rpc.*` 或旧通用指标。Provider 是必填的实例依赖；SDK reader、exporter、
resource、View、cardinality limit 与 exemplar 策略仍由应用拥有。

继承的 `metrics.Server`、`metrics.Client`、通用 instrument option、默认
instrument/View helper 与修改进程环境的 exemplar helper 均直接移除，不保留兼容
shim。HTTP instrumentation 覆盖原生 `ServeHTTP` 与 `RoundTrip` 生命周期，不再
位于 unary middleware；gRPC 的 unary、stream、retry 与 hedging 生命周期由
grpc-go 官方实现负责。完整合同与基数策略见
[`docs/design/otel-metrics.md`](docs/design/otel-metrics.md)。

## Metadata 透传编码

`middleware/metadata` 对无法直接作为 header 值传输的透传值做百分号转义，并在
接收端还原。gRPC 的非二进制 header 只接受 0x20 至 0x7E 的可打印 ASCII，否则
以 `Internal` 错误终止整个 RPC；因此在 Kratos v3 的行为下，只要 metadata 中带
非 ASCII 的姓名、地址或控制字符，调用在到达服务端之前就已失败。

只有当值落在该范围之外，或含有转义标记 `%` 本身时才会编码。不含 `%` 的可打印
ASCII 值原样传输，因此该编码对常见情形不可见。

这是线格式变更，其版本错配行为是刻意设计的：

- Forge 发送方与不做解码的对端，在所有原本就能传输的值上一致，因为这些值不会
  被改写。原本需要编码的值会以百分号转义的形式到达，而此前该调用直接失败。
- Forge 接收方与不做编码的对端，在所有值上一致，包括含裸 `%` 的值：无法还原的
  值原样透传，而不是被拒绝。

因此两个方向都退化为此前的行为，而不会损坏值或使请求失败。`Server`、`Client`
与 `ServerStream` 均参与其中；其中流式服务端路径为对应上游提案所未覆盖。

## 错误

错误携带与传输无关的 `Kind`，而不是 HTTP 状态码。每个 transport 把 `Kind`
**单向**投影到自己的词汇表，因此不存在经由异构码空间的往返。旧设计中该往返是
有损的：构造为 422 的错误经一跳 gRPC 后变成 500，因为 HTTP 有六十余个状态码而
gRPC 只有十七个。

作为服务契约的错误在 Protobuf 中用 `kind` 声明，`protoc-gen-go-errors` 为每个
reason 生成一个不可变 sentinel 值，而非一个构造函数加一个判定函数。因此判定使用
`errors.Is` 而非生成的 `IsXxx`，`errors.KindOf` 取代 `errors.Code`。generator 会在
**编译期**拒绝那些会变成线格式不一致的声明：reason 不是 `SCREAMING_SNAKE_CASE`、
未以 enum 名为前缀、或零值上标注了 kind。

身份信息（含 domain）通过 `errdetails.ErrorInfo` 传递，因此调用方匹配远端错误与
匹配本地错误使用同一个 sentinel。聚合失败通过 `errdetails.BadRequest` 传递；
`errors.Join` 在本契约中不是聚合错误，过线时只保留第一个。

cause 链不跨进程。收到的错误 `errors.Unwrap` 返回 nil，`errors.As` 也取不到远端
类型。跨服务排查改用 trace ID：tracing middleware 会自动为出站错误打上当前
trace，完整记录由 trace 后端保存。Forge 不会另外把 cause 摘要序列化上线。

`transport/http/status` 包已移除。它的双向码转换正是 `Kind` 要取代的有损环节；
各 transport 直接投影 `Kind` 后，已无任何引用。

错误响应固定为 `application/problem+json`，**不参与内容协商**。此前参与协商时，同一个
错误有两种写法 —— JSON 写 `NOT_FOUND`、ProtoJSON 写 `KIND_NOT_FOUND` —— 客户端读到
非预期的那种会静默丢失 kind 或 reason。协商「结果」有意义，协商「失败」只会制造两端
不一致。SSE 与 WebSocket 的错误帧使用同一份文档。

接收方不认识的 kind 会保留其身份，仅分类回退到状态码，因此运行较新版本的对端仍可理解。

过线的是 `errors.Public`：kind、身份，以及调用方**显式声明**的 message、metadata
与 violations。cause 由**结构**排除而非由规则排除，因此没有任何配置能让它泄漏。

这取代了 `errors.Policy` 及 `PolicySafe` / `PolicyStrict` / `PolicyVerbose`，三者
均已删除。policy 读 Kind 去推断它无法观察的来源：它从不检查 metadata 或 violation
文本，且那三个值是可重赋值的导出包变量，任何依赖库都能改掉。「什么是公开的」只有
写下该字段的调用方能正确回答，`Msg` / `Meta` / `WithMetadata` / `Violations` 就是
他们表态的方式。来自 Forge 之外的错误只披露 `KindUnknown` —— 它的文本是写给运维看
的，不是写给调用方的。

客户端只在 body 为 `application/problem+json`、不超过 `MaxProblemBytes`（64 KiB）、
且其 kind 与响应状态码一致时才解析它。与状态码矛盾的 body 一律丢弃、改用状态行 ——
因为陈旧中间层可能用新状态码回放旧 body，信它会让调用方把 503 匹配成 NotFound
sentinel 而停止重试。未知 kind **不算**矛盾：保留其身份，仅分类回退到状态行。

身份仅在 domain 与 reason 成对时保留；半个身份无法匹配 sentinel，按匿名处理。

`errors.IsRetryable` 与 `errors.Kind.Retryable` 不存在。重试是否安全取决于错误
类别、请求是否到达服务端、以及操作本身是否幂等，而 Kind 只回答第一项。
`KindUnavailable` 尤其如此：它既可能产生于连接从未建立，也可能产生于服务端已执行
完毕而响应在回传途中丢失，因此仅凭 Kind 得出的布尔值会授权重复已经发生的工作。调用方
用 `errors.KindOf` 组合幂等声明与传输层的投递证据自行判定，或交给
`middleware/retry`。

参见 [`docs/design/errors.md`](docs/design/errors.md)。

## 客户端重试需要证据或声明

`middleware/retry` 只在两种情况下重试失败的调用：传输层证明请求从未到达服务端，
或调用方用 `retry.Idempotent(ctx)` 声明该操作幂等。两者皆无的错误在一次尝试后
直接返回。

因此 `KindUnavailable` 本身不再触发重试。服务端可能在执行完请求之后、响应送达
之前才报告自己不可用，此时对非幂等操作重试就是重复执行 —— 要避免的正是重复扣款
或重复写入这类最难追查到根因的故障。在幂等声明下，`KindUnavailable` 与
`KindDeadlineExceeded` 都会重试；`KindResourceExhausted` 与 `KindConflict` 不会，
因为无服务端退避指引地重试限流调用会加剧过载，而冲突需要调用方先重新读取状态。

投递证据来自传输层 —— 只有它知道字节是否离开了本进程。`transport.MarkNotSent`
记录该证据，`transport.WasNotSent` 读取它。HTTP 客户端标记节点选择失败、dial
失败与 WebSocket 握手失败。gRPC 客户端不做任何标记：grpc-go 把 dial 失败与调用
途中断连报告为同一个没有类型化 cause 的 `codes.Unavailable` status，因此需要自动
重试的 gRPC 调用应声明幂等。

要为某个操作恢复自动重试，只需在明确知道该操作可安全重复的调用点加一次声明 ——
通常是生成的调用包装或客户端 facade。

参见 [`docs/design/retry.md`](docs/design/retry.md)。

## 负载均衡是每客户端的

`selector.GlobalSelector` 与 `selector.SetGlobalSelector` 已移除。客户端用
`http.WithSelector` 或 `grpc.WithSelector` 选择策略，默认加权轮询。

原全局变量可被任何依赖库重赋值、被并发读取且无同步，默认值还取决于哪个 transport
先被链接。改为每客户端 option 后三者同时消失：一个依赖不再能改变另一个客户端的均衡
行为，默认值也成为常量。

gRPC 侧该 option 作用于经服务发现的端点；直连固定地址的客户端只有一个节点，不查询
策略。

## HTTP 客户端流显式声明自己能做什么

`ClientStream` 只保留每种流都能兑现的方法 —— `Header`、`Trailer`、`CloseSend`、
`Context`、`RecvMsg`。发送是独立能力：

```go
type SendingClientStream interface {
	ClientStream
	SendMsg(any) error
	CloseAndRecv(any) error
}
```

SSE 是单向的，而旧接口把这个**编译期事实**表达成三个方法的运行时错误字符串。现在
调用方从类型就能知道：`Client.WebSocket` 返回 `SendingClientStream`，
`Client.ServerSentEvent` 返回 `ClientStream`，持有窄接口而需要发送的代码做类型断言。

`Send` 与 `Recv` 已移除 —— 它们是 `SendMsg` / `RecvMsg` 的别名。生成的客户端方法集
不变，因此调用生成代码的业务代码无需修改，但**必须用配套的 `protoc-gen-go-http`
重新生成**。

## Protobuf 是可选的

`transport/http` 只处理字节与 Go 值。所有需要 schema 的能力 —— HTTP transcoding、
ProtoJSON 投影、path/query 绑定到声明字段、原始 `HttpBody`、stream body field ——
都在 `transport/http/transcoding`，import 它即安装进 transport。

生成的绑定代码会 import 它，因此从 `.proto` 构建的服务行为与开销均与从前一致。
只提供纯 JSON 的服务两者都不 import：

| 应用形态 | 二进制 | Protobuf 包 | gRPC 包 |
| --- | --- | --- | --- |
| 纯 JSON（`transport/http`） | 11 MB | 0 | 0 |
| 只使用 `errors` 的库 | 2.6 MB | 0 | 0 |
| 生成代码 + gRPC | 18 MB | 40 | 75 |

手写服务若要绑定 Protobuf 消息而不经生成代码，MUST 自行 import：

```go
import _ "github.com/sylphylabs/forge/transport/http/transcoding"
```

未 import 时：原始 `HttpBody` 不被识别，path 变量不会绑定到消息字段，stream body
field 会报告 schema runtime 缺失 —— transport 会明确报错，不静默失败。

`transport` 不再 blank-import Protobuf codec，改由 `transcoding` 注册。服务若需要
`proto` / `protojson` content type 而不使用生成代码，同样 import 该子包。

## 继承自 Kratos v3 的行为

以下行为很重要，但不是 Forge 与 Kratos v3 的差异：

- 日志使用 `log/slog`；
- `encoding/json` 与 `encoding/protojson` 是独立 codec；
- 生成的 HTTP handler 会先 Bind 请求，再进入 service middleware。

只有在 Forge 修改并完成验证后，这些项目才会进入差异章节。

## 迁移

可执行迁移步骤见
[`docs/migration/kratos-to-forge_zh.md`](docs/migration/kratos-to-forge_zh.md)。
Kratos v2 应用还需要先处理 v2 到 v3 的 API 变化，因为 Forge 从 v3 设计
开始，不提供直接的 v2 兼容层。

## 维护规则

任何修改公开 API、默认行为、wire format、module、工具或最低 Go 版本的变更，
都必须在同一变更中更新英文规范文档和本文。

与 Forge 兼容本身不是保留较差公开 API 的理由。破坏性替代只有在迁移文档已
说明技术依据、替代 API、新旧代码示例、重新生成要求与验证步骤后才能合并。

- 本文只记录当前事实，不记录愿望；
- 待决上游变更写入 `docs/upstream-adoptions.md`；
- 可复现性能数据写入 `docs/design/performance.md` 或 `docs/benchmarks/`；
- 破坏性更新合并前必须提供迁移步骤与替代 API；没有明确用途的兼容 shim 不应
  长期保留；
- 变更提交后补充 implementation commit 与聚焦测试链接；
- 每次发布前重新核对比较日期与基线。
