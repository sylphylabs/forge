module github.com/openkratos/kratos/cmd/protoc-gen-go-http

go 1.27rc2

require (
	github.com/openkratos/kratos v0.0.0
	google.golang.org/protobuf v1.36.11
)

require google.golang.org/genproto/googleapis/api v0.0.0-20260720211330-0afa2a65878a

replace github.com/openkratos/kratos => ../..
