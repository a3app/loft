# 10.13 is the floor. Go cannot ship in-process (Go 1.20 was last for 10.13).
export MACOSX_DEPLOYMENT_TARGET := 10.13
MIN := 10.13
HOSTARCH := $(shell uname -m)
ARCH ?= $(HOSTARCH)
ifeq ($(filter $(ARCH),arm64 x86_64),)
$(error ARCH must be arm64 or x86_64 (got $(ARCH)))
endif
ifeq ($(ARCH),x86_64)
TRIPLE := x86_64-apple-macosx$(MIN)
GOARCH_FRONT := amd64
else
TRIPLE := arm64-apple-macosx$(MIN)
GOARCH_FRONT := arm64
endif

SDK := $(shell xcrun --sdk macosx --show-sdk-path)
SWIFTC := xcrun swiftc
CLANG := xcrun clang
ROOT := $(abspath ../..)
# Intermediates per slice (.o, test_aead). Next to the .app, not in Sources.
OBJ := $(ROOT)/dist/clients/macOS/obj/$(ARCH)
HOSTOBJ := $(ROOT)/dist/clients/macOS/obj/$(HOSTARCH)
APP := $(ROOT)/dist/clients/macOS/loft.app
MACOS := $(APP)/Contents/MacOS
RES := $(APP)/Contents/Resources
FW := $(APP)/Contents/Frameworks
ROOTVER := $(shell cat $(ROOT)/VERSION 2>/dev/null || echo 1.0.0)
# Menu-bar marketing version. Patch +1 on every `make app` (1.0.19 → 1.0.20 → 1.0.22).
# CI: BUMP=0 stamps Info.plist from the file and leaves VERSION alone.
MACVER_FILE := VERSION
BUMP ?= 1

