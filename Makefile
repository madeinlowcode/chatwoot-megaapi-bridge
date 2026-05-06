.PHONY: test integration lint build run clean

test:
	go test ./...

integration:
	go test -tags=integration ./...

lint:
	go vet ./...
	golangci-lint run

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bridge ./cmd/bridge

run: build
	./bridge serve

clean:
	rm -f bridge bridge.exe coverage.out
