#!/usr/bin/make -f

VERSION ?= latest
DOCKER := $(shell which docker)
export GO111MODULE = on

build: go.sum
	@go build

go.sum: go.mod
	@echo "Ensure dependencies have not been modified ..." >&2
	@go mod verify
	@go mod tidy

.PHONY: build

docker:
	@$(DOCKER) build -f Dockerfile . -t quicksilverzone/interchain-queries:${VERSION}

docker-local:
	@go build
	@$(DOCKER) build -f Dockerfile.local . -t quicksilverzone/interchain-queries:${VERSION}

docker-push:
	@$(DOCKER) push quicksilverzone/interchain-queries:${VERSION}
