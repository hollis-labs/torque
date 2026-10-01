.PHONY: build build-all build-prod gui test test-scheduler lint profiles-lint clean install

BINARY=torque
APIKEY_HELPER=torque-apikey-helper
MODULE=github.com/hollis-labs/torque
GUI_DIR=apps/gui
EMBED_DIR=internal/httpserver/webui/dist
# BINDIR is where `make install` puts the binaries; override it per host,
# e.g. `make install BINDIR=$HOME/.local/bin`.
BINDIR ?= $(HOME)/go/bin

build:
	go build -o $(BINARY) ./cmd/torque
	go build -o $(APIKEY_HELPER) ./cmd/torque-apikey-helper

build-all: build

# gui compiles the React frontend bundle into apps/gui/dist. With a lockfile
# it installs through `npm ci`, which installs exactly what the lockfile pins
# and never rewrites it, so a build leaves the tree clean; `npm ci` also
# replaces node_modules wholesale. `npm install` covers a checkout without one.
gui:
	cd $(GUI_DIR) && if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi && npm run build

# build-prod compiles the GUI bundle INTO the torque binary via the
# `embedgui` build tag, producing a self-contained artifact: one binary
# serves the API and the GUI on a single port (8990) with no apps/gui/dist
# dependency on disk. This is the target the Cerberus torque-api-service
# resource builds. A plain `make build` stays GUI-less and fast for dev.
#
# The copy source is `dist/.`, not `dist/`: copying `dist/` into an existing
# directory copies its contents with BSD cp but nests it as dist/dist with GNU
# cp, which embeds a bundle with no index.html at its root, so a Linux binary
# 404s on / while it logs that the GUI is available.
build-prod: gui
	rm -rf $(EMBED_DIR)
	mkdir -p $(EMBED_DIR)
	cp -R $(GUI_DIR)/dist/. $(EMBED_DIR)/
	go build -tags embedgui -o $(BINARY) ./cmd/torque
	go build -o $(APIKEY_HELPER) ./cmd/torque-apikey-helper

# install builds the GUI-embedded binary, since an installed binary runs from
# wherever it is started and cannot count on apps/gui/dist being beside it.
#
# install(1) unlinks the destination before writing, so every rebuild lands on
# a fresh inode. A plain `cp` overwrites in place and reuses the inode; macOS
# then SIGKILLs the new binary at exec ("Killed: 9") because the inode still
# carries the kernel code-signing/provenance binding for the PREVIOUS build's
# cdhash. (2026-05-16: an in-place rebuild broke `torque mcp` this way.)
install: build-prod
	install -d $(BINDIR)
	install -m 0755 $(BINARY) $(BINDIR)/$(BINARY)
	install -m 0755 $(APIKEY_HELPER) $(BINDIR)/$(APIKEY_HELPER)

test:
	go test ./... -v -count=1

test-scheduler:
	go test ./internal/runtime/... -v -count=1

lint:
	go vet ./...

profiles-lint:
	go run ./cmd/torque profiles lint

clean:
	rm -f $(BINARY)
	rm -f $(APIKEY_HELPER)
	rm -f *.db *.db-shm *.db-wal
