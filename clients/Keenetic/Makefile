# loft-keenetic Entware package.
# make test     host tests (no TUN, no ndmc)
# make ipk      aarch64 ipk (Giga / Peak / Hopper)
# make ipk-all  aarch64, armv7 soft-float, mipsel soft-float
VERSION ?= $(shell cat ../../VERSION)
LDFLAGS := -s -w -X github.com/a3app/loft/internal/version.Version=$(VERSION)
ROOT := $(abspath ../..)
PACK := $(ROOT)/clients/Keenetic/pack

.PHONY: test ipk ipk-all clean one

test:
	cd $(ROOT) && go test ./clients/Keenetic/ ./clients/go/inbound/ -count=1

ipk:
	$(MAKE) one ARCH=aarch64

ipk-all:
	$(MAKE) one ARCH=aarch64
	$(MAKE) one ARCH=armv7
	$(MAKE) one ARCH=mipsel

# ARCH is the release asset name, not the opkg Architecture field.
one:
	@set -e; \
	case "$(ARCH)" in \
	  aarch64) GOARCH=arm64; GOARM=; GOMIPS=; ENT=aarch64-3.10 ;; \
	  armv7) GOARCH=arm; GOARM=7,softfloat; GOMIPS=; ENT=armv7-3.2 ;; \
	  mipsel) GOARCH=mipsle; GOARM=; GOMIPS=softfloat; ENT=mipsel-3.4 ;; \
	  *) echo "unknown ARCH $(ARCH)"; exit 1 ;; \
	esac; \
	echo "build loft-keenetic $$GOARCH $(VERSION)"; \
	cd $(ROOT) && CGO_ENABLED=0 GOOS=linux GOARCH=$$GOARCH GOARM=$$GOARM GOMIPS=$$GOMIPS \
	  go build -trimpath -ldflags "$(LDFLAGS)" \
	  -o clients/Keenetic/pack/loft-keenetic ./clients/Keenetic/cmd/loft-keenetic; \
	cd $(ROOT) && go run ./clients/Keenetic/pack/mkipk \
	  -bin clients/Keenetic/pack/loft-keenetic \
	  -out clients/Keenetic/pack/loft-keenetic-$(VERSION)-$(ARCH).ipk \
	  -version $(VERSION) -arch $$ENT \
	  -init clients/Keenetic/pack/S99loft \
	  -prerm clients/Keenetic/pack/prerm \
	  -postinst clients/Keenetic/pack/postinst \
	  -sample clients/Keenetic/pack/client.yaml.sample; \
	cd $(PACK) && shasum -a 256 loft-keenetic-$(VERSION)-$(ARCH).ipk | sed 's#  #  #' > loft-keenetic-$(VERSION)-$(ARCH).ipk.sha256; \
	echo $(PACK)/loft-keenetic-$(VERSION)-$(ARCH).ipk

clean:
	rm -f $(PACK)/loft-keenetic $(PACK)/*.ipk $(PACK)/*.sha256
