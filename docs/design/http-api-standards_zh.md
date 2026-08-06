# HTTP API 标准参考

Status: reference baseline

Last reviewed: July 24, 2026

## Purpose

本文档记录 OpenKratos 设计 HTTP、gRPC、OpenAPI、错误、路由、middleware
和生成器时应参考的公开标准。它不是当前行为声明，也不是一次性功能列表。
某个标准只有在对应实现、测试和兼容性文档落地后，才成为 OpenKratos 的
兼容性承诺。

OpenKratos 的目标不是重新实现 HTTP 协议栈。Go 标准库、gRPC-Go、浏览器、
代理和网关已经承担大量 wire-level 行为。框架需要做的是：

- 不破坏底层协议语义。
- 在路由、编解码、错误、缓存、代理、鉴权和文档生成中使用同一套术语。
- 将可验证的协议规则前移到代码生成、构造期或测试中。
- 对尚未稳定的草案保持显式 experimental 边界。

## Classification

本文档将规范分成三档：

- Core：影响 OpenKratos HTTP/gRPC runtime、生成器或兼容性声明的基础规范。
- Optional：适合提供 middleware、helper、contrib package 或生成器选项。
- Experimental：有产品价值，但规范、生态或代理支持仍需单独确认。

## Core Standards

| Area | Reference | OpenKratos impact |
| --- | --- | --- |
| HTTP semantics | [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html) | 方法语义、状态码、header、content negotiation、条件请求、range、safe/idempotent/cacheable 是 HTTP runtime 的基础词汇。 |
| HTTP caching | [RFC 9111](https://www.rfc-editor.org/rfc/rfc9111.html) | Cache-Control、Vary、ETag、Last-Modified、304、412、代理缓存语义会影响 cache/conditional middleware 和文档生成。 |
| HTTP/1.1 | [RFC 9112](https://www.rfc-editor.org/rfc/rfc9112.html) | Go 标准库处理 wire-level 解析，但框架不能破坏 connection、message framing、trailers 和安全边界。 |
| HTTP/2 | [RFC 9113](https://www.rfc-editor.org/rfc/rfc9113.html) | gRPC 运行在 HTTP/2 上；stream、header、trailer、cancellation 行为必须保留。 |
| HTTP/3 | [RFC 9114](https://www.rfc-editor.org/rfc/rfc9114.html) | OpenKratos core 不需要拥有 QUIC 实现，但 API 设计不能假设 HTTP/1.1-only 行为。 |
| URI syntax | [RFC 3986](https://www.rfc-editor.org/rfc/rfc3986.html) | 路由匹配、path escaping、query parsing、reserved characters 和代理转发路径必须以 URI 语义为底线。 |
| URI templates | [RFC 6570](https://www.rfc-editor.org/rfc/rfc6570.html) | 生成客户端 path expansion、HTTP transcoding 和文档输出时，需要明确模板变量展开规则。 |
| Problem Details | [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html) | 可作为通用 HTTP API 错误体的互操作参考，但不能无迁移地替换 OpenKratos 现有 {code, reason, message, metadata} 错误契约。 |
| QUERY method | [RFC 10008](https://www.rfc-editor.org/rfc/rfc10008.html) | QUERY 是 safe/idempotent、允许请求体的查询方法。可作为未来路由、OpenAPI 和客户端生成的 opt-in 能力。 |

## Patch and Partial Update

| Area | Reference | OpenKratos impact |
| --- | --- | --- |
| PATCH method | [RFC 5789](https://www.rfc-editor.org/rfc/rfc5789.html) | 如果框架声明支持标准 PATCH，应明确支持的 patch document media type，而不是仅把 PATCH 当成任意 HTTP verb。 |
| JSON Pointer | [RFC 6901](https://www.rfc-editor.org/rfc/rfc6901.html) | 适合用于 validation error 字段路径、JSON Patch path、OpenAPI schema error 定位。 |
| JSON Patch | [RFC 6902](https://www.rfc-editor.org/rfc/rfc6902.html) | 适合提供 application/json-patch+json helper 或 contrib middleware。 |
| JSON Merge Patch | [RFC 7396](https://www.rfc-editor.org/rfc/rfc7396.html) | 适合提供 application/merge-patch+json helper，尤其是管理 API 或资源配置 API。 |

## Optional Middleware and Helpers

| Area | Reference | OpenKratos impact |
| --- | --- | --- |
| Structured Fields | [RFC 9651](https://www.rfc-editor.org/rfc/rfc9651.html) | 新设计 HTTP header 时优先采用 structured fields，避免自定义逗号、分号和 quoted-string 解析。 |
| Web Linking | [RFC 8288](https://www.rfc-editor.org/rfc/rfc8288.html) | Pagination、schema discovery、alternate representation、deprecation docs 可通过 Link 表达。 |
| Well-Known URIs | [RFC 8615](https://www.rfc-editor.org/rfc/rfc8615.html) | capability discovery、metadata、health 或文档入口可以放在 /.well-known/*，但需要避免与业务路由冲突。 |
| Deprecation | [RFC 9745](https://www.rfc-editor.org/rfc/rfc9745.html) | 可提供 deprecation middleware/helper，用于接口迁移提醒。 |
| Sunset | [RFC 8594](https://www.rfc-editor.org/rfc/rfc8594.html) | 可与 Deprecation 配合表达接口计划下线时间和迁移文档链接。 |
| Prefer | [RFC 7240](https://www.rfc-editor.org/rfc/rfc7240.html) | Prefer: return=minimal、异步处理偏好、响应偏好适合资源型 API。 |
| Additional status codes | [RFC 6585](https://www.rfc-editor.org/rfc/rfc6585.html) | 429 Too Many Requests、431 Request Header Fields Too Large 等状态码适合限流和请求保护 middleware 使用。 |
| Digest Fields | [RFC 9530](https://www.rfc-editor.org/rfc/rfc9530.html) | Webhook、上传、跨服务调用可使用 representation/content digest 做完整性校验。 |
| HTTP Message Signatures | [RFC 9421](https://www.rfc-editor.org/rfc/rfc9421.html) | Webhook、回调、跨组织调用签名适合进入 contrib middleware，而不是 root runtime。 |
| WebSocket | [RFC 6455](https://www.rfc-editor.org/rfc/rfc6455.html) | 如果提供 WebSocket 支持，需要尊重 handshake、subprotocol、close code 和 frame 生命周期。 |

## Proxy and Deployment Boundary

| Area | Reference | OpenKratos impact |
| --- | --- | --- |
| Forwarded header | [RFC 7239](https://www.rfc-editor.org/rfc/rfc7239.html) | 真实 client IP、scheme、host 应通过可信代理配置解析；框架不能默认信任所有 forwarded headers。 |
| Proxy-Status | [RFC 9209](https://www.rfc-editor.org/rfc/rfc9209.html) | 网关、反向代理、service mesh 错误定位可通过 Proxy-Status 暴露，但应用 runtime 不应伪造代理链路事实。 |

Recommended rule:

- 默认只信任直连请求信息。
- 应用显式配置 trusted proxy / trusted network 后，才解析 Forwarded、X-Forwarded-* 或类似头。
- telemetry 和 access log 应记录解析来源，避免把伪造头当作安全事实。

## Security and Auth References

| Area | Reference | OpenKratos impact |
| --- | --- | --- |
| Bearer token | [RFC 6750](https://www.rfc-editor.org/rfc/rfc6750.html) | Authorization: Bearer、错误响应 header 和 token 使用方式应按该 RFC 设计。 |
| OAuth 2.0 Security BCP | [RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html) | 框架不应内置过时 OAuth 安全建议；认证 middleware 和示例应参考最新 BCP。 |
| DPoP | [RFC 9449](https://www.rfc-editor.org/rfc/rfc9449.html) | sender-constrained token 可以作为高级认证扩展，不应进入默认 hot path。 |
| JWT | [RFC 7519](https://www.rfc-editor.org/rfc/rfc7519.html) | JWT 是格式规范；验证策略还需结合 JWT BCP。 |
| JWT BCP | [RFC 8725](https://www.rfc-editor.org/rfc/rfc8725.html) | 避免 alg confusion、弱签名、错误 audience/issuer 验证等常见问题。 |
| JWT access token profile | [RFC 9068](https://www.rfc-editor.org/rfc/rfc9068.html) | 如果示例或 contrib 支持 OAuth access token JWT，应参考该 profile。 |
| Cookie | [RFC 6265](https://www.rfc-editor.org/rfc/rfc6265.html) | session、CSRF、same-site、secure cookie 行为应按浏览器 cookie 规范处理。 |
| HSTS | [RFC 6797](https://www.rfc-editor.org/rfc/rfc6797.html) | security headers middleware 可提供 HSTS，但必须让应用按部署拓扑显式启用。 |
| TLS 1.3 | [RFC 8446](https://www.rfc-editor.org/rfc/rfc8446.html) | TLS 配置属于 server/deployment boundary；框架可提供安全默认值和文档，不应隐藏部署责任。 |

## Experimental or Draft References

这些项目有实际 API 设计价值，但不应在文档中写成已稳定 RFC。落地时需要在实现、
README 和兼容性文档里保留 experimental 说明，并在 release 前重新检查 IETF
Datatracker 状态。

| Area | Reference | OpenKratos impact |
| --- | --- | --- |
| RateLimit fields | IETF HTTPAPI RateLimit draft | 适合限流 middleware 输出统一 header。429 状态码本身已由 RFC 6585 定义，但 RateLimit header 字段仍需在发布前复核草案状态。 |
| Idempotency-Key | IETF HTTPAPI Idempotency-Key draft | 适合支付、创建资源、外部回调等场景；core 可以提供接口，但存储、过期和冲突策略应由应用或 contrib 实现。 |

## Important Non-RFC Standards

| Area | Reference | OpenKratos impact |
| --- | --- | --- |
| OpenAPI | OpenAPI Specification | OpenAPI 不是 IETF RFC。OpenKratos 的 OpenAPI 生成器应按 OAS 3.1+ 的 JSON Schema 语义设计，而不是用 RFC 9457 或 Protobuf descriptor 替代 API 文档契约。 |
| CORS | WHATWG Fetch | CORS 不是 RFC。preflight、credentials、exposed headers 和 browser enforcement 应参考 Fetch 标准。 |
| gRPC protocol | gRPC HTTP/2 protocol | gRPC 不是 RFC。google.rpc.Status 是 gRPC/Google API 生态的错误模型，不等同于 OpenKratos HTTP JSON 错误体。 |
| Google API HTTP annotations | google.api.HttpRule | HTTP transcoding 的 path、body、response_body 和 additional_bindings 应以 vendored proto contract 和 conformance tests 为准。 |
| Protobuf JSON | Protobuf JSON Mapping | HTTP JSON 编解码应遵守 Protobuf JSON，而不是 Go encoding/json 的普通 struct 规则。 |

## Design Guidance

### Error model

OpenKratos 当前保留 {code, reason, message, metadata} 四字段 HTTP JSON
错误契约。gRPC transport 可以映射为 google.rpc.Status 加
google.rpc.ErrorInfo，但这不意味着 HTTP/OpenAPI 层可以忽略错误 schema。

Guidance:

- HTTP/OpenAPI 文档应描述 OpenKratos HTTP JSON 错误体。
- gRPC 文档应描述 canonical gRPC code、message 和 ErrorInfo details。
- RFC 9457 可以作为未来兼容扩展或替代格式的参考，但必须通过显式迁移文档落地。

### Routing and transcoding

Guidance:

- 基础 HTTP 路由遵守 RFC 9110 的 method semantics。
- URI parsing、escaping 和 matching 遵守 RFC 3986。
- 生成器 path template 能力参考 RFC 6570，但 Google HTTP transcoding 仍以 google.api.HttpRule 的具体语法为准。
- QUERY 支持应作为 opt-in 新能力，不应自动改变现有 GET/POST 语义。

### Headers

Guidance:

- 新增框架自有 header 时，优先评估是否能用 RFC 9651 structured fields。
- 与缓存、条件请求、认证、代理、安全相关的标准 header 不应重新发明。
- 兼容 legacy header 时，应在文档里标注来源和信任边界。

### Middleware placement

Guidance:

- Core middleware 只放协议正确性、生命周期、恢复、基础日志/metrics/tracing 等通用能力。
- 签名、DPoP、Digest、RateLimit、Idempotency-Key、WebSocket 等能力默认放 contrib 或独立 module，避免扩大 root runtime hot path 和依赖图。
- middleware 不应从 proto descriptor 读取部署 secret、provider 配置或业务策略。

### OpenAPI generation

Guidance:

- OpenAPI 生成器应明确目标 OAS 版本。
- 错误 response 应区分 HTTP JSON 错误体和 gRPC google.rpc.Status。
- 枚举错误注解可以驱动 response status code 和 reason 描述，但不能单独证明响应体 schema 已完整表达。
- 生成器应避免把 experimental draft header 生成为稳定契约，除非用户显式 opt-in。

## Acceptance Checklist

新增或修改 OpenKratos HTTP/API 功能时，至少检查：

- 是否依赖某个 RFC 或非 RFC 生态规范。
- 该规范属于 core、optional 还是 experimental。
- 是否改变外部业务代码能观察到的行为。
- 是否需要更新 COMPATIBILITY.md 或 migration 文档。
- 是否有 conformance tests 或 golden OpenAPI 输出验证。
- 是否会增加 root module 依赖或 hot path runtime cost。
- 是否需要可信代理、部署拓扑或安全配置作为前置条件。

## Current Open Questions

- OpenAPI 生成器的长期目标版本应固定在 OAS 3.1，还是允许 OAS 3.2 作为可选输出。
- 是否引入 OpenKratos 自有 annotation 来描述 error responses，或继续复用已有错误 enum 注解生成 response status/reason 文档。
- QUERY 是否应进入 core router 支持，还是只在生成器和文档层 opt-in。
- RateLimit 和 Idempotency-Key 在标准稳定前是否只提供 contrib 实现。
- 是否为 RFC 9651 structured fields 提供小型解析/格式化 helper，还是完全交给应用代码。
