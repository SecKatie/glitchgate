.PHONY: build test lint audit clean generate image image-push image-push-version

BINARY  := glitchgate
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
           -X github.com/seckatie/glitchgate/cmd.version=$(VERSION) \
           -X github.com/seckatie/glitchgate/cmd.commit=$(COMMIT) \
           -X github.com/seckatie/glitchgate/cmd.date=$(DATE)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

test:
	go test -race ./...

lint:
	golangci-lint run

audit: test lint
	gosec ./...
	govulncheck ./...

generate:
	sqlc generate

clean:
	rm -f $(BINARY)

IMAGE ?= ghcr.io/seckatie/glitchgate
TAG   ?= latest

image:
	-podman manifest rm $(IMAGE):$(TAG) 2>/dev/null
	podman build --no-cache --platform linux/amd64,linux/arm64 --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(DATE) --manifest $(IMAGE):$(TAG) .

image-push:
	$(MAKE) image TAG=$(TAG)
	podman manifest push $(IMAGE):$(TAG) docker://$(IMAGE):$(TAG)

image-push-version:
	$(MAKE) image-push TAG=latest
	podman manifest push $(IMAGE):latest docker://$(IMAGE):$(VERSION)
	@echo "Pushed $(IMAGE):$(VERSION)"
