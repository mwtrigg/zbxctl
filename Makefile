.PHONY: build test vet lint clean

BINARY  := zbxctl
CMD     := ./cmd/zbxctl
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X codeberg.org/mwtrigg/zbxctl/internal/version.Version=$(VERSION)"

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) $(CMD)

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY)
