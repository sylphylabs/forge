# ADR-0004：框架提供 `Principal`，不提供 `Tenant` 与 `Session`

**状态：** Accepted
**日期：** 2026-08-07
**决策者：** 项目所有者

---

## Context

框架收敛工作原本把三项「业务地基」打包处理：`Principal`（谁）、`Tenant`（哪个租户）、`Session`（哪个会话）。三者当时被打包处理，理由是全仓 grep 命中数均为 0。

**这个打包是错的。** 三者的性质不同，命中数为 0 只说明「没有」，不说明「该有」。

### 触发重审的提问

> 框架应该涉及到 tenant 吗？

该提问命中了 Forge 的定位约束：**MUST NOT 把某个下游应用的领域概念写进框架核心**。而 `Tenant` 恰恰是从一个多租户 SaaS 应用的需求倒推出来的 —— 属于该约束明令禁止的做法。

### 现状：身份处理确实缺一层

`contrib/middleware/jwt/jwt.go:179` 验证 token 后，把裸的 `jwt.Claims` 放进 context：

```go
func NewContext(ctx context.Context, info jwt.Claims) context.Context {
	return context.WithValue(ctx, authKey{}, info)
}
```

于是每个需要知道「谁在调用」的业务 handler 都要写三层断言：

```go
claims, ok := jwt.FromContext(ctx)   // jwt.Claims，一个 interface
mc, ok := claims.(jwt.MapClaims)     // 断言具体类型
sub, ok := mc["sub"].(string)        // 自己挖字段名
```

字段名靠约定，换认证方式（mTLS、API key、session cookie）需全部改写。**框架缺一个「调用者是谁」的抽象**，这一点成立。

### 外部先例

`google.golang.org/grpc@v1.82.1` 提供 `peer.Peer`（含 `AuthInfo credentials.AuthInfo`）作为传输层身份，**但不提供任何 tenant 概念**。`[一手数据]`（本机 module cache 实测）

这不是疏忽，而是分层：**身份是「谁连进来的」，属协议关心的事；租户是「这属于哪个客户」，属应用关心的事。**

---

## Decision

### 1. `Principal` 进核心

新增 `auth` 包，定义「本次调用的发起者」抽象，并提供 context 载体。它是接口而非结构体，因此 JWT、mTLS、API key、session cookie 都能实现。

放在核心（而非 contrib）的理由：`contrib/middleware/jwt` 是独立模块，若把 `Principal` 定义在其中，其他认证方式就要依赖 JWT 模块才能实现它 —— 这是错误的依赖方向。

新增独立的 `auth/` 包而非并入 `middleware/`：身份是被中间件*填充*、被业务代码*读取*的数据，不是中间件本身。

### 2. `Tenant` **不进**框架

多租户是**业务模型**，不是传输语义。租户标识可能来自 JWT claim、子域名、URL 路径、请求头，甚至需查库解析 —— 完全取决于应用如何建模。框架定义 `Tenant` 等于替使用者做模型决策。

对下一个基于 Forge 的单租户项目而言，它是死重量。

**框架提供机制，应用提供策略**：框架给 `Principal` 与既有的 metadata 传播；应用自行定义 `Tenant`，从 `Principal` 的属性中取值，写自己的中间件。

### 3. `Session` **不进**框架（至少现在）

各传输已各自管理其会话生命周期：gRPC 有流、WebSocket 有连接、MCP 有 SSE 会话。目前**找不到一个不依赖具体协议的通用形态** —— 再抽一层有无中生有之嫌。

推迟至某个具体传输 contrib 真正需要它时，依其实际需要决定：留在该 contrib 模块内，还是上提核心。

### 4. 那条安全规则改属应用

原计划写道：「这三者 MUST 只能由受信中间件写入，MUST NOT 由入站 metadata 填充」——`middleware/metadata` 按 `x-md-` 前缀转发入站键，租户 ID 若走该路径即构成跨租户伪造。

该规则**依然成立**，但它约束的是**应用的租户中间件**，不是框架契约。框架侧对应的责任只有一条：`Principal` MUST 由认证中间件写入，MUST NOT 从入站 metadata 反序列化。

---

## Consequences

### 正面

- 业务代码读「调用者是谁」不再需要三层类型断言与字段名约定
- 认证方式可替换：换 mTLS 或 API key 时业务代码不动
- 框架不承载任何多租户假设，对单租户项目零负担

### 负面与代价

- `contrib/middleware/jwt` 需同时产出 `Principal` 与既有的 `jwt.Claims`。**保留后者**：`Claims` 携带 JWT 特有信息（如自定义 claim），`Principal` 只暴露通用面。两者并存不是冗余，是不同抽象层级。
- `Principal` 接口的方法集是**猜测**：目前只有 JWT 一个实现，无法验证它对 mTLS、API key 是否够用。故接口 MUST 保持最小 —— 加方法容易，减方法是破坏性变更。
- 应用需自己实现 tenant 解析与传播。这是**有意的成本转移**，不是遗漏。

### 未解决

- `Principal` 是否需要 `Scopes()` / `Roles()` 之类的授权信息面。**本 ADR 不决定** —— 授权（谁能做什么）与认证（谁在调用）是两件事，且框架是否该提供授权层尚未决定。`internal/operationpolicy/` 空目录与被删除的 `policy/v1` 原型表明此处曾有过尝试并被放弃。
- 客户端侧是否需要对应的 `Principal` 传播（服务间调用时转发调用者身份）。目前只做服务端。

### 触发重新评估的条件

- 出现第二、第三个 `Principal` 实现（mTLS、API key），届时可验证接口方法集是否够用
- 框架决定提供授权层，届时 `Principal` 可能需要扩展
- 有多个基于 Forge 的项目都需要多租户，届时可判断 tenant 是否真有可复用的通用形态
