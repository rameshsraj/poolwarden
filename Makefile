VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%d)
LDFLAGS  = -s -w -X github.com/smarsh/poolwarden/internal/cli.version=$(VERSION) \
           -X github.com/smarsh/poolwarden/internal/cli.commit=$(COMMIT) \
           -X github.com/smarsh/poolwarden/internal/cli.date=$(DATE)

.PHONY: build build-linux test vet fmt-check staticcheck lint clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/poolwarden ./cmd/poolwarden

build-linux:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/poolwarden-linux-amd64 ./cmd/poolwarden
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/poolwarden-linux-arm64 ./cmd/poolwarden

test:
	go test -race ./...

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

lint: vet fmt-check staticcheck

clean:
	rm -rf bin dist

.PHONY: release man docs package gen

gen:
	go build -o bin/gendocs ./cmd/gendocs

man: gen
	mkdir -p man
	bin/gendocs man man

docs: gen
	mkdir -p docs/commands
	bin/gendocs md docs/commands

package:
	@command -v nfpm >/dev/null || { \
		echo "nfpm not installed — see https://nfpm.goreleaser.com/install/"; exit 1; }
	mkdir -p dist
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/poolwarden-linux-$$arch ./cmd/poolwarden; \
	done
	ARCH=amd64 nfpm package -f packaging/nfpm.yaml -t dist/poolwarden-$(VERSION)-linux-amd64.rpm
	ARCH=amd64 nfpm package -f packaging/nfpm.yaml -t dist/poolwarden-$(VERSION)-linux-amd64.deb
	ARCH=arm64 nfpm package -f packaging/nfpm.yaml -t dist/poolwarden-$(VERSION)-linux-arm64.rpm
	ARCH=arm64 nfpm package -f packaging/nfpm.yaml -t dist/poolwarden-$(VERSION)-linux-arm64.deb

release: build-linux man docs package
