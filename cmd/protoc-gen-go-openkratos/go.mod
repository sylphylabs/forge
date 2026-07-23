module github.com/openkratos/kratos/cmd/protoc-gen-go-openkratos

go 1.27rc2

require (
	github.com/openkratos/api v0.0.0
	github.com/openkratos/kratos v0.0.0
	google.golang.org/genproto/googleapis/api v0.0.0-20260720211330-0afa2a65878a
	google.golang.org/protobuf v1.36.11
)

require google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.6.1 // indirect

replace github.com/openkratos/kratos => ../..

// Local-only until github.com/openkratos/api has its first public release.
replace github.com/openkratos/api => ../../../OpenKratos-api

tool google.golang.org/grpc/cmd/protoc-gen-go-grpc
