# ADR-0012：传输投影守门——只有声明过的错误身份才出线

**状态：** Accepted
**日期：** 2026-08-17
**决策者：** 项目所有者
**关系：** 细化 [ADR-0006](./0006-errors-kind-over-transport-code.md)「契约错误走 proto 声明、`Of()` 仅限进程内」的意图，将其从文档约定升级为运行时机制；依赖 [ADR-0007](./0007-errors-zero-dependency-leaf.md) 建立的 `Public` 信息边界。

---

## Context

ADR-0006 的分工是：契约错误在 Protobuf 中声明、由 protoc-gen-go-errors 生成 sentinel；`errors.Of()` 供进程内失败使用，不需要声明。但 ADR-0007 落地的 `PublicOf` 对两者一视同仁——**任何** `*errors.Error` 的 Kind、domain、reason、message、metadata 都原样出线。这留下一个 fail-open 缺口，消费方审计（2026-08-17，vane）给出了两个实测后果 `[一手：vane 审计工作底稿 §2.2]`：

1. **内部 reason 意外冻结为公共 API。** 未经处理的存储层错误漏到边界时，其进程内 reason（如 `store.vane/DEVICE_NOT_FOUND`）被序列化为客户端可匹配的线上契约。客户端一旦开始匹配它，改名即破坏性变更——一个从未打算公开的标识符被事故性地冻结。
2. **分类泄漏可探测语义。** 内部错误携带的 KindNotFound 自动投影为 404/NotFound。认证路径上「token 未命中」若以 NotFound 出线，客户端可借状态码区分「标识存在但凭证错」与「标识不存在」（OWASP 认证错误不可区分原则 `[行业惯例]`）。错误的 404 比正确的 500 更危险：客户端会把它当权威答案做不可逆决策。

反面约束同样清晰：validate/ratelimit/timeout 等框架中间件的错误、以及经 RPC 转发的远端错误，**必须**继续完整出线——守门不能把正当的契约错误一并灭口。

## Decision

**`errors.PublicOf` 只对「声明过的身份」披露完整公开数据；未声明身份的本地错误一律投影为 `Public{Kind: KindInternal, TraceID: …}`。**

「声明」的判据是 `MustDefine`:它是生成的 `*_errors.pb.go` 与手写框架 sentinel 共用的唯一构造入口,构造时把 (domain, reason) 对登记进包内注册表(`errors/contract.go`)。`PublicOf` 按身份对查表:

| 出站错误 | 投影 |
| --- | --- |
| 身份经 `MustDefine` 声明(生成物或手写 sentinel 及其派生) | Kind/domain/reason/message/metadata/violations 完整出线(不变) |
| `Of()` 产物、ad-hoc `WithDomain`/`WithReason` 拼出的未声明身份 | `KindInternal`,仅携 TraceID;原 Kind 与身份留在进程内(日志/metrics 在投影前观察原错误) |
| 远端错误(`FromPublic` 重建,`remote` 标记) | 原样透传——其数据已由生产方决定披露,转发不构成新披露 |
| 非 Forge 错误 | `Public{}`(不变,ADR-0007 已定) |

要点:

- **按身份对匹配,不按值匹配。** 中间件重组错误(如 validate 聚合 violations 后 `WithDomain().WithReason()` 重挂身份)仍命中注册表。这与 `Error.Is` 的身份语义一致(ADR-0007:domain+reason 是身份,Kind 是可变分类)。
- **守门在 `PublicOf`,不在各 transport。** 它是 ADR-0007 建立的唯一披露咽喉(grpc `projectError`、http `encodeError`、SSE/WS 错误帧全部流经),在此处守门自动覆盖现有与未来的全部传输,transport 无需各自复制规则。
- **TraceID 仍出线。** 它是跨进程关联的唯一正道(ADR-0006 决策 3),被压制的错误尤其需要它。
- **注册幂等、写少读多。** 注册只发生在包初始化期(sentinel 构造),读路径 RWMutex 只读锁;重复声明无害。

## 被否决的方案

**枚举「框架 domain 白名单」而非注册表。** 把 `errors.Domain` 及已知 contrib domain 硬编码为可出线集合。否决:应用自己的 proto 声明错误(非框架 domain)是最主要的合法出线者,白名单必然漏掉它们;而注册表让「生成即可出线」零配置成立。

**在 transport 层各自守门。** 否决:两个 transport 两份规则,SSE/WS 错误帧是第三份;ADR-0007 已把披露判定收敛到 `PublicOf` 一个函数,守门放回 transport 是倒退。

**降级预案——不守门,消费方改走「store 裸 sentinel + service 全量手工映射」。** 仅当生成物无可判标记且不能加时才需要。实际上 `MustDefine` 正是那个标记(生成物的唯一构造路径),预案不触发。

**编译期强制(linter 禁 `Of()` 出 handler)。** 与运行时守门不冲突,但单靠它是口头约定:漏网一处即 fail-open。运行时守门使系统默认 fail-closed,linter 可另行叠加。

## Consequences

- **行为变更(破坏性,对依赖旧行为者)。** 未声明身份的错误从「原样出线」变为「500/Internal 出线」。修复路径不是绕过守门,而是把该错误声明进 proto(或对框架内部错误用 `MustDefine`)——这正是 ADR-0006 本来的用法。仓库内全部生产代码已用 `MustDefine`(实测:middleware/transport/contrib 共 30+ sentinel,零处 `Of()` 出线);受影响的只有测试中的 ad-hoc 错误,已随本 ADR 改为声明式。
- **`Of()` 的文档语义（「仅限进程内」）现在由机制保证**,不再依赖使用者自觉。
- **客户端视角不变:** 声明过的错误(含全部框架中间件错误)投影不变;远端错误经代理/网关转发不变。
- 新增测试:`errors/contract_test.go`(守门六项行为)、`transport/grpc/errorpolicy_test.go`、`transport/http/codec_test.go`(两传输各一个「未声明身份不出线」回归)。
