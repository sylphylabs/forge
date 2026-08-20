module example.com/sylphy/api-consumer

go 1.23.0

require (
	github.com/sylphylabs/forge/api v0.0.0
	google.golang.org/protobuf v1.36.12
)

replace github.com/sylphylabs/forge/api => ../..
