# 从 Kratos v3 迁移到 OpenKratos

OpenKratos 是预发布的独立 fork，并不是 Kratos 的原地升级版本。请在独立分支中
执行迁移，并在修改依赖前阅读 [`COMPATIBILITY.md`](../../COMPATIBILITY.md)。
OpenKratos 可能直接替换 Kratos API，而不是保留兼容 shim；每项已接受的移除都会
在本文记录替代方案与验证步骤。

## 1. 建立迁移基线

修改导入路径前先确认当前项目能够通过验证：

```shell
go test ./...
go vet ./...
```

提交或单独保存生成代码与 `go.mod`，避免迁移差异和无关改动混在一起。
OpenKratos 要求 Go 1.27，因此应先完成工具链升级。

## 2. 替换 Module 路径

将根导入前缀从：

```text
github.com/go-kratos/kratos/v3
```

替换为：

```text
github.com/openkratos/kratos
```

每个使用到的 contrib module 也需要单独替换。OpenKratos 当前使用 v0 版本线，
因此路径不包含上游的 `/v3`：

```text
github.com/go-kratos/kratos/contrib/middleware/jwt/v3
github.com/openkratos/kratos/contrib/middleware/jwt
```

预发布阶段可以从 `main` 解析根 module：

```shell
go get github.com/openkratos/kratos@main
go mod tidy
```

正式发布 tag 后应改用明确版本，不应长期依赖 `main`。

如果应用直接使用 SDK 类型构造 contrib adapter，需要同步修改 provider import：

```text
github.com/hashicorp/consul/api
github.com/hashicorp/consul/api/v2

github.com/nacos-group/nacos-sdk-go/clients/config_client
github.com/nacos-group/nacos-sdk-go/v2/clients/config_client
```

Apollo integration 使用 `github.com/apolloconfig/agollo/v5`。OpenKratos 使用
Go 1.27 标准库 `uuid`，不再直接使用 Google 或 gofrs UUID 类型。默认 application
ID 从 UUIDv1 改为 UUIDv4；如果其值或 version 属于运维契约，应显式配置 ID。

## 3. 更新代码生成器

OpenKratos 将公开错误 descriptor 统一放在独立的
`github.com/openkratos/api` module 中。runtime 对外发送和接收的仍然是
`{code, reason, message, metadata}` 四字段错误体，不会用
`google.rpc.Status` 替换它。

大多数业务通过 `errors.New`、`errors.Newf` 或生成的错误辅助函数构造错误，不需要
修改 runtime 调用。只有显式引用旧生成消息类型的代码需要更新 import 和类型：

```text
github.com/go-kratos/kratos/v3/errors.Status
github.com/openkratos/api/errors/v1.Status
```

API import path `github.com/openkratos/api/errors/v1` 声明的 package 名是
`errors`。runtime 内部将其 alias 为 `errorapi`，并在 `errors.Error` 中嵌入
`errorapi.Status`，因此 `err.Code`、`err.Reason`、`err.Message` 与
`err.Metadata` 等字段访问保持不变。待 OpenKratos API module 发布后，还需更新
`.proto` import 与 enum annotation；不应继续保留或 vendor 继承自 Kratos 的任一份
`errors/errors.proto`。

schema 的具体改写如下：

```proto
// 修改前
import "errors/errors.proto";
enum ErrorReason {
  option (errors.default_code) = 500;
  ERROR_REASON_UNSPECIFIED = 0;
}

// OpenKratos
import "openkratos/errors/v1/errors.proto";
enum ErrorReason {
  option (openkratos.errors.v1.default_code) = 500;
  ERROR_REASON_UNSPECIFIED = 0;
}
```

enum value override 同样需要从 `(errors.code)` 改为
`(openkratos.errors.v1.code)`。修改源文件后应重新生成 helper；generator 不再携带
旧 annotation 的私有 fallback descriptor。

用三个原子化 OpenKratos 命令替换继承来的 generator module。本地开发期间它们
共享一个源码 module：

```text
github.com/openkratos/kratos/cmd
```

errors 与 HTTP 生成保持独立可选；只有应用使用生成的 service plan 时才加入
middleware 生成：

