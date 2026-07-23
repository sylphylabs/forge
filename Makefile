user	:=	$(shell whoami)
rev		:= 	$(shell git rev-parse --short HEAD)
os		:=	$(shell uname)

# GOBIN > GOPATH > INSTALLDIR
# Mac OS X
ifeq ($(os),Darwin)
GOBIN	:=	$(shell echo $(GOBIN) | cut -d':' -f1)
GOPATH	:=	$(shell echo $(GOPATH) | cut -d':' -f1)
endif

# Linux
ifeq ($(os),Linux)
GOBIN	:=	$(shell echo $(GOBIN) | cut -d':' -f1)
GOPATH	:=	$(shell echo $(GOPATH) | cut -d':' -f1)
endif

# Windows
ifneq ($(findstring MINGW,$(shell uname -s)),)
GOBIN := $(shell echo "$(GOBIN)" | sed 's|\\|/|g' | cut -d';' -f1 | sed 's|^\([A-Za-z]\):|/\1|')
GOPATH := $(shell echo "$(GOPATH)" | sed 's|\\|/|g' | cut -d';' -f1 | sed 's|^\([A-Za-z]\):|/\1|')
endif
BIN		:= ""

TOOLS_SHELL="./hack/tools.sh"
# golangci-lint
LINTER := bin/golangci-lint
BUF_VERSION := v1.72.0
BUF := go run github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)

# check GOBIN
ifneq ($(GOBIN),)
	BIN=$(GOBIN)
else
# check GOPATH
	ifneq ($(GOPATH),)
		BIN=$(GOPATH)/bin
	endif
endif

$(LINTER):
	curl -SL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s latest

all:
	@cd cmd/protoc-gen-go-openkratos && go build && cd - &> /dev/null

.PHONY: install
install: all
ifeq ($(user),root)
#root, install for all user
	@cp ./cmd/protoc-gen-go-openkratos/protoc-gen-go-openkratos /usr/bin
else
#!root, install for current user
	$(shell if [ -z '$(BIN)' ]; then read -p "Please select installdir: " REPLY; mkdir -p $${REPLY};\
	cp ./cmd/protoc-gen-go-openkratos/protoc-gen-go-openkratos $${REPLY}/;else mkdir -p '$(BIN)';\
	cp ./cmd/protoc-gen-go-openkratos/protoc-gen-go-openkratos '$(BIN)'; fi)
endif
	@which protoc-gen-go &> /dev/null || go get google.golang.org/protobuf/cmd/protoc-gen-go
	@which protoc-gen-go-grpc &> /dev/null || go get google.golang.org/grpc/cmd/protoc-gen-go-grpc
	@which protoc-gen-validate  &> /dev/null || go get github.com/envoyproxy/protoc-gen-validate
	@echo "install finished"

.PHONY: uninstall
uninstall:
	$(shell for i in `which -a protoc-gen-go-openkratos 2>/dev/null | sort | uniq`; do read -p "Press to remove $${i} (y/n): " REPLY; if [ $${REPLY} = "y" ]; then rm -f $${i}; fi; done)
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

.PHONY: proto-check
proto-check:
	@$(BUF) lint
	@$(BUF) build
	@echo "protobuf check finished"
