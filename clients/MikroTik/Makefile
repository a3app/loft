# loft RouterOS image: static linux loft in an OCI image.
#
#   make image GOARCH=arm64   one image as pack/loft-linux-<arch>.tar (docker
#                             save), to upload by hand as <disk>/loft/loft.tar.
#                             GOARCH: amd64 (CHR, x86) | arm64 (RB5009, hAP ax,
#                             CCR2004) | arm (32-bit ARM, built as ARMv7).
#   make dist                 all three as dist/mikrotik/loft-mikrotik-<ver>-<arch>.tar
#                             plus SHA256SUMS: the files of the GitHub Release.
#   make push                 one multi-arch image to $(REPO) as :<ver> and
#                             :latest (Dist does it on a tag, after docker login).
#
# Every build passes --provenance=false --sbom=false. Recent Docker otherwise
# adds an attestation manifest next to the image, the tar then holds an index
# of indexes, and RouterOS stops with "no config found in manifest". Without
# attestations there is one plain image manifest per architecture, which
# RouterOS unpacks.
VERSION ?= $(shell cat ../../VERSION)
LDFLAGS := -s -w -X github.com/a3app/loft/internal/version.Version=$(VERSION)
REPO ?= ghcr.io/a3app/loft-mikrotik
GOARCH ?= arm64

ROOT := $(abspath ../..)
DIST := $(ROOT)/dist/mikrotik
SHA256 := $(shell command -v sha256sum 2>/dev/null || echo shasum -a 256)

# Docker's names for each architecture: platform, and the suffix of the
# binary the Dockerfile copies (TARGETARCH + TARGETVARIANT).
plat_amd64 := linux/amd64
plat_arm64 := linux/arm64
plat_arm   := linux/arm/v7
bin_amd64  := amd64
bin_arm64  := arm64
bin_arm    := armv7

BUILD := docker buildx build --provenance=false --sbom=false -f pack/Dockerfile

.PHONY: loft-bin bins image dist push clean

# loft-bin builds pack/loft-<amd64|arm64|armv7> for GOARCH. 32-bit ARM is
# pinned to ARMv7 (GOARM=7): every MikroTik ARM board that runs containers
# is v7 or newer.
loft-bin:
	cd $(ROOT) && CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) GOARM=7 go build -trimpath -ldflags "$(LDFLAGS)" \
		-o clients/MikroTik/pack/loft-$(bin_$(GOARCH)) ./clients/go/cmd/loft

bins:
	$(MAKE) loft-bin GOARCH=amd64
	$(MAKE) loft-bin GOARCH=arm64
	$(MAKE) loft-bin GOARCH=arm

image: loft-bin
	$(BUILD) --platform $(plat_$(GOARCH)) --load -t loft-mikrotik:$(VERSION)-$(GOARCH) pack
	docker save loft-mikrotik:$(VERSION)-$(GOARCH) -o pack/loft-linux-$(GOARCH).tar
	@echo pack/loft-linux-$(GOARCH).tar

# dist writes each architecture straight to a docker-save tarball
# (type=docker), the format RouterOS reads from a file.
dist: bins
	mkdir -p $(DIST)
	for a in amd64 arm64 arm; do \
		case $$a in amd64) p=linux/amd64; n=amd64;; arm64) p=linux/arm64; n=arm64;; arm) p=linux/arm/v7; n=armv7;; esac; \
		$(BUILD) --platform $$p -t $(REPO):$(VERSION) \
			--output type=docker,dest=$(DIST)/loft-mikrotik-$(VERSION)-$$n.tar pack || exit 1; \
	done
	cd $(DIST) && $(SHA256) loft-mikrotik-$(VERSION)-*.tar > loft-mikrotik-$(VERSION)-SHA256SUMS.txt
	@ls -l $(DIST)

# push needs a builder that can build several platforms at once
# (docker buildx create --use) and a docker login to the registry.
push: bins
	$(BUILD) --platform $(plat_amd64),$(plat_arm64),$(plat_arm) \
		-t $(REPO):$(VERSION) -t $(REPO):latest --push pack

clean:
	rm -f pack/loft pack/loft-amd64 pack/loft-arm64 pack/loft-armv7 pack/loft-linux-*.tar
