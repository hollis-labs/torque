.PHONY: build build-all test test-scheduler lint clean install

BINARY=torque
APIKEY_HELPER=torque-apikey-helper
MODULE=github.com/hollis-labs/torque

build:
	go build -o $(BINARY) ./cmd/torque
	go build -o $(APIKEY_HELPER) ./cmd/torque-apikey-helper

build-all: build

# install(1) unlinks the destination before writing, so every rebuild lands on
# a fresh inode. A plain `cp` overwrites in place and reuses the inode; macOS
# then SIGKILLs the new binary at exec ("Killed: 9") because the inode still
# carries the kernel code-signing/provenance binding for the PREVIOUS build's
# cdhash. (2026-05-16: an in-place rebuild broke `torque mcp` this way.)
install: build-all
	install -m 0755 $(BINARY) ~/go/bin/$(BINARY)
	install -m 0755 $(APIKEY_HELPER) ~/go/bin/$(APIKEY_HELPER)

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
