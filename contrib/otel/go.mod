module github.com/openkratos/kratos/contrib/otel

go 1.27rc2

require (
	github.com/openkratos/kratos v0.0.0
	go.opentelemetry.io/contrib/bridges/otelslog v0.19.0
	go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/log v0.20.0
	go.opentelemetry.io/otel/log/logtest v0.20.0
	go.opentelemetry.io/otel/metric v1.44.0
	go.opentelemetry.io/otel/sdk v1.44.0
	go.opentelemetry.io/otel/sdk/metric v1.44.0
	go.opentelemetry.io/otel/trace v1.44.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-playground/form/v4 v4.3.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260720211330-0afa2a65878a // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260720211330-0afa2a65878a // indirect
)

replace github.com/openkratos/kratos => ../../
