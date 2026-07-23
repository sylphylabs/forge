module github.com/openkratos/kratos/cmd

go 1.27rc2

require (
	github.com/google/gnostic v0.7.1
	github.com/openkratos/api v0.0.0
	github.com/openkratos/kratos v0.0.0
	google.golang.org/genproto/googleapis/api v0.0.0-20260720211330-0afa2a65878a
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/google/gnostic-models v0.7.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.6.1 // indirect
)

replace github.com/openkratos/kratos => ..

// Local-only until github.com/openkratos/api has its first public release.
replace github.com/openkratos/api => ../../OpenKratos-api

tool google.golang.org/grpc/cmd/protoc-gen-go-grpc
