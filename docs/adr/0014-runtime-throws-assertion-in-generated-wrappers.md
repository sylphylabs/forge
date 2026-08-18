# ADR-0014：运行期 throws 断言——生成 wrapper 守住方法错误声明

**状态：** Accepted
**日期：** 2026-08-18
**决策者：** 项目所有者
**关系：** 补全 [ADR-0013](./0013-method-error-declaration-via-marked-extensions.md)（throws 声明驱动精确 OpenAPI responses）留下的缺口「声明 ≠ 运行时保证」；建立在 [ADR-0012](./0012-transport-projection-gate.md)（`PublicOf` 是唯一披露咽喉）与 [ADR-0006](./0006-errors-kind-over-transport-code.md)（错误身份 = (domain, reason)）之上。

---

## Context

ADR-0013 之后，方法的可抛错误在 proto 上声明、OpenAPI 文档由声明生成——文档与声明同源。但声明对运行时没有约束力：handler（或方法 plan 里的中间件）返回一个未声明的契约错误时，它照样完整出线（ADR-0012 只挡未声明**身份**，不挡未声明**于该方法**的已注册身份），文档就在撒谎。远端错误更隐蔽：`PublicOf` 对 remote 标记原样透传（转发不构成新披露），于是 business 层忘记翻译外域 sentinel 时，一个从未出现在本服务任何声明里的外域身份直接成为本服务的线上行为。

需要一个运行期机制，在错误出线前把「出线身份 ∈ 该方法声明集」变成可观测、可执法的事实。

## Decision

**断言在 protoc-gen-go-middleware 生成的 wrapper 外层方法执行；声明集编译进生成代码；默认 observe（log 违规），strict / fail 为 opt-in。**

### 断言位置：wrapper 外层方法，不是终端适配器，不是 encoder

生成的 HTTP 与 gRPC wrapper 的外层方法（`GetBook(ctx, req)` / `WatchBooks(req, stream)`）是「该方法的错误」最后一次可归属到方法的地方：

- **不在终端适配器**（compose 链最内层）：会漏掉方法 plan 中间件铸造的契约错误——per-method authz 返回的 PERMISSION_DENIED 正是最需要声明的错误之一，它产生在终端适配器之外、wrapper 方法之内。
- **不在 encoder / `PublicOf`**：披露咽喉是全局的，不知道错误属于哪个方法；把 per-method 声明集塞进全局函数需要按 operation 查表，重新引入 ADR-0012 已经排除的 transport 层分散规则,并违反 generated-middleware.md 的 request-path contract（不做 operation 字符串查找）。
- wrapper 是既有的 per-method 代码生成点：声明集可以按方法编译成静态数据,请求路径零反射、零 descriptor 读、零字符串分发,与既有 request-path contract 完全一致。

### 声明集编译进生成代码，解析逻辑单源

`cmd/internal/throws` 是 ADR-0013 marker 解析的唯一实现（从 openapi generator 提取,protoc-gen-openapi 与 protoc-gen-go-middleware 共用）：动态 descriptor 池、marker 认领、service ∪ method 并集、fail 清单 a–h 全部语义随共享包统一——对一个生成器非法的声明对另一个也非法。

middleware 生成器对每个有声明的方法 emit：

```go
var _LibraryServiceThrowsGetBook = throws.Declare("throws.test.v1.LibraryService/GetBook",
    throws.Identity{Domain: "throws.test.v1", Reason: "FAILURE_REASON_DENIED"},
    throws.Identity{Domain: "throws.test.v1", Reason: "FAILURE_REASON_NOT_FOUND"},
    ...
)
```

wrapper 构造时把声明集解析为每方法一个 `throws.Assert` 闭包；外层方法在 `err != nil` 时调用它。运行期实现在 `middleware/throws`（runtime 包,零 proto 依赖）。

### 参与语义：声明即断言，无声明零变化

方法（含 service 级并集）无任何 throws 声明 = 不参与。不参与的服务生成物与本 ADR 之前逐字节一致：构造函数保持二参签名,无断言字段,无 throws import。声明了任何一条,该方法全程断言,构造函数增加 `opts ...throws.Option` 变参——对既有调用方源码兼容。

### 有效集：声明 ∪ 框架域 ∪ 不可披露者

出线身份属于以下任一集合即放行：

1. **声明集**——service ∪ method 并集,身份 = (枚举 proto 包名, 枚举值全名),与 `MustDefine` 注册、`ErrorInfo`/Problem JSON 线格式同一词汇。
2. **框架域豁免**（domain == `errors.Domain`）——限流、超时、熔断、panic 背板、VALIDATION_FAILED 等操作性身份在任何方法上真实可达,逐方法声明它们是噪声不是信息。不做 buf.validate 特判:框架域整体豁免已覆盖。
3. **非契约且非 remote 的本地身份**——`Of()` 产物、ad-hoc 身份,`PublicOf` 反正投 KindInternal（ADR-0012）,它们无法让文档撒谎。
4. 已被 `Undisclose` 标记的错误——判决已下,投影为 internal。

**remote 不豁免。** remote 身份经 `PublicOf` 原样透传,未声明的 remote 身份出线正是「business 层未翻译外域 sentinel」违规——断言必须抓,这是它相对 ADR-0012 守门的独有增量。

