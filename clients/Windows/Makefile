# Loft Windows client. x86 and x86-64: Windows 7 SP1 and later.
# arm64: Windows 10 on ARM / Windows 11 ARM. Built with llvm-mingw when present.
# Artifacts: ../../dist/clients/Windows/  (same tree as macOS, iOS, Android).
#
#   make                         x86 and x64; arm64 too if LLVM_MINGW or clang is set
#   make portable                GitHub portable zips and SHA256SUMS
#   make release                 portable zips, setup exes, Store layout
#   make test                    host clang tests of the portable parsers
#
# Dist passes VER from the repository VERSION so the GitHub build matches the
# release tag. A local make keeps clients/Windows/VERSION.
#   make front GO_WIN7=/path/to/go-win7/bin/go GOROOT=/path/to/go-win7
#   make front-arm64             loft-front.exe for arm64 with official Go
#
# GO_WIN7 is a patched Go (XTLS/go-win7). Official Go 1.21+ cannot target Windows 7.
# arm64 front uses stock Go (GOOS=windows GOARCH=arm64). With GO_WIN7 unset,
# x86/x64 loft-front.exe is skipped. wintun.dll is not in git.
#
# Dist zip names: windows-x86-64, windows-x86, windows-arm64.
# Build dirs stay x64 / x86 / arm64.

ROOT := $(abspath ../..)
DIST := $(ROOT)/dist/clients/Windows
VER  := $(shell cat VERSION)
CC64 := x86_64-w64-mingw32-gcc
CC86 := i686-w64-mingw32-gcc
WR64 := x86_64-w64-mingw32-windres
WR86 := i686-w64-mingw32-windres
LLVM_MINGW ?=
ifneq ($(strip $(LLVM_MINGW)),)
CCARM := $(LLVM_MINGW)/bin/aarch64-w64-mingw32-clang
WRARM := $(LLVM_MINGW)/bin/aarch64-w64-mingw32-windres
else
CCARM := $(shell command -v aarch64-w64-mingw32-clang 2>/dev/null)
WRARM := $(shell command -v aarch64-w64-mingw32-windres 2>/dev/null)
endif

CFLAGS := -std=c11 -Wall -Wextra -O2 -DUNICODE -D_UNICODE \
	-D_WIN32_WINNT=0x0601 -DWINVER=0x0601 \
	-DLOFT_VERSION=\"$(VER)\" \
	-mwindows -static -municode -Isrc -Isrc/win

GUI_SRC := src/util.c src/json.c src/l10n.c src/profile.c src/share.c src/routes.c \
	src/win/utf.c src/win/key.c src/win/svcclient.c src/win/assoc.c \
	src/win/store.c src/win/app.c src/win/session.c src/win/net.c \
	src/win/draw.c src/win/chrome.c src/win/tray.c src/win/ui.c src/win/upd.c src/win/puff.c src/win/main.c
HDR := src/util.h src/json.h src/l10n.h src/profile.h src/share.h src/routes.h src/win/common.h

GUI_LIBS := -lws2_32 -lcrypt32 -lwinhttp -lshell32 -lcomdlg32 -lole32 -luuid -lgdi32 -luser32 -ladvapi32 -luserenv -lbcrypt
SVC_LIBS := -liphlpapi -ladvapi32 -lws2_32 -luser32 -lshell32

.PHONY: all test front front-arm64 assets clean release github portable store payload

ARM_OK := $(if $(and $(CCARM),$(WRARM)),1,)

all: $(DIST)/x64/loft.exe $(DIST)/x86/loft.exe $(DIST)/x64/loft-svc.exe $(DIST)/x86/loft-svc.exe assets front
ifeq ($(ARM_OK),1)
all: $(DIST)/arm64/loft.exe $(DIST)/arm64/loft-svc.exe
endif

ICONS := $(ROOT)/docs/brand/windows

$(DIST)/x64/app.res: res/app.rc res/app.manifest $(ICONS)/loft.ico $(ICONS)/loft-file.ico
	mkdir -p $(@D)
	$(WR64) -O coff -I $(ICONS) -o $@ res/app.rc

$(DIST)/x86/app.res: res/app.rc res/app.manifest $(ICONS)/loft.ico $(ICONS)/loft-file.ico
	mkdir -p $(@D)
	$(WR86) -O coff -I $(ICONS) -o $@ res/app.rc

$(DIST)/x64/setup.res: res/setup.rc res/app.manifest $(ICONS)/loft-setup.ico
	mkdir -p $(@D)
	$(WR64) -O coff -I $(ICONS) -o $@ res/setup.rc

