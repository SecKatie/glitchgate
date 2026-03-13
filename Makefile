.PHONY: build test lint audit clean generate image image-push

BINARY := glitchgate

build:
	CGO_ENABLED=0 go build -o $(BINARY) .

test:
	go test -race ./...

lint:
	golangci-lint run

audit: lint
	gosec ./...
	govulncheck ./...

generate:
	sqlc generate

clean:
	rm -f $(BINARY)

IMAGE ?= ghcr.io/seckatie/glitchgate
TAG   ?= latest

image:
	podman build --platform linux/amd64,linux/arm64 --manifest $(IMAGE):$(TAG) .

image-push:
	podman manifest push $(IMAGE):$(TAG)
