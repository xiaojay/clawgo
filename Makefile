.PHONY: all build test clean fmt lint

all: fmt lint build test

build:
	go build -o bin/clawgo ./cmd/clawgo

test:
	go test ./... -v -race

clean:
	rm -rf bin/

fmt:
	gofmt -w .

lint:
	go vet ./...