$(DIST)/x86/setup.res: res/setup.rc res/app.manifest $(ICONS)/loft-setup.ico
	mkdir -p $(@D)
	$(WR86) -O coff -I $(ICONS) -o $@ res/setup.rc

$(DIST)/x64/loft.exe: $(GUI_SRC) $(HDR) $(DIST)/x64/app.res VERSION
	$(CC64) $(CFLAGS) -o $@ $(GUI_SRC) $(DIST)/x64/app.res $(GUI_LIBS)

$(DIST)/x86/loft.exe: $(GUI_SRC) $(HDR) $(DIST)/x86/app.res VERSION
	$(CC86) $(CFLAGS) -o $@ $(GUI_SRC) $(DIST)/x86/app.res $(GUI_LIBS)

$(DIST)/x64/svc.res: res/svc.rc res/app.manifest $(ROOT)/docs/brand/windows/loft-svc.ico
	mkdir -p $(@D)
	$(WR64) -O coff -I $(ROOT)/docs/brand/windows -o $@ res/svc.rc

$(DIST)/x86/svc.res: res/svc.rc res/app.manifest $(ROOT)/docs/brand/windows/loft-svc.ico
	mkdir -p $(@D)
	$(WR86) -O coff -I $(ROOT)/docs/brand/windows -o $@ res/svc.rc

$(DIST)/x64/loft-svc.exe: svc/loft-svc.c $(DIST)/x64/svc.res
	$(CC64) $(CFLAGS) -o $@ svc/loft-svc.c $(DIST)/x64/svc.res $(SVC_LIBS)

$(DIST)/x86/loft-svc.exe: svc/loft-svc.c $(DIST)/x86/svc.res
	$(CC86) $(CFLAGS) -o $@ svc/loft-svc.c $(DIST)/x86/svc.res $(SVC_LIBS)

$(DIST)/arm64/app.res: res/app.rc res/app.manifest $(ICONS)/loft.ico $(ICONS)/loft-file.ico
	mkdir -p $(@D)
	$(WRARM) -O coff -I $(ICONS) -o $@ res/app.rc

$(DIST)/arm64/svc.res: res/svc.rc res/app.manifest $(ROOT)/docs/brand/windows/loft-svc.ico
	mkdir -p $(@D)
	$(WRARM) -O coff -I $(ROOT)/docs/brand/windows -o $@ res/svc.rc

$(DIST)/arm64/loft.exe: $(GUI_SRC) $(HDR) $(DIST)/arm64/app.res VERSION
	$(CCARM) $(CFLAGS) -o $@ $(GUI_SRC) $(DIST)/arm64/app.res $(GUI_LIBS)

$(DIST)/arm64/loft-svc.exe: svc/loft-svc.c $(DIST)/arm64/svc.res
	$(CCARM) $(CFLAGS) -o $@ svc/loft-svc.c $(DIST)/arm64/svc.res $(SVC_LIBS)