```yaml
# 修改前
plugins:
  - local: protoc-gen-go-http
    out: gen/go
    opt: paths=source_relative,omitempty=true
  - local: protoc-gen-go-errors
    out: gen/go
    opt: paths=source_relative

# OpenKratos 本地切换
plugins:
  - local: protoc-gen-go-errors
    out: gen/go
    opt: paths=source_relative
  - local: protoc-gen-go-http
    out: gen/go
    opt: paths=source_relative,omitempty=true
  - local: protoc-gen-go-middleware
    out: gen/go
    opt: paths=source_relative,http=annotated,grpc=true
```

HTTP plugin 保留原 option 名称。middleware 的 HTTP 方法集合必须与 HTTP binding
策略一致：

| `go-http` | 对应的 `go-middleware` |
| --- | --- |
| `omitempty=true` | `http=annotated` |
| `omitempty=false` | `http=all` |

不需要 HTTP wrapper 时省略 middleware 的 `http` option。只有同一生成流程也运行
`protoc-gen-go-grpc` 时才设置 `grpc=true`。三类输出分别是
`_errors.pb.go`、`_http.pb.go` 和 `_middleware.pb.go`；检查生成 diff 前应删除旧的
`_openkratos.pb.go`。项目不保留 `protoc-gen-go-openkratos` 转发命令或
`--go-openkratos_out` flag。

三个 plugin 公开后，已发布项目应把本地 entry 替换为固定 revision 的
`buf.build/openkratos/go-errors`、`go-http` 和 `go-middleware`，不得使用未固定的
开发 revision。

```shell
buf generate
go generate ./...
go mod tidy
```

不要通过全局文本替换修改 `.pb.go`。应修改 `.proto` 源文件和生成配置，然后重新
生成代码。

当前生成的 HTTP 文件会断言 `transport/http.SupportPackageIsVersion5`，从而在
编译期发现“新 generator 搭配旧 runtime”的错误组合。Version 3 和 4 sentinel
已经不再导出。升级 runtime 前必须重新生成 client 与 server；只升级 runtime
module 不会改写已有生成代码。

## 4. 替换 Kratos CLI 工作流

OpenKratos 不提供通用的 `kratos` 可执行文件。

| 原工作流 | OpenKratos 工作流 |
| --- | --- |
| `kratos new` | 创建普通 Go module，或使用经过审查的仓库模板 |
| `kratos run` | 使用 `go run` 启动服务 |
| `kratos proto ...` | 使用项目自身的 Buf 或 `protoc` 流程 |
| `kratos upgrade` | 使用 Go module 工具链 |

传统 Kratos 项目布局通常可以直接运行：

```shell
go run ./cmd/server -conf ./configs
```

## 5. 检查 HTTP 路由

OpenKratos 使用标准库 `http.ServeMux` 的优先级规则，不再依赖 Gorilla mux 的
注册顺序。应为以下行为增加测试：

- 重叠的 literal 与变量路由；
- 注册时会 panic 的冲突路由；
- 尾部斜杠与路径清理；
- 自定义正则；
- prefix handler；
- 预期的 404 与 405 响应。

跨多段路径的 Gorilla 正则应改写为 Google AIP 模板。从 server 构造中删除
`http.StrictSlash(...)`；OpenKratos 遵循 `http.ServeMux` 的路径清理和尾部斜杠
行为。如果服务有意使用 `http.DefaultServeMux` 兜底，需要通过 `NotFoundHandler`
显式传入。

## 6. 迁移 Server Middleware

OpenKratos 直接移除 server 侧 selector API，不保留运行时兼容路径。先完成以下
机械重命名：

| Kratos 名称 | OpenKratos 名称 |
| --- | --- |
| `middleware.Handler` | `middleware.UnaryHandler` |
| `middleware.Middleware` | `middleware.UnaryMiddleware` |
| `middleware.Chain` | `middleware.ChainUnary` |

删除 `http.Middleware`、`grpc.Middleware`、`grpc.StreamMiddleware`、
`Server.Use`、`Server.WrapMiddleware` 与 `http.Context.Middleware`。把每个
selector 精确展开为对应的 protobuf method，再将 middleware 直接写入生成的
service plan：

