BINARY := jev-routing
.DEFAULT_GOAL := install
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: build install test test-jev-live test-x-cell clean

build:
	go build -o bin/$(BINARY) ./cmd/jev-routing

install:
	go build -o $(GOBIN)/$(BINARY) ./cmd/jev-routing

test:
	go test ./...

# TypeSafe live catalog-accuracy checks. Skipped by `go test ./...`.
# Requires TYPESAFE_API_KEY or JEV_API_KEY. Two POSTs per run.
test-jev-live:
	go test -tags jev_live ./internal/proxy -run TestLiveJevCatalogAccuracy -count=1 -v

# 実課金のある比較計測。例: make test-x-cell claude
# Codex は CODEX_MODEL（既定 gpt-5.6-terra）。短名 terra は ChatGPT ログインで 400。
test-x-cell:
	./scripts/test-x-cell.sh $(filter claude codex grok cursor devin,$(MAKECMDGOALS))

claude codex grok cursor devin:
	@:

clean:
	rm -rf bin
