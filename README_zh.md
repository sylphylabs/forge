Translations: [English](README.md) | [简体中文](README_zh.md)

# OpenKratos

OpenKratos 是 [go-kratos/kratos](https://github.com/go-kratos/kratos) 的独立预发布分支，用于探索有意的破坏性更新、更快的标准库实现，以及更小的长期依赖面。本项目与 go-kratos 官方维护团队不存在隶属或背书关系。

项目目前处于 `v0` 开发阶段，不承诺稳定 API 或兼容性。源码基线和上游同步策略见 [UPSTREAM.md](UPSTREAM.md)。

## 功能特性

- 以 Protobuf 为中心定义 API，并生成 HTTP/gRPC 代码。
- 基于标准库 `http.ServeMux` 的路由，支持方法模式、路径参数和 Google AIP 模板。
- 统一的 [Transport](https://go-kratos.dev/zh-cn/docs/component/transport/overview) 抽象，支持 [HTTP](https://go-kratos.dev/zh-cn/docs/component/transport/http) 和 [gRPC](https://go-kratos.dev/zh-cn/docs/component/transport/grpc)。
- 可组合的 [Middleware](https://go-kratos.dev/zh-cn/docs/component/middleware/overview)，覆盖 Recovery、Logging、Validation、Tracing、Metrics、Auth 等场景。
- 插件化的 [Registry](https://go-kratos.dev/zh-cn/docs/component/registry)、[Config](https://go-kratos.dev/zh-cn/docs/component/config) 和 [Encoding](https://go-kratos.dev/zh-cn/docs/component/encoding) 能力。
- 基于标准库 `log/slog` 的日志能力，OpenTelemetry 扩展由 contrib 包提供。
- 统一的 Metadata、Errors、Validation、OpenAPI 和代码生成工作流。
- contrib 生态提供注册中心、配置中心、Middleware、编码和可观测性等可选集成。

## 安装

### 环境要求

- [Go](https://go.dev/dl/) 1.26 或更高版本；同时使用 Go 1.27 RC 做前向验证
- [protoc](https://github.com/protocolbuffers/protobuf)
- [protoc-gen-go](https://github.com/protocolbuffers/protobuf-go)

### 安装 CLI

```shell
go install github.com/openkratos/kratos/cmd/kratos@latest
kratos upgrade
```

## 创建服务

```shell
kratos new helloworld
cd helloworld
go mod tidy
kratos run
```

服务启动后访问 `http://localhost:8000/helloworld/kratos`。

如果需要从 proto 开始生成服务代码：

```shell
kratos proto add api/helloworld/helloworld.proto
kratos proto client api/helloworld/helloworld.proto
kratos proto server api/helloworld/helloworld.proto -t internal/service
go generate ./...
kratos run
```

## 使用示例

```go
package main

import (
	"github.com/openkratos/kratos"
	"github.com/openkratos/kratos/transport/grpc"
	"github.com/openkratos/kratos/transport/http"
)

func main() {
	httpSrv := http.NewServer(http.Address(":8000"))
	grpcSrv := grpc.NewServer(grpc.Address(":9000"))

	app := kratos.New(
		kratos.Name("helloworld"),
		kratos.Version("v1.0.0"),
		kratos.Server(httpSrv, grpcSrv),
	)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
```

## 上游基线

OpenKratos 以 Kratos v3 为起点。现有 Kratos 用户应将 module path 变化及后续 OpenKratos 版本视为显式迁移，而不是 Kratos 的原地升级。

## 扩展阅读

- [文档](https://go-kratos.dev/zh-cn/docs/getting-started/start)
- [示例](https://github.com/go-kratos/examples)
- [项目模板](https://github.com/go-kratos/kratos-layout)
- [上游基线与同步策略](UPSTREAM.md)
- [上游 v2 到 v3 迁移指南](docs/migration/v2-to-v3_zh.md)
- [贡献指南](https://go-kratos.dev/zh-cn/docs/community/contribution)

## 开发

```shell
make test
make lint
```

多 module 检查和 Go 1.27 RC 验证方式见 [DEVELOPMENT.md](DEVELOPMENT.md)。

## 安全

请通过 OpenKratos 仓库的 GitHub Security Advisory 私密报告安全问题，不要将 OpenKratos 特有的问题提交给上游 Kratos 项目。

## 致谢

OpenKratos 保留了完整的 Kratos Git 历史和原始 MIT 版权声明。上游 Kratos 项目及其贡献者构建了本项目的基础。

以下项目对原始 Kratos 设计有重要影响：

- [go-kit/kit](https://github.com/go-kit/kit)
- [go-micro](https://github.com/asim/go-micro)
- [google/go-cloud](https://github.com/google/go-cloud)
- [go-zero](https://github.com/zeromicro/go-zero)
- [beego](https://github.com/beego/beego)

## License

OpenKratos 基于 [MIT license](./LICENSE) 开源。
