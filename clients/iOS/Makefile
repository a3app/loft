# Unsigned Simulator loft.app → ../../dist/clients/iOS/
# Intermediates stay under dist/clients/iOS/, not clients/iOS/.
# Device IPA / TestFlight needs the Packet Tunnel entitlement; not this target.

ROOT := $(abspath ../..)
DIST := $(ROOT)/dist/clients/iOS
XCF := $(ROOT)/clients/mobile/build/Mobile.xcframework
VER := $(shell tr -d ' \n' < $(ROOT)/VERSION)
DESTINATION ?= generic/platform=iOS Simulator
SIM ?= loft-ios
PROJ := Loft.xcodeproj
SCHEME := loft

.PHONY: all app bind run install open clean

all: app

bind:
	$(MAKE) -C ../mobile ios

$(XCF):
	$(MAKE) -C ../mobile ios

app: $(XCF)
	mkdir -p $(DIST)/obj
	xcodebuild -project $(PROJ) -scheme $(SCHEME) \
		-destination '$(DESTINATION)' \
		-configuration Debug \
		CODE_SIGNING_ALLOWED=NO \
		MARKETING_VERSION=$(VER) \
		SYMROOT=$(DIST) OBJROOT=$(DIST)/obj \
		build
	rm -rf $(DIST)/loft.app
	cp -R $(DIST)/Debug-iphonesimulator/loft.app $(DIST)/loft.app
	@echo "built $(DIST)/loft.app  $(VER)"

install: app
	open -a Simulator
	-xcrun simctl boot $(SIM)
	@if xcrun simctl list devices available | grep -q '$(SIM) ('; then \
		xcrun simctl install $(SIM) $(DIST)/loft.app; \
	else \
		xcrun simctl install booted $(DIST)/loft.app; \
	fi
	@echo "installed $(DIST)/loft.app"

run: install
	@if xcrun simctl list devices available | grep -q '$(SIM) ('; then \
		xcrun simctl launch $(SIM) app.loft.ios; \
	else \
		xcrun simctl launch booted app.loft.ios; \
	fi

open: bind
	open $(PROJ)

clean:
	rm -rf $(DIST)
