GOLANGCI_LINT_VERSION := v2.15.0
PKGS ?= ./...
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help build test vet lint check clean

.DEFAULT_GOAL := help

help:
	@echo "Targets:"
	@echo "  build  Build bin/ralph as a static binary"
	@echo "  test   Run the tests"
	@echo "  vet    Run go vet"
	@echo "  lint   Run golangci-lint (PKGS=./internal/db/... limits the packages)"
	@echo "  check  Run vet, lint, then test"
	@echo "  clean  Remove bin/"

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/bengearig/ralph/internal/version.Version=$(VERSION)" -o bin/ralph ./cmd/ralph

test:
	go test ./...

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run $(PKGS)

check: vet lint test

clean:
	rm -rf bin
