BINARY := jev-routing
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: build install test test-jev-live clean

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

clean:
	rm -rf bin
