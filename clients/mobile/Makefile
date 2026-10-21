# gomobile bind of this package. Output in build/ (gitignored).
# iOS:  make ios      → build/Mobile.xcframework
# Android: make android → build/engine.aar  (needs NDK)

ROOT := $(abspath ../..)
VERSION ?= $(shell cat $(ROOT)/VERSION)
LDFLAGS := -s -w -X github.com/a3app/loft/internal/version.Version=$(VERSION)
OUT := build
IOS_MIN := 15.0
ANDROID_API := 24
GOBIN := $(shell go env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

.PHONY: tools ios android clean

tools:
	@command -v gomobile >/dev/null || go install golang.org/x/mobile/cmd/gomobile@latest
	@command -v gobind >/dev/null || go install golang.org/x/mobile/cmd/gobind@latest

ios: tools
	mkdir -p $(OUT)
	cd $(ROOT) && gomobile bind -v \
		-target ios \
		-iosversion $(IOS_MIN) \
		-ldflags "$(LDFLAGS)" \
		-o $(CURDIR)/$(OUT)/Mobile.xcframework \
		github.com/a3app/loft/clients/mobile

android: tools
	mkdir -p $(OUT)
	cd $(ROOT) && CGO_LDFLAGS="-Wl,-z,max-page-size=16384" gomobile bind -v \
		-target android \
		-androidapi $(ANDROID_API) \
		-javapkg app.loft.engine \
		-ldflags "$(LDFLAGS)" \
		-o $(CURDIR)/$(OUT)/engine.aar \
		github.com/a3app/loft/clients/mobile

clean:
	rm -rf $(OUT)
