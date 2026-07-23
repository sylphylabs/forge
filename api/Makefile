BUF_VERSION := v1.72.0
BUF := go run github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
PROTOC_GEN_GO_VERSION := v1.36.11

.PHONY: all lint build generate test vet descriptor consumer

all: lint build generate test vet descriptor consumer

lint:
	$(BUF) lint

build:
	$(BUF) build

generate:
	$(BUF) generate
	mkdir -p bin
	GOBIN=$(CURDIR)/bin go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	protoc -I proto -I testdata/consumer \
		--plugin=protoc-gen-go=$(CURDIR)/bin/protoc-gen-go \
		--go_out=testdata/consumer --go_opt=paths=source_relative \
		testdata/consumer/test/v1/consumer.proto

test:
	go test ./...

vet:
	go vet ./...

descriptor:
	mkdir -p build
	$(BUF) build --as-file-descriptor-set -o build/openkratos-api.binpb
	protoc -I proto -I testdata/consumer --include_imports \
		--descriptor_set_out=build/consumer.binpb \
		testdata/consumer/test/v1/consumer.proto

consumer:
	cd testdata/consumer && GOWORK=off go test ./...