```go
plan := pb.GreeterMiddleware{
	Unary: []middleware.UnaryMiddleware{
		recovery.Recovery(),
		logging.Server(logger),
	},
	Methods: pb.GreeterMethodMiddleware{
		SayHello: []middleware.UnaryMiddleware{authorizeSayHello},
	},
}

httpService, err := pb.WrapGreeterHTTPServer(service, plan)
if err != nil {
	return err
}
pb.RegisterGreeterHTTPServer(httpServer, httpService)

grpcService, err := pb.WrapGreeterGRPCServer(service, plan)
if err != nil {
	return err
}
pb.RegisterGreeterServer(grpcServer, grpcService)
```

`Unary` 与 `Stream` 作用于整个 service；`Methods` 下的字段追加 method 专属
middleware。切片第一项位于最外层。Wrapper 构造时会拒绝 nil middleware 或 nil
handler，并快照所有切片，因此随后修改 plan 不会影响已构造的 wrapper。

完整 stream 生命周期行为应迁移到 `middleware.StreamMiddleware`。它围绕整个
stream 只执行一次；需要观察每次 `SendMsg` 或 `RecvMsg` 时，应装饰
`middleware.ServerStream`。旧 gRPC stream middleware 路径只构造 handler chain
却没有调用它，因此迁移后的生命周期 middleware 会真正执行；对比行为时应将其
视为正确性修复。

原始 HTTP request/response 行为应放入 `http.Filter`。gRPC 原生 metadata、peer、
status、compression、header 或 trailer 行为应放入 `grpc.UnaryInterceptor`、
`grpc.StreamInterceptor` 或 `grpc.Options`。这些 transport 原生层位于生成式
service middleware 外层。

`middleware/selector.Server` 随 server selector 路径一并移除；client 侧仍可用
`middleware/selector.Client` 按 operation 选择 middleware。

## 7. 检查 Google HTTP Transcoding

重新生成所有 HTTP client 与 server。OpenKratos 会更严格地校验 inline unary
`google.api.HttpRule`；原 generator 仅警告或推迟到运行期处理的 schema 现在可能
直接生成失败。

- 删除 `response_body: "*"`；省略 `response_body` 即表示整条响应。
- `body` 与 `response_body` 必须引用顶层字段。
- 不要让 map 或 repeated message 落入 query；应将其绑定到 body，或重新设计请求。
- 删除嵌套 `additional_bindings` 以及重复或冲突的路由匹配集合。
- Google JSON endpoint 对外使用 `application/json`，但 wire value 遵循 protobuf
  JSON，例如 64 位整数使用字符串，bytes 使用标准 base64。
- 生成 client 只使用 primary binding；additional binding 是 server 提供给原始
  REST client 的替代入口。

生成 client 会为每个固定 binding 编译一次并复用并发安全的 `CompiledPath`；路径
展开错误仍会在网络 I/O 前返回。

手写的 `transport/http.BuildPath` 调用必须处理 error。模板本身需要动态选择时，
继续使用这个 API：

```go
path, err := http.BuildPath(pattern, request, http.WithQueryParams())
if err != nil {
	return err
}
```

对于重复使用的固定模板，应将逐请求编译：

```go
// 迁移前：每次调用都解析并校验同一个模板。
path, err := http.BuildPath(
	"/v1/users/{name}", request, http.WithQueryParams(),
)
```

替换为可复用的路径计划：

```go
var userPath = http.MustCompilePath(
	"/v1/users/{name}", new(pb.GetUserRequest), http.WithQueryParams(),
)

path, err := userPath.Build(request)
```

如果非法配置应作为 error 从 constructor 或初始化流程返回，应使用 `CompilePath`；
程序自身拥有的 literal 模板和生成代码可以使用 `MustCompilePath`。

对于 primary `custom.kind: "*"`，生成 client 无法推导 HTTP method；对于 primary
路径中的裸 `*`/`**`，生成 client 也没有可取值的请求字段。这两类调用会在网络
I/O 前分别返回 `ErrUnspecifiedHTTPMethod` 或 `ErrUnboundPathWildcard`。需要生成
Go client 时，应使用具体 primary rule，并把含糊规则放到 additional binding。

编码后的 slash 涉及安全边界。请为 resource-name 路径增加集成测试：多路径段
变量保留 `%2F`/`%2f`，单路径段变量则完整解码。

外部 `google.api.Service` YAML 与 `fully_decode_reserved_expansion` 目前尚未支持。

## 8. 检查流式请求超时

