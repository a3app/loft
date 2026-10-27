# Local phone+TV APKs + macOS loft.app / loft-tv.app / loft-gtv.app → ../../dist/clients/Android/
# Intermediates stay under dist/clients/Android/gradle, not clients/Android/.
# Channel github is the Dist/sideload APK (self-update). store is Play/App Gallery.

ROOT := $(abspath ../..)
DIST := $(ROOT)/dist/clients/Android
AAR := $(ROOT)/clients/mobile/build/engine.aar
VER := $(shell tr -d ' \n' < $(ROOT)/VERSION)
KEY := $(ROOT)/secrets/android-github.p12
PASSFILE := $(ROOT)/secrets/android-github.pass
GRADLE ?= $(shell command -v gradle 2>/dev/null || echo $(HOME)/.local/gradle/bin/gradle)
JAVA_HOME ?= $(HOME)/Library/Java/JavaVirtualMachines/temurin-17.jdk/Contents/Home
ANDROID_HOME ?= /usr/local/share/android-commandlinetools
ANDROID_SDK_ROOT ?= $(ANDROID_HOME)
export JAVA_HOME ANDROID_HOME ANDROID_SDK_ROOT

.PHONY: all debug release test github-key avds clean

all: debug

debug: $(AAR)
	$(GRADLE) --project-cache-dir $(DIST)/.gradle :app:assemblePhoneGithubDebug :app:assembleTvGithubDebug

test: $(AAR)
	$(GRADLE) --project-cache-dir $(DIST)/.gradle :app:testPhoneGithubDebugUnitTest :app:testPhoneStoreDebugUnitTest

# Long-lived GitHub key in secrets/ (gitignored). Dist and sideload share it.
release: $(AAR)
	@test -f "$(KEY)" || { echo "missing $(KEY) — run: make -C clients/Android github-key" >&2; exit 1; }
	@test -f "$(PASSFILE)" || { echo "missing $(PASSFILE)" >&2; exit 1; }
	mkdir -p $(DIST)
	LOFT_STORE_FILE=$(KEY) \
	LOFT_STORE_PASSWORD="$$(cat $(PASSFILE))" \
	LOFT_KEY_ALIAS=loft \
	LOFT_KEY_PASSWORD="$$(cat $(PASSFILE))" \
	LOFT_ANDROID_CRASH_TOKEN="$${LOFT_ANDROID_CRASH_TOKEN}" \
	$(GRADLE) --project-cache-dir $(DIST)/.gradle :app:assemblePhoneGithubRelease :app:assembleTvGithubRelease
	cp $(DIST)/gradle/app/outputs/apk/phoneGithub/release/app-phone-github-release.apk $(DIST)/loft-$(VER)-android.apk
	cp $(DIST)/gradle/app/outputs/apk/tvGithub/release/app-tv-github-release.apk $(DIST)/loft-$(VER)-android-tv.apk
	cp $(DIST)/loft-$(VER)-android.apk $(DIST)/loft-android.apk
	cp $(DIST)/loft-$(VER)-android-tv.apk $(DIST)/loft-android-tv.apk
	cp $(DIST)/loft-$(VER)-android.apk $(DIST)/app-release.apk
	cp $(DIST)/loft-$(VER)-android-tv.apk $(DIST)/app-tv-release.apk
	cd $(DIST) && (command -v sha256sum >/dev/null && sha256sum loft-$(VER)-android.apk loft-$(VER)-android-tv.apk || shasum -a 256 loft-$(VER)-android.apk loft-$(VER)-android-tv.apk) > loft-android-$(VER)-SHA256SUMS.txt

avds:
	./emulator/setup-avds

github-key:
	mkdir -p $(ROOT)/secrets
	@if [ -f "$(KEY)" ]; then echo "$(KEY) exists" >&2; exit 1; fi
	python3 -c 'import secrets; open("$(PASSFILE)","w").write(secrets.token_urlsafe(24))'
	chmod 600 $(PASSFILE)
	keytool -genkeypair -keystore $(KEY) -alias loft \
		-storetype PKCS12 -storepass "$$(cat $(PASSFILE))" -keypass "$$(cat $(PASSFILE))" \
		-dname 'CN=a3app loft github' -keyalg RSA -keysize 3072 -validity 3650
	chmod 600 $(KEY)
	@echo "wrote $(KEY) — keep the pass file next to it; add both to Dist secrets"

$(AAR):
	$(MAKE) -C ../mobile android

clean:
	rm -rf $(DIST)