assets:
	@set -e; \
	arches="x64 x86"; \
	if [ -f $(DIST)/arm64/loft.exe ] || [ "$(ARM_OK)" = 1 ]; then arches="$$arches arm64"; fi; \
	for a in $$arches; do \
	  mkdir -p $(DIST)/$$a/lang $(DIST)/$$a/tray; \
	  cp ../macOS/lang/*.lang $(DIST)/$$a/lang/; \
	  cp design/lang/win.lang $(DIST)/$$a/lang/win.lang; \
	  for p in 16 20 24 32; do \
	    for t in tray tray-off tray-err tray-light tray-light-off tray-light-err; do \
	      cp ../../docs/brand/tray/loft-$$t-$$p.png $(DIST)/$$a/tray/; \
	    done; \
	  done; \
	  cp wintun-check/dist/$$a/wintun.dll $(DIST)/$$a/wintun.dll; \
	done

front:
ifeq ($(strip $(GO_WIN7)),)
	@echo "skip loft-front.exe (set GO_WIN7 to a patched go-win7)"
else
	@set -e; \
	front=$(ROOT)/clients/go/cmd/loft-front; \
	trap 'rm -f "$$front"/rsrc_windows_amd64.syso "$$front"/rsrc_windows_386.syso' EXIT; \
	mkdir -p $(DIST)/x64 $(DIST)/x86; \
	$(WR64) -O coff -I $(ROOT)/docs/brand/windows -o "$$front"/rsrc_windows_amd64.syso res/front.rc; \
	$(WR86) -O coff -I $(ROOT)/docs/brand/windows -o "$$front"/rsrc_windows_386.syso res/front.rc; \
	cd $(ROOT) && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local \
		$(if $(GOROOT),GOROOT=$(GOROOT),) \
		$(GO_WIN7) build -trimpath -ldflags="-s -w" \
		-o $(DIST)/x64/loft-front.exe ./clients/go/cmd/loft-front; \
	cd $(ROOT) && GOOS=windows GOARCH=386 CGO_ENABLED=0 GOTOOLCHAIN=local \
		$(if $(GOROOT),GOROOT=$(GOROOT),) \
		$(GO_WIN7) build -trimpath -ldflags="-s -w" \
		-o $(DIST)/x86/loft-front.exe ./clients/go/cmd/loft-front
endif

# Official Go. Dist runs this before overwriting GOROOT with go-win7.
front-arm64:
ifeq ($(ARM_OK),1)
	@set -e; \
	front=$(ROOT)/clients/go/cmd/loft-front; \
	trap 'rm -f "$$front"/rsrc_windows_arm64.syso' EXIT; \
	mkdir -p $(DIST)/arm64; \
	$(WRARM) -O coff -I $(ROOT)/docs/brand/windows -o "$$front"/rsrc_windows_arm64.syso res/front.rc; \
	cd $(ROOT) && GOOS=windows GOARCH=arm64 CGO_ENABLED=0 \
		go build -trimpath -ldflags="-s -w" \
		-o $(DIST)/arm64/loft-front.exe ./clients/go/cmd/loft-front
else
	@echo "skip arm64 loft-front.exe (set LLVM_MINGW or aarch64-w64-mingw32-clang)"
endif

test:
	clang -std=c11 -Wall -Wextra -Isrc -o /tmp/loft-core-test tests/test_core.c \
		src/util.c src/json.c src/l10n.c src/profile.c src/share.c src/routes.c
	/tmp/loft-core-test

clean:
	rm -rf $(DIST)

# release: GitHub portable zips, GitHub installers, and the Store layout.
# The Store layout is not an installable MSIX. makeappx and signing run on Windows.
# Logos carry scale-/targetsize- qualifiers (docs/brand/windows/store), so build
# resources.pri with makepri before makeappx.

release: github store

portable: payload
	@set -e; \
	mkdir -p $(DIST)/github; \
	zips=""; \
	for a in x64 x86 arm64; do \
	  [ -f $(DIST)/payload/$$a/loft.exe ] || continue; \
	  case $$a in x64) tag=x86-64 ;; x86) tag=x86 ;; arm64) tag=arm64 ;; esac; \
	  z=$(DIST)/github/loft-$(VER)-windows-$$tag.zip; \
	  rm -f "$$z"; \
	  ( cd $(DIST)/payload/$$a && COPYFILE_DISABLE=1 zip -9 -X -r "$$z" . -x '.*' -x '*/.*' ); \
	  zips="$$zips loft-$(VER)-windows-$$tag.zip"; \
	done; \
	[ -n "$$zips" ]; \
	cd $(DIST)/github && \
	( command -v sha256sum >/dev/null && sha256sum $$zips || shasum -a 256 $$zips ) \
	  > loft-windows-$(VER)-SHA256SUMS.txt

github: portable \
	$(DIST)/github/loft-windows-x64-setup.exe $(DIST)/github/loft-windows-x86-setup.exe

payload: $(DIST)/x64/loft.exe $(DIST)/x86/loft.exe $(DIST)/x64/loft-svc.exe $(DIST)/x86/loft-svc.exe assets
ifeq ($(ARM_OK),1)
payload: $(DIST)/arm64/loft.exe $(DIST)/arm64/loft-svc.exe
endif
	@set -e; \
	arches="x64 x86"; \
	if [ -f $(DIST)/arm64/loft.exe ]; then arches="$$arches arm64"; fi; \
	for a in $$arches; do \
	  rm -rf $(DIST)/payload/$$a; \
	  mkdir -p $(DIST)/payload/$$a; \
	  cp $(DIST)/$$a/loft.exe $(DIST)/$$a/loft-svc.exe $(DIST)/$$a/wintun.dll $(DIST)/payload/$$a/; \
	  cp -R $(DIST)/$$a/lang $(DIST)/$$a/tray $(DIST)/payload/$$a/; \
	  if [ -f $(DIST)/$$a/loft-front.exe ]; then cp $(DIST)/$$a/loft-front.exe $(DIST)/payload/$$a/; fi; \
	done

$(DIST)/x64/loft-setup-stub.exe: setup/loft-setup.c VERSION $(DIST)/x64/setup.res
	$(CC64) $(CFLAGS) -o $@ setup/loft-setup.c $(DIST)/x64/setup.res -luser32 -lgdi32 -lshell32 -lole32 -luuid -ladvapi32 -luserenv