### 身份提取与出线一致

断言用 `errors.FromError(err)` 取身份——与两个出线路径同源:HTTP 的 `PublicOf` 与 gRPC 的 `projectError → forgeError → FromError` 都取 `errors.As` 命中的第一个 `*Error`。断言判定的身份就是将要出线的身份,不存在第二套选主逻辑。

### 模式：observe 默认,strict / fail opt-in

- **observe（默认）**:违规时 WARN log（方法全名、domain/reason、kind、remote 标记、该补的声明提示、投影前的原错误）,错误原样放行。理由:断言守的是**文档诚实**,不是披露安全——披露安全已由 ADR-0012 fail-closed 保证。违规的责任人是漏写声明的服务作者,strict 默认会把惩罚转嫁给收到 500 的客户端与被打断的生产流量,错罚对象。
- **strict（opt-in,粒度到 wrapper 构造与方法）**:`throws.Strict()` / `throws.Strict("GetBook")`。违规错误不改写——`errors.Undisclose` 打上不可披露标记,`PublicOf` 据此投 KindInternal+TraceID。进程内日志/metrics 在投影前观察原错误的架构（ADR-0012）原样保持:改判只发生在唯一披露咽喉,wrapper 只做标记。
- **fail（测试期）**:`throws.FailUndeclared(...)` 或环境变量 `FORGE_THROWS=fail`（wrapper 构造期读取,请求路径不读环境）——违规错误替换为框架身份 `THROWS_UNDECLARED` 并 wrap 原错误,让集成测试直接红。strict 与 fail 同时配置时 strict 胜:拒绝披露是边界上更强的判决。

### 反向观测

`Declaration.Unobserved()` 报告声明过但从未出线的 (method, identity)——疑似过期声明。观测状态挂在生成的包级 Declaration 上,测试期可直接断言,不引入额外全局注册表。它只证明「自进程启动起至少出线一次」,不证明更多。

### errors 包新增（保持零依赖叶）

- `Undisclose(err) error` / `IsUndisclosed(err) bool`:不可披露标记,`PublicOf` 最先检查,覆盖包括 remote 透传在内的一切放行规则;原错误经 `Unwrap` 保持可诊断,`Is`/`As`/`KindOf` 语义不变。
- `IsContract(domain, reason) bool`:导出既有注册表查询,断言据此区分「会完整出线的未声明契约身份」与「反正投 internal 的匿名身份」。

三者均纯标准库,无 proto、无传输依赖。

## 被否决的方案

**在终端适配器断言。** 否决:漏掉方法 plan 中间件铸造的契约错误(per-method authz 的 PERMISSION_DENIED 等),而那正是声明集的主要成员。

**在 encoder / `PublicOf` 断言。** 否决:披露咽喉方法盲(method-blind),per-method 查表违反 request-path contract,并把 ADR-0012 刚收敛到一处的规则重新摊开。

**strict 下由 wrapper 直接改写错误为 internal。** 否决:改写发生在日志/metrics 观察之前,违反 ADR-0012「投影前观察原错误」;且两个 transport wrapper 各改一次是两份披露规则。标记 + 咽喉改判保持单点。

**remote 一并豁免。** 否决:remote 是 `PublicOf` 透传的,未声明 remote 身份出线恰是断言的独有价值;豁免它,断言退化为「ADR-0012 已经挡住的都不告警」。

**声明集运行期从 descriptor 读取。** 否决:违反 generated-middleware.md request-path contract(不读 descriptor、不做反射);生成期解析一次、emit 静态数据,与 plan/wrapper 的既有生成模式一致。

**独立 lint/CI 静态分析替代运行期断言。** 否决:静态分析无法看见运行期组合(中间件铸错、remote 转发、动态翻译),与 ADR-0012 否决「仅 linter」同理;两者可叠加,不互替。

## Consequences

- **签名变更(源码兼容)。** 有声明的服务,`Wrap*Server` 增加 `opts ...throws.Option` 变参;无声明的服务生成物零变化。
- **覆盖残差(如实声明)。** 断言点是 per-method wrapper,以下错误不经过它:server-wide 中间件与 transport 原生层(HTTP Filter、gRPC interceptor、panic 背板)产生的错误、以及未套 wrapper 的服务。框架自身的这类错误在框架域豁免内;应用在 server-wide 层铸造的契约错误可用 service_throws 文档化,但运行期不可断言——这是位置选择的代价,接受之。
- **声明负担与缓解。** 声明即断言意味着漏声明会被持续告警;缓解:框架域豁免消除操作性噪声、observe 默认不伤流量、`Unobserved()` 帮助修剪过期声明、strict 永不默认。
- **观测形态。** 违规经 slog 记录(结构化字段:method/domain/reason/error_kind/remote/fix);runtime 无既有 metrics 埋点约定(otel 在 contrib),故核心只 log,metric 由应用或 contrib 在同一 log/错误观察点自行挂接。
- 新增测试:`errors/undisclose_test.go`(标记语义、投影链路、remote 覆盖)、`middleware/throws/throws_test.go`(有效集逐条、三模式、身份提取一致性、反向观测)、`cmd/protoc-gen-go-middleware`(生成物形态断言 + 全链路消费者测试:声明→生成→真实 wrapper 上三模式行为)。
