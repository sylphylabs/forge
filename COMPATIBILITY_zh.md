# OpenKratos 与 Kratos 兼容性说明

状态：预发布

最后核对：2026 年 7 月 23 日

本文是 [`COMPATIBILITY.md`](COMPATIBILITY.md) 的中文翻译；如果两份文档存在
歧义，以英文规范版本为准。

OpenKratos 是 `go-kratos/kratos` 的独立 fork，不是 Kratos v3 的直接替代品，
也不承诺与未来 Kratos 版本保持源码、行为或发布兼容。

OpenKratos 也不会仅为了兼容 Kratos 而保留 API。如果已有更清晰或更高效的替代
方案，并且变更有充分技术依据，就可以移除旧 API。这类移除属于有意的破坏性
更新，必须同时提供可执行的迁移说明。

本文只记录已经被 OpenKratos 接受并完成验证的有意差异。尚未完成的工作不是
兼容性事实；拟采用、待重做或已拒绝的上游变更记录在
[`docs/upstream-adoptions.md`](docs/upstream-adoptions.md)。

## 对比基线

- 上游仓库：`https://github.com/go-kratos/kratos`
- 初始上游 commit：`668db92c2c001e9552594ba5a8aede8456af6d7e`
- 初始上游版本线：Kratos v3
- OpenKratos 版本线：v0，目前尚未正式发布

除非条目显式链接了更新的上游 revision，否则本文均以上述 commit 为比较基线。

## 差异摘要

| 领域 | Kratos v3 基线 | OpenKratos | 影响 |
| --- | --- | --- | --- |
| 根 module | `github.com/go-kratos/kratos/v3` | `github.com/openkratos/kratos` | 源码不兼容 |
| 版本线 | v3 | v0 预发布 | 发布不兼容 |
| 最低 Go 版本 | Go 1.25 | Go 1.27 | 构建要求变化 |
| Module 数量 | 28，包含 `cmd/kratos` | 27 | 发布和工具变化 |
| 异步消息 Transport | 没有协议中立的异步契约 | 根 module 提供 `transport/message`；broker SDK 适配器仍是可选嵌套 module | 新增 API；不承诺 broker wire 兼容 |
| 项目 CLI | `cmd/kratos` | 已移除 | 工作流不兼容 |
| Protobuf generator | Kratos module 路径 | OpenKratos module 路径 | 安装路径变化 |
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
| Config watch | reload 期间可能观察到部分新旧值 | 原子发布完整、已解析的 snapshot | 行为变化 |
| OTel 属性 | 旧 semconv，transport 属性混用 | semconv v1.41，按 transport 区分 | 遥测 schema 变化 |

## 仓库身份与版本

所有 OpenKratos module 和生成的 Go package 都使用
`github.com/openkratos/kratos` 前缀。OpenKratos 当前处于 v0 版本线，因此路径
不带上游的 `/v3` 后缀。

contrib 与 generator module 同样遵循这一规则，例如：

```text
github.com/go-kratos/kratos/contrib/middleware/jwt/v3
github.com/openkratos/kratos/contrib/middleware/jwt

github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v3
github.com/openkratos/kratos/cmd
```

首次发布前，仓库内部 module 使用临时的 `v0.0.0` requirement 和相对路径
`replace`。发布时必须为每个发布的嵌套 module 创建带 module 前缀的 tag；根
module tag 不会自动发布嵌套 module。

## 工具链

OpenKratos 要求 Go 1.27。在 final 工具链发布前，开发与 CI 使用 Go 1.27 RC2。
上游基线要求 Go 1.25，因此仍需停留在 Go 1.25 或 Go 1.26 的项目无法直接迁移。

仓库当前包含 26 个 Go module。根目录的 `go test ./...` 不会覆盖嵌套 module，
完整验证方式见 [`DEVELOPMENT.md`](DEVELOPMENT.md)。

## 项目 CLI 与代码生成

OpenKratos 不提供通用 `kratos` CLI。被移除的 module 曾包含项目脚手架、源码
生成封装、应用运行器、升级器和 changelog 辅助功能。

上游脚手架会复制模板，并对复制后的所有文件执行不受约束的字节替换。该操作
可能修改 protobuf raw descriptor，却不更新其中编码的长度，最终造成初始化
panic。OpenKratos 不保留或修补这种隐式改写源码的工作流。

| 已移除命令 | 替代方案 |
| --- | --- |
| `kratos new` | 创建普通 Go module，或使用可审查的仓库模板 |
| `kratos run` | `go run ./cmd/server -conf ./configs` |
| `kratos proto ...` | 项目自身的 Buf 或 `protoc` 配置 |
| `kratos upgrade` | `go get`、`go install` 与 `go mod tidy` |
| `kratos changelog` | Git 历史与 GitHub release notes |

