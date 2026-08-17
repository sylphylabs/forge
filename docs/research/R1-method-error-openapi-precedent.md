# R1 — 方法级错误声明 → OpenAPI responses:业界先例调研

调研问题:在 Protobuf/gRPC 生态里,"把 RPC 方法可能返回的错误声明在 proto 中,
并让 OpenAPI 生成器自动产出对应的 4xx/5xx response 文档"是否有现成机制。

结论:**业界空白**。已知格局是三条互相独立的技术线,没有工具打通
"方法实际可抛的错误枚举 → 精确 per-code OpenAPI responses"。

## 1. grpc-gateway protoc-gen-openapiv2

[一手] `openapiv2_operation`(extend MethodOptions, field 1042)的 `responses`
是 `map<string, Response>`,key 为裸字符串状态码("404"、"default")——纯
magic string,与真实错误逻辑零联动,写错写漏不会被发现。默认自动生成一条
`default` response 指向 `google.rpc.Status`,是笼统兜底,不区分错误原因。

- https://github.com/grpc-ecosystem/grpc-gateway/blob/main/protoc-gen-openapiv2/options/annotations.proto
- https://raw.githubusercontent.com/grpc-ecosystem/grpc-gateway/main/protoc-gen-openapiv2/options/openapiv2.proto

## 2. Google AIP-193 / google.api

[一手] AIP-193 只规定单个错误的线格式(google.rpc.Status + ErrorInfo,
(domain, reason) 机器可读标识)。**没有**"按 RPC 方法枚举其可能错误"的条款,
google.api 下不存在 method 级错误声明 annotation。

- https://google.aip.dev/193

## 3. Kratos errors

[一手] `extend EnumOptions { int32 default_code = 1108; }` /
`extend EnumValueOptions { int32 code = 1109; }`,protoc-gen-go-errors 只生成
Go 辅助函数,与其 protoc-gen-openapi **零联动**——两条 pipeline 互不知晓
(issue #2154 甚至显示两生成器 flag 不兼容)。

- https://raw.githubusercontent.com/go-kratos/kratos/main/errors/errors.proto
- https://github.com/go-kratos/kratos/issues/2154

## 4. connect / gnostic / protoc-gen-connect-openapi

[一手/行业惯例] protoc-gen-connect-openapi 的 `with-google-error-detail` 仅是
"把 google.rpc.*ErrorDetails 标准 schema 挂到 default 响应"的开关;gnostic
protoc-gen-openapi 的 `default_response` 同为全局兜底。connect-go 的
Code/Error 是纯运行时类型,不经 proto 声明。均无方法级错误声明。

- https://github.com/sudorandom/protoc-gen-connect-openapi
- https://github.com/connectrpc/connect-go/discussions/256

## 5. typed MethodOptions extension(字段类型为应用错误枚举)先例

[未找到] 没有任何先例把应用错误枚举作为 MethodOptions extension 字段类型
(`extend MethodOptions { MyErrorReason throws = 5xxxx; }`)并由生成器读取产出
精确 responses。已知先例都停在两种弱形式:枚举值级 annotation(Kratos,
无"哪个方法抛它"的反向索引)或方法级裸字符串 map(grpc-gateway)。

[一手] extension number 惯例:50000–99999 为组织内部自用号段,无全局登记;
公开发布的库需向 protobuf 官方登记专属号段(grpc-gateway 1042、Kratos
1108/1109 等)。

- https://protobuf.dev/programming-guides/proto2/

## 总结

三条独立技术线:① 错误内容标准化(AIP-193)只管"错误长什么样";
② 错误枚举 + 生成期辅助代码(Kratos)止步于目标语言;③ OpenAPI responses
声明(grpc-gateway/gnostic)全是手写字面量或笼统 default 兜底。
"typed MethodOptions extension + 生成器结构化发现 → 精确 responses"
是原创缝合,无现成方案可抄。
