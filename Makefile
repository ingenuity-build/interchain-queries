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

###############################################################################
###                                Linting                                  ###
###############################################################################

lint:
	@go run github.com/golangci/golangci-lint/cmd/golangci-lint run --out-format=tab

lint-fix:
	@go run github.com/golangci/golangci-lint/cmd/golangci-lint run --fix --out-format=tab --issues-exit-code=0

.PHONY: lint lint-fix

format:
	@find . -name '*.go' -type f -not -path "./vendor*" -not -path "*.git*" -not -name '*.pb.go' -not -name '*.gw.go' | xargs go run mvdan.cc/gofumpt -w .
	@find . -name '*.go' -type f -not -path "./vendor*" -not -path "*.git*" -not -name '*.pb.go' -not -name '*.gw.go' | xargs go run github.com/client9/misspell/cmd/misspell -w
	@find . -name '*.go' -type f -not -path "./vendor*" -not -path "*.git*" -not -name '*.pb.go' -not -name '*.gw.go' | xargs go run golang.org/x/tools/cmd/goimports -w -local github.com/ingenuity-build/interchain-queries

.PHONY: format

mdlint:
	@echo "--> Running markdown linter"
	@$(DOCKER) run -v $(PWD):/workdir ghcr.io/igorshubovych/markdownlint-cli:latest "**/*.md"

mdlint-fix:
	@$(DOCKER)  run -v $(PWD):/workdir ghcr.io/igorshubovych/markdownlint-cli:latest "**/*.md" --fix
