VERSION ?= $(shell cat VERSION)
LDFLAGS := -s -w -X github.com/a3app/loft/internal/version.Version=$(VERSION)
DIST := dist
CLIENTS := $(DIST)/clients
NODE := $(DIST)/node
DEBDIR := server/debian
ARCH ?= amd64

CLIENT_OSARCH := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64
CLIENT_CMDS := loft loft-front loft-probe loft-bench

.PHONY: test node client front probe bench macos macos-dmg macos-dmgs dist dist-node dist-clients \
	dist-clean-node dist-clean-legacy dist-debian dist-debian-amd64 dist-debian-arm64 \
	dist-one dist-deb dist-deb-amd64 dist-deb-arm64 deb \
	mobile-ios mobile-android mobile

mobile-ios:
	$(MAKE) -C clients/mobile ios

mobile-android:
	$(MAKE) -C clients/mobile android

mobile: mobile-ios mobile-android

test:
	go test ./...
	sh $(DEBDIR)/install_os_test.sh

node:
	go build -ldflags "$(LDFLAGS)" -o loft-node ./server/cmd/loft-node

client:
	mkdir -p $(CLIENTS)/go
	go build -ldflags "$(LDFLAGS)" -o $(CLIENTS)/go/loft ./clients/go/cmd/loft

front:
	mkdir -p $(CLIENTS)/go
	go build -ldflags "$(LDFLAGS)" -o $(CLIENTS)/go/loft-front ./clients/go/cmd/loft-front

probe:
	mkdir -p $(CLIENTS)/go
	go build -ldflags "$(LDFLAGS)" -o $(CLIENTS)/go/loft-probe ./clients/go/cmd/loft-probe

bench:
	mkdir -p $(CLIENTS)/go
	go build -ldflags "$(LDFLAGS)" -o $(CLIENTS)/go/loft-bench ./clients/go/cmd/loft-bench

# BUMP is forwarded so `make macos-dmg BUMP=0` works on GNU and BSD make (CI).
# Root ARCH defaults to debian amd64; only pass it through when it is a macOS slice.
MAC_ARCH :=
ifneq ($(filter $(ARCH),arm64 x86_64),)
MAC_ARCH := ARCH=$(ARCH)
endif

macos:
	$(MAKE) -C clients/macOS app BUMP=$(BUMP) $(MAC_ARCH)

macos-dmg:
	$(MAKE) -C clients/macOS dmg BUMP=$(BUMP) $(MAC_ARCH)

macos-dmgs:
	$(MAKE) -C clients/macOS dmgs BUMP=$(BUMP)

# Server packages + CLI archives. macOS .app/.dmg is make macos-dmg / macos-dmgs (needs Xcode).
dist: dist-node dist-clients

dist-node: dist-clean-legacy dist-debian dist-deb
	cp $(DEBDIR)/DIST.md $(NODE)/README.md
	cd $(NODE) && find . \( -name '*.tar.gz' -o -name '*.deb' \) -print | sort | \
		while IFS= read -r f; do \
			if command -v sha256sum >/dev/null 2>&1; then sha256sum "$$f"; else shasum -a 256 "$$f"; fi; \
		done > SHA256SUMS
	@echo $(NODE)

# Cross-compiled CLI (CGO off). Windows is not a target: TUN uses unix.
dist-clients:
	mkdir -p $(CLIENTS)/go
	rm -f $(CLIENTS)/go/*.tar.gz $(CLIENTS)/go/SHA256SUMS
	@for pair in $(CLIENT_OSARCH); do \
		goos=$${pair%%-*}; \
		goarch=$${pair#*-}; \
		dest=$(CLIENTS)/go/$$goos-$$goarch; \
		name=loft-$(VERSION)-$$goos-$$goarch; \
		rm -rf $$dest; \
		mkdir -p $$dest; \
		for b in $(CLIENT_CMDS); do \
			CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -trimpath -ldflags "$(LDFLAGS)" \
				-o $$dest/$$b ./clients/go/cmd/$$b || exit 1; \
		done; \
		tar -C $$dest -czf $(CLIENTS)/go/$$name.tar.gz $(CLIENT_CMDS); \
		echo $(CLIENTS)/go/$$name.tar.gz; \
	done
	cd $(CLIENTS)/go && find . -name '*.tar.gz' -print | sort | \
		while IFS= read -r f; do \
			if command -v sha256sum >/dev/null 2>&1; then sha256sum "$$f"; else shasum -a 256 "$$f"; fi; \
		done > SHA256SUMS

dist-clean-node:
	rm -rf $(NODE)

# Drop the old flat dist/loft-node-* layout.
dist-clean-legacy:
	rm -rf $(DIST)/loft-node-[0-9]* $(DIST)/loft-node_[0-9]*.deb

dist-debian: dist-debian-amd64 dist-debian-arm64

dist-debian-amd64:
	$(MAKE) dist-one GOOS=linux GOARCH=amd64

dist-debian-arm64:
	$(MAKE) dist-one GOOS=linux GOARCH=arm64

# GOOS/GOARCH must be set.
dist-one:
	@test -n "$(GOOS)" && test -n "$(GOARCH)"
	DEST=$(NODE)/$(GOOS)-$(GOARCH); \
	NAME=loft-node-$(VERSION)-$(GOOS)-$(GOARCH); \
	rm -rf $$DEST; \
	mkdir -p $$DEST/$$NAME/configs $$DEST/$$NAME/sysctl.d; \
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $$DEST/$$NAME/loft-node ./server/cmd/loft-node; \
	cp $(DEBDIR)/install.sh $$DEST/$$NAME/install.sh; \
	cp $(DEBDIR)/loft-node.service $$DEST/$$NAME/loft-node.service; \
	cp $(DEBDIR)/README.md $$DEST/$$NAME/README.md; \
	cp $(DEBDIR)/sysctl.d/99-loft.conf $$DEST/$$NAME/sysctl.d/99-loft.conf; \
	cp $(DEBDIR)/configs/*.conf $$DEST/$$NAME/configs/; \
	cp VERSION $$DEST/$$NAME/VERSION; \
	chmod 0755 $$DEST/$$NAME/install.sh $$DEST/$$NAME/loft-node; \
	tar -C $$DEST -czf $$DEST/$$NAME.tar.gz $$NAME; \
	echo $$DEST/$$NAME.tar.gz

dist-deb: dist-deb-amd64 dist-deb-arm64

dist-deb-amd64:
	ARCH=amd64 OUTDIR=$(NODE)/linux-amd64 sh $(DEBDIR)/build-deb.sh

dist-deb-arm64:
	ARCH=arm64 OUTDIR=$(NODE)/linux-arm64 sh $(DEBDIR)/build-deb.sh

deb:
	ARCH=$(ARCH) OUTDIR=$(NODE)/linux-$(ARCH) sh $(DEBDIR)/build-deb.sh
