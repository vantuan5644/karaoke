VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: build test check run dist clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o karaoke .

test:
	go test -race ./...
	node --test "web/test/*.test.mjs"

check: test
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...

run: build
	./karaoke -open

# One binary per platform in dist/, the web UI compiled into each.
dist:
	rm -rf dist && mkdir -p dist
	for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/karaoke-$(VERSION)-$$os-$$arch$$ext . || exit 1; \
	done
	cd dist && sha256sum * > SHA256SUMS

clean:
	rm -rf dist karaoke karaoke.exe