$(DIST)/x86/loft-setup-stub.exe: setup/loft-setup.c VERSION $(DIST)/x86/setup.res
	$(CC86) $(CFLAGS) -o $@ setup/loft-setup.c $(DIST)/x86/setup.res -luser32 -lgdi32 -lshell32 -lole32 -luuid -ladvapi32 -luserenv

$(DIST)/github/loft-windows-x64-setup.exe $(DIST)/github/loft-windows-x86-setup.exe: $(DIST)/x64/loft-setup-stub.exe $(DIST)/x86/loft-setup-stub.exe payload setup/pack_setup.py
	@set -e; \
	mkdir -p $(DIST)/github; \
	python3 setup/pack_setup.py $(DIST)/payload/x64 $(DIST)/x64/loft-setup-stub.exe $(DIST)/github/loft-windows-x64-setup.exe; \
	python3 setup/pack_setup.py $(DIST)/payload/x86 $(DIST)/x86/loft-setup-stub.exe $(DIST)/github/loft-windows-x86-setup.exe

$(DIST)/store/x64/loft.exe: $(GUI_SRC) $(HDR) $(DIST)/x64/app.res VERSION
	mkdir -p $(@D)
	$(CC64) $(CFLAGS) -DLOFT_STORE=1 -o $@ $(GUI_SRC) $(DIST)/x64/app.res $(GUI_LIBS)

store: $(DIST)/store/x64/loft.exe assets
	@set -e; \
	mkdir -p $(DIST)/store/x64/lang $(DIST)/store/x64/tray; \
	cp $(DIST)/x64/lang/* $(DIST)/store/x64/lang/; \
	cp $(DIST)/x64/tray/* $(DIST)/store/x64/tray/; \
	if [ -f $(DIST)/x64/loft-front.exe ]; then cp $(DIST)/x64/loft-front.exe $(DIST)/store/x64/; fi; \
	rm -rf $(DIST)/store/x64/assets; mkdir -p $(DIST)/store/x64/assets; \
	cp $(ICONS)/store/*.png $(DIST)/store/x64/assets/; \
	ver=$$(python3 -c 'v=open("VERSION").read().strip().split("."); v += ["0"]*(4-len(v)); print(".".join(v[:4]))'); \
	printf '%s\n' \
	'<?xml version="1.0" encoding="utf-8"?>' \
	'<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10" xmlns:uap="http://schemas.microsoft.com/appx/manifest/uap/windows10" xmlns:rescap="http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities" IgnorableNamespaces="uap rescap">' \
	"  <Identity Name=\"a3app.Loft\" Publisher=\"CN=a3app\" Version=\"$$ver\" ProcessorArchitecture=\"x64\"/>" \
	'  <Properties><DisplayName>Loft</DisplayName><PublisherDisplayName>a3app</PublisherDisplayName><Logo>assets\StoreLogo.png</Logo></Properties>' \
	'  <Dependencies><TargetDeviceFamily Name="Windows.Desktop" MinVersion="10.0.17763.0" MaxVersionTested="10.0.26100.0"/></Dependencies>' \
	'  <Resources><Resource Language="en-us"/><Resource Language="ru-ru"/><Resource Language="zh-cn"/></Resources>' \
	'  <Capabilities><rescap:Capability Name="runFullTrust"/><Capability Name="internetClient"/></Capabilities>' \
	'  <Applications><Application Id="Loft" Executable="loft.exe" EntryPoint="Windows.FullTrustApplication">' \
	'    <uap:VisualElements DisplayName="Loft" Description="Loft" BackgroundColor="#1A1714" Square150x150Logo="assets\Square150x150Logo.png" Square44x44Logo="assets\Square44x44Logo.png">' \
	'      <uap:DefaultTile Wide310x150Logo="assets\Wide310x150Logo.png" Square71x71Logo="assets\Square71x71Logo.png" Square310x310Logo="assets\Square310x310Logo.png"/>' \
	'      <uap:SplashScreen Image="assets\SplashScreen.png" BackgroundColor="#1A1714"/>' \
	'    </uap:VisualElements>' \
	'    <Extensions>' \
	'      <uap:Extension Category="windows.protocol"><uap:Protocol Name="loft"/></uap:Extension>' \
	'      <uap:Extension Category="windows.fileTypeAssociation"><uap:FileTypeAssociation Name="loft"><uap:SupportedFileTypes><uap:FileType>.loft</uap:FileType></uap:SupportedFileTypes></uap:FileTypeAssociation></uap:Extension>' \
	'    </Extensions>' \
	'  </Application></Applications>' \
	'</Package>' \
	> $(DIST)/store/x64/AppxManifest.xml
