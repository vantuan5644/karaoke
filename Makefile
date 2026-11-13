VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
DIST_DIR ?= dist

.PHONY: build test check run dist clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o karaoke .

test:
	go test -race ./...
	node --test "web/test/*.test.mjs"

check: test
	@unformatted=$$(gofmt -l .); test -z "$$unformatted" || { printf '%s\n' "$$unformatted"; exit 1; }
	go vet ./...

run: build
	./karaoke -open

# Each archive carries its documentation and all distribution notices.
# DIST_DIR can point to a separate build volume.
dist:
	@set -eu; \
	command -v zip >/dev/null; \
	mkdir -p "$(DIST_DIR)"; \
	stage=$$(mktemp -d "$(DIST_DIR)/.stage.XXXXXX"); \
	trap 'rm -rf "$$stage"' EXIT HUP INT TERM; \
	for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		name=karaoke-$(VERSION)-$$os-$$arch; \
		mkdir -p "$$stage/$$name"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o "$$stage/$$name/karaoke$$ext" .; \
		cp LICENSE README.md FEATURES.MD THIRD_PARTY.md CONTRIBUTING.md SECURITY.md "$$stage/$$name/"; \
		cp -R LICENSES docs "$$stage/$$name/"; \
		if [ $$os = windows ]; then \
			(cd "$$stage" && zip -qr "$$name.zip" "$$name"); \
		else \
			tar --owner=0 --group=0 --numeric-owner --mode='a=r,u+w,a+X' \
				--exclude="$$name/karaoke" -cf "$$stage/$$name.tar" -C "$$stage" "$$name"; \
			tar --owner=0 --group=0 --numeric-owner --mode=0755 \
				-rf "$$stage/$$name.tar" -C "$$stage" "$$name/karaoke"; \
			gzip "$$stage/$$name.tar"; \
		fi; \
	done; \
	(cd "$$stage" && sha256sum *.tar.gz *.zip > SHA256SUMS); \
	cp "$$stage/"*.tar.gz "$$stage/"*.zip "$$stage/SHA256SUMS" "$(DIST_DIR)/"

clean:
	rm -rf dist karaoke karaoke.exe
