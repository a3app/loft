# Local phone APK + macOS loft.app → ../../dist/clients/Android/
# Intermediates stay under dist/clients/Android/gradle, not clients/Android/.

ROOT := $(abspath ../..)
DIST := $(ROOT)/dist/clients/Android
AAR := $(ROOT)/clients/mobile/build/engine.aar
GRADLE ?= $(shell command -v gradle 2>/dev/null || echo $(HOME)/.local/gradle/bin/gradle)
JAVA_HOME ?= $(HOME)/Library/Java/JavaVirtualMachines/temurin-17.jdk/Contents/Home
ANDROID_HOME ?= /usr/local/share/android-commandlinetools
ANDROID_SDK_ROOT ?= $(ANDROID_HOME)
export JAVA_HOME ANDROID_HOME ANDROID_SDK_ROOT

.PHONY: all debug release clean

all: debug

debug: $(AAR)
	$(GRADLE) --project-cache-dir $(DIST)/.gradle :app:distDebug

# Ad-hoc keystore in dist/ (gitignored). Sideload on a Pixel / Shield.
release: $(AAR)
	mkdir -p $(DIST)
	@if [ ! -f $(DIST)/loft-adhoc.jks ]; then \
		keytool -genkeypair -keystore $(DIST)/loft-adhoc.jks -alias loft \
			-storepass loft-adhoc -keypass loft-adhoc \
			-dname 'CN=loft ad-hoc' -keyalg RSA -keysize 2048 -validity 3650; \
	fi
	LOFT_STORE_FILE=$(DIST)/loft-adhoc.jks \
	LOFT_STORE_PASSWORD=loft-adhoc \
	LOFT_KEY_ALIAS=loft \
	LOFT_KEY_PASSWORD=loft-adhoc \
	$(GRADLE) --project-cache-dir $(DIST)/.gradle :app:assemblePhoneRelease :app:assembleTvRelease
	cp $(DIST)/gradle/app/outputs/apk/phone/release/app-phone-release.apk $(DIST)/app-release.apk
	cp $(DIST)/gradle/app/outputs/apk/tv/release/app-tv-release.apk $(DIST)/app-tv-release.apk

$(AAR):
	$(MAKE) -C ../mobile android

clean:
	rm -rf $(DIST)
