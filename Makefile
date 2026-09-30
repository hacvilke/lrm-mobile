# LRM Mobile
GO ?= go

.PHONY: all build test vet fmt release verify install clean help submodule

all: build

submodule:
	git submodule update --init --recursive

build: ## build a native lrm into ./lrm
	$(GO) build -o lrm ./cmd/lrm

release: ## cross-build every release target into ./dist
	sh scripts/build.sh

verify: ## check the built Android binaries are PIE
	sh scripts/verify-elf.sh dist

test: ## go tests + installer tests
	$(GO) test ./... -count=1
	sh scripts/test-install.sh

vet:
	$(GO) vet ./...

fmt:
	gofmt -w ./cmd ./internal

install: build ## install to $HOME/.local/bin (never root, never /usr/local)
	sh scripts/install.sh --from ./lrm --force

clean:
	rm -rf lrm dist
	$(GO) clean -testcache

help:
	@echo "Targets: submodule build release verify test vet fmt install clean"
