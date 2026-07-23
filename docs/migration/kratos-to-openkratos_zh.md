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

在 Buf 配置或工具依赖中固定 OpenKratos 生成器版本：

```text
github.com/openkratos/kratos/cmd/protoc-gen-go-http
github.com/openkratos/kratos/cmd/protoc-gen-go-errors
```

修改路径后重新生成 HTTP client、HTTP server 和错误辅助代码：

```shell
buf generate
go generate ./...
go mod tidy
```

不要通过全局文本替换修改 `.pb.go`。应修改 `.proto` 源文件和生成配置，然后重新
生成代码。

当前生成的 HTTP 文件会断言 `transport/http.SupportPackageIsVersion4`，从而在
编译期发现“新 generator 搭配旧 runtime”的错误组合。仍断言 version 3 的旧生成
文件可以继续编译，但会在每次请求中解析固定路径模板。必须重新生成 client 与
server 才能使用预编译路径实现；只升级 runtime module 不会改写已有生成代码。

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

跨多段路径的 Gorilla 正则应改写为 Google AIP 模板。`StrictSlash` 不再改变
行为。如果服务有意使用 `http.DefaultServeMux` 兜底，需要通过
`NotFoundHandler` 显式传入。

## 6. 检查 Google HTTP Transcoding

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

## 7. 检查流式请求超时

HTTP SSE 与 WebSocket stream 不会再被 unary server timeout 终止。如果业务
要求最大连接时长、读写超时或空闲超时，需要显式配置相应策略。

## 8. 保留 Kratos v3 已完成的迁移

OpenKratos 继续使用 Kratos v3 的 `log/slog` 日志模型、兼容标准库的 errors，
以及相互独立的 `json` 与 `protojson` codec。已经使用 Kratos v3 的服务不应
撤销这些迁移。

HTTP generator 已支持 Edition 2023 Open/Opaque API。应从 schema 重新生成，
不要继续保留上游 generator 产生的旧代码。

## 9. 验证迁移

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
- [ ] 确认生成的 HTTP 文件断言 `SupportPackageIsVersion4`。
- [ ] 使用 Go 与 Buf 命令替代 `kratos` CLI。
- [ ] 检查路由优先级、冲突、prefix、斜杠、404 与 405。
- [ ] 重新生成并测试每个 inline `google.api.HttpRule` binding。
- [ ] 动态模板继续使用 `BuildPath`；重复使用的固定模板只编译一次。
- [ ] 检查 body/query 分类、ProtoJSON wire value 和 `%2F` 路径。
- [ ] 为 HTTP stream 定义显式生命周期策略。
- [ ] 运行 race test、vet 和服务集成测试。
- [ ] 发布前再次检查 `COMPATIBILITY.md`。