SWIFT_SRC := $(wildcard Sources/Engine/*.swift Sources/App/*.swift)
LANG_SRC := $(wildcard lang/*.lang)
LIST_CATALOG := $(ROOT)/lists/catalog.json
C_OBJ := $(OBJ)/chacha.o $(OBJ)/utun.o
CARCH := -arch $(ARCH) -mmacosx-version-min=$(MIN) -O2 -isysroot $(SDK)

.PHONY: all app dmg dmgs run clean test-crypto shot front-probe

all: app

# Each `make app` / `make macos` raises the patch, then stamps Info.plist.
# run / shot / front-probe do not bump. CI: `make dmg BUMP=0`.
# Sign: ad-hoc unless CODESIGN_IDENTITY is a Developer ID (see pack/identity.sh).
app: test-crypto $(APP)/Contents/MacOS/loft
	@if [ "$(BUMP)" = "0" ]; then \
		v=$$(tr -d ' \n' < $(MACVER_FILE)); \
	else \
		v=$$(awk -F. '{printf "%d.%d.%d", $$1, $$2, $$3+1}' $(MACVER_FILE)); \
		echo $$v > $(MACVER_FILE); \
	fi; \
	sed "s/__LOFT_VERSION__/$$v/g" Info.plist > $(APP)/Contents/Info.plist; \
	sh pack/sign.sh $(APP); \
	echo "built $(APP)  $$v  (macOS $(MIN)+ $(ARCH))"

# Disk image: loft.app + Applications symlink + README.txt.
# CODESIGN_IDENTITY / NOTARY_PROFILE as for `app`.
dmg: app
	ARCH=$(ARCH) sh pack/make-dmg.sh

# Both slices. First build may bump VERSION; the second keeps it.
# Apple Silicon runner: native arm64 + Rosetta-free Go/Swift x86_64 cross.
# Intel Mac: native x86_64 + arm64 cross. AEAD test always runs as HOSTARCH.
dmgs:
	$(MAKE) dmg ARCH=arm64 BUMP=$(BUMP)
	rm -rf $(APP)
	$(MAKE) dmg ARCH=x86_64 BUMP=0

# RFC 8439 on the host slice so a cross `ARCH=` still runs the test.
test-crypto: $(HOSTOBJ)/chacha.o
	mkdir -p $(HOSTOBJ)
	$(CLANG) tests/test_aead.c $(HOSTOBJ)/chacha.o -o $(HOSTOBJ)/test_aead \
		-arch $(HOSTARCH) -mmacosx-version-min=$(MIN) -O2 -isysroot $(SDK) -I Sources/Crypto
	$(HOSTOBJ)/test_aead

ifneq ($(ARCH),$(HOSTARCH))
$(HOSTOBJ)/chacha.o: Sources/Crypto/chacha20poly1305.c Sources/Crypto/chacha20poly1305.h
	mkdir -p $(HOSTOBJ)
	$(CLANG) -c Sources/Crypto/chacha20poly1305.c -o $@ \
		-arch $(HOSTARCH) -mmacosx-version-min=$(MIN) -O2 -isysroot $(SDK)
endif

$(OBJ)/chacha.o: Sources/Crypto/chacha20poly1305.c Sources/Crypto/chacha20poly1305.h
	mkdir -p $(OBJ)
	$(CLANG) -c Sources/Crypto/chacha20poly1305.c -o $@ $(CARCH)

$(OBJ)/utun.o: Sources/TUN/utun.c Sources/TUN/utun.h
	mkdir -p $(OBJ)
	$(CLANG) -c Sources/TUN/utun.c -o $@ $(CARCH)

$(OBJ)/target-arch:
	mkdir -p $(OBJ)
	echo $(ARCH) > $@

$(MACOS)/loft-priv: $(OBJ)/target-arch Sources/TUN/loft-priv.c $(OBJ)/utun.o
	mkdir -p $(MACOS)
	$(CLANG) Sources/TUN/loft-priv.c $(OBJ)/utun.o -o $(MACOS)/loft-priv \
		$(CARCH) -I Sources/TUN

FRONT_GO := $(shell find $(ROOT)/clients/go $(ROOT)/internal -name '*.go' 2>/dev/null)

$(MACOS)/loft-front: $(OBJ)/target-arch $(ROOT)/go.mod $(FRONT_GO)
	mkdir -p $(MACOS)
	cd $(ROOT) && CGO_ENABLED=0 GOOS=darwin GOARCH=$(GOARCH_FRONT) go build -trimpath \
		-ldflags="-s -w -X github.com/a3app/loft/internal/version.Version=$(ROOTVER)" \
		-o dist/clients/macOS/loft.app/Contents/MacOS/loft-front ./clients/go/cmd/loft-front

$(APP)/Contents/MacOS/loft: $(OBJ)/target-arch $(SWIFT_SRC) $(LANG_SRC) $(LIST_CATALOG) $(C_OBJ) $(MACOS)/loft-priv $(MACOS)/loft-front
	mkdir -p $(MACOS) $(RES) $(RES)/lang $(RES)/lists $(FW)
	$(SWIFTC) -swift-version 5 -target $(TRIPLE) -sdk $(SDK) -O \
		-import-objc-header Sources/Bridging-Header.h \
		-I Sources \
		-framework AppKit -framework Foundation -framework Security -framework QuartzCore -framework CoreImage -framework SystemConfiguration \
		-Xlinker $(OBJ)/chacha.o -Xlinker $(OBJ)/utun.o \
		-Xlinker -rpath -Xlinker @executable_path/../Frameworks \
		$(SWIFT_SRC) \
		-o $(MACOS)/loft
	install_name_tool -add_rpath "@executable_path/../Frameworks" $(MACOS)/loft 2>/dev/null || true
	printf 'APPL????' > $(APP)/Contents/PkgInfo
	cp Resources/loft.icns $(RES)/
	cp Resources/loftTemplate*.png $(RES)/
	cp lang/*.lang $(RES)/lang/
	cp $(LIST_CATALOG) $(RES)/lists/
	xcrun swift-stdlib-tool --copy --scan-executable $(MACOS)/loft \
	  --destination $(FW) --platform macosx --unsigned-destination $(FW)
	rm -rf $(FW)/macosx
	@for b in loft loft-front loft-priv; do \
		got=`lipo -archs $(MACOS)/$$b 2>/dev/null || true`; \
		echo "$$got" | grep -qw $(ARCH) || { echo "arch: $$b is '$$got', want $(ARCH)" >&2; exit 1; }; \
	done
	# Nested sign happens in `app` via pack/sign.sh (identity or ad-hoc).

run: $(APP)/Contents/MacOS/loft
	open $(APP)

# Debug: PNG dump of windows → /tmp/loft-shots (needs AppKit; flag lives in main.swift).
shot: $(APP)/Contents/MacOS/loft
	$(MACOS)/loft --shot

# Debug: one loft-front handshake, no TUN (same binary as the app).
front-probe: $(APP)/Contents/MacOS/loft
	$(MACOS)/loft --probe

clean:
	rm -rf build $(ROOT)/dist/clients/macOS/obj $(APP)
