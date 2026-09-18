BINARY := jev-routing
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: build install test clean

build:
	go build -o bin/$(BINARY) ./cmd/jev-routing

install:
	go build -o $(GOBIN)/$(BINARY) ./cmd/jev-routing

test:
	go test ./...

clean:
	rm -rf bin
