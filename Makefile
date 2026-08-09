.PHONY: build test test-race vet demo clean

build:
	go build -o bin/mac-agent ./cmd/agent

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

demo:
	./scripts/demo.sh

clean:
	go clean
	rm -f bin/mac-agent
