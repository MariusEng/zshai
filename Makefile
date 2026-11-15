# zshai build (see README).
# Uses .tools/go when present, otherwise the go on PATH.
GO      ?= $(if $(wildcard $(CURDIR)/.tools/go/bin/go),$(CURDIR)/.tools/go/bin/go,go)
export GOPATH  := $(CURDIR)/.tools/gopath
export GOCACHE := $(CURDIR)/.tools/gocache
export GOTELEMETRY := off
# Version: the VERSION file; commit and build date are stamped in too (zshai version).
VERSION ?= $(shell cat VERSION)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo uncommitted)$(shell git diff --quiet HEAD -- 2>/dev/null || echo +dirty)
DATE    := $(shell date -u +%Y-%m-%d)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)
# the plugin carries the same version, so `zshai: status` can tell when the two don't match
PLUGIN_SED := sed 's/^typeset -g ZSHAI_PLUGIN_VERSION=dev /typeset -g ZSHAI_PLUGIN_VERSION=$(VERSION) /' 

.PHONY: build test catalog dist install clean
build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o zshai ./cmd/zshai

test:
	$(GO) vet ./...
	$(GO) test ./...

# Re-extract the Azure CLI catalog from the az on this machine and embed it.
catalog:
	/opt/az/bin/python3 -I internal/azcat/az_extract.py catalog/az.json
	gzip -9 -c catalog/az.json > internal/azcat/az.json.gz

dist:
	for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do \
	  GOOS=$${t%/*} GOARCH=$${t#*/} CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
	    -o dist/zshai-$${t%/*}-$${t#*/} ./cmd/zshai || exit 1; done
	$(PLUGIN_SED) plugin/zshai.plugin.zsh > dist/zshai.plugin.zsh
	echo $(VERSION) > dist/VERSION

# Install the helper into ~/.local/bin and the plugin into oh-my-zsh's custom plugins.
install: build
	install -d $(HOME)/.local/bin $${ZSH_CUSTOM:-$(HOME)/.oh-my-zsh/custom}/plugins/zshai
	install -m 755 zshai $(HOME)/.local/bin/zshai
	$(PLUGIN_SED) plugin/zshai.plugin.zsh > $${ZSH_CUSTOM:-$(HOME)/.oh-my-zsh/custom}/plugins/zshai/zshai.plugin.zsh

clean:
	rm -f zshai
	rm -f dist/zshai-* dist/zshai.plugin.zsh dist/VERSION
