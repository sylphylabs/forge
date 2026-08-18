# ADR-0015：OpenAPI 生成——自研 write-only 文档模型与 sylphy.openapi.v1 精简注解

**状态：** Accepted
**日期：** 2026-08-18
**决策者：** 项目所有者
**关系：** 承接 [ADR-0013](./0013-method-error-declaration-via-marked-extensions.md)（throws 声明驱动精确 responses）与 [openapi-3.2.md](../design/openapi-3.2.md) Next Gates 第 3 条（"Replace or extend the gnostic model before enabling QUERY, arbitrary operations, or streaming behavior"）；调研见 [docs/research/R2-openapi-document-model-options.md](../research/R2-openapi-document-model-options.md)。

---

## Context

protoc-gen-openapi 源自 google/gnostic 生成器 fork，内部用 gnostic-models 的
`v3.Document` proto 类型建文档、序列化 YAML，并读取 gnostic 的 1143 号注解
（`gnostic.openapi.v3.document/operation/schema/property`）。两个问题：

1. **模型冻结在 OAS 3.0。** gnostic-models 不表达 OAS 3.2 的 QUERY path item
   operation、`additionalOperations`、sequential media type 字段
   （`itemSchema`/`prefixEncoding`/`itemEncoding`）、层级 tags
   （`parent`/`kind`/`summary`）、`$self`。文档声明 3.2 而模型是 3.0 子集,
   Deliberate Limitations 的三条限制全部源于模型而非生成逻辑。OAS 3.2 已于
   2025-09-19 正式发布(spec.openapis.org/oas/v3.2.0,非 RC)`[一手：R2 §4]`,
   模型是唯一瓶颈。
2. **注解词汇是别人的冻结契约。** gnostic 注解镜像整个 OAS 3.0 对象树,与
   内部模型同构——换模型就要么继续背着 3.0 形状的注解词汇,要么写一层
   全对象树的转换。已知消费方尚未使用任何 gnostic 注解,迁移面为零,
   这是换词汇成本最低的时刻。

## Decision

### 1. 自研手写 write-only 模型（`cmd/internal/openapi/model`）

手写 Go struct,只服务"构建 + 确定性序列化为 YAML(与 JSON)"。不解析、不
round-trip、不在生成期做 OAS schema 校验——校验留在测试侧。

业界先例 `[一手：R2 §3]`:Smithy(手写不可变 POJO)、TypeSpec(手写 TS
interface,含 3.0/3.1/3.2 版本变体)、grpc-gateway protoc-gen-openapiv2
(`internal/genopenapi/types.go` 手写 struct)三个成熟 protobuf/IDL→OpenAPI
生成器独立选择了同一路径,且全部不在生成期做 schema 级校验;TypeSpec 与
Smithy 的多版本演进证明"新增 OAS minor 版本"在手写模型下可持续。

**字节确定性由模型架构保证,不依赖编码器默认行为。** 每个 keyed 集合都是
命名对 slice(`[]*NamedSchema`、`MediaTypes` 等),序列化经显式构建的
`yaml.Node` 树(gopkg.in/yaml.v3),节点按声明顺序追加;JSON 输出走同一棵
节点树,两种格式不可能在内容或顺序上分歧。裸 Go map 不进序列化路径。模型包
有确定性测试(同输入重复序列化字节相等)与 golden 输出锁定。

**OAS 3.2 表达能力完整。** `PathItem.Query`、`AdditionalOperations`、
`MediaType.ItemSchema/PrefixEncoding/ItemEncoding`、
`Tag.Parent/Kind/Summary`、`Document.Self` 全部建模。生成器本期不 emit
QUERY(仍按文档 fail with diagnostic),但限制从此在生成器 emit 逻辑,
不在模型。

oneof 形态用 Go 惯用法:`Schema.Ref` 非空即引用(只 emit `$ref`),
`AdditionalProperties{Allowed *bool; Schema *Schema}` 二选一——不照抄
gnostic 的 proto oneof 包装。

### 2. 精简注解 `sylphy.openapi.v1`（api 模块,扩展号 500301–500304）

替换 gnostic 1143。设计原则:**只表达 descriptor 说不出的东西**——展示性
元数据(title/summary/tags)、server URL、security。schema、路径、错误
responses 全部由 descriptor/`google.api.http`/throws 声明推导,注解里没有
它们的位置,文档因此无法与契约分歧。词汇 append-only 演进,字段按已证明的
需求增加,不预留。

- `FileOptions document = 500301`:title、version、description、servers、
  security_schemes(v1 只覆盖 HTTP bearer 与 API key header 两种 oneof
  形态,其余留待 append)
- `MethodOptions operation = 500302`:summary、description、tags、
  deprecated、security(requirement 引用 document 定义的 scheme 名,
  引用不存在的 scheme 是生成期错误)
- `MessageOptions schema = 500303`:description
- `FieldOptions field = 500304`:description、example、format

