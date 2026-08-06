module github.com/openkratos/kratos/contrib/transport/nats

go 1.27rc2

require (
	github.com/nats-io/nats-server/v2 v2.14.3
	github.com/nats-io/nats.go v1.51.0
	github.com/openkratos/kratos v0.0.0
)

require (
	github.com/antithesishq/antithesis-sdk-go v0.7.0-default-no-op // indirect
	github.com/go-playground/form/v4 v4.3.0 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/nats-io/jwt/v2 v2.8.2 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/openkratos/kratos => ../../../

replace github.com/openkratos/api => ../../../../OpenKratos-api
