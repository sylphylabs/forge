# ADR-0003：MCP 传输经 `ToolHandlerMiddleware` 接入框架中间件

**状态：** Accepted
**日期：** 2026-08-07
**决策者：** 项目所有者
**关系：** 修正 [ADR-0002](./0002-transporter-describes-transport-commonality.md) 「未解决」一节中关于 `contrib/transport/mcp` 的判断。ADR-0002 的三项决策本身不变。

---

## Context

ADR-0002 写道：

> `contrib/transport/mcp` 的 `MiddlewareFunc` **不在本 ADR 范围内**。它是 `func(http.Handler) http.Handler`，属 mcp-go 库边界，收敛它等于重写该适配器。

**该判断是错的，且形成过程有方法论缺陷：它只读了 Forge 侧 117 行的适配器，未读 `mcp-go` 库本身提供了什么。** 结论「必须重复解析 JSON-RPC，或重写协议实现」因此是推测，而非核查所得。

### 实测更正

对 `github.com/mark3labs/mcp-go@v0.56.0`（`contrib/transport/mcp/go.mod` 所钉版本）在本机 module cache 检索，2026-08-07 `[一手数据]`：

**一、mcp-go 不是「JSON-RPC 薄壳」。** 26,952 行（不含测试）／73,883 行（含测试），其中 `server/` 11,008 行、`mcp/` 5,788 行、`client/` 5,891 行。它实现完整 MCP 协议面：tools / prompts / resources / tasks、SSE 会话管理、输入输出 schema 校验、elicitation、completion、CORS。

**二、它已提供形状匹配的中间件扩展点。**

```go
// mcp-go server/server.go:61,70
type ToolHandlerFunc       func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
type ToolHandlerMiddleware func(ToolHandlerFunc) ToolHandlerFunc
```

`ToolHandlerFunc` 与 Forge 的 `middleware.UnaryHandler`（`func(ctx, req any) (any, error)`）形状一致：ctx 进、请求进、响应出、error 出。注册入口为 `server.WithToolHandlerMiddleware`（`server.go:307`），中间件在 `server.go:1999-2005` 于调用时包裹 tool handler。

**三、tool 名可直接读取。** `request.Params.Name`（`mcp/tools.go:64-65`）即被调用的 tool 名 —— 正是 ADR-0002 重定义后 `Operation()` 该填的值。

因此 ADR-0002 所述的「边界」并不阻断接入：mcp-go 自己在边界内侧开了口。

**四、`hooks.go` 那条路确实不通**（此点原判断成立）。`OnBeforeCallTool`（`hooks.go:107`）签名为 `func(ctx, id, message)`，**无返回值**，只能观察不能注入 context。但 `ToolHandlerMiddleware` 绕开了该限制。

---

## Decision

**`contrib/transport/mcp` 实现 `Transporter`，并通过 `ToolHandlerMiddleware` 桥接框架的 `UnaryMiddleware`。**

### 1. `mcp.Transport` 实现 `transport.Transporter`

| 方法 | 取值 |
| --- | --- |
| `Kind()` | `KindMCP`（常量声明在 mcp 包内，核心不知情 —— 依 ADR-0002 的开放 `Kind`） |
| `Endpoint()` | 服务端 endpoint |
| `Operation()` | **被调用的 tool 名** |
| `RequestHeader()` | 原始 HTTP 请求头（`CallToolRequest.Header`） |

**不实现 `ReplyHeaderer`** —— tool 结果经已建立的 SSE 流返回，没有独立的可写响应头。有测试断言此点。

### 2. 桥接

`UnaryMiddleware(endpoint, m...)` 返回 `server.ToolHandlerMiddleware`：注入 `Transport` 到 context，把 `CallToolRequest` 作为 `req`、`*CallToolResult` 作为 `reply` 穿过 unary 链。

选项形式 `WithToolMiddleware(m...)` 传给 `NewServer`，在选项应用**之后**注册，以便读到可能被选项设置的 endpoint。

中间件若把 reply 替换成非 `*CallToolResult` 的值，返回 `ErrUnexpectedReply` —— **显式失败而非静默丢弃结果**。

### 3. 顺带修正 `WithMiddleware()` 的覆盖语义

原 `WithMiddleware(m MiddlewareFunc)` 是**赋值**（`s.middleware = m`），调用两次静默丢弃第一个。改为 variadic + append + nil 过滤，与 `transport/message` 的 `WithMiddleware` 一致，并明确「首个中间件为最外层」。

这是上游遗留的弱设计，非本次接入所必需，但在同一文件里留下两种相反的累积语义会持续误导使用者。

---

## Consequences

### 正面

- MCP tool 调用现在获得 T1 统一的全部 unary 中间件：日志、追踪、recovery、限流、metadata、validate
- `Operation()` 有了真实区分度（tool 名），而非所有调用共享同一 SSE 路径
- 验证了 ADR-0002 的 `Kind` 开放性：第二个仓外声明的 `Kind` 常量（继 `KindMessage` 之后）

### 负面与代价

- **`WithMiddleware()` 签名变更为 variadic**，属源码破坏性变更。单参数调用仍编译通过（README 示例未受影响），但显式取 `MiddlewareFunc` 类型的调用点需调整。模块未公开发布，代价为零。
- 桥接只覆盖 **tool 调用**。prompts、resources、tasks 各有独立的 `PromptHandlerMiddleware` / `ResourceHandlerMiddleware`（`server.go:73,83`），本次未接。**MUST NOT 宣称「MCP 全面接入框架中间件」** —— 只有 tools 接了。
- 中间件运行在 mcp-go 的 handler 层，而非 HTTP 层。因此它看不到未被路由到 tool 的请求（协议握手、SSE 建连）。需要覆盖那一层时仍用 `WithMiddleware()` 的 `http.Handler` 中间件 —— **两层各有职责，不是冗余。**

### 方法论教训

**评估「能否接入第三方库」MUST 先读该库提供的扩展点，MUST NOT 仅凭本方适配器代码推断。** ADR-0002 的错误判断即源于此，且当时以确定语气写入了 Accepted 文档。

### 未解决

- prompts / resources / tasks 三类 handler 的中间件是否一并桥接。形状与 tools 相同，工作量小，但**目前无使用者**，按需再做。
- `transport/message` 的私有 `Middleware`（签名带 `destination string` 一等参数）是否收敛，仍未决定，见 ADR-0002。
