.PHONY: build test lint audit clean generate

BINARY := llm-proxy

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
