# ADR-0005：session 认证只定义契约，不提供 Store

**状态：** Accepted
**日期：** 2026-08-07
**决策者：** 项目所有者
**关系：** 补强 [ADR-0004](./0004-principal-in-core-tenant-in-application.md) —— 其「未解决」一节指出 `Principal` 接口方法集只有 JWT 一个实现、无法验证；本 ADR 提供了第二个实现。

---

## Context

框架此前只有 token-based 认证（`contrib/middleware/jwt`）。session-based 认证是常见的第二种形态，且性质不同：**JWT 靠验签，session 靠查库，因此 session 可以被吊销** —— 服务端持有记录，而非仅验证签名。

### 一个先前判断的空缺已被填补

ADR-0004 把 `Principal` 放进核心，理由是「让 mTLS / API key / session cookie 无需依赖 JWT 模块即可实现」。但当时**只有 JWT 一个实现**，该理由是预期而非事实。

期间曾出现一个合理质疑：**`auth` 包在框架内零消费**（`transport/`、`middleware/`、`log/` 均不引用它），看起来像可以下放到业务层的孤岛。`[一手数据]`

session 中间件的加入解决了这个疑问：**两个互不依赖的 contrib 模块现在产出同一个 `Principal`**，业务代码无需知道凭证是 JWT 还是 session。这正是接口必须位于两者共同上游的论据 —— 该论据现在有实据，不再是预期。

### gorilla/sessions 不能直接用

`github.com/gorilla/sessions`（v1.4.0 为最新）`[一手数据]` 的 `Store` 签名为：

```go
Get(r *http.Request, name string) (*Session, error)
Save(r *http.Request, w http.ResponseWriter, s *Session) error
```

**它绑死了 `*http.Request` 与 `http.ResponseWriter`。** Forge 中间件运行于 `transport.Transporter` 抽象之上，gRPC、message、MCP 均无 `*http.Request`。照搬会使 session 中间件只能用于 HTTP —— 正是 ADR-0002 刚修正的那类错误：用一种传输的形状定义所有传输的契约。

其 `Options`（`Secure` / `HttpOnly` / `SameSite`）与 `MaxAge` 三态语义值得借鉴，但接口形状不能照搬。

---

## Decision

### 1. 新增 `contrib/middleware/session`，契约传输中立

```go
type Store interface {
	Load(ctx context.Context, id string) (*Session, error)
	Save(ctx context.Context, s *Session) error
	Delete(ctx context.Context, id string) error
}
```

签名不提任何传输。凭证从 `transport.Transporter` 的 `RequestHeader()` 读取，默认读 `session_id` cookie 并**回退到同名 header** —— 该回退使 gRPC 等基于 metadata 的传输无需额外配置即可携带同一凭证。

提供 `Server()`（unary）与 `ServerStream()`（流）。流在**开启时解析一次** session；中途过期不打断已建立的流，与 metadata 中间件的处理一致。

### 2. **不提供任何 Store 实现**

包内只有契约与中间件。理由：

- 存储选型（Redis / 数据库 / 其他）属应用决策，框架不应代选
- **内存 Store 是个陷阱**：测试与单副本下工作正常，一旦多副本部署即静默失效 —— 用户会因请求落到不同 Pod 而随机登出。让依赖显式化正是目的。

测试用的内存 Store 只存在于 `_test.go` 中。

### 3. 错误语义

- `Store.Load` 找不到记录 MUST 返回 `ErrNotFound`，中间件将其转为 `ErrSessionNotFound`（401）
- **其他错误原样向上传递** —— 存储故障不是认证失败，MUST NOT 报成 401。有测试断言此点。
- `Store.Delete` 删除不存在的 session MUST 成功，使登出幂等

### 4. 无凭证即拒绝

中间件拒绝一切没有有效 session 的请求，handler 不执行。登录等必须开放的操作，通过生成的 per-method 中间件计划不列入，或用 `middleware/selector` 按 operation 模式豁免。

**不提供「可选认证」模式** —— 「有 session 就填，没有就放行」看似方便，实则让每个 handler 都要自己判断是否已认证，把认证决策散落到业务代码里。

---

## Consequences

### 正面

- `Principal` 的存在理由从预期变为事实：两个独立实现共用同一抽象
- session 可吊销，补上了 JWT 无法覆盖的场景
- 契约传输中立，gRPC / MCP 同样可用

### 负面与代价

- **开箱不能用**：使用方必须先写一个 `Store`。这是有意的成本转移。
- **用不了 gorilla 生态现成的 store 实现**（Redis / Postgres 等第三方适配器均面向 gorilla 的接口）。代价真实，但换来的是非 HTTP 传输也能用。若日后确有需要，可另出一个适配 gorilla `Store` 的 HTTP-only 子模块。
- 写入侧（登录建 session、登出删 session、设置响应 cookie）**不在本模块范围内**，属应用代码。中间件只读。
- Session ID 的不可猜测性由**使用方**保证。文档已警示 MUST 用密码学随机源，但框架无法强制。

### 未解决

- 是否提供一个 `contrib/middleware/session/redis` 子模块。目前**无真实使用者验证接口设计**，按需再做。
- 是否需要「滑动过期」（每次访问延长有效期）。这需要中间件在读路径上写 Store，性能与语义都需权衡，目前不做。
- `Principal` 是否需要授权面（`Scopes()` / `Roles()`），仍未决定，见 ADR-0004。两个实现都只提供了 `Subject()`，暂无证据表明需要更多。
