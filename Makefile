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

.PHONY: build test catalog dist install clean llama install-model bundle
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

# --- the bundled model (see README "The built-in model") ---
LLAMA_TAG ?= b12287
HOST_OS   := $(shell uname -s | tr A-Z a-z)
HOST_ARCH := $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
PLAT      := $(HOST_OS)-$(HOST_ARCH)
CMAKE     ?= $(or $(wildcard $(CURDIR)/.tools/cmake-*/bin/cmake),cmake)
MODEL     ?= $(lastword $(sort $(wildcard models/zshai-mini-*.gguf)))
DATA_DIR  ?= $${XDG_DATA_HOME:-$(HOME)/.local/share}/zshai
# Linux: portable x86-64 (AVX2), libstdc++ linked in. macOS: Metal, shaders embedded.
LLAMA_FLAGS_linux  := -DGGML_NATIVE=OFF -DGGML_AVX=ON -DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON -DGGML_OPENMP=OFF \
                      -DCMAKE_EXE_LINKER_FLAGS="-static-libstdc++ -static-libgcc"
LLAMA_FLAGS_darwin := -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON -DGGML_NATIVE=OFF -DCMAKE_OSX_DEPLOYMENT_TARGET=13.0

# Build llama.cpp's llama-server (one self-contained binary) for this machine.
llama:
	test -d .tools/llama.cpp || git clone -q --depth 1 --branch $(LLAMA_TAG) https://github.com/ggml-org/llama.cpp.git .tools/llama.cpp
	$(CMAKE) -S .tools/llama.cpp -B .tools/llama.cpp/build-$(PLAT) -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
	  -DLLAMA_CURL=OFF -DLLAMA_OPENSSL=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF $(LLAMA_FLAGS_$(HOST_OS)) \
	  -DCMAKE_C_FLAGS="-ffile-prefix-map=$(CURDIR)/.tools/llama.cpp=llama.cpp" -DCMAKE_CXX_FLAGS="-ffile-prefix-map=$(CURDIR)/.tools/llama.cpp=llama.cpp"
	$(CMAKE) --build .tools/llama.cpp/build-$(PLAT) --target llama-server -j
	install -d build/$(PLAT)
	install -m 755 .tools/llama.cpp/build-$(PLAT)/bin/llama-server build/$(PLAT)/llama-server
	strip build/$(PLAT)/llama-server 2>/dev/null || true

# Install llama-server and the model next to zshai (~/.local/share/zshai).
install-model:
	test -n "$(MODEL)" || { echo "no models/zshai-mini-*.gguf (download one, or MODEL=path)"; exit 1; }
	test -x build/$(PLAT)/llama-server || $(MAKE) llama
	install -d $(DATA_DIR)/models
	install -m 755 build/$(PLAT)/llama-server $(DATA_DIR)/llama-server
	install -m 644 $(MODEL) $(DATA_DIR)/models/
	install -m 644 third_party/llama.cpp-LICENSE $(DATA_DIR)/llama.cpp-LICENSE

# A release bundle for one platform: zshai + plugin + llama-server + model + install.sh.
#   make bundle OS=darwin ARCH=arm64   (llama-server from build/darwin-arm64/, built on that platform)
OS   ?= $(HOST_OS)
ARCH ?= $(HOST_ARCH)
bundle: dist
	test -x build/$(OS)-$(ARCH)/llama-server || { echo "build/$(OS)-$(ARCH)/llama-server is missing: run make llama on that platform"; exit 1; }
	test -n "$(MODEL)" || { echo "no models/zshai-mini-*.gguf"; exit 1; }
	rm -rf dist/zshai-$(VERSION)-$(OS)-$(ARCH)
	install -d dist/zshai-$(VERSION)-$(OS)-$(ARCH)/bin dist/zshai-$(VERSION)-$(OS)-$(ARCH)/share/zshai/models
	install -m 755 dist/zshai-$(OS)-$(ARCH) dist/zshai-$(VERSION)-$(OS)-$(ARCH)/bin/zshai
	install -m 755 build/$(OS)-$(ARCH)/llama-server dist/zshai-$(VERSION)-$(OS)-$(ARCH)/share/zshai/llama-server
	install -m 644 $(MODEL) dist/zshai-$(VERSION)-$(OS)-$(ARCH)/share/zshai/models/
	install -m 644 dist/zshai.plugin.zsh dist/zshai-$(VERSION)-$(OS)-$(ARCH)/share/zshai/zshai.plugin.zsh
	install -m 644 third_party/llama.cpp-LICENSE LICENSE README.md dist/zshai-$(VERSION)-$(OS)-$(ARCH)/share/zshai/
	install -m 755 scripts/install.sh dist/zshai-$(VERSION)-$(OS)-$(ARCH)/install.sh
	tar -C dist -czf dist/zshai-$(VERSION)-$(OS)-$(ARCH).tar.gz zshai-$(VERSION)-$(OS)-$(ARCH)
	ls -lh dist/zshai-$(VERSION)-$(OS)-$(ARCH).tar.gz

clean:
	rm -f zshai
	rm -f dist/zshai-* dist/zshai.plugin.zsh dist/VERSION
