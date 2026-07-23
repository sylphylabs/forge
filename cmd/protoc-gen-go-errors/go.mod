module github.com/openkratos/kratos/cmd/protoc-gen-go-errors

go 1.27rc2

require (
	github.com/openkratos/api v0.0.0
	google.golang.org/protobuf v1.36.11
)

// Local-only until github.com/openkratos/api has its first public release.
replace github.com/openkratos/api => ../../../OpenKratos-api