**Security 进 v1 而非推迟。** 理由:operation 级 security requirement 与
document 级 scheme 定义是一对不可拆的最小闭环,单独 gate 任何一半都发布
不出可用的东西;两种 scheme 形态(bearer/API key header)覆盖绝大多数
service-to-service 与网关场景,oneof 使追加 OAuth2 等形态是纯 append。

**扩展号选取。** 沿用 errors.proto 的登记逻辑:sylphy.errors.v1 占
500101–500103,sylphy.message.v1 占 500201,OpenAPI 词汇取下一个百位段
500301–500304,api/internal/contracttest 的号段登记表新增四行。

**读取机制沿用 cmd/internal/throws 的动态解析。** 插件编译依赖已发布的
forge api 模块(GOWORK=off 必须过),注解以 descriptor 形态随
CodeGeneratorRequest 到达:按全名(`sylphy.openapi.v1.document` 等)从
请求自带的 descriptor 池解析扩展,protoreflect 按字段名读值。cmd 内不
重复生成同全名 proto Go 代码(避免 init 重复注册 panic),不新增对未发布
api 版本的依赖。

### 3. libopenapi + jsonschema/v6 保留为测试侧交叉验证

生成物用 libopenapi 独立解析(结构断言全部走它的高层模型,不再有第二套
gnostic 解析),并对 libopenapi 内嵌的 OAS 3.2 官方 schema
(spec.openapis.org/oas/3.2/schema/2025-09-17)做 jsonschema/v6 校验。
两个库都只是 cmd 模块的测试依赖,不进生成路径、不进 runtime 模块。

## 被否决的方案

**继续 gnostic / gnostic-models。** 否决:上游冻结在 OAS 3.0,QUERY、
sequential media types、层级 tags 无处安放;fork 并扩展 proto 模型意味着
维护一整套 protobuf 形状的 OpenAPI 对象树,收益为零。

**libopenapi 作构建模型。** 否决 `[一手：R2 §1]`:它是 parse→mutate→render
的往返库,高层构造函数全部要求已解析的低层模型作入参,"纯代码从零建
Document"依赖渲染器对 nil 低层模型的容错——非 API 背书的用法;且该路径下
无行号支撑的字段排序退化为 `sort.Slice` 非稳定排序,从零构建的字节确定性
无架构保证。作为测试侧解析器它是正确的工具,作为写模型不是。

**kin-openapi 作构建模型。** 否决 `[一手：R2 §2]`:OAS 3.2 官方声明为部分
支持,层级 tags 未实现;`Components.Schemas` 等是裸 Go map,确定性部分
依赖下游编码器默认行为;3.2 功能 2026-07 才落地,未经长期验证。

**继续读取 gnostic 1143 注解、仅换内部模型。** 有先例
(protoc-gen-connect-openapi,`[一手：R2 §5]`)但否决:1143 词汇镜像整个
OAS 3.0 对象树,含 responses 字面量——正是 ADR-0013 fail g 要消灭的
双源;背着一套 3.0 形状的冻结词汇走 3.2 之路,还要为它写全树转换层。
迁移面为零(已知消费方未用),没有理由继承。

**注解词汇镜像完整 OAS 对象树(gnostic/grpc-gateway 1042 式)。** 否决:
词汇越大,注解与推导事实冲突的表面越大(手写 response、手写 schema 类型
都是文档撒谎的入口);精简词汇从结构上排除这类冲突,ADR-0013 的
"声明是唯一源"原则延伸到整个文档。

## Consequences

- **gnostic 注解不再被读取。** 迁移面为零(消费方未使用);曾依赖 gnostic
  `Operation.responses` 字面量给空 4xx/5xx response 自动补 problem+json
  content 的行为随词汇一起消失——错误 responses 的唯一来源是 throws 声明
  (ADR-0013),这正是该 ADR 的方向。fail g(声明状态码与既有 response
  冲突即失败)作为内部不变量保留并有单元测试。
- **输出格式差异。** 键序、引号风格与 gnostic 序列化有无害差异;结构语义
  等价由测试侧 libopenapi 解析 + 官方 schema 校验兜底。
- **QUERY/streaming 的剩余工作在生成器。** 模型已可表达,启用它们只需
  emit 逻辑 + fixture + 诊断(openapi-3.2.md Next Gates 更新)。
- **cmd/go.mod 依赖净变化:** 移除 github.com/google/gnostic(及间接的
  gnostic-models),新增 gopkg.in/yaml.v3(直接)。
- 新增测试:`cmd/internal/openapi/model`(确定性、golden、JSON)、
  `cmd/protoc-gen-openapi/annotations_test.go`(注解端到端:document/
  operation/schema/field 注入 → 文档字段,dangling security scheme 与
  formless scheme 的失败诊断)、api consumer 测试
  (`api/testdata/consumer/test/v1/consumer_openapi_test.go`)、端到端
  确定性测试(同请求两次生成字节相等)。
