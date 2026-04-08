.PHONY: build test lint clean install

BINARY=clockwork
MODULE=github.com/hollis-labs/clockwork-manifold

build:
	go build -o $(BINARY) ./cmd/clockwork

install: build
	cp $(BINARY) ~/go/bin/$(BINARY)

test:
	go test ./... -v -count=1

lint:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -f *.db *.db-shm *.db-wal