HTTP SSE 与 WebSocket stream 不会再被 unary server timeout 终止。如果业务
要求最大连接时长、读写超时或空闲超时，需要显式配置相应策略。

## 9. 保留 Kratos v3 已完成的迁移

OpenKratos 继续使用 Kratos v3 的 `log/slog` 日志模型、兼容标准库的 errors，
以及相互独立的 `json` 与 `protojson` codec。已经使用 Kratos v3 的服务不应
撤销这些迁移。

HTTP generator 已支持 Edition 2023 Open/Opaque API。应从 schema 重新生成，
不要继续保留上游 generator 产生的旧代码。

## 10. 替换旧 Metrics Middleware

OpenKratos 直接移除继承的通用 metrics middleware，不提供兼容 shim，也不双发旧
指标。HTTP 应在原生 filter 与 `RoundTripper` 边界安装：

```go
serverMetrics, err := metrics.NewHTTPServerFilter(provider)
if err != nil {
	return err
}
server := kratoshttp.NewServer(kratoshttp.Filter(serverMetrics))

clientMetrics, err := metrics.NewHTTPClientWrapper(provider)
if err != nil {
	return err
}
client, err := kratoshttp.NewClient(
	ctx,
	kratoshttp.WithEndpoint(endpoint),
	kratoshttp.WithRoundTripperWrapper(clientMetrics),
)
```

MeterProvider 是必填依赖。需要显式关闭指标时应传入
`metric/noop.NewMeterProvider()`；package 不读取 global provider，也不会静默
安装 no-op provider。

gRPC 直接使用 grpc-go 的 gRFC A66 实现：

```go
metricSet := grpcstats.NewMetricSet(
	grpcotel.ClientCallDurationMetricName,
	grpcotel.ClientAttemptDurationMetricName,
	grpcotel.ServerCallDurationMetricName,
)
otelOptions := grpcotel.Options{
	MetricsOptions: grpcotel.MetricsOptions{
		MeterProvider: provider,
		Metrics:       metricSet,
	},
}

server := kratosgrpc.NewServer(
	kratosgrpc.Options(grpcotel.ServerOption(otelOptions)),
)
conn, err := kratosgrpc.NewClient(
	ctx,
	kratosgrpc.WithOptions(grpcotel.DialOption(otelOptions)),
)
```

不要让 `Metrics` 保持 nil，也不要使用 `DefaultMetrics()`；两者都会启用 started、
message size 指标，并可能在 grpc-go 升级后自动采用新的默认项。已有 tracing 时让
`TraceOptions` 保持零值，否则可能产生重复 span。

旧 API 对应关系如下：

| Kratos API | OpenKratos 替代方式 |
| --- | --- |
| `metrics.Server(...)` | HTTP 使用 `metrics.NewHTTPServerFilter(provider, ...)`；gRPC 使用 `grpcotel.ServerOption` |
| `metrics.Client(...)` | HTTP 使用 `metrics.NewHTTPClientWrapper(provider, ...)`；gRPC 使用 `grpcotel.DialOption` |
| `Option`、`WithRequests`、`WithSeconds` | 已移除；各 transport constructor 拥有固定 instrument 集合 |
| `DefaultRequestsCounter` | 使用 duration histogram 的 count |
| `DefaultSecondsHistogram` 与各 `Default*` 名称 | 固定的 HTTP semconv 或 gRPC A66 instrument |
| `DefaultSecondsHistogramView` | 应用 SDK View |
| `EnableOTELExemplar` | 应用配置中的 `sdkmetric.WithExemplarFilter(...)` 或 `OTEL_METRICS_EXEMPLAR_FILTER` |

旧通用 stream 按 transport 拆分；应根据旧 `kind` 的值选择对应行：

| 旧 instrument | HTTP 替代 | gRPC 替代 |
| --- | --- | --- |
| `server_requests_seconds` | `http.server.request.duration` | `grpc.server.call.duration` |
| `client_requests_seconds` | `http.client.request.duration` | `grpc.client.call.duration` |
| `server_requests_code_total` | `http.server.request.duration` count | `grpc.server.call.duration` count |
| `client_requests_code_total` | `http.client.request.duration` count | `grpc.client.call.duration` count |

