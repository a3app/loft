# loft RouterOS image: static linux loft in an OCI tarball.
# GOARCH=arm64 (RB5009, hAP ax, CCR) | arm (32-bit ARM) | amd64 (CHR).
VERSION ?= $(shell cat ../../VERSION)
LDFLAGS := -s -w -X github.com/a3app/loft/internal/version.Version=$(VERSION)
GOARCH ?= arm64
PLATFORM = linux/$(GOARCH)
ifeq ($(GOARCH),arm)
PLATFORM = linux/arm/v7
endif

.PHONY: loft-bin image clean

ROOT := $(abspath ../..)
loft-bin:
	cd $(ROOT) && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -trimpath -ldflags "$(LDFLAGS)" \
		-o clients/MikroTik/pack/loft ./clients/go/cmd/loft

# docker save → pack/loft-linux-$(GOARCH).tar  (upload as <disk>/loft/loft.tar)
image: loft-bin
	docker build --platform $(PLATFORM) -f pack/Dockerfile -t loft-mikrotik:$(VERSION)-$(GOARCH) pack
	docker save loft-mikrotik:$(VERSION)-$(GOARCH) -o pack/loft-linux-$(GOARCH).tar
	@echo pack/loft-linux-$(GOARCH).tar

clean:
	rm -f pack/loft pack/loft-linux-*.tar
