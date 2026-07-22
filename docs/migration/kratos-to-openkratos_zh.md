# 从 Kratos v3 迁移到 OpenKratos

OpenKratos 是预发布的独立 fork，并不是 Kratos 的原地升级版本。请在独立分支中
执行迁移，并在修改依赖前阅读 [`COMPATIBILITY.md`](../../COMPATIBILITY.md)。

## 1. 建立迁移基线

修改导入路径前先确认当前项目能够通过验证：

```shell
go test ./...
go vet ./...
```

提交或单独保存生成代码与 `go.mod`，避免迁移差异和无关改动混在一起。
OpenKratos 要求 Go 1.26，因此应先完成工具链升级。

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

## 6. 检查流式请求超时

HTTP SSE 与 WebSocket stream 不会再被 unary server timeout 终止。如果业务
要求最大连接时长、读写超时或空闲超时，需要显式配置相应策略。

## 7. 保留 Kratos v3 已完成的迁移

OpenKratos 继续使用 Kratos v3 的 `log/slog` 日志模型、兼容标准库的 errors，
以及相互独立的 `json` 与 `protojson` codec。已经使用 Kratos v3 的服务不应
撤销这些迁移。

当前 HTTP generator 仍不支持 protobuf Editions 和 Opaque API。在
`COMPATIBILITY.md` 将其记录为已实现之前，应继续使用受支持的 protobuf API。

## 8. 验证迁移

先生成代码，再运行测试，避免生成文件中残留旧导入路径：

```shell
buf generate
go mod tidy
go test -race ./...
go vet ./...
```

服务集成测试还应覆盖 HTTP 路由、stream、服务发现、配置重载和优雅退出。

## 检查清单

- [ ] 升级到 Go 1.26 或更高版本。
- [ ] 替换 Kratos v3 根 module 路径。
- [ ] 替换每个使用到的 contrib module，并移除 `/v3`。
- [ ] 固定 OpenKratos generator 版本。
- [ ] 从源文件重新生成所有 Go 代码。
- [ ] 使用 Go 与 Buf 命令替代 `kratos` CLI。
- [ ] 检查路由优先级、冲突、prefix、斜杠、404 与 405。
- [ ] 为 HTTP stream 定义显式生命周期策略。
- [ ] 运行 race test、vet 和服务集成测试。
- [ ] 发布前再次检查 `COMPATIBILITY.md`。
