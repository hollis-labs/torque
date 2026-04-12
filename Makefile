.PHONY: build build-all test test-scheduler lint clean install

BINARY=clockwork
MODULE=github.com/hollis-labs/clockwork-manifold

build:
	go build -o $(BINARY) ./cmd/clockwork

build-all: build

install: build-all
	cp $(BINARY) ~/go/bin/$(BINARY)

test:
	go test ./... -v -count=1

test-scheduler:
	go test ./internal/runtime/... -v -count=1

lint:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -f *.db *.db-shm *.db-wal
