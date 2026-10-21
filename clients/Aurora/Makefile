# Cross loft-front for the phone. Open the tree in Aurora IDE for the QML RPM.
ROOT := $(abspath ../..)
GO ?= go
FRONT := bin/loft-front

.PHONY: front

front:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -o $(FRONT) $(ROOT)/clients/go/cmd/loft-front
