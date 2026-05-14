.PHONY: build test vet lint clean

BINARY := zbxctl
CMD     := ./cmd/zbxctl

build:
	CGO_ENABLED=0 go build -o $(BINARY) $(CMD)

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

clean:
	rm -f $(BINARY)
