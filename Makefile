.PHONY: build build-all test test-scheduler lint clean install

BINARY=clockwork
APIKEY_HELPER=clockwork-apikey-helper
MODULE=github.com/hollis-labs/clockwork-manifold

build:
	go build -o $(BINARY) ./cmd/clockwork
	go build -o $(APIKEY_HELPER) ./cmd/clockwork-apikey-helper

build-all: build

install: build-all
	cp $(BINARY) ~/go/bin/$(BINARY)
	cp $(APIKEY_HELPER) ~/go/bin/$(APIKEY_HELPER)

test:
	go test ./... -v -count=1

test-scheduler:
	go test ./internal/runtime/... -v -count=1

lint:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -f $(APIKEY_HELPER)
	rm -f *.db *.db-shm *.db-wal
