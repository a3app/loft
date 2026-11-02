# loft-ndms Entware package.
# make test     host tests (no TUN, no ndmc)
# make ipk      aarch64 ipk (Giga / Peak / Hopper)
# make ipk-all  aarch64, armv7 soft-float, mipsel soft-float
# make dist     those three ipk, a .sha256 sidecar each, and
#               dist/ndms/loft-ndms-<ver>-SHA256SUMS.txt
VERSION ?= $(shell cat ../../VERSION)
# CHANNEL is github, web, store, or oem. An installed ipk cannot change it.
# UPDATE_BASE is the OEM catalog (latest.json, or a directory that has one).
CHANNEL ?= github
UPDATE_BASE ?=
LDFLAGS := -s -w -X github.com/a3app/loft/internal/version.Version=$(VERSION) -X github.com/a3app/loft/internal/version.Channel=$(CHANNEL) -X github.com/a3app/loft/internal/version.UpdateBase=$(UPDATE_BASE)
ROOT := $(abspath ../..)
PACK := $(ROOT)/clients/NDMS/pack
DIST := $(ROOT)/dist/ndms
# sha256sum on Ubuntu CI; shasum on macOS. Both print "hex  name".
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)

.PHONY: test ipk ipk-all dist clean one

test:
	cd $(ROOT) && go test ./clients/NDMS/ ./clients/go/inbound/ -count=1

ipk:
	$(MAKE) one ARCH=aarch64

ipk-all:
	$(MAKE) one ARCH=aarch64
	$(MAKE) one ARCH=armv7
	$(MAKE) one ARCH=mipsel

# ARCH is the release asset name, not the opkg Architecture field.
# armv7 and mipsel are soft-float. Go 1.21+ defaults GOARM=7 to hard-float,
# which Entware's armv7-3.2 toolchain does not run. GOMIPS=softfloat matches
# mipsel-3.4. CGO stays off so the binary does not need the Entware libc.
one:
	@set -e; \
	case "$(ARCH)" in \
	  aarch64) GOARCH=arm64; GOARM=; GOMIPS=; ENT=aarch64-3.10 ;; \
	  armv7) GOARCH=arm; GOARM=7,softfloat; GOMIPS=; ENT=armv7-3.2 ;; \
	  mipsel) GOARCH=mipsle; GOARM=; GOMIPS=softfloat; ENT=mipsel-3.4 ;; \
	  *) echo "unknown ARCH $(ARCH)"; exit 1 ;; \
	esac; \
	echo "build loft-ndms $$GOARCH $(VERSION)"; \
	cd $(ROOT) && CGO_ENABLED=0 GOOS=linux GOARCH=$$GOARCH GOARM=$$GOARM GOMIPS=$$GOMIPS \
	  go build -trimpath -ldflags "$(LDFLAGS)" \
	  -o clients/NDMS/pack/loft-ndms ./clients/NDMS/cmd/loft-ndms; \
	cd $(ROOT) && go run ./clients/NDMS/pack/mkipk \
	  -bin clients/NDMS/pack/loft-ndms \
	  -out clients/NDMS/pack/loft-ndms-$(VERSION)-$(ARCH).ipk \
	  -version $(VERSION) -arch $$ENT \
	  -init clients/NDMS/pack/S99loft \
	  -prerm clients/NDMS/pack/prerm \
	  -postinst clients/NDMS/pack/postinst \
	  -sample clients/NDMS/pack/client.yaml.sample; \
	cd $(PACK) && $(SHA256) loft-ndms-$(VERSION)-$(ARCH).ipk > loft-ndms-$(VERSION)-$(ARCH).ipk.sha256; \
	echo $(PACK)/loft-ndms-$(VERSION)-$(ARCH).ipk

# dist is what the Release uploads. The panel matches each ipk to the
# sidecar named "<ipk>.sha256". SHA256SUMS is the same digests in one
# file, with bare names, so sha256sum -c works in a flat folder.
dist: ipk-all
	mkdir -p $(DIST)
	cp $(PACK)/loft-ndms-$(VERSION)-aarch64.ipk \
	   $(PACK)/loft-ndms-$(VERSION)-aarch64.ipk.sha256 \
	   $(PACK)/loft-ndms-$(VERSION)-armv7.ipk \
	   $(PACK)/loft-ndms-$(VERSION)-armv7.ipk.sha256 \
	   $(PACK)/loft-ndms-$(VERSION)-mipsel.ipk \
	   $(PACK)/loft-ndms-$(VERSION)-mipsel.ipk.sha256 \
	   $(DIST)/
	cd $(DIST) && $(SHA256) loft-ndms-$(VERSION)-*.ipk > loft-ndms-$(VERSION)-SHA256SUMS.txt
	@ls -l $(DIST)

clean:
	rm -f $(PACK)/loft-ndms $(PACK)/*.ipk $(PACK)/*.sha256
