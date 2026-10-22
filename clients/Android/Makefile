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

.PHONY: all debug clean

all: debug

debug: $(AAR)
	$(GRADLE) --project-cache-dir $(DIST)/.gradle :app:assembleDebug

$(AAR):
	$(MAKE) -C ../mobile android

clean:
	rm -rf $(DIST)
