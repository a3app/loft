# Unsigned Simulator payload + macOS loft.app wrapper → ../../dist/clients/iOS/
# Intermediates stay under dist/clients/iOS/, not clients/iOS/.
# Device IPA / TestFlight needs the Packet Tunnel entitlement; not this target.

ROOT := $(abspath ../..)
DIST := $(ROOT)/dist/clients/iOS
XCF := $(ROOT)/clients/mobile/build/Mobile.xcframework
VER := $(shell tr -d ' \n' < $(ROOT)/VERSION)
CODE := $(lastword $(subst ., ,$(VER)))
DESTINATION ?= generic/platform=iOS Simulator
SIM ?= loft-ios
PROJ := Loft.xcodeproj
SCHEME := loft
EMU := emulator
ICNS := $(ROOT)/clients/macOS/Resources/loft.icns
IOSAPP := $(DIST)/iphonesimulator/loft.app
WRAP := $(DIST)/loft.app

.PHONY: all app pack sim run install open clean

all: pack

bind:
	$(MAKE) -C ../mobile ios

$(XCF):
	$(MAKE) -C ../mobile ios

app: $(XCF)
	mkdir -p $(DIST)/obj $(DIST)/iphonesimulator
	xcodebuild -project $(PROJ) -scheme $(SCHEME) \
		-destination '$(DESTINATION)' \
		-configuration Debug \
		CODE_SIGNING_ALLOWED=NO \
		MARKETING_VERSION=$(VER) \
		SYMROOT=$(DIST) OBJROOT=$(DIST)/obj \
		build
	rm -rf $(IOSAPP)
	cp -R $(DIST)/Debug-iphonesimulator/loft.app $(IOSAPP)
	@echo "built $(IOSAPP)  $(VER)"

pack: app
	rm -rf $(WRAP)
	mkdir -p $(WRAP)/Contents/MacOS $(WRAP)/Contents/Resources
	cp $(EMU)/loft $(WRAP)/Contents/MacOS/loft
	chmod +x $(WRAP)/Contents/MacOS/loft
	sed -e 's/__LOFT_BUNDLE_ID__/app.loft.ios.emulator/' \
		-e 's/__LOFT_NAME__/loft/' \
		-e 's/__LOFT_DISPLAY_NAME__/loft/' \
		-e 's/__LOFT_VERSION__/$(VER)/' \
		-e 's/__LOFT_CODE__/$(CODE)/' \
		$(EMU)/Info.plist > $(WRAP)/Contents/Info.plist
	printf 'APPL????' > $(WRAP)/Contents/PkgInfo
	if [ -f "$(ICNS)" ]; then cp "$(ICNS)" $(WRAP)/Contents/Resources/loft.icns; fi
	-xattr -cr $(WRAP)
	@echo "built $(WRAP)  $(VER)  (macOS wrapper → $(SIM))"

sim:
	chmod +x $(EMU)/setup-sim
	$(EMU)/setup-sim

install: pack
	open $(WRAP)

run: install

open: bind
	open $(PROJ)

clean:
	rm -rf $(DIST)
