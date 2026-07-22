# OpenKratos 与 Kratos 兼容性说明

状态：预发布

最后核对：2026 年 7 月 22 日

本文是 [`COMPATIBILITY.md`](COMPATIBILITY.md) 的中文翻译；如果两份文档存在
歧义，以英文规范版本为准。

OpenKratos 是 `go-kratos/kratos` 的独立 fork，不是 Kratos v3 的直接替代品，
也不承诺与未来 Kratos 版本保持源码、行为或发布兼容。

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
| 最低 Go 版本 | Go 1.25 | Go 1.26 | 构建要求变化 |
| Module 数量 | 28，包含 `cmd/kratos` | 27 | 发布和工具变化 |
| 项目 CLI | `cmd/kratos` | 已移除 | 工作流不兼容 |
| Protobuf generator | Kratos module 路径 | OpenKratos module 路径 | 安装路径变化 |
| HTTP protobuf 生成 | Open API 字段访问 | Editions 2023 Open/Opaque API accessor | 新增生成能力 |
| HTTP router | Gorilla mux | 标准库 `http.ServeMux` 路由树 | 行为不兼容 |
| HTTP client 路径 | endpoint base path 和转义变量可能丢失 | 保留 base path，并按 AIP 规则转义 | 正确性与 URL 行为变化 |
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
github.com/openkratos/kratos/cmd/protoc-gen-go-http
```

首次发布前，仓库内部 module 使用临时的 `v0.0.0` requirement 和相对路径
`replace`。发布时必须为每个发布的嵌套 module 创建带 module 前缀的 tag；根
module tag 不会自动发布嵌套 module。

## 工具链

OpenKratos 要求 Go 1.26，并使用 Go 1.27 RC 对根 module 做前向验证。上游基线
要求 Go 1.25，因此仍需停留在 Go 1.25 的项目无法直接迁移。

仓库当前包含 27 个 Go module。根目录的 `go test ./...` 不会覆盖嵌套 module，
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

以下确定性的 Protobuf generator 仍作为独立 module 保留：

```text
github.com/openkratos/kratos/cmd/protoc-gen-go-http
github.com/openkratos/kratos/cmd/protoc-gen-go-errors
```

`protoc-gen-go-http` 声明支持到 protobuf Edition 2024。命名 `body` 与
`response_body` 使用与 API level 对应的 accessor 和赋值方式，因此 Edition
2023 的 Open 与 Opaque message 可覆盖 message、scalar、repeated、map、显式
presence 和 oneof 字段并成功编译；整条 message 的 `*` binding 保持原行为。

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
- `StrictSlash` 已弃用且不产生效果，路径清理和尾部斜杠重定向遵循标准库；
- `HandlePrefix` 使用路径段前缀语义，而不是任意字符串前缀；
- 未匹配请求不会落入进程级 `http.DefaultServeMux`；确有需要时应将其显式传给
  `NotFoundHandler`；
- method 不匹配时使用配置的 method-not-allowed handler，不依赖 Gorilla matcher
  注册顺序。
- 单路径段变量会对 slash 与 URL delimiter 做百分号编码；只有 AIP 多路径段模板
  保留结构性 slash；
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

- 本文只记录当前事实，不记录愿望；
- 待决上游变更写入 `docs/upstream-adoptions.md`；
- 可复现性能数据写入 `docs/design/performance.md` 或 `docs/benchmarks/`；
- 破坏性更新合并前必须提供迁移步骤；
- 变更提交后补充 implementation commit 与聚焦测试链接；
- 每次发布前重新核对比较日期与基线。
