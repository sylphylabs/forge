module github.com/sylphylabs/forge

go 1.27rc2

require (
	github.com/fsnotify/fsnotify v1.10.1
	github.com/go-playground/form/v4 v4.3.0
	github.com/gorilla/websocket v1.5.3
	github.com/sylphylabs/forge/api v0.0.0
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/sync v0.22.0
	google.golang.org/genproto/googleapis/api v0.0.0-20260720211330-0afa2a65878a
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260720211330-0afa2a65878a
	google.golang.org/grpc v1.83.0
	google.golang.org/protobuf v1.36.11
)

// The protobuf contract lives in this repository; see api/.
replace github.com/sylphylabs/forge/api => ./api

require (
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)
