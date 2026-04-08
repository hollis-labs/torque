.PHONY: build build-daemon build-all test test-scheduler lint clean install

BINARY=clockwork
DAEMON=clockworkd
MODULE=github.com/hollis-labs/clockwork-manifold

build:
	go build -o $(BINARY) ./cmd/clockwork

build-daemon:
	go build -o $(DAEMON) ./cmd/clockworkd

build-all: build build-daemon

install: build-all
	cp $(BINARY) ~/go/bin/$(BINARY)
	cp $(DAEMON) ~/go/bin/$(DAEMON)

test:
	go test ./... -v -count=1

test-scheduler:
	go test ./internal/runtime/... -v -count=1

lint:
	go vet ./...

clean:
	rm -f $(BINARY) $(DAEMON)
	rm -f *.db *.db-shm *.db-wal
