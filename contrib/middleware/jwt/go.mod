module github.com/sylphylabs/forge/contrib/middleware/jwt

go 1.27rc2

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/sylphylabs/forge v0.0.0
)

require (
	github.com/go-playground/form/v4 v4.3.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/sylphylabs/forge/api v0.0.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260720211330-0afa2a65878a // indirect
	google.golang.org/grpc v1.82.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/sylphylabs/forge => ../../..

replace github.com/sylphylabs/forge/api => ../../../api
