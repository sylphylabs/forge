Translations: [English](README.md) | [简体中文](README_zh.md)

# Forge

Forge 是 [go-kratos/kratos](https://github.com/go-kratos/kratos) 的独立预发布分支，用于探索有意的破坏性更新、更快的标准库实现，以及更小的长期依赖面。本项目与 go-kratos 官方维护团队不存在隶属或背书关系。

项目目前处于 `v0` 开发阶段，不承诺稳定 API 或兼容性。迁移前请先阅读 [COMPATIBILITY_zh.md](COMPATIBILITY_zh.md)，源码基线和上游同步策略见 [UPSTREAM.md](UPSTREAM.md)。

## 功能特性

- 以 Protobuf 为中心定义 API，并生成 HTTP/gRPC 代码。
- 基于标准库 `http.ServeMux` 的路由，支持方法模式、路径参数和 Google AIP 模板。
- 统一的 Transport 抽象，支持 HTTP 和 gRPC。
- 协议中立的异步消息契约，broker 适配器按需引入。
- 可组合的 Middleware，覆盖 Recovery、Logging、Validation、Tracing、Metrics、Auth 等场景。
- 插件化的 Registry、Config 和 Encoding 能力。
- 基于标准库 `log/slog` 的日志能力，OpenTelemetry 扩展由 contrib 包提供。
- 统一的 Metadata、Errors、Validation、OpenAPI 和代码生成工作流。
- contrib 生态提供注册中心、配置中心、Middleware、编码和可观测性等可选集成。

## 安装

### 环境要求

- [Go](https://go.dev/dl/) 1.26 或更高版本；同时使用 Go 1.27 RC 做前向验证
- [protoc](https://github.com/protocolbuffers/protobuf)
- [protoc-gen-go](https://github.com/protocolbuffers/protobuf-go)
- [Buf](https://buf.build/) 或等效的 `protoc` 工作流

### 引入 Forge

```shell
go get github.com/sylphylabs/forge@main
```

Forge 有意不提供项目脚手架 CLI。项目创建、依赖升级和服务运行均使用
标准 Go 工具链。

公开 Buf plugin 发布前，可以从当前 checkout 构建三个原子化 Forge
Protobuf generator：

```shell
cd cmd
GOWORK=off go install ./protoc-gen-go-errors ./protoc-gen-go-http ./protoc-gen-go-middleware
```

## 生成并运行

使用项目自身的 Buf 或 `protoc` 配置生成代码，然后直接运行服务：

```shell
buf generate
go generate ./...
go run ./cmd/server -conf ./configs
```

## 使用示例

```go
package main

import (
	"github.com/sylphylabs/forge"
	"github.com/sylphylabs/forge/transport/grpc"
	"github.com/sylphylabs/forge/transport/http"
)

func main() {
	httpSrv := http.NewServer(http.Address(":8000"))
	grpcSrv := grpc.NewServer(grpc.Address(":9000"))

	app := forge.New(
		forge.Name("helloworld"),
		forge.Version("v1.0.0"),
		forge.Server(httpSrv, grpcSrv),
	)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
```

## 上游基线

Forge 以 Kratos v3 为起点。现有 Forge 用户应将 module path 变化及后续 Forge 版本视为显式迁移，而不是 Forge 的原地升级。

## 扩展阅读

- [兼容性契约](COMPATIBILITY_zh.md)
- [从 Kratos v3 迁移](docs/migration/kratos-to-forge_zh.md)
- [文档索引](docs/README.md)
- [上游基线与同步策略](UPSTREAM.md)
- [性能现代化记录](docs/design/performance.md)
- [上游变更吸收记录](docs/upstream-adoptions.md)
- [贡献指南](CONTRIBUTING.md)
- [上游 Kratos 文档](https://go-kratos.dev/zh-cn/docs/getting-started/start)（仅供参考，行为可能不同）

## 开发

```shell
make test
make lint
```

多 module 检查和 Go 1.27 RC 验证方式见 [DEVELOPMENT.md](DEVELOPMENT.md)。

## 安全

请通过 Forge 仓库的 GitHub Security Advisory 私密报告安全问题，不要将 Forge 特有的问题提交给上游 Kratos 项目。

## 致谢

Forge 保留了完整的 Kratos Git 历史和原始 MIT 版权声明。上游 Kratos 项目及其贡献者构建了本项目的基础。

以下项目对原始 Forge 设计有重要影响：

- [go-kit/kit](https://github.com/go-kit/kit)
- [go-micro](https://github.com/asim/go-micro)
- [google/go-cloud](https://github.com/google/go-cloud)
- [go-zero](https://github.com/zeromicro/go-zero)
- [beego](https://github.com/beego/beego)

## License

Forge 基于 [MIT license](./LICENSE) 开源。
