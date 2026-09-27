.PHONY: lint lint-install test build docker-build docker-up docker-down docker-save

GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo "$(shell go env GOPATH)/bin/golangci-lint")

DOCKER_IMAGE ?= litepan-go:dev
DOCKER_PLATFORM ?= linux/amd64
DOCKER_EXPORT ?= dist/$(DOCKER_IMAGE).tar.gz

lint:
	@GOWORK=off "$(GOLANGCI_LINT)" run -c .golangci.yml ./...

lint-install:
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

test:
	@GOWORK=off go test -race ./...

build:
	@GOWORK=off go build -tags fuse ./...

build-nofuse:
	@GOWORK=off go build ./...

docker-build:
	@mkdir -p dist
	docker build --platform $(DOCKER_PLATFORM) -t $(DOCKER_IMAGE) .

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-save: docker-build
	@mkdir -p dist
	docker save $(DOCKER_IMAGE) | gzip > $(DOCKER_EXPORT)