旧 gRPC client middleware 每个逻辑 unary call 只执行一次，因此 rate、错误率与
延迟 SLO 应迁移到 `grpc.client.call.duration`。A66 的
`grpc.client.attempt.duration` 是新增的 retry/hedging 诊断信号：一个逻辑
call 可以产生多个 attempt，因此 attempt count 不是等价替代。A66 还新增了
旧 unary middleware 没有的完整 stream 生命周期覆盖。

使用标准 Prometheus 名称转换时，duration stream 通常导出为：

| OTel instrument | Prometheus histogram 基础名称 |
| --- | --- |
| `http.server.request.duration` | `http_server_request_duration_seconds` |
| `http.client.request.duration` | `http_client_request_duration_seconds` |
| `grpc.server.call.duration` | `grpc_server_call_duration_seconds` |
| `grpc.client.call.duration` | `grpc_client_call_duration_seconds` |
| `grpc.client.attempt.duration` | `grpc_client_attempt_duration_seconds` |

Prometheus histogram 会为基础名称增加 `_bucket`、`_sum` 与 `_count` 后缀。原
counter rate 查询应改为对应的 `_count` 时序，并且只保留有界的 semantic
attributes。Exporter 或 Collector 的转换配置可能改变最终名称，因此修改 dashboard
和 alert 前必须检查实际 scrape 输出。

旧 `kind`、`operation`、`code` 与 `reason` 不会整体迁移。请使用各协议的标准
method、route/target、status 与 `error.type` 属性。`reason` 没有指标替代项；详细
业务失败原因应保留在 trace 和 log。自定义 histogram bucket 应由 SDK View 配置，
exemplar 策略应配置在 SDK MeterProvider 上。

HTTP 计时边界也发生变化。Server duration 覆盖完整 `ServeHTTP` 调用；Client
duration 在收到 response header 或 transport 失败时结束，不再包含 response body
读取和 Kratos decoder。Redirect 的每次 `RoundTrip` 独立计量。迁移 latency SLO
时不能把新旧 client 时序视为等价数据。

完整的属性、状态、路由、基数与生命周期合同见
[`docs/design/otel-metrics.md`](../design/otel-metrics.md)。

## 11. 验证迁移

先生成代码，再运行测试，避免生成文件中残留旧导入路径：

```shell
buf generate
go mod tidy
go test -race ./...
go vet ./...
```

服务集成测试还应覆盖 HTTP 路由、stream、服务发现、配置重载和优雅退出。

## 检查清单

- [ ] 升级到 Go 1.27 或更高版本。
- [ ] 替换 Kratos v3 根 module 路径。
- [ ] 替换每个使用到的 contrib module，并移除 `/v3`。
- [ ] 更新 Consul、Nacos 与 Apollo provider SDK import path。
- [ ] 替换直接使用的 Google/gofrs UUID import，并检查 application ID version 变化。
- [ ] 固定 OpenKratos generator 版本。
- [ ] 从源文件重新生成所有 Go 代码。
- [ ] 确认生成的 HTTP 文件断言 `SupportPackageIsVersion5`。
- [ ] 使用 Go 与 Buf 命令替代 `kratos` CLI。
- [ ] 检查路由优先级、冲突、prefix、斜杠、404 与 405。
- [ ] 重命名 unary middleware 类型并重新生成 service middleware plan。
- [ ] 用生成的 method 字段与 wrapper 替换 server selector。
- [ ] 将 stream 生命周期行为迁移到 `StreamMiddleware`，按消息行为通过装饰 `ServerStream` 实现。
- [ ] 将 transport 原生行为迁移到 HTTP filter 或 gRPC interceptor。
- [ ] 重新生成并测试每个 inline `google.api.HttpRule` binding。
- [ ] 动态模板继续使用 `BuildPath`；重复使用的固定模板只编译一次。
- [ ] 检查 body/query 分类、ProtoJSON wire value 和 `%2F` 路径。
- [ ] 为 HTTP stream 定义显式生命周期策略。
- [ ] 用 HTTP filter/wrapper 与显式 gRPC A66 metric set 替换通用 metrics middleware。
- [ ] 将 counter 查询改为 histogram `_count`，并移除依赖 `reason` 的指标维度。
- [ ] 根据实际 exporter 输出验证 Prometheus 名称、bucket、exemplar、dashboard 与 alert。
- [ ] 运行 race test、vet 和服务集成测试。
- [ ] 发布前再次检查 `COMPATIBILITY.md`。
