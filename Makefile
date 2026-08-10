BIN := $(shell go env GOBIN)
ifeq ($(BIN),)
BIN := $(shell go env GOPATH)/bin
endif

TOOLS_SHELL="./hack/tools.sh"
# golangci-lint
LINTER := bin/golangci-lint
BUF_VERSION := v1.72.0
BUF := go run github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)

$(LINTER):
	curl -SL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s latest

all:
	@cd cmd && GOWORK=off go build ./protoc-gen-go-errors ./protoc-gen-go-http ./protoc-gen-go-message ./protoc-gen-go-middleware ./protoc-gen-openapi

.PHONY: install
install: all
	@cd cmd && GOWORK=off GOBIN='$(BIN)' go install ./protoc-gen-go-errors ./protoc-gen-go-http ./protoc-gen-go-message ./protoc-gen-go-middleware ./protoc-gen-openapi
	@echo "install finished"

.PHONY: uninstall
uninstall:
	$(shell for tool in protoc-gen-go-errors protoc-gen-go-http protoc-gen-go-message protoc-gen-go-middleware protoc-gen-openapi; do for i in `which -a $${tool} 2>/dev/null | sort | uniq`; do read -p "Press to remove $${i} (y/n): " REPLY; if [ $${REPLY} = "y" ]; then rm -f $${i}; fi; done; done)
	@echo "uninstall finished"

.PHONY: clean
clean:
	@${TOOLS_SHELL} tidy
	@echo "clean finished"

.PHONY: fix
fix: $(LINTER)
	@${TOOLS_SHELL} fix
	@echo "lint fix finished"

.PHONY: test
test:
	@${TOOLS_SHELL} test
	@echo "go test finished"

.PHONY: test-coverage
test-coverage:
	@${TOOLS_SHELL} test_coverage
	@echo "go test with coverage finished"

.PHONY: lint
lint: $(LINTER)
	@${TOOLS_SHELL} lint
	@echo "lint check finished"

.PHONY: generate
generate:
	@$(BUF) generate
	@echo "buf generate finished"

.PHONY: proto-check
proto-check:
	@$(BUF) lint
	@$(BUF) build
	@echo "protobuf check finished"