三个原子化 OpenKratos Protobuf 命令共享一个确定性的源码 module：

```text
github.com/openkratos/kratos/cmd/protoc-gen-go-errors
github.com/openkratos/kratos/cmd/protoc-gen-go-http
github.com/openkratos/kratos/cmd/protoc-gen-go-middleware
```

每条命令都声明支持到 protobuf Edition 2024，并且只生成自己拥有的
`_errors.pb.go`、`_http.pb.go` 或 `_middleware.pb.go` 产物。项目不保留
`protoc-gen-go-openkratos` 转发命令。真实 `protoc` fixture 已经对 Edition 2023
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

OpenKratos contrib module 使用当前稳定的 Apollo v5、Consul API v2 与 Nacos SDK
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
[`docs/dependency-maintenance.md`](docs/dependency-maintenance.md)；OpenKratos
并不宣称所有传递仓库仍处于维护状态。

## UUID 生成

OpenKratos 直接生成的所有 UUID 均使用 Go 1.27 标准库 `uuid`。根 module 与
contrib provider 不再直接依赖 `github.com/google/uuid` 或
`github.com/gofrs/uuid`。

Resolver selector key 与 provider instance ID 仍使用 UUIDv4。默认 application
instance ID 从 Google UUIDv1（`google/uuid.NewUUID`）改为标准库默认
（`uuid.New`），在 Go 1.27 中即 UUIDv4。需要稳定 ID 或指定 UUID version 的服务
仍应显式传入 application ID。

Provider SDK 仍可能传递依赖其自身的 UUID package。OpenKratos 不暴露这些类型，
也不会为了隐藏上游依赖再引入第二套直接 UUID 实现。

## HTTP 路由

OpenKratos 使用基于标准库 `http.ServeMux` 的路由树替换
`github.com/gorilla/mux`。生成的 Google AIP 路径会编译为标准库方法与路径
pattern，匹配变量仍可通过 `transport/http.Context.Vars()` 和
`http.Request.PathValue()` 获取。

公开行为差异包括：

- literal 和更具体的 pattern 按 `http.ServeMux` 优先级获胜，与注册顺序无关；
- 冲突 pattern 在注册时 panic，不再静默选择第一个路由；
- 支持 AIP 变量、末尾 `**`、末尾 custom verb 和单路径段旧式正则；
- 拒绝跨多个路径段的任意 Gorilla 正则，应改写为 AIP 模板；
- 已移除继承自 Kratos 且不产生效果的 `StrictSlash` 选项；从 server 构造中删除
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

## Config Reload

Watch event 会重新构建所有 config source，解析跨 source placeholder，然后原子
发布完整 reader snapshot。Reload 期间不再向 reader 暴露新旧 source 混合状态。
File watcher 会直接跳过隐藏文件，不再把它们作为 reload error 上报。

## 可观测性

OpenTelemetry tracing 使用 semantic conventions v1.41。HTTP 与 gRPC 属性分开
发出，peer port 使用整数，gRPC method 会被严格校验，同时保留非法原始 method
便于诊断。原自定义 `rpc.status_code` 字段改为 `kratos.error.code`，避免与标准
RPC semantic attribute 冲突。

## 继承自 Kratos v3 的行为

以下行为很重要，但不是 OpenKratos 与 Kratos v3 的差异：

- 日志使用 `log/slog`；
- errors package 提供兼容标准库的包装；
- `encoding/json` 与 `encoding/protojson` 是独立 codec；
- 生成的 HTTP handler 会先 Bind 请求，再进入 service middleware。

只有在 OpenKratos 修改并完成验证后，这些项目才会进入差异章节。

## 迁移

可执行迁移步骤见
[`docs/migration/kratos-to-openkratos_zh.md`](docs/migration/kratos-to-openkratos_zh.md)。
Kratos v2 应用还需要先处理 v2 到 v3 的 API 变化，因为 OpenKratos 从 v3 设计
开始，不提供直接的 v2 兼容层。

## 维护规则

任何修改公开 API、默认行为、wire format、module、工具或最低 Go 版本的变更，
都必须在同一变更中更新英文规范文档和本文。

与 Kratos 兼容本身不是保留较差公开 API 的理由。破坏性替代只有在迁移文档已
说明技术依据、替代 API、新旧代码示例、重新生成要求与验证步骤后才能合并。

- 本文只记录当前事实，不记录愿望；
- 待决上游变更写入 `docs/upstream-adoptions.md`；
- 可复现性能数据写入 `docs/design/performance.md` 或 `docs/benchmarks/`；
- 破坏性更新合并前必须提供迁移步骤与替代 API；没有明确用途的兼容 shim 不应
  长期保留；
- 变更提交后补充 implementation commit 与聚焦测试链接；
- 每次发布前重新核对比较日期与基线。
