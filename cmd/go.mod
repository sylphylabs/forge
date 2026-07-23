module github.com/openkratos/kratos/cmd

go 1.27rc2

require (
	github.com/google/gnostic v0.7.1
	github.com/openkratos/api v0.0.0
	github.com/openkratos/kratos v0.0.0
	github.com/pb33f/libopenapi v0.38.7
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
	google.golang.org/genproto/googleapis/api v0.0.0-20260720211330-0afa2a65878a
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/google/gnostic-models v0.7.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pb33f/jsonpath v0.8.2 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.6 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.6.1 // indirect
)

replace github.com/openkratos/kratos => ..

// Local-only until github.com/openkratos/api has its first public release.
replace github.com/openkratos/api => ../../OpenKratos-api

tool google.golang.org/grpc/cmd/protoc-gen-go-grpc
