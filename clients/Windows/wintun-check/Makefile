# Build loft-wintun-check for Windows 7 (x86 and x64).
#
#   make                         build with $(GO) into dist/
#   make GO=~/sdk/go1.20.14/bin/go
#   make GO=/path/to/go-win7/bin/go GOROOT=/path/to/go-win7   patched Go
#   make zip                     dist/loft-wintun-check-win7.zip
#
# wintun.dll is not in git: it is fetched from wintun.net and checked by SHA-256.

GO ?= go
WINTUN_VER    := 0.14.1
WINTUN_SHA256 := 07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51
WINTUN_ZIP    := dist/wintun-$(WINTUN_VER).zip

all: dist/x86/loft-wintun-check.exe dist/x64/loft-wintun-check.exe dist/x86/wintun.dll dist/x64/wintun.dll

dist/x86/loft-wintun-check.exe: *.go go.mod
	mkdir -p $(@D)
	GOOS=windows GOARCH=386 GOTOOLCHAIN=local $(GO) build -trimpath -ldflags="-s -w" -o $@ .

dist/x64/loft-wintun-check.exe: *.go go.mod
	mkdir -p $(@D)
	GOOS=windows GOARCH=amd64 GOTOOLCHAIN=local $(GO) build -trimpath -ldflags="-s -w" -o $@ .

$(WINTUN_ZIP):
	mkdir -p $(@D)
	curl -fsSL -o $@.tmp https://www.wintun.net/builds/wintun-$(WINTUN_VER).zip
	if command -v sha256sum >/dev/null; then echo "$(WINTUN_SHA256)  $@.tmp" | sha256sum -c -; \
	else echo "$(WINTUN_SHA256)  $@.tmp" | shasum -a 256 -c -; fi
	mv $@.tmp $@

dist/x86/wintun.dll: $(WINTUN_ZIP)
	mkdir -p $(@D)
	unzip -p $< wintun/bin/x86/wintun.dll > $@

dist/x64/wintun.dll: $(WINTUN_ZIP)
	mkdir -p $(@D)
	unzip -p $< wintun/bin/amd64/wintun.dll > $@

zip: all
	cd dist && rm -f loft-wintun-check-win7.zip && zip -qr loft-wintun-check-win7.zip x86 x64

clean:
	rm -rf dist

.PHONY: all zip clean
